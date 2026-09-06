package oauth

import (
	"context"
	"log/slog"
	"time"

	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/provider/openai"
	"github.com/user/one-llm-router/internal/store"
)

type UsageRefresher struct {
	accounts    accountStore
	client      *openai.Client
	coordinator *Coordinator
	logger      *slog.Logger
	interval    time.Duration
	stop        chan struct{}
}

func NewUsageRefresher(accounts accountStore, client *openai.Client, coordinator *Coordinator, logger *slog.Logger, interval time.Duration) *UsageRefresher {
	return &UsageRefresher{
		accounts:    accounts,
		client:      client,
		coordinator: coordinator,
		logger:      logger,
		interval:    interval,
		stop:        make(chan struct{}),
	}
}

func (r *UsageRefresher) Start() {
	go r.loop()
}

func (r *UsageRefresher) Stop() {
	close(r.stop)
}

func (r *UsageRefresher) loop() {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	r.refreshAll()

	for {
		select {
		case <-ticker.C:
			r.refreshAll()
		case <-r.stop:
			return
		}
	}
}

func (r *UsageRefresher) refreshAll() {
	ctx := context.Background()
	accounts, err := r.accounts.ListActive(ctx)
	if err != nil {
		r.logger.Error("usage refresher: failed to list active accounts", "error", err)
		return
	}

	for _, acc := range accounts {
		if !acc.IsOAuth() {
			continue
		}
		r.refreshOne(ctx, acc)
	}
}

func (r *UsageRefresher) refreshOne(ctx context.Context, acc domain.UpstreamAccount) {
	token, _, err := r.coordinator.RefreshIfStale(ctx, &acc)
	if err != nil {
		r.logger.Error("usage refresher: failed to refresh token", "account_id", acc.ID, "error", err)
		return
	}

	chatGPTAccountID := ""
	if acc.ChatGPTAccountID != nil {
		chatGPTAccountID = *acc.ChatGPTAccountID
	}

	usage, err := r.client.FetchUsage(ctx, string(token), chatGPTAccountID, acc.UseProxy, acc.ID)
	if err != nil {
		r.logger.Error("usage refresher: failed to fetch usage", "account_id", acc.ID, "error", err)
		return
	}

	// Codex's 5h/7d quota windows are rolling: entering a new window with
	// no messages, the API answers used_percent=0 with a NOMINAL reset_at
	// that keeps sliding ("now + period") — nothing pins the reset until a
	// real message is sent. A window counts as fixed when it carries real
	// usage (used_percent > 0, fast path) or when its reset_at lands
	// strictly before a full period from now (anchored earlier). When no
	// window is fixed, send one minimal probe so the next refresh can
	// persist a fixed next-reset time. No re-fetch here — the anchored
	// window surfaces on the next scheduled pass. The probe re-fires on
	// every pass until the window reports fixed usage.
	if !usageResetFixed(usage, time.Now().UTC()) {
		if err := r.client.ProbeResetAnchor(ctx, acc, string(token)); err != nil {
			r.logger.Warn("usage refresher: reset-anchor probe failed",
				"account_id", acc.ID, "error", err)
		} else {
			r.logger.Info("usage refresher: reset-anchor probe sent",
				"account_id", acc.ID)
		}
	}

	var snapshot store.UsageSnapshot
	if rateLimit := usage.RateLimit; rateLimit != nil {
		if window := rateLimit.PrimaryWindow; window != nil {
			used := window.UsedPercent
			snapshot.PrimaryUsedPercent = &used
			snapshot.PrimaryResetAt = unixToTime(window.ResetAt)
			snapshot.PrimaryWindowSeconds = window.LimitWindowSeconds
		}
		if window := rateLimit.SecondaryWindow; window != nil {
			used := window.UsedPercent
			snapshot.SecondaryUsedPercent = &used
			snapshot.SecondaryResetAt = unixToTime(window.ResetAt)
			snapshot.SecondaryWindowSeconds = window.LimitWindowSeconds
		}
	}

	err = r.accounts.UpdateUsage(ctx, acc.ID, snapshot)
	if err != nil {
		r.logger.Error("usage refresher: failed to update store", "account_id", acc.ID, "error", err)
	}
}

// resetAnchorTolerance absorbs the small skew between the router clock
// and the upstream clock in the reset_at vs now+period comparison. The
// nominal rolling reset_at is computed as server_now + period, so it
// must read as "not fixed" (probe) even when the server clock leads the
// router by a few seconds.
//
// The tolerance MUST be smaller than the usage-refresh interval: a
// freshly-anchored reset (probe time + period) re-evaluated one interval
// later is only "period - interval" away from now, and an over-large
// tolerance would keep re-classifying it as rolling and re-probe every
// pass — pushing the reset forward indefinitely. 1 minute is comfortably
// below the 5-minute default refresh while still absorbing latency and
// sub-minute clock skew.
const resetAnchorTolerance = time.Minute

// usageResetFixed reports whether every quota window PRESENT in the
// response has a fixed (non-sliding) reset time. Fast path: a window
// with real usage (used_percent > 0) is anchored. Slow path: a window
// with reset_at landing strictly before a full period from now was
// anchored by a message in the past; a reset_at ≈ now + period is the
// nominal rolling value and does NOT count as fixed.
//
// Absent windows are ignored — an entitlement gap, not something a
// probe can fix. The probe anchors every present window, so it fires
// whenever at least one PRESENT window is still rolling (e.g. the 5h
// primary is used 0 and sliding while the 7d secondary is already
// anchored by real usage).
func usageResetFixed(usage *openai.UsageResponse, now time.Time) bool {
	if usage == nil || usage.RateLimit == nil {
		return false
	}
	rl := usage.RateLimit
	if rl.PrimaryWindow != nil && !usageWindowFixed(rl.PrimaryWindow, now) {
		return false
	}
	if rl.SecondaryWindow != nil && !usageWindowFixed(rl.SecondaryWindow, now) {
		return false
	}
	return true
}

func usageWindowFixed(w *openai.UsageWindow, now time.Time) bool {
	if w == nil {
		return false
	}
	if w.UsedPercent > 0 {
		return true
	}
	if w.ResetAt == nil || w.LimitWindowSeconds == nil {
		return false
	}
	period := time.Duration(*w.LimitWindowSeconds) * time.Second
	nominalReset := now.Add(period).Add(-resetAnchorTolerance)
	return nominalReset.After(time.Unix(*w.ResetAt, 0).UTC())
}

// unixToTime converts a unix epoch seconds pointer to a UTC time.Time.
// A nil input yields nil so a window that omitted reset_at stays NULL
// on the row instead of being fabricated.
func unixToTime(epochSeconds *int64) *time.Time {
	if epochSeconds == nil {
		return nil
	}
	t := time.Unix(*epochSeconds, 0).UTC()
	return &t
}

package oauth

import (
	"context"
	"log/slog"
	"time"

	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/provider/openai"
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

	usage, err := r.client.FetchUsage(ctx, string(token), chatGPTAccountID)
	if err != nil {
		r.logger.Error("usage refresher: failed to fetch usage", "account_id", acc.ID, "error", err)
		return
	}

	var primary *float64
	var secondary *float64
	if rateLimit := usage.RateLimit; rateLimit != nil {
		if window := rateLimit.PrimaryWindow; window != nil {
			used := window.UsedPercent
			primary = &used
		}
		if window := rateLimit.SecondaryWindow; window != nil {
			used := window.UsedPercent
			secondary = &used
		}
	}

	err = r.accounts.UpdateUsage(ctx, acc.ID, primary, secondary)
	if err != nil {
		r.logger.Error("usage refresher: failed to update store", "account_id", acc.ID, "error", err)
	}
}

package app

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/user/one-llm-router/internal/config"
	"github.com/user/one-llm-router/internal/proxydial"
)

// newRefreshTransport builds a Transport for the auxiliary 30s clients
// (model refresh): DefaultTransport tuning with a fully explicit Proxy
// function — nil for direct, proxydial.ProxyFunc() for the proxied
// twin. Cloning keeps the production timeouts and pooling; overriding
// Proxy removes the implicit environment-variable behaviour (009 D3).
func newRefreshTransport(proxyFn func(*http.Request) (*url.URL, error)) *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = proxyFn
	return t
}

// applyOutboundProxy installs cfg.Network.ProxyURL into the
// process-wide proxydial slot (Feature 009). It runs on every config
// application point: boot (BuildApp), wizard commit
// (promoteToSteadyState), and the settings on-reload hook — the three
// moments config.json changes while the process lives.
//
// An empty value clears the slot (direct egress). An invalid value
// returns an error and leaves the previously-installed slot untouched,
// so boot/promote callers can fail fast instead of running with a
// silently degraded egress, and the hot-reload path can log-and-keep
// the previous value. Boot and promotion pass logUnchanged=true so the
// effective state is always visible; hot reload passes false to avoid
// noise for unrelated settings updates.
func applyOutboundProxy(cfg *config.Config, logUnchanged bool) error {
	raw := ""
	if cfg != nil {
		raw = cfg.Network.ProxyURL
	}
	prev := proxydial.Current()
	if err := proxydial.Configure(raw); err != nil {
		return fmt.Errorf("apply outbound proxy: %w", err)
	}

	// Log only on actual change (NFR-5: one line per real transition,
	// not one per unrelated settings update). Masked forms only —
	// credentials never reach the log.
	newState := proxydial.MaskedURL(raw)
	if newState == "" {
		newState = "none"
	}
	prevState := "none"
	if prev != nil {
		prevState = proxydial.MaskedURL(prev.String())
		if prevState == "" {
			prevState = "none"
		}
	}
	if logUnchanged || newState != prevState {
		slog.Info("outbound proxy state", "proxy", newState)
	}
	return nil
}

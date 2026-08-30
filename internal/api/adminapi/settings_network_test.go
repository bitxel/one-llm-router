package adminapi

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/user/one-llm-router/internal/api/errcode"
	"github.com/user/one-llm-router/internal/config"
)

func withNetwork(cfg *config.Config, raw string) *config.Config {
	cfg.Network.ProxyURL = raw
	return cfg
}

// TestSettings_Update_NetworkPatch_RoundTrip covers the 009 settings
// surface: accept a valid proxy_url, project the MASKED form (never
// the raw credential), reject invalid URLs with 9001, and clear via
// the empty string.
func TestSettings_Update_NetworkPatch_RoundTrip(t *testing.T) {
	t.Run("valid url is accepted and projected masked", func(t *testing.T) {
		cfg := baseConfig()
		updater := &fakeUpdater{next: withNetwork(cfg, "socks5://op:pw@10.0.0.1:1080")}
		h := newHandler(&fakeReader{cfg: cfg}, updater)

		rec := doUpdate(t, h, `{"network":{"proxy_url":"socks5://op:pw@10.0.0.1:1080"}}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status: got %d body=%s", rec.Code, rec.Body.String())
		}
		code, data := decodeEnvelope(t, rec)
		if code != 0 {
			t.Fatalf("code: got %d body=%s", code, rec.Body.String())
		}
		network, _ := data["network"].(map[string]any)
		if network["proxy_configured"] != true {
			t.Fatalf("proxy_configured: got %v want true", network["proxy_configured"])
		}
		masked, _ := network["proxy_url_masked"].(string)
		if masked != "socks5://op:*****@10.0.0.1:1080" {
			t.Fatalf("proxy_url_masked: got %q — raw credentials must never reach the wire", masked)
		}
		if network["proxy_has_auth"] != true {
			t.Fatalf("proxy_has_auth: got %v want true", network["proxy_has_auth"])
		}

		got := updater.inputs[0]
		if got.NetworkProxyURL == nil || *got.NetworkProxyURL != "socks5://op:pw@10.0.0.1:1080" {
			t.Fatalf("patch.NetworkProxyURL mismatch: %+v", got.NetworkProxyURL)
		}
	})

	t.Run("invalid url returns 9001", func(t *testing.T) {
		cfg := baseConfig()
		updater := &fakeUpdater{next: cfg}
		h := newHandler(&fakeReader{cfg: cfg}, updater)

		rec := doUpdate(t, h, `{"network":{"proxy_url":"ftp://nope"}}`)
		code, data := decodeEnvelope(t, rec)
		if code != errcode.InvalidProxyURL {
			t.Fatalf("code: got %d want %d (invalid_proxy_url)", code, errcode.InvalidProxyURL)
		}
		if data["field"] != "network.proxy_url" {
			t.Fatalf("data.field: got %v want network.proxy_url", data["field"])
		}
		if atomic.LoadInt32(&updater.called) != 0 {
			t.Fatal("updater must not be invoked for rejected patches")
		}
	})

	t.Run("malformed credential URL does not echo the password", func(t *testing.T) {
		cfg := baseConfig()
		h := newHandler(&fakeReader{cfg: cfg}, &fakeUpdater{next: cfg})

		rec := doUpdate(t, h, `{"network":{"proxy_url":"socks5://user:secret%zz@proxy:1080"}}`)
		if strings.Contains(rec.Body.String(), "secret") {
			t.Fatalf("response exposed proxy password: %s", rec.Body.String())
		}
		if code, _ := decodeEnvelope(t, rec); code != errcode.InvalidProxyURL {
			t.Fatalf("code: got %d want %d", code, errcode.InvalidProxyURL)
		}
	})

	t.Run("unknown network key returns 2012", func(t *testing.T) {
		cfg := baseConfig()
		h := newHandler(&fakeReader{cfg: cfg}, &fakeUpdater{next: cfg})

		rec := doUpdate(t, h, `{"network":{"enabled":true}}`)
		if code, _ := decodeEnvelope(t, rec); code != errcode.UnknownConfigKey {
			t.Fatalf("code: got %d want %d (unknown_config_key)", code, errcode.UnknownConfigKey)
		}
	})

	t.Run("null proxy url returns malformed body", func(t *testing.T) {
		cfg := baseConfig()
		h := newHandler(&fakeReader{cfg: cfg}, &fakeUpdater{next: cfg})

		rec := doUpdate(t, h, `{"network":{"proxy_url":null}}`)
		if code, _ := decodeEnvelope(t, rec); code != errcode.MalformedBody {
			t.Fatalf("code: got %d want %d", code, errcode.MalformedBody)
		}
	})

	t.Run("empty string clears and projection shows unconfigured", func(t *testing.T) {
		cfg := withNetwork(baseConfig(), "socks5://127.0.0.1:7890")
		updater := &fakeUpdater{next: withNetwork(cfg, "")}
		h := newHandler(&fakeReader{cfg: cfg}, updater)

		rec := doUpdate(t, h, `{"network":{"proxy_url":""}}`)
		code, data := decodeEnvelope(t, rec)
		if code != 0 {
			t.Fatalf("code: got %d body=%s", code, rec.Body.String())
		}
		network, _ := data["network"].(map[string]any)
		if network["proxy_configured"] != false {
			t.Fatalf("proxy_configured: got %v want false", network["proxy_configured"])
		}
	})
}

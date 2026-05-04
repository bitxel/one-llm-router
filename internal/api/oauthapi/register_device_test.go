package oauthapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/user/one-llm-router/internal/api/testutil"
	"github.com/user/one-llm-router/internal/oauth"
)

func TestRegisterDeviceHandlers(t *testing.T) {
	t.Run("happy path route is reachable with identity chain", func(t *testing.T) {
		h := newRegisterDeviceHarness(t, nil)

		rec := h.post(t, "/api/admin/oauth/device/start", `{"provider":"openai"}`)
		testutil.AssertEnvelopeDataShape(t, rec, 0)
	})

	t.Run("chain wraps device route exactly once", func(t *testing.T) {
		var count atomic.Int32
		h := newRegisterDeviceHarness(t, func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				next.ServeHTTP(w, r)
			})
		})

		h.post(t, "/api/admin/oauth/device/start", `{"provider":"openai"}`)
		assert.Equal(t, int32(1), count.Load())
	})

	t.Run("inventory matches phase 5 surface and union matches openapi inventory", func(t *testing.T) {
		assert.Equal(t, []string{"/api/admin/oauth/device/start"}, DeviceRouteInventory())
		assert.Equal(t, []string{
			"/api/admin/oauth/browser/start",
			"/api/admin/oauth/browser/manual-callback",
			"/api/admin/oauth/flow",
			"/api/admin/oauth/cancel",
			"/api/admin/oauth/device/start",
		}, append(BrowserRouteInventory(), DeviceRouteInventory()...))
		assert.Equal(t, append(BrowserRouteInventory(), DeviceRouteInventory()...), oauthRoutesFromOpenAPI(t))
	})

	t.Run("device handlers do not register browser or poll-only routes", func(t *testing.T) {
		var count atomic.Int32
		h := newRegisterDeviceHarness(t, func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				next.ServeHTTP(w, r)
			})
		})

		for _, tc := range []struct {
			method string
			path   string
		}{
			{method: http.MethodPost, path: "/api/admin/oauth/browser/start"},
			{method: http.MethodPost, path: "/api/admin/oauth/device/poll"},
		} {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"provider":"openai"}`))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.server.ServeHTTP(rec, req)
			assert.Equal(t, http.StatusNotFound, rec.Code)
		}
		assert.Equal(t, int32(0), count.Load())
	})
}

type registerDeviceHarness struct {
	coord  *oauth.Coordinator
	server http.Handler
}

func newRegisterDeviceHarness(t *testing.T, chain func(http.Handler) http.Handler) *registerDeviceHarness {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	fake := oauth.NewFakeClock(time.Date(2026, 4, 21, 20, 0, 0, 0, time.UTC))
	provider := &startDeviceProvider{
		deviceCode: oauth.DeviceCode{
			DeviceAuthID:    "dev_route_123",
			UserCode:        "ABCD-1234",
			VerificationURL: "https://auth.openai.com/codex/device",
			Interval:        5 * time.Second,
			ExpiresIn:       15 * time.Minute,
		},
	}
	coord := oauth.NewCoordinatorWithClock(fake, provider, logger)
	mux := http.NewServeMux()
	RegisterDeviceHandlers(mux, coord, chain)

	t.Cleanup(func() {
		if flow := coord.CurrentFlow(); flow != nil {
			coord.ReleaseFlow(flow.ID)
		}
	})

	return &registerDeviceHarness{
		coord:  coord,
		server: mux,
	}
}

func (h *registerDeviceHarness) post(t *testing.T, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.server.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	return rec
}

func oauthRoutesFromOpenAPI(t *testing.T) []string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("..", "..", "..", "openapi", "admin.yaml"))
	require.NoError(t, err)

	var doc struct {
		Paths yaml.Node `yaml:"paths"`
	}
	require.NoError(t, yaml.Unmarshal(data, &doc))
	require.Equal(t, yaml.MappingNode, doc.Paths.Kind)

	set := make(map[string]struct{})
	for i := 0; i < len(doc.Paths.Content); i += 2 {
		path := doc.Paths.Content[i].Value
		switch path {
		case "/api/admin/oauth/browser/start",
			"/api/admin/oauth/browser/manual-callback",
			"/api/admin/oauth/flow",
			"/api/admin/oauth/cancel",
			"/api/admin/oauth/device/start":
			set[path] = struct{}{}
		}
	}

	ordered := []string{
		"/api/admin/oauth/browser/start",
		"/api/admin/oauth/browser/manual-callback",
		"/api/admin/oauth/flow",
		"/api/admin/oauth/cancel",
		"/api/admin/oauth/device/start",
	}
	for _, path := range ordered {
		_, ok := set[path]
		require.Truef(t, ok, "missing %s in openapi/admin.yaml", path)
	}
	return ordered
}

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

func TestRegisterBrowserHandlers(t *testing.T) {
	t.Run("happy path routes are reachable with identity chain", func(t *testing.T) {
		h := newRegisterHarness(t, nil)

		startRec := h.post(t, "/api/admin/oauth/browser/start", `{"provider":"openai"}`)
		testutil.AssertEnvelopeDataShape(t, startRec, 0)
		startBody := decodeBrowserStartResponse(t, startRec)
		startEnv, err := startBody.AsBrowserStartEnvelope()
		require.NoError(t, err)

		flowRec := h.get(t, "/api/admin/oauth/flow")
		testutil.AssertEnvelopeDataShape(t, flowRec, 0)

		manualRec := h.post(t, "/api/admin/oauth/browser/manual-callback", `{"callback_url":"https://evil.com/auth/callback?code=abc&state=s"}`)
		testutil.AssertEnvelopeDataShape(t, manualRec, 3007)

		cancelRec := h.post(t, "/api/admin/oauth/cancel", `{"flow_id":"`+startEnv.Data.FlowId+`"}`)
		testutil.AssertEnvelopeDataShape(t, cancelRec, 0)
	})

	t.Run("chain wraps each phase 3 route exactly once", func(t *testing.T) {
		var count atomic.Int32
		h := newRegisterHarness(t, func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				next.ServeHTTP(w, r)
			})
		})

		h.post(t, "/api/admin/oauth/browser/start", `{"provider":"claude"}`)
		assert.Equal(t, int32(1), count.Load())

		h.post(t, "/api/admin/oauth/browser/manual-callback", `{"callback_url":"https://evil.com/auth/callback?code=abc&state=s"}`)
		assert.Equal(t, int32(2), count.Load())

		h.get(t, "/api/admin/oauth/flow?ignored=true")
		assert.Equal(t, int32(3), count.Load())

		h.post(t, "/api/admin/oauth/cancel", `{"flow_id":"fl_cancel_idle_1234567890123456789012"}`)
		assert.Equal(t, int32(4), count.Load())
	})

	t.Run("openapi parity and phase isolation", func(t *testing.T) {
		inventory := BrowserRouteInventory()
		assert.Equal(t, []string{
			"/api/admin/oauth/browser/start",
			"/api/admin/oauth/browser/manual-callback",
			"/api/admin/oauth/flow",
			"/api/admin/oauth/cancel",
		}, inventory)
		require.NotContains(t, inventory, "/api/admin/oauth/device/start")
		require.NotContains(t, inventory, "/api/admin/oauth/device/poll")

		openapiRoutes := browserRoutesFromOpenAPI(t)
		assert.Equal(t, openapiRoutes, inventory)
	})

	t.Run("non phase-3 routes stay unregistered and bypass chain", func(t *testing.T) {
		var count atomic.Int32
		h := newRegisterHarness(t, func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				next.ServeHTTP(w, r)
			})
		})

		deviceReq := httptest.NewRequest(http.MethodPost, "/api/admin/oauth/device/start", strings.NewReader(`{"provider":"openai"}`))
		deviceReq.Header.Set("Content-Type", "application/json")
		deviceRec := httptest.NewRecorder()
		h.server.ServeHTTP(deviceRec, deviceReq)
		assert.Equal(t, http.StatusNotFound, deviceRec.Code)
		assert.Equal(t, int32(0), count.Load())

		callbackReq := httptest.NewRequest(http.MethodGet, "/auth/callback?code=abc&state=s", nil)
		callbackRec := httptest.NewRecorder()
		h.server.ServeHTTP(callbackRec, callbackReq)
		assert.Equal(t, http.StatusNotFound, callbackRec.Code)
		assert.Equal(t, int32(0), count.Load())
	})
}

type registerHarness struct {
	coord  *oauth.Coordinator
	server http.Handler
}

func newRegisterHarness(t *testing.T, chain func(http.Handler) http.Handler) *registerHarness {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	coord := oauth.NewCoordinatorWithClock(
		oauth.NewFakeClock(time.Date(2026, 4, 21, 20, 0, 0, 0, time.UTC)),
		nil,
		logger,
	)
	mux := http.NewServeMux()
	RegisterBrowserHandlers(mux, coord, chain)

	t.Cleanup(func() {
		if flow := coord.CurrentFlow(); flow != nil {
			coord.ReleaseFlow(flow.ID)
		}
	})

	return &registerHarness{
		coord:  coord,
		server: mux,
	}
}

func (h *registerHarness) post(t *testing.T, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.server.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	return rec
}

func (h *registerHarness) get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.server.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	return rec
}

func browserRoutesFromOpenAPI(t *testing.T) []string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("..", "..", "..", "openapi", "admin.yaml"))
	require.NoError(t, err)

	var doc struct {
		Paths yaml.Node `yaml:"paths"`
	}
	require.NoError(t, yaml.Unmarshal(data, &doc))
	require.Equal(t, yaml.MappingNode, doc.Paths.Kind)

	var routes []string
	for i := 0; i < len(doc.Paths.Content); i += 2 {
		pathNode := doc.Paths.Content[i]
		path := pathNode.Value
		switch path {
		case "/api/admin/oauth/browser/start",
			"/api/admin/oauth/browser/manual-callback",
			"/api/admin/oauth/flow",
			"/api/admin/oauth/cancel":
			routes = append(routes, path)
		}
	}
	return routes
}

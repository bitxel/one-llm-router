package setup

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/user/one-llm-router/internal/api"
)

func TestGate_SetupPending_006DataPlanePrefixesReturnNative503(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
	}{
		{name: "v1 platform proxy", path: "/v1/chat/completions"},
		{name: "backend api exact", path: "/backend-api"},
		{name: "backend api codex path", path: "/backend-api/codex/responses"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			g, _, sentinel := newGateWithDone(t, false)
			h := g.Wrap(sentinel)

			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader("{}"))
			req = req.WithContext(api.WithRequestID(req.Context(), "req-gate-006"))
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
			}
			if sentinel.saw(tc.path) {
				t.Fatalf("sentinel saw %s; setup-pending data plane must short-circuit", tc.path)
			}
			assertNativeSetupRequired(t, rec.Body.String())
		})
	}
}

func TestGate_SetupPending_006DoesNotTreatArbitraryAPIsAsDataPlane(t *testing.T) {
	t.Parallel()

	g, _, sentinel := newGateWithDone(t, false)
	h := g.Wrap(sentinel)

	for _, path := range []string{"/api/other", "/api/codex", "/api/codex/usage", "/api/codexish", "/backend-apix"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()

		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want sentinel 200", path, rec.Code)
		}
		if !sentinel.saw(path) {
			t.Fatalf("%s: sentinel was not called", path)
		}
	}
}

func assertNativeSetupRequired(t *testing.T, body string) {
	t.Helper()

	if strings.Contains(body, `"request_id"`) {
		t.Fatalf("body leaks request_id; request id must be response-header-only: %s", body)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if _, ok := raw["error"]; !ok {
		t.Fatalf("body = %s, want native top-level error", body)
	}
	if _, ok := raw["code"]; ok {
		t.Fatalf("body = %s, must not be admin envelope", body)
	}

	var env api.RouterErrorEnvelope
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("unmarshal router error: %v", err)
	}
	if env.Error.Type != "service_unavailable" {
		t.Fatalf("error.type = %q, want service_unavailable", env.Error.Type)
	}
	if env.Error.Code != "setup_required" {
		t.Fatalf("error.code = %q, want setup_required", env.Error.Code)
	}
}

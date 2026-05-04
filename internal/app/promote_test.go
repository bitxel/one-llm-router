package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/user/one-llm-router/internal/config"
)

// TestPromoteToSteadyState_AfterWizardCommit verifies F-002: after
// the setup wizard commits successfully (config.json written, DB
// seeded), the running router MUST transition to steady-state WITHOUT
// a process restart. Concretely:
//
//   - /api/admin/accounts must become reachable (was refused with
//     2011 setup_required before commit)
//   - /v1/* must become wired to the real proxy handler (F-001; was
//     refused with 503 by the gate before commit)
//   - /api/admin/health must report setup_state=done.
//
// The test drives the commit through the real HTTP handler chain
// rather than calling promoteToSteadyState directly so the full
// wiring (setup.Commit → postCommitReloader → promoteToSteadyState
// → handler swap) is exercised end-to-end.
func TestPromoteToSteadyState_AfterWizardCommit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	dbFile := filepath.Join(dir, "router.db")

	// 1. Boot in setup-pending mode (cfg=nil, no brownfield).
	a, err := BuildApp(context.Background(), nil, nil, Deps{
		ConfigPath: cfgPath,
		Env:        config.MapEnv(map[string]string{}),
		Logger:     silentLogger(),
	})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer func() { _ = a.Stop(context.Background()) }()

	handler := a.Handler()

	// 2. Assert pre-commit behaviour:
	//    /api/admin/accounts → 2011 setup_required
	//    /v1/responses       → 503 native error shape (not 404!)
	preAdmin := httptest.NewRecorder()
	handler.ServeHTTP(preAdmin, httptest.NewRequest(http.MethodGet, "/api/admin/accounts", nil))
	if preAdmin.Code != http.StatusOK {
		t.Fatalf("pre-commit /api/admin/accounts status = %d, want 200", preAdmin.Code)
	}
	var preAdminEnv map[string]any
	if err := json.Unmarshal(preAdmin.Body.Bytes(), &preAdminEnv); err != nil {
		t.Fatalf("decode admin envelope: %v", err)
	}
	if code, _ := preAdminEnv["code"].(float64); int(code) != 2011 {
		t.Errorf("pre-commit admin code = %v, want 2011", preAdminEnv["code"])
	}
	preProxy := httptest.NewRecorder()
	handler.ServeHTTP(preProxy, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
	if preProxy.Code != http.StatusServiceUnavailable {
		t.Errorf("pre-commit /v1/responses status = %d, want 503", preProxy.Code)
	}

	// 3. POST /api/setup/commit with a valid payload.
	commitBody := map[string]any{
		"db": map[string]any{
			"driver": "sqlite3",
			"url":    dbFile,
		},
		"first_account": map[string]any{
			"name":     "prod-01",
			"provider": "openai",
			"api_key":  "sk-test-post-commit",
		},
		"plugins": map[string]any{
			"admin_auth":  map[string]any{"enabled": false},
			"client_keys": map[string]any{"enabled": false},
		},
	}
	raw, _ := json.Marshal(commitBody)
	commitRec := httptest.NewRecorder()
	commitReq := httptest.NewRequest(http.MethodPost, "/api/setup/commit", bytes.NewReader(raw))
	handler.ServeHTTP(commitRec, commitReq)
	if commitRec.Code != http.StatusOK {
		t.Fatalf("commit http = %d; body=%s", commitRec.Code, commitRec.Body.String())
	}
	var commitEnv map[string]any
	if err := json.Unmarshal(commitRec.Body.Bytes(), &commitEnv); err != nil {
		t.Fatalf("decode commit envelope: %v", err)
	}
	if code, _ := commitEnv["code"].(float64); int(code) != 0 {
		t.Fatalf("commit code = %v, want 0; body=%s", commitEnv["code"], commitRec.Body.String())
	}
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("config.json not materialized after commit: %v", err)
	}

	// 4. Post-commit: the handler pointer MUST have been swapped in
	//    place by promoteToSteadyState. Re-acquire the handler to
	//    make sure clients that call a.Handler() AFTER commit see
	//    the new surface (the adapter reads atomic.Pointer every
	//    request, so any cached reference works too).
	handler = a.Handler()

	// 4a. /api/admin/accounts now reachable (envelope code=0).
	postAdmin := httptest.NewRecorder()
	handler.ServeHTTP(postAdmin, httptest.NewRequest(http.MethodGet, "/api/admin/accounts", nil))
	if postAdmin.Code != http.StatusOK {
		t.Fatalf("post-commit /api/admin/accounts status = %d, want 200; body=%s",
			postAdmin.Code, postAdmin.Body.String())
	}
	var postAdminEnv map[string]any
	if err := json.Unmarshal(postAdmin.Body.Bytes(), &postAdminEnv); err != nil {
		t.Fatalf("decode post-admin envelope: %v", err)
	}
	if code, _ := postAdminEnv["code"].(float64); int(code) != 0 {
		t.Errorf("post-commit admin code = %v, want 0", postAdminEnv["code"])
	}

	// 4b. /v1/responses wired — no longer 503 from the gate, no
	//     longer 404 from the missing proxy (F-001). Whatever the
	//     proxy's own response is (likely a 502 chasing a fake
	//     upstream), it must NOT be 503/404.
	postProxy := httptest.NewRecorder()
	postProxyReq := httptest.NewRequest(http.MethodPost, "/v1/responses",
		bytes.NewReader([]byte(`{"model":"gpt-5","messages":[]}`)))
	postProxyReq.Header.Set("Authorization", "Bearer sk-test-post-commit")
	postProxyReq.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(postProxy, postProxyReq)
	if postProxy.Code == http.StatusServiceUnavailable {
		t.Errorf("post-commit /v1/responses still 503 — gate did not release")
	}
	if postProxy.Code == http.StatusNotFound {
		t.Errorf("post-commit /v1/responses returned 404 — proxy handler not wired (F-001)")
	}

	// 4c. Health now reports setup_state=done.
	healthRec := httptest.NewRecorder()
	handler.ServeHTTP(healthRec, httptest.NewRequest(http.MethodGet, "/api/admin/health", nil))
	if healthRec.Code != http.StatusOK {
		t.Fatalf("health status = %d", healthRec.Code)
	}
	var healthEnv map[string]any
	_ = json.Unmarshal(healthRec.Body.Bytes(), &healthEnv)
	data, _ := healthEnv["data"].(map[string]any)
	if state, _ := data["setup_state"].(string); state != "done" {
		t.Errorf("post-commit setup_state = %q, want done; body=%s", state, healthRec.Body.String())
	}

	// 5. Idempotency: a second promoteToSteadyState call MUST NOT
	//    rebuild the store or panic — App.promoted is latched.
	if err := a.promoteToSteadyState(context.Background()); err != nil {
		t.Errorf("idempotent promote failed: %v", err)
	}
}

// TestPromoteToSteadyState_FailureLeavesPendingIntact verifies that a
// failed promotion (e.g. the DB becomes unreadable between commit and
// promotion) does not corrupt the setup-pending handler state. The
// caller surfaces the error; the gate keeps serving traffic.
func TestPromoteToSteadyState_FailureLeavesPendingIntact(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	a, err := BuildApp(context.Background(), nil, nil, Deps{
		ConfigPath: cfgPath,
		Env:        config.MapEnv(map[string]string{}),
		Logger:     silentLogger(),
	})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer func() { _ = a.Stop(context.Background()) }()

	// Invoke promote without committing — no config.json on disk, so
	// config.Load returns ErrNoConfig and promote must fail cleanly.
	err = a.promoteToSteadyState(context.Background())
	if err == nil {
		t.Fatal("expected error from promote with missing config, got nil")
	}

	// Handler must still answer — the failure MUST NOT leave the
	// atomic.Pointer nil or a partially-initialised steady-state
	// handler.
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/admin/health", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("health after failed promote: status=%d, want 200", rec.Code)
	}
	// Redundant structural assertion — guards a future refactor
	// from accidentally swallowing the error.
	if fmt.Sprintf("%v", err) == "" {
		t.Error("err message is empty")
	}
}

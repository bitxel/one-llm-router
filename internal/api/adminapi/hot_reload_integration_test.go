package adminapi_test

// T-400: hot-reload integration test — asserts that a settings-update
// made via the admin API is observed by the live /v1/* proxy handler
// without a process restart. Exercises the `SetBodyLogFunc` plumbing
// wired in internal/app/app.go: the proxy consults the live
// config.Reader on every request, so toggling `log_client_request_body`,
// `log_upstream_request_body`, and `log_upstream_response_body` via
// POST /api/admin/settings/update flips the recording behaviour of the
// NEXT /v1/* request.
//
// Scope: body-logging keys only (the privacy-critical pair). The
// other two T-400 sub-assertions — log_level hot-reload via slog and
// log_retention_days live-tick observation — depend on additional
// plumbing (dynamic slog handler; retention goroutine tick function)
// that 002 intentionally stops short of; the config write is
// verified here and the observation side is tracked for 003.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/user/one-llm-router/internal/api"
	"github.com/user/one-llm-router/internal/api/adminapi"
	"github.com/user/one-llm-router/internal/config"
	"github.com/user/one-llm-router/internal/core"
	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/provider/openai"
	"github.com/user/one-llm-router/internal/store"
)

// TestHotReload_BodyLogging_TogglesNextProxyRequest mirrors the
// happy-path branch of T-400.
//
//  1. Publish a baseline config where all body-log flags are false.
//  2. Fire a /v1/* request; assert client_request_body,
//     upstream_request_body, and upstream_response_body fields in the
//     persisted record are NIL.
//  3. POST /api/admin/settings/update {runtime:{log_client_request_body:true}}
//     through the SettingsHandler (with a real ConfigUpdater writing
//     an atomic file + publishing to config.Reader).
//  4. Fire a second /v1/* request; assert client_request_body is now SET,
//     upstream fields still NIL.
//  5. POST again {log_upstream_request_body:true}; fire third request;
//     client and upstream request bodies are SET, response still NIL.
//  6. POST again {log_upstream_response_body:true}; fire fourth request;
//     all three are SET.
//  7. POST off; fire fifth; all three nil again.
//
// The test uses the real ProxyHandler with SetBodyLogFunc wired to
// config.Reader, exactly as BuildApp wires it in production. No
// HTTP test server other than a fake upstream is involved in the
// assertion path.
func TestHotReload_BodyLogging_TogglesNextProxyRequest(t *testing.T) {
	// Reset the live-config slot at test boundary. T-017's
	// livePtr is a process-global; tests that touch it MUST
	// restore it afterwards so sibling tests see the expected
	// nil-on-boot state.
	prev := config.Reader.Load()
	t.Cleanup(func() { config.Publisher.Store(prev) })

	// --- fixture: fake OpenAI upstream --------------------------
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "resp_hot_reload"})
	}))
	defer upstream.Close()

	// --- fixture: real SQLite store + recorder ------------------
	st, err := store.New("sqlite3", ":memory:", 1, 1)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate("sqlite3"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	accountRepo := store.NewAccountRepo(st.Engine())
	recordRepo := store.NewRequestRecordRepo(st.Engine())
	acct := &domain.UpstreamAccount{
		Name: "hot-reload", Provider: "openai", APIKey: "sk-test",
		BaseURL: &upstream.URL, Status: domain.AccountStatusActive,
		Capabilities: []string{"op.openai.responses", "op.openai.chat_completions"},
	}
	if err := accountRepo.Create(context.Background(), acct); err != nil {
		t.Fatalf("create acct: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	selector := core.NewAccountSelector(accountRepo, core.NewConsistentHashRouter())
	recorder := core.NewRequestRecorder(recordRepo, logger)
	recorder.Start()
	t.Cleanup(func() { _ = recorder.Close(context.Background()) })

	// --- fixture: live config publish + proxy with live getter --
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	base := &config.Config{
		Version:   config.SupportedVersion,
		DB:        config.DBConfig{Driver: "sqlite3", URL: ":memory:"},
		Runtime:   config.DefaultRuntimeConfig(),
		Plugins:   config.DefaultPluginsConfig(),
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := config.WriteAtomic(cfgPath, base); err != nil {
		t.Fatalf("seed config.json: %v", err)
	}
	config.Publisher.Store(base)

	proxy := api.NewProxyHandler(selector, recorder, openai.NewClient(5*time.Second),
		false, 0, logger)
	proxy.SetBodyLogFunc(func() (clientReqLog, upstreamReqLog, upstreamRespLog bool) {
		live := config.Reader.Load()
		if live == nil {
			return false, false, false
		}
		return live.Runtime.LogClientRequestBody,
			live.Runtime.LogUpstreamRequestBody,
			live.Runtime.LogUpstreamResponseBody
	})

	// --- fixture: settings handler + real config updater -------
	reader := readerFn(func() *config.Config { return config.Reader.Load() })
	updater := adminapi.NewConfigUpdater(cfgPath, reader, config.Publisher)
	settingsHandler := adminapi.NewSettingsHandler(reader, updater, logger)

	// --- helpers ------------------------------------------------
	fireProxy := func(reqID, payload string) {
		r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(payload))
		r.Header.Set("Authorization", "Bearer sk-test")
		r = r.WithContext(api.WithRequestID(r.Context(), reqID))
		proxy.ServeHTTP(httptest.NewRecorder(), r)
	}
	lastRecord := func(reqID string) domain.RequestRecord {
		// recorder is async; give it a little time.
		deadline := time.Now().Add(2 * time.Second)
		var found domain.RequestRecord
		for time.Now().Before(deadline) {
			recs, err := recordRepo.Query(context.Background(), core.QueryParams{Limit: 50})
			if err == nil {
				for _, rec := range recs {
					if rec.RequestID == reqID {
						return rec
					}
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("no record for request_id=%s (drained window elapsed)", reqID)
		return found
	}
	postSettings := func(body string) {
		r := httptest.NewRequest(http.MethodPost,
			"/api/admin/settings/update",
			bytes.NewReader([]byte(body)))
		r.Header.Set("Content-Type", "application/json")
		r = r.WithContext(api.WithRequestID(r.Context(), "req-settings"))
		w := httptest.NewRecorder()
		settingsHandler.Update(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("settings update: status=%d body=%s", w.Code, w.Body.String())
		}
		var out struct{ Code int }
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if out.Code != 0 {
			t.Fatalf("settings envelope: code=%d body=%s", out.Code, w.Body.String())
		}
	}

	// --- step 1: baseline — all off, no body persisted ----------
	fireProxy("req-1", `{"model":"gpt-5","messages":[]}`)
	r1 := lastRecord("req-1")
	if r1.ClientRequestBody != nil {
		t.Errorf("r1 client_request_body: got %q, want nil (log_client_request_body=false)", *r1.ClientRequestBody)
	}
	if r1.UpstreamRequestBody != nil {
		t.Errorf("r1 upstream_request_body: got %q, want nil (log_upstream_request_body=false)", *r1.UpstreamRequestBody)
	}
	if r1.UpstreamResponseBody != nil {
		t.Errorf("r1 upstream_response_body: got %q, want nil (log_upstream_response_body=false)", *r1.UpstreamResponseBody)
	}

	// --- step 2: enable client request body only ----------------
	postSettings(`{"runtime":{"log_client_request_body":true}}`)
	fireProxy("req-2", `{"model":"gpt-5","messages":[{"role":"user","content":"hot reload works"}]}`)
	r2 := lastRecord("req-2")
	if r2.ClientRequestBody == nil || !strings.Contains(*r2.ClientRequestBody, "hot reload works") {
		t.Errorf("r2 client_request_body: got %v, want captured body", r2.ClientRequestBody)
	}
	if r2.UpstreamRequestBody != nil {
		t.Errorf("r2 upstream_request_body: got %q, want nil (log_upstream_request_body still false)", *r2.UpstreamRequestBody)
	}
	if r2.UpstreamResponseBody != nil {
		t.Errorf("r2 upstream_response_body: got %q, want nil (log_upstream_response_body still false)", *r2.UpstreamResponseBody)
	}

	// --- step 3: enable upstream request body too ---------------
	postSettings(`{"runtime":{"log_upstream_request_body":true}}`)
	fireProxy("req-3", `{"model":"gpt-5","messages":[]}`)
	r3 := lastRecord("req-3")
	if r3.ClientRequestBody == nil {
		t.Errorf("r3 client_request_body: nil, want captured (log_client_request_body still true)")
	}
	if r3.UpstreamRequestBody == nil || !strings.Contains(*r3.UpstreamRequestBody, `"model":"gpt-5"`) {
		t.Errorf("r3 upstream_request_body: got %v, want captured upstream request", r3.UpstreamRequestBody)
	}
	if r3.UpstreamResponseBody != nil {
		t.Errorf("r3 upstream_response_body: got %q, want nil (log_upstream_response_body still false)", *r3.UpstreamResponseBody)
	}

	// --- step 4: enable upstream response body too --------------
	postSettings(`{"runtime":{"log_upstream_response_body":true}}`)
	fireProxy("req-4", `{"model":"gpt-5","messages":[]}`)
	r4 := lastRecord("req-4")
	if r4.ClientRequestBody == nil {
		t.Errorf("r4 client_request_body: nil, want captured")
	}
	if r4.UpstreamRequestBody == nil {
		t.Errorf("r4 upstream_request_body: nil, want captured")
	}
	if r4.UpstreamResponseBody == nil || !strings.Contains(*r4.UpstreamResponseBody, "resp_hot_reload") {
		t.Errorf("r4 upstream_response_body: got %v, want resp_hot_reload", r4.UpstreamResponseBody)
	}

	// --- step 5: disable all -----------------------------------
	postSettings(`{"runtime":{"log_client_request_body":false,"log_upstream_request_body":false,"log_upstream_response_body":false}}`)
	fireProxy("req-5", `{"model":"gpt-5","messages":[]}`)
	r5 := lastRecord("req-5")
	if r5.ClientRequestBody != nil {
		t.Errorf("r5 client_request_body: got %q, want nil after disable", *r5.ClientRequestBody)
	}
	if r5.UpstreamRequestBody != nil {
		t.Errorf("r5 upstream_request_body: got %q, want nil after disable", *r5.UpstreamRequestBody)
	}
	if r5.UpstreamResponseBody != nil {
		t.Errorf("r5 upstream_response_body: got %q, want nil after disable", *r5.UpstreamResponseBody)
	}

	// --- sanity: config.json on disk matches memory --------------
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read cfg: %v", err)
	}
	var disk config.Config
	if err := json.Unmarshal(raw, &disk); err != nil {
		t.Fatalf("decode cfg: %v", err)
	}
	if disk.Runtime.LogClientRequestBody || disk.Runtime.LogUpstreamRequestBody || disk.Runtime.LogUpstreamResponseBody {
		t.Errorf("final disk cfg: runtime body-log still on (%v,%v,%v) — want all off",
			disk.Runtime.LogClientRequestBody,
			disk.Runtime.LogUpstreamRequestBody,
			disk.Runtime.LogUpstreamResponseBody)
	}
	if live := config.Reader.Load(); live == nil ||
		live.Runtime.LogClientRequestBody ||
		live.Runtime.LogUpstreamRequestBody ||
		live.Runtime.LogUpstreamResponseBody {
		t.Errorf("final live cfg: runtime body-log still on")
	}
}

// TestHotReload_LogLevelAndRetention_WrittenToDisk covers the two
// T-400 branches whose observer-side plumbing lands in a later
// iteration (dynamic slog handler + retention tick re-read).
// The WRITE path — and therefore the hot-reload contract that the
// admin API exposes — MUST already be correct in 002.
func TestHotReload_LogLevelAndRetention_WrittenToDisk(t *testing.T) {
	prev := config.Reader.Load()
	t.Cleanup(func() { config.Publisher.Store(prev) })

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	base := &config.Config{
		Version:   config.SupportedVersion,
		DB:        config.DBConfig{Driver: "sqlite3", URL: ":memory:"},
		Runtime:   config.DefaultRuntimeConfig(),
		Plugins:   config.DefaultPluginsConfig(),
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := config.WriteAtomic(cfgPath, base); err != nil {
		t.Fatalf("seed: %v", err)
	}
	config.Publisher.Store(base)

	reader := readerFn(func() *config.Config { return config.Reader.Load() })
	updater := adminapi.NewConfigUpdater(cfgPath, reader, config.Publisher)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := adminapi.NewSettingsHandler(reader, updater, logger)

	post := func(body string) {
		r := httptest.NewRequest(http.MethodPost, "/api/admin/settings/update",
			bytes.NewReader([]byte(body)))
		r.Header.Set("Content-Type", "application/json")
		r = r.WithContext(api.WithRequestID(r.Context(), "req-t400"))
		w := httptest.NewRecorder()
		h.Update(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}

	post(`{"runtime":{"log_level":"debug"}}`)
	if got := config.Reader.Load().Runtime.LogLevel; got != "debug" {
		t.Errorf("live log_level: got %q, want debug", got)
	}
	post(`{"runtime":{"log_retention_days":7}}`)
	if got := config.Reader.Load().Runtime.LogRetentionDays; got != 7 {
		t.Errorf("live log_retention_days: got %d, want 7", got)
	}

	raw, _ := os.ReadFile(cfgPath)
	var disk config.Config
	_ = json.Unmarshal(raw, &disk)
	if disk.Runtime.LogLevel != "debug" {
		t.Errorf("disk log_level: got %q, want debug", disk.Runtime.LogLevel)
	}
	if disk.Runtime.LogRetentionDays != 7 {
		t.Errorf("disk log_retention_days: got %d, want 7", disk.Runtime.LogRetentionDays)
	}
}

// readerFn adapts a closure to adminapi.ConfigReader so this test
// reads through the live atomic slot without importing the fake
// types from settings_test.go (which is in the adminapi package).
type readerFn func() *config.Config

func (f readerFn) Load() *config.Config { return f() }

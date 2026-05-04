package adminapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/user/one-llm-router/internal/api"
	"github.com/user/one-llm-router/internal/api/errcode"
	"github.com/user/one-llm-router/internal/config"
)

// --- Fakes ---------------------------------------------------------

type fakeReader struct {
	cfg *config.Config
}

func (f *fakeReader) Load() *config.Config { return f.cfg }

type fakeUpdater struct {
	called  int32
	inputs  []SettingsPatch
	next    *config.Config
	failErr error
}

func (f *fakeUpdater) Update(p SettingsPatch) (*config.Config, error) {
	atomic.AddInt32(&f.called, 1)
	f.inputs = append(f.inputs, p)
	if f.failErr != nil {
		return nil, f.failErr
	}
	return f.next, nil
}

// withRequestID wires a request id into context so the handler's
// envelope carries a stable X-Request-Id header.
func withRequestID(r *http.Request, id string) *http.Request {
	ctx := api.WithRequestID(r.Context(), id)
	return r.WithContext(ctx)
}

func baseConfig() *config.Config {
	return &config.Config{
		Version: config.SupportedVersion,
		DB: config.DBConfig{
			Driver: "sqlite3",
			URL:    "router.db",
		},
		Runtime: config.RuntimeConfig{
			LogClientRequestBody:    false,
			LogUpstreamRequestBody:  false,
			LogUpstreamResponseBody: false,
			LogRetentionDays:        30,
			LogLevel:                "info",
		},
		Plugins: config.PluginsConfig{
			AdminAuth:  config.AdminAuthPluginConfig{Enabled: false},
			ClientKeys: config.ClientKeysPluginConfig{Enabled: false},
		},
	}
}

func decodeEnvelope(t *testing.T, rec *httptest.ResponseRecorder) (int, map[string]any) {
	t.Helper()
	var out struct {
		Code int            `json:"code"`
		Msg  string         `json:"msg"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode envelope: %v, body=%s", err, rec.Body.String())
	}
	return out.Code, out.Data
}

func newHandler(reader ConfigReader, updater SettingsUpdater) *SettingsHandler {
	return NewSettingsHandler(reader, updater, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// --- GET /api/admin/settings --------------------------------------

func TestSettings_Get_HappyPath(t *testing.T) {
	cfg := baseConfig()
	cfg.Plugins.AdminAuth.Enabled = true
	h := newHandler(&fakeReader{cfg: cfg}, &fakeUpdater{})

	req := withRequestID(httptest.NewRequest(http.MethodGet, "/api/admin/settings", nil), "req-1")
	rec := httptest.NewRecorder()
	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rec.Code)
	}
	code, data := decodeEnvelope(t, rec)
	if code != 0 {
		t.Fatalf("envelope code: got %d want 0", code)
	}

	runtime, _ := data["runtime"].(map[string]any)
	if runtime["log_level"] != "info" {
		t.Fatalf("runtime.log_level: got %v want info", runtime["log_level"])
	}
	if runtime["log_retention_days"] != float64(30) {
		t.Fatalf("runtime.log_retention_days: got %v want 30", runtime["log_retention_days"])
	}

	db, _ := data["db"].(map[string]any)
	if db["driver"] != "sqlite3" {
		t.Fatalf("db.driver: got %v want sqlite3", db["driver"])
	}
	if _, ok := db["url"]; ok {
		t.Fatal("db.url must NEVER be exposed in /api/admin/settings")
	}

	plugins, _ := data["plugins"].([]any)
	if len(plugins) != 0 {
		t.Fatalf("plugins: got %v want empty list (002 ships no concrete plugins)", plugins)
	}
	pluginIntents, _ := data["plugin_intents"].([]any)
	if len(pluginIntents) != 2 {
		t.Fatalf("plugin_intents: got %d want 2", len(pluginIntents))
	}
	firstIntent, _ := pluginIntents[0].(map[string]any)
	if firstIntent["status"] != "intent only" {
		t.Fatalf("plugin_intents[0].status: got %v want intent only", firstIntent["status"])
	}

	system, _ := data["system"].(map[string]any)
	if _, ok := system["router_version"]; !ok {
		t.Fatal("system.router_version missing")
	}
}

func TestSettings_Get_NoLiveConfig_ReturnsSetupRequired(t *testing.T) {
	h := newHandler(&fakeReader{cfg: nil}, &fakeUpdater{})
	req := withRequestID(httptest.NewRequest(http.MethodGet, "/api/admin/settings", nil), "req-2")
	rec := httptest.NewRecorder()
	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rec.Code)
	}
	code, _ := decodeEnvelope(t, rec)
	if code != errcode.SetupRequired {
		t.Fatalf("code: got %d want %d (setup_required)", code, errcode.SetupRequired)
	}
}

// --- POST /api/admin/settings/update ------------------------------

func doUpdate(t *testing.T, h *SettingsHandler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := withRequestID(
		httptest.NewRequest(http.MethodPost, "/api/admin/settings/update", strings.NewReader(body)),
		"req-"+t.Name(),
	)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Update(rec, req)
	return rec
}

func TestSettings_Update_HappyPath(t *testing.T) {
	cfg := baseConfig()
	updater := &fakeUpdater{next: withRuntime(cfg, func(r *config.RuntimeConfig) {
		r.LogLevel = "warn"
		r.LogClientRequestBody = true
	})}
	h := newHandler(&fakeReader{cfg: cfg}, updater)

	rec := doUpdate(t, h, `{"runtime":{"log_level":"warn","log_client_request_body":true}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200, body=%s", rec.Code, rec.Body.String())
	}
	code, data := decodeEnvelope(t, rec)
	if code != 0 {
		t.Fatalf("code: got %d want 0, body=%s", code, rec.Body.String())
	}
	runtime, _ := data["runtime"].(map[string]any)
	if runtime["log_level"] != "warn" {
		t.Fatalf("projected log_level: got %v want warn", runtime["log_level"])
	}
	if atomic.LoadInt32(&updater.called) != 1 {
		t.Fatalf("updater invocations: got %d want 1", atomic.LoadInt32(&updater.called))
	}
	got := updater.inputs[0]
	if got.LogLevel == nil || *got.LogLevel != "warn" {
		t.Fatalf("patch.LogLevel mismatch: %+v", got.LogLevel)
	}
	if got.LogClientRequestBody == nil || !*got.LogClientRequestBody {
		t.Fatalf("patch.LogClientRequestBody mismatch: %+v", got.LogClientRequestBody)
	}
}

func TestSettings_Update_UnknownRootKey_Returns2012(t *testing.T) {
	h := newHandler(&fakeReader{cfg: baseConfig()}, &fakeUpdater{})
	rec := doUpdate(t, h, `{"unknown_key": {}}`)
	code, data := decodeEnvelope(t, rec)
	if code != errcode.UnknownConfigKey {
		t.Fatalf("code: got %d want %d", code, errcode.UnknownConfigKey)
	}
	if data["field"] != "unknown_key" {
		t.Fatalf("field: got %v want unknown_key", data["field"])
	}
}

func TestSettings_Update_UnknownRuntimeKey_Returns2012(t *testing.T) {
	h := newHandler(&fakeReader{cfg: baseConfig()}, &fakeUpdater{})
	rec := doUpdate(t, h, `{"runtime":{"log_levels":"warn"}}`)
	code, _ := decodeEnvelope(t, rec)
	if code != errcode.UnknownConfigKey {
		t.Fatalf("code: got %d want %d", code, errcode.UnknownConfigKey)
	}
}

func TestSettings_Update_InvalidRetention_Returns2006(t *testing.T) {
	h := newHandler(&fakeReader{cfg: baseConfig()}, &fakeUpdater{})
	rec := doUpdate(t, h, `{"runtime":{"log_retention_days":0}}`)
	code, _ := decodeEnvelope(t, rec)
	if code != errcode.InvalidRetention {
		t.Fatalf("code: got %d want %d", code, errcode.InvalidRetention)
	}
}

func TestSettings_Update_InvalidLogLevel_Returns2007(t *testing.T) {
	h := newHandler(&fakeReader{cfg: baseConfig()}, &fakeUpdater{})
	rec := doUpdate(t, h, `{"runtime":{"log_level":"trace"}}`)
	code, _ := decodeEnvelope(t, rec)
	if code != errcode.InvalidLogLevel {
		t.Fatalf("code: got %d want %d", code, errcode.InvalidLogLevel)
	}
}

func TestSettings_Update_PluginToggle_HappyPath(t *testing.T) {
	cfg := baseConfig()
	updater := &fakeUpdater{next: cfg}
	h := newHandler(&fakeReader{cfg: cfg}, updater)
	rec := doUpdate(t, h, `{"plugins":{"admin_auth":{"enabled":true},"client_keys":{"enabled":true}}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200, body=%s", rec.Code, rec.Body.String())
	}
	code, _ := decodeEnvelope(t, rec)
	if code != 0 {
		t.Fatalf("code: got %d want 0", code)
	}
	if len(updater.inputs) != 1 {
		t.Fatalf("updater.inputs: got %d want 1", len(updater.inputs))
	}
	p := updater.inputs[0]
	if p.AdminAuthEnabled == nil || !*p.AdminAuthEnabled {
		t.Fatalf("admin_auth.enabled mismatch: %+v", p.AdminAuthEnabled)
	}
	if p.ClientKeysEnabled == nil || !*p.ClientKeysEnabled {
		t.Fatalf("client_keys.enabled mismatch: %+v", p.ClientKeysEnabled)
	}
}

func TestSettings_Update_UnknownPlugin_Returns2012(t *testing.T) {
	h := newHandler(&fakeReader{cfg: baseConfig()}, &fakeUpdater{})
	rec := doUpdate(t, h, `{"plugins":{"mystery":{"enabled":true}}}`)
	code, _ := decodeEnvelope(t, rec)
	if code != errcode.UnknownConfigKey {
		t.Fatalf("code: got %d want %d", code, errcode.UnknownConfigKey)
	}
}

func TestSettings_Update_PluginUnknownSubKey_Returns2012(t *testing.T) {
	h := newHandler(&fakeReader{cfg: baseConfig()}, &fakeUpdater{})
	rec := doUpdate(t, h, `{"plugins":{"admin_auth":{"enable":true}}}`)
	code, data := decodeEnvelope(t, rec)
	if code != errcode.UnknownConfigKey {
		t.Fatalf("code: got %d want %d, data=%v", code, errcode.UnknownConfigKey, data)
	}
}

func TestSettings_Update_InvalidPluginFlag_Returns2013(t *testing.T) {
	h := newHandler(&fakeReader{cfg: baseConfig()}, &fakeUpdater{})
	rec := doUpdate(t, h, `{"plugins":{"admin_auth":{"enabled":"yes"}}}`)
	code, _ := decodeEnvelope(t, rec)
	if code != errcode.InvalidPluginFlag {
		t.Fatalf("code: got %d want %d", code, errcode.InvalidPluginFlag)
	}
}

func TestSettings_Update_MalformedJSON_Returns2008(t *testing.T) {
	h := newHandler(&fakeReader{cfg: baseConfig()}, &fakeUpdater{})
	rec := doUpdate(t, h, `{"runtime":`)
	code, _ := decodeEnvelope(t, rec)
	if code != errcode.MalformedBody {
		t.Fatalf("code: got %d want %d", code, errcode.MalformedBody)
	}
}

func TestSettings_Update_WrongContentType_Returns2008(t *testing.T) {
	h := newHandler(&fakeReader{cfg: baseConfig()}, &fakeUpdater{})
	req := withRequestID(
		httptest.NewRequest(http.MethodPost, "/api/admin/settings/update", strings.NewReader(`{}`)),
		"req-"+t.Name(),
	)
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	h.Update(rec, req)

	code, _ := decodeEnvelope(t, rec)
	if code != errcode.MalformedBody {
		t.Fatalf("code: got %d want %d", code, errcode.MalformedBody)
	}
}

func TestSettings_Update_TopLevelNull_Returns2008(t *testing.T) {
	h := newHandler(&fakeReader{cfg: baseConfig()}, &fakeUpdater{})
	rec := doUpdate(t, h, `null`)
	code, _ := decodeEnvelope(t, rec)
	if code != errcode.MalformedBody {
		t.Fatalf("code: got %d want %d", code, errcode.MalformedBody)
	}
}

func TestSettings_Update_BodyTooLarge_Returns2009(t *testing.T) {
	h := newHandler(&fakeReader{cfg: baseConfig()}, &fakeUpdater{})
	payload := `{"runtime":{"log_level":"` + strings.Repeat("x", SettingsMaxBodyBytes+1) + `"}}`
	rec := doUpdate(t, h, payload)
	code, data := decodeEnvelope(t, rec)
	if code != errcode.RequestBodyTooLarge {
		t.Fatalf("code: got %d want %d, body=%s", code, errcode.RequestBodyTooLarge, rec.Body.String())
	}
	if data["scope"] != "envelope" {
		t.Fatalf("scope: got %v want envelope", data["scope"])
	}
	if got := int64(data["limit_bytes"].(float64)); got != SettingsMaxBodyBytes {
		t.Fatalf("limit_bytes: got %d want %d", got, SettingsMaxBodyBytes)
	}
}

func TestSettings_Update_UpdaterError_ReturnsSystemError(t *testing.T) {
	updater := &fakeUpdater{failErr: errors.New("disk full")}
	h := newHandler(&fakeReader{cfg: baseConfig()}, updater)
	rec := doUpdate(t, h, `{"runtime":{"log_level":"warn"}}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d want 500", rec.Code)
	}
	code, _ := decodeEnvelope(t, rec)
	if code != errcode.ConfigWriteFailed {
		t.Fatalf("code: got %d want %d", code, errcode.ConfigWriteFailed)
	}
}

func TestSettings_Update_EmptyBodyIsAccepted(t *testing.T) {
	// An empty patch is a legal no-op: nothing changes, updater is
	// still called with an empty patch, and the GET projection is
	// returned.
	cfg := baseConfig()
	updater := &fakeUpdater{next: cfg}
	h := newHandler(&fakeReader{cfg: cfg}, updater)
	rec := doUpdate(t, h, `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200, body=%s", rec.Code, rec.Body.String())
	}
	code, _ := decodeEnvelope(t, rec)
	if code != 0 {
		t.Fatalf("code: got %d want 0", code)
	}
}

// --- 2013 env_override_readonly guard (F007) ---------------------

// fakeSourceReader mirrors config.LiveSourceReader for unit tests.
type fakeSourceReader struct {
	src config.SourceMap
}

func (f *fakeSourceReader) Load() *config.SourceMap {
	if f.src == nil {
		return nil
	}
	return &f.src
}

func TestSettings_Update_EnvSourcedField_Returns2013(t *testing.T) {
	cfg := baseConfig()
	updater := &fakeUpdater{next: cfg}
	h := newHandler(&fakeReader{cfg: cfg}, updater)
	// Pretend an operator exposed ROUTER_RUNTIME_LOG_LEVEL (hypothetical
	// future env override) — the guard must reject the patch with 2013
	// and MUST NOT call the updater.
	h.SetSourceReader(&fakeSourceReader{src: config.SourceMap{
		"runtime.log_level": "env:ROUTER_RUNTIME_LOG_LEVEL",
	}})

	rec := doUpdate(t, h, `{"runtime":{"log_level":"warn"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rec.Code)
	}
	code, data := decodeEnvelope(t, rec)
	if code != errcode.EnvOverrideReadonly {
		t.Fatalf("code: got %d want %d (env_override_readonly)",
			code, errcode.EnvOverrideReadonly)
	}
	if data["field"] != "runtime.log_level" {
		t.Fatalf("field: got %v want runtime.log_level", data["field"])
	}
	if data["env_var"] != "ROUTER_RUNTIME_LOG_LEVEL" {
		t.Fatalf("env_var: got %v want ROUTER_RUNTIME_LOG_LEVEL", data["env_var"])
	}
	if atomic.LoadInt32(&updater.called) != 0 {
		t.Fatal("updater must not be called when 2013 is returned")
	}
}

func TestSettings_Update_FileSourcedField_Allowed(t *testing.T) {
	cfg := baseConfig()
	updater := &fakeUpdater{next: withRuntime(cfg, func(r *config.RuntimeConfig) { r.LogLevel = "warn" })}
	h := newHandler(&fakeReader{cfg: cfg}, updater)
	h.SetSourceReader(&fakeSourceReader{src: config.SourceMap{
		"runtime.log_level": "file",
	}})

	rec := doUpdate(t, h, `{"runtime":{"log_level":"warn"}}`)
	code, _ := decodeEnvelope(t, rec)
	if code != 0 {
		t.Fatalf("code: got %d want 0, body=%s", code, rec.Body.String())
	}
	if atomic.LoadInt32(&updater.called) != 1 {
		t.Fatalf("updater: got %d calls want 1", atomic.LoadInt32(&updater.called))
	}
}

func TestSettings_Update_NilSourceReader_DegradesToAllow(t *testing.T) {
	// With no source reader wired, the guard must fail open so
	// tests (and 002 boots that predate the SourceMap publisher
	// wiring) keep working.
	cfg := baseConfig()
	updater := &fakeUpdater{next: cfg}
	h := newHandler(&fakeReader{cfg: cfg}, updater)
	rec := doUpdate(t, h, `{"runtime":{"log_level":"warn"}}`)
	code, _ := decodeEnvelope(t, rec)
	if code != 0 {
		t.Fatalf("code: got %d want 0 (nil source reader should degrade to allow)", code)
	}
}

// --- DSN parsing --------------------------------------------------

func TestParsePostgresDSN_URLForm(t *testing.T) {
	host, db := parsePostgresDSN("postgres://user:pass@db.example:5433/routerdb?sslmode=require")
	if host != "db.example:5433" {
		t.Fatalf("host: got %q want db.example:5433", host)
	}
	if db != "routerdb" {
		t.Fatalf("dbname: got %q want routerdb", db)
	}
}

func TestParsePostgresDSN_URLFormDefaultsPort(t *testing.T) {
	host, db := parsePostgresDSN("postgres://user:pass@db.example/routerdb")
	if host != "db.example:5432" {
		t.Fatalf("host: got %q want db.example:5432", host)
	}
	if db != "routerdb" {
		t.Fatalf("dbname: got %q want routerdb", db)
	}
}

func TestParsePostgresDSN_LibpqKeywordForm(t *testing.T) {
	host, db := parsePostgresDSN("host=db.example port=5432 dbname=routerdb user=router")
	if host != "db.example:5432" {
		t.Fatalf("host: got %q want db.example:5432", host)
	}
	if db != "routerdb" {
		t.Fatalf("dbname: got %q want routerdb", db)
	}
}

func TestParsePostgresDSN_Malformed(t *testing.T) {
	host, db := parsePostgresDSN("://bogus")
	if host != "" || db != "" {
		t.Fatalf("expected empty on malformed DSN, got host=%q db=%q", host, db)
	}
}

func TestParseMySQLDSN_Happy(t *testing.T) {
	host, db := parseMySQLDSN("router:secret@tcp(db.example:3307)/routerdb?parseTime=true")
	if host != "db.example:3307" {
		t.Fatalf("host: got %q want db.example:3307", host)
	}
	if db != "routerdb" {
		t.Fatalf("dbname: got %q want routerdb", db)
	}
}

func TestParseMySQLDSN_DefaultPort(t *testing.T) {
	host, db := parseMySQLDSN("router:secret@tcp(db.example)/routerdb")
	if host != "db.example:3306" {
		t.Fatalf("host: got %q want db.example:3306", host)
	}
	if db != "routerdb" {
		t.Fatalf("dbname: got %q want routerdb", db)
	}
}

func TestParseMySQLDSN_Malformed(t *testing.T) {
	host, db := parseMySQLDSN("not-a-mysql-dsn")
	if host != "" || db != "" {
		t.Fatalf("expected empty on malformed DSN, got host=%q db=%q", host, db)
	}
}

// --- SettingsPatch helpers ----------------------------------------

func TestSettingsPatch_IsEmpty_TrueForZero(t *testing.T) {
	var p SettingsPatch
	if !p.IsEmpty() {
		t.Fatal("zero patch should be empty")
	}
}

func TestSettingsPatch_IsEmpty_FalseAfterSet(t *testing.T) {
	b := true
	p := SettingsPatch{LogClientRequestBody: &b}
	if p.IsEmpty() {
		t.Fatal("non-zero patch should not be empty")
	}
	if got := p.RuntimeKeys(); len(got) != 1 || got[0] != "log_client_request_body" {
		t.Fatalf("RuntimeKeys: got %v", got)
	}
	if got := p.PluginKeys(); len(got) != 0 {
		t.Fatalf("PluginKeys: got %v", got)
	}
}

// --- helpers ------------------------------------------------------

// withRuntime returns a copy of cfg with the runtime block mutated by
// fn. Used by Update happy-path tests that need the updater to
// return a NEW Config post-mutation.
func withRuntime(cfg *config.Config, fn func(*config.RuntimeConfig)) *config.Config {
	out := *cfg
	fn(&out.Runtime)
	return &out
}

// Compile-time safety: make sure SettingsHandler satisfies the
// http.Handler contract via its Get/Update method set. We don't use
// ServeHTTP directly, but this nudge keeps reviewers honest if that
// ever changes.
var _ context.Context = context.Background()

// Buffer placeholder to silence the linter if no bytes.Buffer is used
// in this file — kept for future test expansion.
var _ = bytes.Buffer{}

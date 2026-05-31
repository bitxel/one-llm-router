package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// captureLogsLoader mirrors plugins_test.go#captureLogsConfig but is
// kept local so the two test files never depend on each other's
// ordering (Go runs test files in lexical order, which is OK, but
// explicit > implicit). Captures at DEBUG level for broad visibility.
func captureLogsLoader(t *testing.T, fn func()) string {
	t.Helper()
	return captureLogsLoaderAtLevel(t, slog.LevelDebug, fn)
}

// captureLogsLoaderAtLevel is the level-parameterized variant. The
// N-002 tests use LevelInfo to prove path scrubbing at INFO+ without
// tripping over DEBUG lines that are allowed to carry the path per
// plan.md §362.
func captureLogsLoaderAtLevel(t *testing.T, level slog.Level, fn func()) string {
	t.Helper()
	orig := slog.Default()
	t.Cleanup(func() { slog.SetDefault(orig) })

	var (
		buf bytes.Buffer
		mu  sync.Mutex
	)
	handler := slog.NewTextHandler(&syncedWriter{w: &buf, mu: &mu}, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(handler))
	fn()
	mu.Lock()
	defer mu.Unlock()
	return buf.String()
}

type syncedWriter struct {
	w  *bytes.Buffer
	mu *sync.Mutex
}

func (s *syncedWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// goodConfigBytes returns a minimal-but-complete config.json marshaling
// the full supported schema (all 10 tracked keys present).
func goodConfigBytes(t *testing.T) []byte {
	t.Helper()
	cfg := Config{
		Version: 1,
		DB:      DBConfig{Driver: "sqlite3", URL: "file:router.db"},
		Runtime: RuntimeConfig{
			LogClientRequestBody:    false,
			LogUpstreamRequestBody:  false,
			LogUpstreamResponseBody: false,
			LogRetentionDays:        30,
			LogLevel:                "info",
			ModelRenames:            []ModelRenameRule{},
		},
		Plugins: DefaultPluginsConfig(),
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal good config: %v", err)
	}
	return out
}

func writeFile(t *testing.T, path string, data []byte, perm os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, perm); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestLoad_FileNotFound_ReturnsErrNoConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.json")

	cfg, src, err := Load(context.Background(), path, MapEnv(nil))
	if !errors.Is(err, ErrNoConfig) {
		t.Fatalf("err = %v, want ErrNoConfig", err)
	}
	if cfg != nil {
		t.Errorf("cfg = %+v, want nil on ErrNoConfig", cfg)
	}
	if src != nil {
		t.Errorf("src = %+v, want nil on ErrNoConfig", src)
	}
}

func TestLoad_Directory_ReturnsDescriptiveError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	cfg, src, err := Load(context.Background(), dir, MapEnv(nil))
	if err == nil {
		t.Fatal("err = nil, want descriptive error")
	}
	if errors.Is(err, ErrNoConfig) {
		t.Errorf("err = ErrNoConfig, want unrelated directory error")
	}
	if cfg != nil || src != nil {
		t.Errorf("cfg=%v src=%v, want both nil on error", cfg, src)
	}
}

func TestLoad_MalformedJSON_ReturnsWrappedError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeFile(t, path, []byte("{not-json"), 0o600)

	cfg, src, err := Load(context.Background(), path, MapEnv(nil))
	if err == nil {
		t.Fatal("err = nil, want parse error")
	}
	if errors.Is(err, ErrNoConfig) || errors.Is(err, ErrUnsupportedVersion) {
		t.Errorf("err = %v, want unrelated parse error", err)
	}
	if cfg != nil || src != nil {
		t.Errorf("cfg=%v src=%v, want both nil on parse error", cfg, src)
	}
}

func TestLoad_UnsupportedVersion(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
	}{
		{"missing_version", `{"db":{"driver":"sqlite3","url":"file:x"}}`},
		{"zero_version", `{"version":0,"db":{"driver":"sqlite3","url":"file:x"}}`},
		{"future_version", `{"version":2,"db":{"driver":"sqlite3","url":"file:x"}}`},
		{"negative_version", `{"version":-1,"db":{"driver":"sqlite3","url":"file:x"}}`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "config.json")
			writeFile(t, path, []byte(tc.body), 0o600)

			_, _, err := Load(context.Background(), path, MapEnv(nil))
			if !errors.Is(err, ErrUnsupportedVersion) {
				t.Fatalf("err = %v, want ErrUnsupportedVersion", err)
			}
		})
	}
}

func TestLoad_HappyPath_AllFieldsFileSourced(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeFile(t, path, goodConfigBytes(t), 0o600)

	cfg, src, err := Load(context.Background(), path, MapEnv(nil))
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if cfg == nil {
		t.Fatal("cfg = nil, want non-nil")
	}

	wantSources := map[string]string{
		"db.driver":                          "file",
		"db.url":                             "file",
		"runtime.log_client_request_body":    "file",
		"runtime.log_upstream_request_body":  "file",
		"runtime.log_upstream_response_body": "file",
		"runtime.log_retention_days":         "file",
		"runtime.log_level":                  "file",
		"runtime.model_renames":              "file",
		"plugins.admin_auth.enabled":         "file",
		"plugins.client_keys.enabled":        "file",
	}
	if len(src) != len(wantSources) {
		t.Errorf("source map size = %d, want %d; full=%#v", len(src), len(wantSources), src)
	}
	for k, want := range wantSources {
		if got := src[k]; got != want {
			t.Errorf("src[%q] = %q, want %q", k, got, want)
		}
	}

	if cfg.DB.Driver != "sqlite3" {
		t.Errorf("DB.Driver = %q, want sqlite3", cfg.DB.Driver)
	}
	if cfg.Runtime.LogLevel != "info" {
		t.Errorf("Runtime.LogLevel = %q, want info", cfg.Runtime.LogLevel)
	}
	if cfg.Runtime.LogRetentionDays != 30 {
		t.Errorf("Runtime.LogRetentionDays = %d, want 30", cfg.Runtime.LogRetentionDays)
	}
}

func TestLoad_MissingRuntimeKeys_UsesDefaults(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	// Only db + version; runtime block missing entirely.
	writeFile(t, path, []byte(`{"version":1,"db":{"driver":"sqlite3","url":"file:r.db"}}`), 0o600)

	cfg, src, err := Load(context.Background(), path, MapEnv(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Runtime.LogRetentionDays != 30 {
		t.Errorf("Runtime.LogRetentionDays = %d, want 30 (default)", cfg.Runtime.LogRetentionDays)
	}
	if cfg.Runtime.LogLevel != "info" {
		t.Errorf("Runtime.LogLevel = %q, want info (default)", cfg.Runtime.LogLevel)
	}
	for _, key := range []string{
		"runtime.log_client_request_body",
		"runtime.log_upstream_request_body",
		"runtime.log_upstream_response_body",
		"runtime.log_retention_days",
		"runtime.log_level",
		"runtime.model_renames",
	} {
		if src[key] != "default" {
			t.Errorf("src[%q] = %q, want default", key, src[key])
		}
	}
}

func TestLoad_MissingPluginBlock_UsesDefaults(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeFile(t, path, []byte(`{"version":1,"db":{"driver":"sqlite3","url":"file:r.db"},"runtime":{"log_client_request_body":false,"log_upstream_request_body":false,"log_upstream_response_body":false,"log_retention_days":7,"log_level":"warn"}}`), 0o600)

	cfg, src, err := Load(context.Background(), path, MapEnv(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Plugins.AdminAuth.Enabled || cfg.Plugins.ClientKeys.Enabled {
		t.Errorf("plugin flags should default to false: %+v", cfg.Plugins)
	}
	if src["plugins.admin_auth.enabled"] != "default" {
		t.Errorf("src[plugins.admin_auth.enabled] = %q, want default", src["plugins.admin_auth.enabled"])
	}
	if src["plugins.client_keys.enabled"] != "default" {
		t.Errorf("src[plugins.client_keys.enabled] = %q, want default", src["plugins.client_keys.enabled"])
	}
	// Runtime values read from file.
	if cfg.Runtime.LogRetentionDays != 7 {
		t.Errorf("Runtime.LogRetentionDays = %d, want 7 (file-sourced)", cfg.Runtime.LogRetentionDays)
	}
	if src["runtime.log_retention_days"] != "file" {
		t.Errorf("src[runtime.log_retention_days] = %q, want file", src["runtime.log_retention_days"])
	}
}

func TestLoad_EnvOverride_DBDriverAndURL(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeFile(t, path, goodConfigBytes(t), 0o600)

	env := MapEnv(map[string]string{
		"ROUTER_DB_DRIVER": "postgres",
		"ROUTER_DB_URL":    "postgres://user:pwd@db:5432/router?sslmode=disable",
	})
	cfg, src, err := Load(context.Background(), path, env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DB.Driver != "postgres" {
		t.Errorf("DB.Driver = %q, want postgres (env override)", cfg.DB.Driver)
	}
	if !strings.HasPrefix(cfg.DB.URL, "postgres://") {
		t.Errorf("DB.URL = %q, want env-sourced postgres DSN", cfg.DB.URL)
	}
	if src["db.driver"] != "env:ROUTER_DB_DRIVER" {
		t.Errorf("src[db.driver] = %q, want env:ROUTER_DB_DRIVER", src["db.driver"])
	}
	if src["db.url"] != "env:ROUTER_DB_URL" {
		t.Errorf("src[db.url] = %q, want env:ROUTER_DB_URL", src["db.url"])
	}
}

func TestLoad_EnvOverride_EmptyStringTreatedAsUnset(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeFile(t, path, goodConfigBytes(t), 0o600)

	// Operator accidentally set the variable to empty (e.g.
	// `ROUTER_DB_DRIVER=` in a script). Behaviour: treat as unset
	// rather than blanking the config field.
	env := MapEnv(map[string]string{"ROUTER_DB_DRIVER": "", "ROUTER_DB_URL": ""})
	cfg, src, err := Load(context.Background(), path, env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DB.Driver != "sqlite3" {
		t.Errorf("DB.Driver = %q, want sqlite3 (empty env ignored)", cfg.DB.Driver)
	}
	if src["db.driver"] != "file" {
		t.Errorf("src[db.driver] = %q, want file (empty env ignored)", src["db.driver"])
	}
}

func TestLoad_EnvOverride_IgnoredVariables_WarnButNoMutation(t *testing.T) {
	// Not parallel: inspects default slog handler.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeFile(t, path, goodConfigBytes(t), 0o600)

	env := MapEnv(map[string]string{
		"ROUTER_LOG_LEVEL":               "debug",
		"ROUTER_ADMIN_AUTH_ENABLED":      "true",
		"ROUTER_LOG_CLIENT_REQUEST_BODY": "yes",
	})

	var (
		cfg *Config
		src SourceMap
	)
	logs := captureLogsLoader(t, func() {
		var err error
		cfg, src, err = Load(context.Background(), path, env)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
	})

	if cfg.Runtime.LogLevel != "info" {
		t.Errorf("Runtime.LogLevel = %q, want info (env override ignored)", cfg.Runtime.LogLevel)
	}
	if cfg.Plugins.AdminAuth.Enabled {
		t.Error("AdminAuth.Enabled = true, want false (env override ignored)")
	}
	if cfg.Runtime.LogClientRequestBody {
		t.Error("Runtime.LogClientRequestBody = true, want false (env override ignored)")
	}

	if src["runtime.log_level"] != "file" {
		t.Errorf("src[runtime.log_level] = %q, want file", src["runtime.log_level"])
	}
	if src["plugins.admin_auth.enabled"] != "file" {
		t.Errorf("src[plugins.admin_auth.enabled] = %q, want file", src["plugins.admin_auth.enabled"])
	}

	for _, envVar := range []string{"ROUTER_LOG_LEVEL", "ROUTER_ADMIN_AUTH_ENABLED", "ROUTER_LOG_CLIENT_REQUEST_BODY"} {
		if !strings.Contains(logs, envVar) {
			t.Errorf("log output missing warning for %q; logs=%s", envVar, logs)
		}
	}
}

func TestLoad_WidePermissions_WarnsButSucceeds(t *testing.T) {
	// Not parallel: inspects default slog handler.
	if os.Getuid() == 0 {
		t.Skip("skipping perm test when running as root: umask semantics differ")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeFile(t, path, goodConfigBytes(t), 0o644)

	var err error
	logs := captureLogsLoader(t, func() {
		_, _, err = Load(context.Background(), path, MapEnv(nil))
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(logs, "wider than 0600") {
		t.Errorf("expected perm warn log; got %s", logs)
	}
}

func TestLoad_TightPermissions_NoWarn(t *testing.T) {
	// Not parallel: inspects default slog handler.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeFile(t, path, goodConfigBytes(t), 0o600)

	var err error
	logs := captureLogsLoader(t, func() {
		_, _, err = Load(context.Background(), path, MapEnv(nil))
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if strings.Contains(logs, "wider than 0600") {
		t.Errorf("unexpected perm warn for 0600 file: %s", logs)
	}
}

func TestLoad_ContextCancelled_ReturnsErr(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeFile(t, path, goodConfigBytes(t), 0o600)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := Load(ctx, path, MapEnv(nil))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestLoad_NilEnv_FallsBackToOSLookup(t *testing.T) {
	// Not parallel: mutates process environment.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeFile(t, path, goodConfigBytes(t), 0o600)

	const key = "ROUTER_DB_DRIVER"
	oldVal, hadOld := os.LookupEnv(key)
	if err := os.Setenv(key, "postgres"); err != nil {
		t.Fatalf("Setenv: %v", err)
	}
	t.Cleanup(func() {
		if hadOld {
			_ = os.Setenv(key, oldVal)
		} else {
			_ = os.Unsetenv(key)
		}
	})

	cfg, src, err := Load(context.Background(), path, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DB.Driver != "postgres" {
		t.Errorf("DB.Driver = %q, want postgres (nil Env should fallback to os.LookupEnv)", cfg.DB.Driver)
	}
	if src["db.driver"] != "env:ROUTER_DB_DRIVER" {
		t.Errorf("src[db.driver] = %q, want env-sourced", src["db.driver"])
	}
}

func TestLoad_UnknownPluginIDInFile_DoesNotBreakParse(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	// File contains a plugin ID the 002 struct does not know about
	// (prometheus is ROADMAP but not shipped). Permissive decode means
	// the extra block is dropped silently; known plugins remain.
	body := `{"version":1,"db":{"driver":"sqlite3","url":"file:r.db"},"runtime":{"log_client_request_body":false,"log_upstream_request_body":false,"log_upstream_response_body":false,"log_retention_days":30,"log_level":"info"},"plugins":{"admin_auth":{"enabled":true},"client_keys":{"enabled":false},"prometheus":{"enabled":true}}}`
	writeFile(t, path, []byte(body), 0o600)

	cfg, src, err := Load(context.Background(), path, MapEnv(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Plugins.AdminAuth.Enabled {
		t.Error("AdminAuth.Enabled = false, want true")
	}
	// Known plugins sourced from file.
	if src["plugins.admin_auth.enabled"] != "file" {
		t.Errorf("src[plugins.admin_auth.enabled] = %q, want file", src["plugins.admin_auth.enabled"])
	}
	if src["plugins.client_keys.enabled"] != "file" {
		t.Errorf("src[plugins.client_keys.enabled] = %q, want file", src["plugins.client_keys.enabled"])
	}
	// The unknown plugin should NOT leak into the source map.
	for k := range src {
		if strings.HasPrefix(k, "plugins.prometheus") {
			t.Errorf("source map leaked unknown plugin key: %q", k)
		}
	}
}

func TestLoad_PluginBlockNonObject_TreatedAsAbsent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	// A future-schema attack vector: plugins.admin_auth is an array
	// instead of an object. Permissive decode drops it; struct stays
	// at zero value.
	body := `{"version":1,"db":{"driver":"sqlite3","url":"file:r.db"},"runtime":{"log_client_request_body":false,"log_upstream_request_body":false,"log_upstream_response_body":false,"log_retention_days":30,"log_level":"info"},"plugins":{"admin_auth":["enabled"],"client_keys":{"enabled":false}}}`
	writeFile(t, path, []byte(body), 0o600)

	_, _, err := Load(context.Background(), path, MapEnv(nil))
	// Go's json.Unmarshal into struct will error when type mismatches
	// (array into object). We expect a wrapped parse error — NOT a
	// silent success — because the file is clearly malformed.
	if err == nil {
		t.Fatal("err = nil, want parse error for malformed plugin block")
	}
	if errors.Is(err, ErrNoConfig) || errors.Is(err, ErrUnsupportedVersion) {
		t.Errorf("err = %v, want generic parse error", err)
	}
}

func TestLoad_SourceMap_HasExactlyTenKeys(t *testing.T) {
	// Forward-compat safety net: if 003 adds a runtime key or plugin
	// and forgets to update Load(), this test will fail. Keep this
	// assertion even though it duplicates coverage from the happy-path
	// test — the intent ("exactly N keys") is testable here.
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeFile(t, path, goodConfigBytes(t), 0o600)

	_, src, err := Load(context.Background(), path, MapEnv(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	const wantKeys = 10
	if got := len(src); got != wantKeys {
		dump, _ := json.MarshalIndent(src, "", "  ")
		t.Errorf("SourceMap has %d keys, want %d; dump=%s", got, wantKeys, dump)
	}
}

// TestLoad_UnreadableParent_DoesNotLeakConfigPath confirms the F-003
// fix at the Load level: an EACCES on the parent must surface as an
// error whose Error() does NOT contain the config.json path. Uses
// chmod 0o000 on a child directory so only the stat syscall sees the
// denial (TempDir cleanup handles the chmod restore for us via
// t.Cleanup).
func TestLoad_UnreadableParent_DoesNotLeakConfigPath(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses chmod denial — scenario not exercisable")
	}
	t.Parallel()

	parent := t.TempDir()
	locked := filepath.Join(parent, "locked-subdir")
	if err := os.Mkdir(locked, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cfgPath := filepath.Join(locked, "config.json")
	writeFile(t, cfgPath, goodConfigBytes(t), 0o600)

	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	_, _, err := Load(context.Background(), cfgPath, MapEnv(nil))
	if err == nil {
		t.Fatal("expected Load to fail on locked parent")
	}
	if errors.Is(err, ErrNoConfig) {
		// ENOENT surfaces as ErrNoConfig — not the scenario we want.
		// Some platforms / filesystems short-circuit EACCES into
		// ENOENT; skip in that case rather than asserting a leak
		// we cannot trigger.
		t.Skipf("platform surfaces ENOENT instead of EACCES: %v", err)
	}
	if strings.Contains(err.Error(), cfgPath) {
		t.Fatalf("Load error leaks config path: %q", err.Error())
	}
	if strings.Contains(err.Error(), locked) {
		t.Fatalf("Load error leaks parent dir: %q", err.Error())
	}
}

// TestLoad_RejectsEmptyDBDriver asserts N-001 fail-closed: a config.json
// with a blank db.driver must not reach boot, even though downstream
// store.New will accept an empty DSN and silently default SQLite to
// ./router.db. The operator deserves an explicit error at Load time.
func TestLoad_RejectsEmptyDBDriver(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	// Hand-craft JSON with driver="" so we do not accidentally pass
	// through our validated Config type (which would zero other fields).
	raw := []byte(`{
	  "version": 1,
	  "db": {"driver": "", "url": "file.db"},
	  "created_at": "2026-01-01T00:00:00Z",
	  "updated_at": "2026-01-01T00:00:00Z"
	}`)
	if err := os.WriteFile(cfgPath, raw, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, _, err := Load(context.Background(), cfgPath, MapEnv(map[string]string{}))
	if err == nil {
		t.Fatal("Load with empty db.driver: want error, got nil")
	}
	if !errors.Is(err, ErrInvalidDBConfig) {
		t.Fatalf("errors.Is(err, ErrInvalidDBConfig) = false: %v", err)
	}
	if !strings.Contains(err.Error(), "db.driver") {
		t.Fatalf("error should name the missing field; got: %v", err)
	}
}

// TestLoad_RejectsEmptyDBURL mirrors TestLoad_RejectsEmptyDBDriver for
// db.url — primary repro for the Codex N-001 finding (sqlite silent
// defaulting to router.db).
func TestLoad_RejectsEmptyDBURL(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	raw := []byte(`{
	  "version": 1,
	  "db": {"driver": "sqlite3", "url": ""},
	  "created_at": "2026-01-01T00:00:00Z",
	  "updated_at": "2026-01-01T00:00:00Z"
	}`)
	if err := os.WriteFile(cfgPath, raw, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, _, err := Load(context.Background(), cfgPath, MapEnv(map[string]string{}))
	if err == nil {
		t.Fatal("Load with empty db.url: want error, got nil")
	}
	if !errors.Is(err, ErrInvalidDBConfig) {
		t.Fatalf("errors.Is(err, ErrInvalidDBConfig) = false: %v", err)
	}
	if !strings.Contains(err.Error(), "db.url") {
		t.Fatalf("error should name the missing field; got: %v", err)
	}
}

// TestLoad_EnvOverlayCanSatisfyEmptyDBFields confirms the validation
// runs AFTER env overlay: a blank field in config.json is acceptable as
// long as ROUTER_DB_DRIVER / ROUTER_DB_URL fill it.
func TestLoad_EnvOverlayCanSatisfyEmptyDBFields(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	raw := []byte(`{
	  "version": 1,
	  "db": {"driver": "", "url": ""},
	  "created_at": "2026-01-01T00:00:00Z",
	  "updated_at": "2026-01-01T00:00:00Z"
	}`)
	if err := os.WriteFile(cfgPath, raw, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	env := MapEnv(map[string]string{
		"ROUTER_DB_DRIVER": "sqlite3",
		"ROUTER_DB_URL":    filepath.Join(dir, "env.db"),
	})
	cfg, _, err := Load(context.Background(), cfgPath, env)
	if err != nil {
		t.Fatalf("Load with env overlay: %v", err)
	}
	if cfg.DB.Driver != "sqlite3" || cfg.DB.URL == "" {
		t.Fatalf("env overlay did not fill empty file values: %#v", cfg.DB)
	}
}

// _ ensures fmt usage — if the file shrinks later we don't want an
// unused-import failure from editors that remove the dep.
var _ = fmt.Sprintf

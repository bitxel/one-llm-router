package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sqliteConfigPath writes a minimal config.json into t.TempDir() and
// returns its absolute path. The config uses a per-test sqlite file so
// migrator operations exercise the real golang-migrate + sqlite stack.
func sqliteConfigPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	cfgPath := filepath.Join(dir, "config.json")
	body := `{"version":1,"db":{"driver":"sqlite3","url":"` + dbPath + `"},"runtime":{"log_client_request_body":false,"log_upstream_request_body":false,"log_upstream_response_body":false,"log_retention_days":30,"log_level":"info"},"plugins":{"admin_auth":{"enabled":false},"client_keys":{"enabled":false}}}`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write cfg: %v", err)
	}
	return cfgPath
}

// runAndCapture drives runMigrate with the given args against the
// sqlite config file; returns (exit code, stdout).
func runAndCapture(t *testing.T, cfgPath string, args ...string) (int, string) {
	t.Helper()
	out := &bytes.Buffer{}
	all := append([]string{"-config", cfgPath}, args...)
	code := runMigrate(context.Background(), out, all)
	return code, out.String()
}

func TestRunMigrate_Status_EmptyDB(t *testing.T) {
	t.Parallel()
	cfg := sqliteConfigPath(t)
	code, out := runAndCapture(t, cfg, "status")
	if code != migrateExitOK {
		t.Errorf("status exit = %d, want %d; out=%s", code, migrateExitOK, out)
	}
	if !strings.Contains(out, "version=0") {
		t.Errorf("status output missing version=0; got: %s", out)
	}
}

func TestRunMigrate_UpThenStatus(t *testing.T) {
	t.Parallel()
	cfg := sqliteConfigPath(t)

	if code, out := runAndCapture(t, cfg, "up"); code != migrateExitOK {
		t.Fatalf("up exit = %d; out=%s", code, out)
	}
	code, out := runAndCapture(t, cfg, "status")
	if code != migrateExitOK {
		t.Fatalf("status exit = %d; out=%s", code, out)
	}
	if !strings.Contains(out, "version=") || strings.Contains(out, "version=0") {
		t.Errorf("after up, status = %q, want version=<non-zero>", out)
	}
	if strings.Contains(out, "dirty=true") {
		t.Errorf("after clean up, status reports dirty=true: %s", out)
	}
}

func TestRunMigrate_VersionCommand_ScriptableShape(t *testing.T) {
	t.Parallel()
	cfg := sqliteConfigPath(t)
	if code, _ := runAndCapture(t, cfg, "up"); code != migrateExitOK {
		t.Fatalf("up failed")
	}
	code, out := runAndCapture(t, cfg, "version")
	if code != migrateExitOK {
		t.Fatalf("version exit = %d; out=%s", code, out)
	}
	// Shape: "version=<n> dirty=<bool>\n"
	if !strings.Contains(out, "version=") || !strings.Contains(out, "dirty=") {
		t.Errorf("version output = %q, want 'version=<n> dirty=<bool>'", out)
	}
}

func TestRunMigrate_Force(t *testing.T) {
	t.Parallel()
	cfg := sqliteConfigPath(t)
	if code, _ := runAndCapture(t, cfg, "up"); code != migrateExitOK {
		t.Fatalf("up failed")
	}
	code, out := runAndCapture(t, cfg, "force", "0")
	if code != migrateExitOK {
		t.Errorf("force exit = %d; out=%s", code, out)
	}
	if !strings.Contains(out, "forced version to 0") {
		t.Errorf("force output = %q, want 'forced version to 0'", out)
	}
}

func TestRunMigrate_Force_MissingArg_UsageError(t *testing.T) {
	t.Parallel()
	cfg := sqliteConfigPath(t)
	code, out := runAndCapture(t, cfg, "force")
	if code != migrateExitUsage {
		t.Errorf("force without arg exit = %d, want %d (usage); out=%s", code, migrateExitUsage, out)
	}
}

func TestRunMigrate_Force_NonNumericArg_UsageError(t *testing.T) {
	t.Parallel()
	cfg := sqliteConfigPath(t)
	code, out := runAndCapture(t, cfg, "force", "notanumber")
	if code != migrateExitUsage {
		t.Errorf("force non-numeric exit = %d, want %d (usage); out=%s", code, migrateExitUsage, out)
	}
}

func TestRunMigrate_Down(t *testing.T) {
	t.Parallel()
	cfg := sqliteConfigPath(t)
	if code, _ := runAndCapture(t, cfg, "up"); code != migrateExitOK {
		t.Fatalf("up failed")
	}
	code, out := runAndCapture(t, cfg, "down")
	if code != migrateExitOK {
		t.Errorf("down exit = %d; out=%s", code, out)
	}
	if !strings.Contains(out, "rolled back") {
		t.Errorf("down output = %q, want contains 'rolled back'", out)
	}
}

func TestRunMigrate_UnknownSubcommand(t *testing.T) {
	t.Parallel()
	cfg := sqliteConfigPath(t)
	code, out := runAndCapture(t, cfg, "xyzzy")
	if code != migrateExitUsage {
		t.Errorf("unknown subcmd exit = %d, want %d; out=%s", code, migrateExitUsage, out)
	}
	if !strings.Contains(out, "unknown subcommand") {
		t.Errorf("unknown subcmd output = %q, want 'unknown subcommand'", out)
	}
}

func TestRunMigrate_NoSubcommand_Usage(t *testing.T) {
	t.Parallel()
	cfg := sqliteConfigPath(t)
	out := &bytes.Buffer{}
	code := runMigrate(context.Background(), out, []string{"-config", cfg})
	if code != migrateExitUsage {
		t.Errorf("no subcmd exit = %d, want %d", code, migrateExitUsage)
	}
	if !strings.Contains(out.String(), "Usage: one-llm-router migrate") {
		t.Errorf("no subcmd output missing usage banner: %s", out.String())
	}
}

func TestRunMigrate_HelpSubcommand(t *testing.T) {
	t.Parallel()
	cfg := sqliteConfigPath(t)
	code, out := runAndCapture(t, cfg, "help")
	if code != migrateExitOK {
		t.Errorf("help exit = %d, want %d", code, migrateExitOK)
	}
	if !strings.Contains(out, "Usage: one-llm-router migrate") {
		t.Errorf("help output missing usage banner")
	}
}

func TestRunMigrate_ConfigMissingAndNoEnv_Error(t *testing.T) {
	// Not Parallel: mutates env.
	t.Setenv("ROUTER_DB_DRIVER", "")
	t.Setenv("ROUTER_DB_URL", "")
	dir := t.TempDir()
	bogus := filepath.Join(dir, "nope.json")

	out := &bytes.Buffer{}
	code := runMigrate(context.Background(), out, []string{"-config", bogus, "status"})
	if code != migrateExitError {
		t.Errorf("exit = %d, want %d for missing config + no env", code, migrateExitError)
	}
	if !strings.Contains(out.String(), "ROUTER_DB_DRIVER") {
		t.Errorf("error message missing env hint: %s", out.String())
	}
}

func TestRunMigrate_ConfigMissing_EnvFallbackWorks(t *testing.T) {
	// Not Parallel: mutates env.
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "envtest.db")
	bogus := filepath.Join(dir, "no-config.json")
	t.Setenv("ROUTER_DB_DRIVER", "sqlite3")
	t.Setenv("ROUTER_DB_URL", dbPath)

	out := &bytes.Buffer{}
	code := runMigrate(context.Background(), out, []string{"-config", bogus, "up"})
	if code != migrateExitOK {
		t.Errorf("env-fallback up exit = %d; out=%s", code, out.String())
	}
	if !strings.Contains(out.String(), "migrations applied") {
		t.Errorf("env-fallback up output = %q, want 'migrations applied'", out.String())
	}
}

func TestRunMigrate_MalformedConfigJSON_PropagatesError(t *testing.T) {
	// Not Parallel: mutates env (to ensure fallback is NOT attempted
	// for non-ErrNoConfig loader errors).
	t.Setenv("ROUTER_DB_DRIVER", "sqlite3")
	t.Setenv("ROUTER_DB_URL", filepath.Join(t.TempDir(), "should-not-be-used.db"))

	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfg, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write malformed cfg: %v", err)
	}

	out := &bytes.Buffer{}
	code := runMigrate(context.Background(), out, []string{"-config", cfg, "status"})
	if code != migrateExitError {
		t.Errorf("malformed config exit = %d, want %d", code, migrateExitError)
	}
	if strings.Contains(out.String(), "migrations applied") {
		t.Errorf("malformed config should not silently succeed via env fallback; out=%s", out.String())
	}
}

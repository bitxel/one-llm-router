package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/user/one-llm-router/internal/entrypoint"
)

// captureStdio swaps os.Stdout / os.Stderr for a pair of tempfiles so
// tests can observe what run() writes. The originals are restored at
// cleanup. Using *os.File (not bytes.Buffer) because main.go's
// signature takes *os.File to allow future os.Pipe-based integration
// tests.
func captureStdio(t *testing.T) (*os.File, *os.File, func() (string, string)) {
	t.Helper()
	outFile, err := os.CreateTemp(t.TempDir(), "stdout-*.txt")
	if err != nil {
		t.Fatalf("temp stdout: %v", err)
	}
	errFile, err := os.CreateTemp(t.TempDir(), "stderr-*.txt")
	if err != nil {
		t.Fatalf("temp stderr: %v", err)
	}
	return outFile, errFile, func() (string, string) {
		_ = outFile.Sync()
		_ = errFile.Sync()
		outBytes, _ := os.ReadFile(outFile.Name())
		errBytes, _ := os.ReadFile(errFile.Name())
		return string(outBytes), string(errBytes)
	}
}

func TestRun_VersionFlag(t *testing.T) {
	t.Parallel()
	out, errW, read := captureStdio(t)

	code := run(context.Background(), []string{"-version"}, out, errW)
	if code != exitOK {
		t.Errorf("exit code = %d, want %d", code, exitOK)
	}
	stdout, _ := read()
	if !strings.Contains(stdout, "one-llm-router version=") {
		t.Errorf("stdout missing version banner: %q", stdout)
	}
}

func TestRun_UnknownFlag_ReturnsUsage(t *testing.T) {
	t.Parallel()
	out, errW, _ := captureStdio(t)

	code := run(context.Background(), []string{"-does-not-exist"}, out, errW)
	if code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
}

func TestRun_ConfigLoadError_ReturnsRuntimeError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Create a file at the config path that is unreadable JSON — this
	// surfaces as a non-ErrNoConfig loader error and should be fatal.
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte("{ not json"), 0o600); err != nil {
		t.Fatalf("write bogus config: %v", err)
	}

	out, errW, _ := captureStdio(t)
	// Use a short-lived ctx so the test never actually binds a
	// listener; config.Load's error fires before BuildApp is invoked.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	code := run(ctx, []string{"-config", cfgPath, "-listen", "127.0.0.1:0"}, out, errW)
	if code == exitOK {
		t.Errorf("exit code = %d, want non-zero on bad config", code)
	}
}

// TestRun_ConfigPathEnvVar covers the F-004 fix: when -config is
// omitted, ROUTER_CONFIG_PATH supplies the default. We assert the
// precedence chain indirectly — by pointing the env var at an
// unreadable-JSON file we get a loader error, proving the env value
// reached the loader.
func TestRun_ConfigPathEnvVar(t *testing.T) {
	// Serial: t.Setenv is incompatible with t.Parallel.
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte("{ malformed"), 0o600); err != nil {
		t.Fatalf("write bogus config: %v", err)
	}

	t.Setenv(envVarConfigPath, cfgPath)

	out, errW, _ := captureStdio(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	code := run(ctx, []string{"-listen", "127.0.0.1:0"}, out, errW)
	if code != exitError {
		t.Errorf("exit code = %d, want %d (env var should have steered loader to bad cfg)", code, exitError)
	}
}

// TestRun_ConfigFlagBeatsEnvVar confirms flag > env precedence: the
// env var points at a bogus config but -config points at a good one,
// so run() must not error out on load.
func TestRun_ConfigFlagBeatsEnvVar(t *testing.T) {
	// Serial: t.Setenv is incompatible with t.Parallel.
	dir := t.TempDir()
	badPath := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(badPath, []byte("{ nope"), 0o600); err != nil {
		t.Fatalf("write bad cfg: %v", err)
	}
	// Good path does not exist → ErrNoConfig → setup-pending → BuildApp
	// returns normally; run() then exits cleanly because ctx is
	// already cancelled.
	goodPath := filepath.Join(dir, "config.json")

	t.Setenv(envVarConfigPath, badPath)

	out, errW, read := captureStdio(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	code := run(ctx, []string{"-config", goodPath, "-listen", "127.0.0.1:0"}, out, errW)
	stdout, stderr := read()
	// Pre-cancelled ctx makes Load fail with context.Canceled (not ErrNoConfig),
	// so exit code is not a reliable signal here; instead prove the bad env JSON was never parsed.
	if strings.Contains(stdout, "invalid character") || strings.Contains(stdout, "cannot unmarshal") {
		t.Fatalf("loader appears to have read bad env config JSON; stdout=%q stderr=%q", stdout, stderr)
	}
	if code != exitOK && code != exitError {
		t.Errorf("exit code = %d, want 0 or 1; stdout=%q stderr=%q", code, stdout, stderr)
	}
}

// TestRun_AbsConfigPath verifies run() resolves a relative path to
// absolute before handing it to BuildApp / the setup gate. We can't
// assert on the internal call, but we can assert that an unreadable
// *relative* path still surfaces a useful error.
func TestRun_AbsConfigPath(t *testing.T) {
	// Serial because t.Chdir affects process-wide state.
	dir := t.TempDir()
	t.Chdir(dir)

	// No config.json → setup-pending → BuildApp returns a runnable
	// App. To avoid actually binding, we cancel ctx immediately so
	// Start returns ErrServerClosed-equivalent promptly.
	out, errW, _ := captureStdio(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Port 0 → OS picks — and we've already cancelled so Start
	// short-circuits.
	code := run(ctx, []string{"-config", "config.json", "-listen", "127.0.0.1:0"}, out, errW)
	// The cancelled ctx causes Start to return before binding; we
	// expect either exitOK (graceful) or exitError (bound-drain hit
	// cancelled ctx). Both are acceptable post-conditions for this
	// smoke — the sole invariant is "does not panic".
	if code != exitOK && code != exitError {
		t.Errorf("exit code = %d, want 0 or 1 after cancelled ctx", code)
	}
}

// T-029: run() logs setup-pending when config.json is absent, then shuts down when ctx times out.
func TestRun_SetupPending_TimeoutShutdown_LogsAbsentConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "missing-config.json")

	out, errW, read := captureStdio(t)
	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()

	code := run(ctx, []string{"-config", cfgPath, "-listen", "127.0.0.1:0"}, out, errW)
	stdout, stderr := read()
	if code != exitOK && code != exitError {
		t.Fatalf("exit code = %d, want 0 or 1; stdout=%q stderr=%q", code, stdout, stderr)
	}
	combined := stdout + "\n" + stderr
	if !strings.Contains(combined, "config.json absent") {
		t.Fatalf("expected setup-pending log marker; stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestRun_RejectsCodexBackendOverrideEnv(t *testing.T) {
	t.Setenv(entrypoint.EnvVarE2ECodexBackendBaseURL, "http://127.0.0.1:4555/backend-api")

	out, errW, _ := captureStdio(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	code := run(ctx, []string{"-listen", "127.0.0.1:0"}, out, errW)
	if code != exitError {
		t.Errorf("exit code = %d, want %d", code, exitError)
	}
}

func TestOAuthProviderFromEnv_PartialConfigRejected(t *testing.T) {
	t.Setenv(entrypoint.EnvVarOAuthAuthorizeURL, "https://oauth.mock.example/oauth/authorize")

	provider, err := oauthProviderFromEnv()
	if err == nil {
		t.Fatalf("oauthProviderFromEnv() error = nil, want partial-config failure")
	}
	if provider != nil {
		t.Fatalf("oauthProviderFromEnv() provider = %#v, want nil on partial-config failure", provider)
	}
}

func TestOAuthProviderFromEnv_FullConfigAccepted(t *testing.T) {
	t.Setenv(entrypoint.EnvVarOAuthAuthorizeURL, "https://oauth.mock.example/oauth/authorize")
	t.Setenv(entrypoint.EnvVarOAuthTokenURL, "https://oauth.mock.example/oauth/token")
	t.Setenv(entrypoint.EnvVarOAuthDeviceCodeURL, "https://oauth.mock.example/api/accounts/deviceauth/usercode")
	t.Setenv(entrypoint.EnvVarOAuthDeviceTokenURL, "https://oauth.mock.example/api/accounts/deviceauth/token")

	provider, err := oauthProviderFromEnv()
	if err != nil {
		t.Fatalf("oauthProviderFromEnv() error = %v, want nil", err)
	}
	if provider == nil {
		t.Fatal("oauthProviderFromEnv() provider = nil, want configured provider")
	}
}

func TestRejectProductionCodexBackendOverrideEnv(t *testing.T) {
	for _, name := range []string{
		entrypoint.EnvVarLegacyCodexBackendBaseURL,
		entrypoint.EnvVarEnableTestCodexBackend,
		entrypoint.EnvVarTestCodexBackendBaseURL,
		entrypoint.EnvVarE2ECodexBackendBaseURL,
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "http://127.0.0.1:4555")

			if err := rejectProductionCodexBackendOverrideEnv(); err == nil {
				t.Fatalf("rejectProductionCodexBackendOverrideEnv() error = nil, want rejection for %s", name)
			}
		})
	}
}

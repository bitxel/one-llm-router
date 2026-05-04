package setup

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/user/one-llm-router/internal/config"
)

// fakeCounter is an AccountCounter that returns a pre-set value and
// records the number of calls it received.
type fakeCounter struct {
	count int64
	err   error
	calls atomic.Int32
}

func (f *fakeCounter) CountUpstreamAccounts(ctx context.Context) (int64, error) {
	f.calls.Add(1)
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return f.count, f.err
}

// envBoth is the convenience "env with both DB vars set" builder.
func envBoth(driver, url string) config.Env {
	return config.MapEnv(map[string]string{
		"ROUTER_DB_DRIVER": driver,
		"ROUTER_DB_URL":    url,
	})
}

func TestBootstrap_FilePresent_NoOp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	counter := &fakeCounter{count: 99}

	materialized, err := BootstrapIfBrownfield(context.Background(), counter, path, envBoth("sqlite3", "router.db"), nil)
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if materialized {
		t.Error("materialized = true, want false (file already present)")
	}
	if got := counter.calls.Load(); got != 0 {
		t.Errorf("counter.calls = %d, want 0 — must not hit DB when file present", got)
	}
}

func TestBootstrap_EnvMissing_NoOp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	counter := &fakeCounter{count: 5}

	cases := []struct {
		name string
		env  config.Env
	}{
		{"both empty", config.MapEnv(map[string]string{})},
		{"driver only", config.MapEnv(map[string]string{"ROUTER_DB_DRIVER": "sqlite3"})},
		{"url only", config.MapEnv(map[string]string{"ROUTER_DB_URL": "router.db"})},
		{"driver empty string", config.MapEnv(map[string]string{"ROUTER_DB_DRIVER": "", "ROUTER_DB_URL": "x"})},
		{"url empty string", config.MapEnv(map[string]string{"ROUTER_DB_DRIVER": "x", "ROUTER_DB_URL": ""})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			materialized, err := BootstrapIfBrownfield(context.Background(), counter, path, tc.env, nil)
			if err != nil {
				t.Errorf("err = %v, want nil", err)
			}
			if materialized {
				t.Error("materialized = true, want false — env not fully set")
			}
		})
	}
	if got := counter.calls.Load(); got != 0 {
		t.Errorf("counter.calls = %d, want 0 — must not hit DB when env incomplete", got)
	}
}

func TestBootstrap_AccountsEmpty_NoOp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	counter := &fakeCounter{count: 0}

	materialized, err := BootstrapIfBrownfield(context.Background(), counter, path, envBoth("sqlite3", "router.db"), nil)
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if materialized {
		t.Error("materialized = true, want false — DB empty")
	}
	if got := counter.calls.Load(); got != 1 {
		t.Errorf("counter.calls = %d, want 1", got)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("file should not be created; Stat err = %v", err)
	}
}

func TestBootstrap_AccountsNegative_NoOp(t *testing.T) {
	// Defensive: a buggy counter returning < 0 must not synthesize.
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	counter := &fakeCounter{count: -1}

	materialized, err := BootstrapIfBrownfield(context.Background(), counter, path, envBoth("sqlite3", "r.db"), nil)
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if materialized {
		t.Error("materialized = true, want false on negative count")
	}
}

func TestBootstrap_PopulatedDB_Materializes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	counter := &fakeCounter{count: 3}

	materialized, err := BootstrapIfBrownfield(
		context.Background(),
		counter,
		path,
		envBoth("postgres", "postgres://user:pw@db:5432/router"),
		nil,
	)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want true")
	}

	// File must exist with 0600 perms on POSIX.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if runtime.GOOS != "windows" {
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Errorf("mode = %04o, want 0600", mode)
		}
	}

	// Parse and verify content.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var cfg config.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if cfg.Version != config.SupportedVersion {
		t.Errorf("Version = %d, want %d", cfg.Version, config.SupportedVersion)
	}
	if cfg.DB.Driver != "postgres" {
		t.Errorf("DB.Driver = %q, want postgres", cfg.DB.Driver)
	}
	if cfg.DB.URL != "postgres://user:pw@db:5432/router" {
		t.Errorf("DB.URL = %q, want env URL round-tripped", cfg.DB.URL)
	}
	// Defaults copied in.
	wantRuntime := config.DefaultRuntimeConfig()
	if cfg.Runtime != wantRuntime {
		t.Errorf("Runtime = %+v, want %+v", cfg.Runtime, wantRuntime)
	}
	wantPlugins := config.DefaultPluginsConfig()
	if cfg.Plugins != wantPlugins {
		t.Errorf("Plugins = %+v, want %+v", cfg.Plugins, wantPlugins)
	}
	// Timestamps populated.
	if cfg.CreatedAt.IsZero() || cfg.UpdatedAt.IsZero() {
		t.Errorf("timestamps zero — CreatedAt=%v UpdatedAt=%v", cfg.CreatedAt, cfg.UpdatedAt)
	}
}

func TestBootstrap_Idempotent_SecondCallSkips(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	counter := &fakeCounter{count: 1}
	env := envBoth("sqlite3", "router.db")

	mat1, err := BootstrapIfBrownfield(context.Background(), counter, path, env, nil)
	if err != nil || !mat1 {
		t.Fatalf("first call: materialized=%v, err=%v", mat1, err)
	}

	mat2, err := BootstrapIfBrownfield(context.Background(), counter, path, env, nil)
	if err != nil {
		t.Errorf("second call err = %v, want nil", err)
	}
	if mat2 {
		t.Error("second call materialized=true — must be idempotent once file exists")
	}
	// counter was hit once (first call only).
	if got := counter.calls.Load(); got != 1 {
		t.Errorf("counter.calls = %d, want 1 (file skip on second)", got)
	}
}

func TestBootstrap_CounterError_Propagates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	counter := &fakeCounter{err: errors.New("boom")}

	materialized, err := BootstrapIfBrownfield(context.Background(), counter, path, envBoth("sqlite3", "r.db"), nil)
	if err == nil {
		t.Fatal("err = nil, want wrapped boom")
	}
	if materialized {
		t.Error("materialized = true, want false on counter error")
	}
	if !errors.Is(err, counter.err) {
		t.Errorf("err = %v, want errors.Is(counter.err)", err)
	}
}

func TestBootstrap_WriteAtomicError_Propagates(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission trick relies on POSIX")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses permission checks")
	}
	t.Parallel()
	dir := t.TempDir()
	// Make the parent non-writable so WriteAtomic fails at tmp open.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	counter := &fakeCounter{count: 7}
	path := filepath.Join(dir, "config.json")
	materialized, err := BootstrapIfBrownfield(context.Background(), counter, path, envBoth("sqlite3", "r.db"), nil)
	if err == nil {
		t.Fatal("err = nil, want write error")
	}
	if materialized {
		t.Error("materialized = true, want false on write error")
	}
}

func TestBootstrap_EmptyCfgPath_Rejected(t *testing.T) {
	t.Parallel()
	counter := &fakeCounter{count: 1}
	materialized, err := BootstrapIfBrownfield(context.Background(), counter, "", envBoth("x", "y"), nil)
	if err == nil {
		t.Fatal("err = nil, want non-nil")
	}
	if materialized {
		t.Error("materialized = true, want false on empty cfgPath")
	}
}

func TestBootstrap_NilCounter_Rejected(t *testing.T) {
	t.Parallel()
	materialized, err := BootstrapIfBrownfield(context.Background(), nil, "/tmp/x", envBoth("x", "y"), nil)
	if err == nil {
		t.Fatal("err = nil, want non-nil")
	}
	if materialized {
		t.Error("materialized = true on nil counter, want false")
	}
}

func TestBootstrap_NilEnv_FallsBackToOS(t *testing.T) {
	// With OS env almost certainly missing ROUTER_DB_DRIVER, passing
	// env=nil should transparently fall back and return (false, nil).
	// NOT Parallel: t.Setenv forbids parallel tests.
	t.Setenv("ROUTER_DB_DRIVER", "")
	t.Setenv("ROUTER_DB_URL", "")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	counter := &fakeCounter{count: 10}

	materialized, err := BootstrapIfBrownfield(context.Background(), counter, path, nil, nil)
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if materialized {
		t.Error("materialized = true, want false (env unset at OS layer)")
	}
}

func TestBootstrap_ContextCanceled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	counter := &fakeCounter{count: 1}

	materialized, err := BootstrapIfBrownfield(ctx, counter, path, envBoth("x", "y"), nil)
	if err == nil {
		t.Fatal("err = nil, want context cancellation")
	}
	if materialized {
		t.Error("materialized = true on canceled ctx, want false")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want Is(context.Canceled)", err)
	}
}

func TestBootstrap_StatError_NonENOENT_Propagates(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission trick relies on POSIX")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses permission checks")
	}
	t.Parallel()
	dir := t.TempDir()
	sub := filepath.Join(dir, "locked")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := os.Chmod(sub, 0o000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(sub, 0o700) })

	counter := &fakeCounter{count: 1}
	path := filepath.Join(sub, "config.json")
	materialized, err := BootstrapIfBrownfield(context.Background(), counter, path, envBoth("x", "y"), nil)
	if err == nil {
		t.Fatal("err = nil, want stat permission error")
	}
	if materialized {
		t.Error("materialized = true on stat error, want false")
	}
}

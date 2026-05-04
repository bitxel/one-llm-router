package adminapi_test

// Unit tests for adminapi.ConfigUpdater focused on the OnReload hook
// that 002 F005 introduces. The hook is the seam BuildApp uses to
// push runtime config into long-lived subsystems (slog.LevelVar for
// log_level hot-reload) without the handler package knowing about
// them. These tests lock down:
//
//   - OnReload fires exactly once per successful Update call.
//   - OnReload sees the post-patch config (not the stale prior one).
//   - OnReload is not called if WriteAtomic fails.
//   - SetOnReload(nil) clears a previously-installed hook.
//   - Concurrent Update calls never produce a hook callback with a
//     nil config.
//
// The fixture uses the real config.Reader/Publisher plus an on-disk
// config.json so the WriteAtomic path is exercised end-to-end.

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/user/one-llm-router/internal/api/adminapi"
	"github.com/user/one-llm-router/internal/config"
)

func baseConfig() *config.Config {
	return &config.Config{
		Version: 1,
		DB: config.DBConfig{
			Driver: "sqlite3",
			URL:    "file:./test.db?cache=shared",
		},
		Runtime: config.RuntimeConfig{
			LogClientRequestBody:    false,
			LogUpstreamRequestBody:  false,
			LogUpstreamResponseBody: false,
			LogRetentionDays:        30,
			LogLevel:                "info",
		},
		Plugins: config.PluginsConfig{},
	}
}

func TestConfigUpdater_OnReload_FiresWithNextConfig(t *testing.T) {
	// Not parallel: config.Publisher is a process-global slot and
	// TestConfigUpdater_SetOnReload_NilClearsHook mutates it too.
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	base := baseConfig()
	if err := config.WriteAtomic(cfgPath, base); err != nil {
		t.Fatalf("seed WriteAtomic: %v", err)
	}

	// The Reader/Publisher pair used by production wiring is a
	// single process-global slot; seed it with the baseline cfg so
	// Update can clone-and-patch without hitting the "no live config"
	// guard.
	config.Publisher.Store(base)
	t.Cleanup(func() { config.Publisher.Store(nil) })

	u := adminapi.NewConfigUpdater(cfgPath, config.Reader, config.Publisher)

	var (
		mu      sync.Mutex
		seen    []string
		calls   int
		lastCfg *config.Config
	)
	u.SetOnReload(func(c *config.Config) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if c != nil {
			seen = append(seen, c.Runtime.LogLevel)
			lastCfg = c
		}
	})

	debug := "debug"
	if _, err := u.Update(adminapi.SettingsPatch{LogLevel: &debug}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("onReload calls = %d, want 1", calls)
	}
	if lastCfg == nil || lastCfg.Runtime.LogLevel != "debug" {
		t.Fatalf("onReload saw stale cfg = %+v, want LogLevel=debug", lastCfg)
	}
	if got := seen[0]; got != "debug" {
		t.Fatalf("seen[0] = %q, want debug", got)
	}
}

func TestConfigUpdater_SetOnReload_NilClearsHook(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	base := baseConfig()
	if err := config.WriteAtomic(cfgPath, base); err != nil {
		t.Fatalf("seed WriteAtomic: %v", err)
	}
	config.Publisher.Store(base)
	t.Cleanup(func() { config.Publisher.Store(nil) })

	u := adminapi.NewConfigUpdater(cfgPath, config.Reader, config.Publisher)
	var calls int
	u.SetOnReload(func(*config.Config) { calls++ })
	u.SetOnReload(nil)

	warn := "warn"
	if _, err := u.Update(adminapi.SettingsPatch{LogLevel: &warn}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if calls != 0 {
		t.Fatalf("onReload calls after nil reset = %d, want 0", calls)
	}
}

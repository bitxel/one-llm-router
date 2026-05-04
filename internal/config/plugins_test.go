package config

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// captureLogsConfig redirects slog.Default to a text handler writing to
// buf for the duration of fn. Must not run in t.Parallel-flavoured
// tests because slog.Default is process-global.
func captureLogsConfig(t *testing.T, fn func()) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	fn()
	return &buf
}

func TestPlugins_EnabledKnownIDs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		plugins  PluginsConfig
		id       string
		expected bool
	}{
		{"admin_auth_on", PluginsConfig{AdminAuth: AdminAuthPluginConfig{Enabled: true}}, "admin_auth", true},
		{"admin_auth_off", PluginsConfig{AdminAuth: AdminAuthPluginConfig{Enabled: false}}, "admin_auth", false},
		{"client_keys_on", PluginsConfig{ClientKeys: ClientKeysPluginConfig{Enabled: true}}, "client_keys", true},
		{"client_keys_off", PluginsConfig{ClientKeys: ClientKeysPluginConfig{Enabled: false}}, "client_keys", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.plugins.Enabled(tc.id); got != tc.expected {
				t.Fatalf("Enabled(%q) = %v, want %v", tc.id, got, tc.expected)
			}
		})
	}
}

func TestPlugins_EnabledUnknownIDReturnsFalse(t *testing.T) {
	// Not Parallel: touches slog.Default.
	resetUnknownPluginWarnOnceForTests()
	_ = captureLogsConfig(t, func() {
		p := PluginsConfig{}
		if got := p.Enabled("nonexistent_plugin_1"); got {
			t.Errorf("Enabled(nonexistent_plugin_1) = true, want false")
		}
	})
}

func TestPlugins_EnabledUnknownIDWarnsOncePerProcessPerID(t *testing.T) {
	// Not Parallel: touches slog.Default + package-level sync.Map.
	resetUnknownPluginWarnOnceForTests()
	t.Cleanup(resetUnknownPluginWarnOnceForTests)

	buf := captureLogsConfig(t, func() {
		p := PluginsConfig{}
		p.Enabled("ghost_a") // first call → warns
		p.Enabled("ghost_a") // repeat → silent
		p.Enabled("ghost_a") // repeat → silent
		p.Enabled("ghost_b") // different id → warns
		p.Enabled("ghost_b") // repeat → silent
	})

	logs := buf.String()
	countA := strings.Count(logs, "plugin_id=ghost_a")
	countB := strings.Count(logs, "plugin_id=ghost_b")
	if countA != 1 {
		t.Errorf("ghost_a WARN count = %d, want 1; full log:\n%s", countA, logs)
	}
	if countB != 1 {
		t.Errorf("ghost_b WARN count = %d, want 1; full log:\n%s", countB, logs)
	}
	if !strings.Contains(logs, "unknown plugin id") {
		t.Errorf("log missing canonical message 'unknown plugin id'; got:\n%s", logs)
	}
}

func TestPlugins_EnabledUnknownIDConcurrent_OneWarning(t *testing.T) {
	resetUnknownPluginWarnOnceForTests()
	t.Cleanup(resetUnknownPluginWarnOnceForTests)

	buf := captureLogsConfig(t, func() {
		p := PluginsConfig{}
		var wg sync.WaitGroup
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = p.Enabled("ghost_concurrent")
			}()
		}
		wg.Wait()
	})

	count := strings.Count(buf.String(), "plugin_id=ghost_concurrent")
	if count != 1 {
		t.Errorf("concurrent callers produced %d warnings for the same id, want exactly 1", count)
	}
}

func TestPlugins_KnownIDsNeverWarn(t *testing.T) {
	resetUnknownPluginWarnOnceForTests()
	t.Cleanup(resetUnknownPluginWarnOnceForTests)

	buf := captureLogsConfig(t, func() {
		p := PluginsConfig{
			AdminAuth:  AdminAuthPluginConfig{Enabled: true},
			ClientKeys: ClientKeysPluginConfig{Enabled: true},
		}
		p.Enabled("admin_auth")
		p.Enabled("client_keys")
		p.Enabled("admin_auth")
	})
	if strings.Contains(buf.String(), "unknown plugin id") {
		t.Errorf("known ids must never emit the unknown-id warning; got:\n%s", buf.String())
	}
}

package config

import (
	"log/slog"
	"sync"
)

// unknownPluginWarnOnce deduplicates "unknown plugin id" warnings to at
// most one per (process, id) pair. sync.Map is chosen over sync.Once
// because each id needs its own once-guard; a fresh sync.Once per id
// would require a different locking dance.
var unknownPluginWarnOnce sync.Map // map[string]struct{}

// Enabled reports whether the plugin with the given id is turned on in
// the current effective config. Unknown ids return false and emit a
// one-shot slog.Warn per process so the scaffolding author notices the
// missing case arm — usually because they added a new plugin without
// extending this switch. Silent-returning false keeps legacy callers
// robust when a new plugin ships in a later feature (003+).
//
// Known ids (002): admin_auth, client_keys. 003+ add their own arms.
func (p PluginsConfig) Enabled(id string) bool {
	switch id {
	case "admin_auth":
		return p.AdminAuth.Enabled
	case "client_keys":
		return p.ClientKeys.Enabled
	default:
		warnUnknownPluginIDOnce(id)
		return false
	}
}

// warnUnknownPluginIDOnce emits the first slog.Warn for a given id and
// suppresses subsequent calls. It is safe to call from concurrent
// goroutines; sync.Map.LoadOrStore is the one-shot marker.
func warnUnknownPluginIDOnce(id string) {
	if _, loaded := unknownPluginWarnOnce.LoadOrStore(id, struct{}{}); loaded {
		return
	}
	slog.Warn("config.PluginsConfig.Enabled called with unknown plugin id",
		"plugin_id", id,
		"hint", "add a case arm in internal/config/plugins.go if this plugin ships in the current binary",
	)
}

// resetUnknownPluginWarnOnceForTests clears the once-warning cache.
// Intended solely for unit tests in this package; avoid referencing
// from production code. (Package-local helper, not exported.)
func resetUnknownPluginWarnOnceForTests() {
	unknownPluginWarnOnce.Range(func(k, _ any) bool {
		unknownPluginWarnOnce.Delete(k)
		return true
	})
}

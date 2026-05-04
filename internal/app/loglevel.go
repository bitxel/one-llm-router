package app

import (
	"log/slog"
	"strings"

	"github.com/user/one-llm-router/internal/config"
)

// ResolveLogLevel maps a config.Runtime.LogLevel string to slog.Level.
// Unknown values fall back to slog.LevelInfo so a malformed runtime
// field cannot silence the router entirely.
func ResolveLogLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// applyLogLevel is the single point that mutates Deps.LogLevel from a
// live *config.Config. Callers: BuildApp (at boot), promoteToSteadyState
// (after wizard commit), and the ConfigUpdater on-reload hook (after
// /api/admin/settings/update). Nil-safe on both axes so tests that do
// not wire a LevelVar still work.
func applyLogLevel(lv *slog.LevelVar, cfg *config.Config) {
	if lv == nil || cfg == nil {
		return
	}
	lv.Set(ResolveLogLevel(cfg.Runtime.LogLevel))
}

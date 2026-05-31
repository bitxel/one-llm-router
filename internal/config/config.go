// Package config owns the on-disk configuration model for the router.
//
// The single source of truth is ./config.json (path overridable via
// ROUTER_CONFIG_PATH). This package ships the Go representation of that
// file plus the loader, writer, and live-reload plumbing. See the
// feature 002 data model for the field-level contract.
//
// The package is intentionally split into small files:
//   - config.go    — struct types and defaults (this file, T-013)
//   - plugins.go   — PluginsConfig.Enabled switch (T-014)
//   - loader.go    — disk → Config + env overlay (T-015)
//   - writer.go    — atomic tmp-file + fsync + rename (T-016)
//   - live.go      — atomic.Value publisher/reader (T-017)
package config

import "time"

// SupportedVersion is the one and only config.json schema version that
// this binary understands. Readers MUST reject unknown versions; see
// data-model.md §Read path case "file.version > SupportedVersion".
const SupportedVersion = 1

// Config is the in-memory form of config.json. The JSON field order and
// tag values MUST stay byte-stable because hot-reload compares file
// contents and because /api/admin/settings projects a subset of these
// fields onto the wire.
type Config struct {
	Version   int           `json:"version"`
	DB        DBConfig      `json:"db"`
	Runtime   RuntimeConfig `json:"runtime"`
	Plugins   PluginsConfig `json:"plugins"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
}

// DBConfig carries the database driver and DSN. The DSN MAY contain
// secrets and MUST NOT be logged or returned by /api/admin/settings (see
// data-model.md).
type DBConfig struct {
	Driver string `json:"driver"`
	URL    string `json:"url"`
}

// RuntimeConfig holds hot-reloadable operator knobs. The body-capture
// keys are independently togglable by traffic direction via
// POST /api/admin/settings/update. None are env-overridable in 002
// (data-model.md §Field-level env override precedence).
type RuntimeConfig struct {
	LogClientRequestBody    bool   `json:"log_client_request_body"`
	LogUpstreamRequestBody  bool   `json:"log_upstream_request_body"`
	LogUpstreamResponseBody bool   `json:"log_upstream_response_body"`
	LogRetentionDays        int    `json:"log_retention_days"`
	LogLevel                string `json:"log_level"`
	ModelRenames            []ModelRenameRule `json:"model_renames"`
}

// ModelRenameRule maps a client-facing model id to the upstream model id
// used by data-plane forwarding. Matching is exact and case-sensitive.
type ModelRenameRule struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// PluginsConfig is the typed surface for plugin operator intents.
// Each concrete plugin gets its own sub-struct so new fields land with
// type safety (no map[string]any). Unknown plugin IDs in the on-disk
// file parse to nothing — data-model.md requires permissive decode and
// strict encode.
type PluginsConfig struct {
	AdminAuth  AdminAuthPluginConfig  `json:"admin_auth"`
	ClientKeys ClientKeysPluginConfig `json:"client_keys"`
}

// AdminAuthPluginConfig holds the operator intents for the (yet-to-ship)
// admin-auth plugin. Only Enabled is writable in 002; the remaining
// fields are reserved by 003 and are tagged omitempty so a 002 binary
// reading a 003-era config.json preserves them on save, and so a
// vanilla 002 config.json does not emit placeholder keys.
//
// NOTE (002): nothing in 002 code reads SessionTTLMinutes / JWTSecretRef
// — they exist purely for forward-compat round-tripping. 003's PR wires
// them up to the real plugin.
type AdminAuthPluginConfig struct {
	Enabled           bool   `json:"enabled"`
	SessionTTLMinutes int    `json:"session_ttl_minutes,omitempty"`
	JWTSecretRef      string `json:"jwt_secret_ref,omitempty"`
}

// ClientKeysPluginConfig holds the operator intent for the (yet-to-ship)
// client-keys plugin. 004 will extend this struct with its own
// operator-editable fields; until then only Enabled is set. No
// placeholder fields are pre-declared because 004's shape is not
// finalized (ROADMAP #4); 004's PR will add omitempty-tagged fields
// here without a version bump (data-model.md §Plugin Registry).
type ClientKeysPluginConfig struct {
	Enabled bool `json:"enabled"`
}

// DefaultRuntimeConfig returns the baseline runtime knobs used by the
// wizard (first install) and the brownfield auto-materializer. Values
// match the data-model.md "Defaults" column and the MVP-parity
// guarantee (FR-014, US-5 AC-1): nothing logged by default, 30-day
// retention, info-level logs.
func DefaultRuntimeConfig() RuntimeConfig {
	return RuntimeConfig{
		LogClientRequestBody:    false,
		LogUpstreamRequestBody:  false,
		LogUpstreamResponseBody: false,
		LogRetentionDays:        30,
		LogLevel:                "info",
		ModelRenames:            []ModelRenameRule{},
	}
}

// DefaultPluginsConfig returns a PluginsConfig with every plugin intent
// set to disabled. Used by the wizard and the brownfield path to
// synthesize a blank plugins block.
func DefaultPluginsConfig() PluginsConfig {
	return PluginsConfig{
		AdminAuth:  AdminAuthPluginConfig{Enabled: false},
		ClientKeys: ClientKeysPluginConfig{Enabled: false},
	}
}

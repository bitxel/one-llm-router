package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
)

// Sentinel errors returned by Load. Callers distinguish the "wizard
// should run" case from "operator misconfiguration" via errors.Is.
var (
	// ErrNoConfig means config.json is absent from disk. Setup-mode
	// gating reads this as "run the wizard". Brownfield boot handles
	// the case where the absence is legitimate (env-configured DB +
	// accounts exist) by synthesizing a fresh file; see
	// internal/setup/brownfield.go.
	ErrNoConfig = errors.New("config file not found")

	// ErrUnsupportedVersion is returned when config.json is present but
	// carries a version the binary cannot safely read (unset, zero, or
	// greater than SupportedVersion). Callers MUST refuse to start so
	// the operator can investigate rather than silently run with wrong
	// assumptions.
	ErrUnsupportedVersion = errors.New("config.json schema version is unsupported by this binary")

	// ErrInvalidDBConfig is returned when config.json + env overlay
	// still leave a required db.* field empty. Without this guard,
	// a config.json with `db.driver:"sqlite3"` but no `db.url` would
	// silently default to "router.db" inside the store/dialect layer
	// and boot against a fresh SQLite file in the router's working
	// directory — masking operator misconfiguration. Fail-closed is
	// required per plan.md R-4 ("no silent defaults for required
	// fields"). See N-001.
	ErrInvalidDBConfig = errors.New("config.json db section is incomplete")
)

// Env is the lookup closure Load uses to read environment variables.
// Wrapping os.LookupEnv keeps Load hermetic in tests.
type Env func(key string) (string, bool)

// OSEnv returns an Env backed by os.LookupEnv. Production callers use
// this; tests build their own closures.
func OSEnv() Env { return os.LookupEnv }

// MapEnv returns an Env that reads from the supplied map. Values that
// are absent from the map produce ok=false (matching os.LookupEnv on
// unset vs empty). Intended for tests.
func MapEnv(m map[string]string) Env {
	return func(key string) (string, bool) {
		v, ok := m[key]
		return v, ok
	}
}

// SourceMap records where each effective config field came from. Keys
// are dotted field paths ("db.driver", "runtime.log_level", ...); values
// are one of:
//
//   - "file"     — the key was present in config.json on disk.
//   - "default"  — the key was absent; the compiled-in default is used.
//   - "env:<VAR>" — an environment variable (<VAR>, e.g. ROUTER_DB_URL)
//     overlaid the file value. 002 only supports env overlay on
//     {db.driver, db.url}; any other ROUTER_* variable is ignored with a
//     slog.Warn line.
//
// Downstream consumers (the 003 Settings handler, the 2013
// env_override_readonly guard) read SourceMap to answer "can this field
// be mutated via the API". 002 does not yet surface this over HTTP.
type SourceMap map[string]string

// source constants — kept unexported so callers compare via the string
// literals documented in the public API (and in tasks.md T-015 asserts).
const (
	srcFile    = "file"
	srcDefault = "default"
)

// Env-overridable keys in 002. 003+ may extend this allow-list (see
// Round-3 decision notes). Keep the slice small and explicit — a map
// hides intent here.
var envOverridableKeys = []envBinding{
	{path: "db.driver", envVar: "ROUTER_DB_DRIVER"},
	{path: "db.url", envVar: "ROUTER_DB_URL"},
}

type envBinding struct {
	path   string
	envVar string
}

// ignoredEnvVars lists environment variables that match the ROUTER_*
// naming convention but are NOT env-overridable in 002. Presence of any
// of them triggers a slog.Warn at Load time so the operator realises
// their intent did not take effect. The list mirrors the runtime and
// plugin-flag keys exposed in config.json — future features extending
// env overlay MUST remove the corresponding entry here.
var ignoredEnvVars = []string{
	"ROUTER_LOG_CLIENT_REQUEST_BODY",
	"ROUTER_LOG_UPSTREAM_REQUEST_BODY",
	"ROUTER_LOG_UPSTREAM_RESPONSE_BODY",
	"ROUTER_LOG_RETENTION_DAYS",
	"ROUTER_LOG_LEVEL",
	"ROUTER_ADMIN_AUTH_ENABLED",
	"ROUTER_CLIENT_KEYS_ENABLED",
}

// Load reads config.json from path, overlays ROUTER_* env variables
// where the 002 spec permits, fills defaults for missing keys, and
// returns the effective Config plus a per-field SourceMap.
//
// Errors:
//   - ErrNoConfig        — the file is absent (setup-mode trigger).
//   - ErrUnsupportedVersion — the file's version is not SupportedVersion.
//   - Wrapped I/O / parse errors — read permissions, malformed JSON,
//     unreadable parent directory.
//
// The caller MUST pass ctx so future callers can cancel the load; the
// current implementation only checks ctx.Err() at entry because Load
// performs only local I/O.
func Load(ctx context.Context, path string, env Env) (*Config, SourceMap, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if env == nil {
		env = OSEnv()
	}

	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, ErrNoConfig
		}
		// plan.md §362: keep the config.json path out of error
		// messages that propagate into INFO+ logs. os.Stat returns
		// *os.PathError whose Error() embeds the path, so ScrubPath
		// rewrites it before %w wrapping. Operators can correlate
		// via DEBUG-level "config.json path" emissions at the caller
		// (see cmd/one-llm-router/main.go).
		return nil, nil, fmt.Errorf("stat config.json: %w", ScrubPath(err))
	}
	if info.IsDir() {
		return nil, nil, errors.New("config path is a directory")
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		// plan.md §362 forbids the config.json path at INFO+; the
		// permission bits themselves are the actionable field.
		slog.Warn("config.json has permissions wider than 0600",
			"mode", fmt.Sprintf("%04o", perm),
			"expected", "0600",
		)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		// plan.md §362: ReadFile returns *os.PathError with path
		// embedded; ScrubPath rewrites it before %w wrapping.
		return nil, nil, fmt.Errorf("read config.json: %w", ScrubPath(err))
	}

	// First pass: scan which top-level + nested keys are present so we
	// can attribute "file" vs "default" per field. Unknown keys at any
	// level are ignored here — the typed Unmarshal below silently drops
	// them (permissive decode).
	present, err := scanPresentKeys(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("parse config.json: %w", err)
	}

	// Second pass: decode into the typed struct. Missing fields land at
	// their Go zero value; we overwrite them with defaults using
	// presence data above.
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, nil, fmt.Errorf("parse config.json: %w", err)
	}

	// Version gate. A missing version (zero after unmarshal) is treated
	// as an unsupported file — data-model.md "version absent" rule.
	if cfg.Version != SupportedVersion {
		return nil, nil, fmt.Errorf("%w: got %d, want %d", ErrUnsupportedVersion, cfg.Version, SupportedVersion)
	}

	srcMap := make(SourceMap, 8)

	// Runtime defaults + source map. Bools can't be distinguished from
	// "not set" by struct state alone, so we lean on the presence set.
	runDefaults := DefaultRuntimeConfig()
	applyRuntimeDefaults(&cfg.Runtime, runDefaults, present, srcMap)

	// Plugin defaults + source map. Zero value of the enabled bool is
	// false, which is also the default — so only presence disambiguates
	// "operator explicitly recorded false" from "field never existed".
	applyPluginDefaults(&cfg.Plugins, present, srcMap)

	// DB: file is authoritative, env may overlay.
	srcMap["db.driver"] = srcFile
	srcMap["db.url"] = srcFile
	overlayEnv(&cfg, env, srcMap)

	// Required-field gate (N-001). After file parse + env overlay, the
	// db section MUST carry both driver and url — empty values would
	// fall through to per-dialect defaults (e.g. sqlite -> router.db)
	// and boot against the wrong data store. Wrap ErrInvalidDBConfig so
	// callers can errors.Is without string matching, but include the
	// missing field names so CLI/operators can fix config.json.
	var missing []string
	if cfg.DB.Driver == "" {
		missing = append(missing, "db.driver")
	}
	if cfg.DB.URL == "" {
		missing = append(missing, "db.url")
	}
	if len(missing) > 0 {
		return nil, nil, fmt.Errorf("%w: missing required field(s): %v", ErrInvalidDBConfig, missing)
	}

	// Warn for ROUTER_* variables that are set but cannot override in
	// 002. Keeps operator intent auditable (FR-008 forward-compat).
	warnOnIgnoredEnvVars(env)

	return &cfg, srcMap, nil
}

// scanPresentKeys walks the raw JSON once and builds a set of dotted
// paths that are present in the file. Nested objects we care about
// (runtime, plugins.admin_auth, plugins.client_keys) each produce
// "parent.child" entries.
func scanPresentKeys(raw []byte) (map[string]struct{}, error) {
	top := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, err
	}
	present := make(map[string]struct{}, 16)

	if msg, ok := top["runtime"]; ok {
		inner := map[string]json.RawMessage{}
		if err := json.Unmarshal(msg, &inner); err != nil {
			return nil, fmt.Errorf("runtime: %w", err)
		}
		for k := range inner {
			present["runtime."+k] = struct{}{}
		}
	}

	if msg, ok := top["plugins"]; ok {
		plugins := map[string]json.RawMessage{}
		if err := json.Unmarshal(msg, &plugins); err != nil {
			return nil, fmt.Errorf("plugins: %w", err)
		}
		for pluginID, sub := range plugins {
			subMap := map[string]json.RawMessage{}
			if err := json.Unmarshal(sub, &subMap); err != nil {
				// Forward-compat: unknown plugin section may not be an
				// object (e.g. malformed future schema). Skip silently;
				// the strict struct decode will either accept or ignore.
				continue
			}
			for k := range subMap {
				present["plugins."+pluginID+"."+k] = struct{}{}
			}
		}
	}

	return present, nil
}

// applyRuntimeDefaults writes defaulted values into fields that were
// absent from the file and records the per-field source.
func applyRuntimeDefaults(rc *RuntimeConfig, defaults RuntimeConfig, present map[string]struct{}, src SourceMap) {
	if _, ok := present["runtime.log_client_request_body"]; ok {
		src["runtime.log_client_request_body"] = srcFile
	} else {
		rc.LogClientRequestBody = defaults.LogClientRequestBody
		src["runtime.log_client_request_body"] = srcDefault
	}
	if _, ok := present["runtime.log_upstream_request_body"]; ok {
		src["runtime.log_upstream_request_body"] = srcFile
	} else {
		rc.LogUpstreamRequestBody = defaults.LogUpstreamRequestBody
		src["runtime.log_upstream_request_body"] = srcDefault
	}
	if _, ok := present["runtime.log_upstream_response_body"]; ok {
		src["runtime.log_upstream_response_body"] = srcFile
	} else {
		rc.LogUpstreamResponseBody = defaults.LogUpstreamResponseBody
		src["runtime.log_upstream_response_body"] = srcDefault
	}
	if _, ok := present["runtime.log_retention_days"]; ok {
		src["runtime.log_retention_days"] = srcFile
	} else {
		rc.LogRetentionDays = defaults.LogRetentionDays
		src["runtime.log_retention_days"] = srcDefault
	}
	if _, ok := present["runtime.log_level"]; ok {
		src["runtime.log_level"] = srcFile
	} else {
		rc.LogLevel = defaults.LogLevel
		src["runtime.log_level"] = srcDefault
	}
	if _, ok := present["runtime.model_renames"]; ok {
		src["runtime.model_renames"] = srcFile
	} else {
		rc.ModelRenames = cloneModelRenameRules(defaults.ModelRenames)
		src["runtime.model_renames"] = srcDefault
	}
}

func cloneModelRenameRules(in []ModelRenameRule) []ModelRenameRule {
	if in == nil {
		return nil
	}
	out := make([]ModelRenameRule, len(in))
	copy(out, in)
	return out
}

// applyPluginDefaults sets per-plugin-flag sources. 002 only tracks
// {admin_auth, client_keys}.enabled; 003+ extend this routine.
func applyPluginDefaults(pc *PluginsConfig, present map[string]struct{}, src SourceMap) {
	if _, ok := present["plugins.admin_auth.enabled"]; ok {
		src["plugins.admin_auth.enabled"] = srcFile
	} else {
		pc.AdminAuth.Enabled = false
		src["plugins.admin_auth.enabled"] = srcDefault
	}
	if _, ok := present["plugins.client_keys.enabled"]; ok {
		src["plugins.client_keys.enabled"] = srcFile
	} else {
		pc.ClientKeys.Enabled = false
		src["plugins.client_keys.enabled"] = srcDefault
	}
}

// overlayEnv applies env overrides for the allow-listed keys in 002. It
// MUST NOT touch runtime.* or plugins.* fields (data-model.md §Field-
// level env override precedence).
func overlayEnv(cfg *Config, env Env, src SourceMap) {
	for _, binding := range envOverridableKeys {
		val, ok := env(binding.envVar)
		if !ok || val == "" {
			continue
		}
		switch binding.path {
		case "db.driver":
			cfg.DB.Driver = val
		case "db.url":
			cfg.DB.URL = val
		default:
			// Compile-time accident: an entry in envOverridableKeys
			// that this switch does not handle. Fail closed so the
			// mismatch is caught in tests.
			panic(fmt.Sprintf("config: overlayEnv has no case for %q", binding.path))
		}
		src[binding.path] = "env:" + binding.envVar
	}
}

// warnOnIgnoredEnvVars iterates the ignoredEnvVars slice and logs a
// slog.Warn for each one that is set. Keeps operators honest about
// which env vars actually reach the process.
func warnOnIgnoredEnvVars(env Env) {
	for _, name := range ignoredEnvVars {
		if _, ok := env(name); !ok {
			continue
		}
		slog.Warn("environment variable is set but NOT overridable in 002",
			"env_var", name,
			"hint", "edit config.json directly; 002 only honours ROUTER_DB_DRIVER and ROUTER_DB_URL",
		)
	}
}

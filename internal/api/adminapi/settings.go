// Package adminapi hosts the 002 admin portal JSON API: the envelope-
// wearing endpoints served under /api/admin/*.
//
// Two surfaces live here:
//
//  1. Settings (this package) — GET /api/admin/settings and
//     POST /api/admin/settings/update. These are new in 002 and are
//     the first endpoints to use the api.WriteOK / WriteBizErr /
//     WriteSysErr envelope helpers.
//  2. Envelope-wrapped 001 admin handlers — account CRUD, requests
//     query, session resolve, health. Implemented in wrap.go as
//     adapters that replay the 001 admin handlers through the
//     envelope so clients see a single consistent wire shape.
//
// The package intentionally carries zero logic beyond request
// shaping + envelope writing + validator delegation. Config persistence
// lives in internal/config; validation in internal/setup/validator.go.
package adminapi

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/user/one-llm-router/internal/api"
	"github.com/user/one-llm-router/internal/api/errcode"
	"github.com/user/one-llm-router/internal/api/httpio"
	"github.com/user/one-llm-router/internal/app/buildinfo"
	"github.com/user/one-llm-router/internal/config"
	"github.com/user/one-llm-router/internal/setup"
)

// SettingsMaxBodyBytes caps the size of /api/admin/settings/update at
// 16 KiB per admin-api.md §Cross-cutting concerns. Larger than the
// setup endpoints (8 KiB) because future settings keys may carry
// plugin-specific blobs in 003+.
const SettingsMaxBodyBytes = 16 << 10 // 16 KiB

// SettingsHandler serves the 002 settings surface. It depends only on
// the live config reader (for GETs) and a settings updater (for POSTs).
// Both are interfaces so unit tests can swap in fakes without touching
// disk.
type SettingsHandler struct {
	reader  ConfigReader
	updater SettingsUpdater
	// sourceReader answers "where did key K come from?" so Update
	// can fail with 2013 env_override_readonly when the operator
	// patches a field that is being overlaid from an env var. Nil
	// is tolerated — in that mode the guard degrades to "always
	// allow" which is the correct default when provenance is
	// unknown. Wired by SetSourceReader from app.go.
	sourceReader SourceReader
	logger       *slog.Logger
}

// SourceReader is the narrow read interface over config.SourceMap
// the Update handler needs to answer the 2013 guard question. Returns
// nil when provenance has not been published (setup-pending boots).
type SourceReader interface {
	Load() *config.SourceMap
}

// ConfigReader is the narrow dependency the handler needs to serve
// GETs. *config.LiveReader satisfies it; tests inject a fake.
type ConfigReader interface {
	Load() *config.Config
}

// SettingsUpdater persists a validated patch and returns the fresh
// Config the GET endpoint should reflect post-update. The handler
// owns envelope serialization; the updater owns atomic writes and the
// live publish.
type SettingsUpdater interface {
	Update(patch SettingsPatch) (*config.Config, error)
}

// NewSettingsHandler constructs a SettingsHandler. logger MUST be
// non-nil in production wiring — update audit lines land at INFO.
func NewSettingsHandler(reader ConfigReader, updater SettingsUpdater, logger *slog.Logger) *SettingsHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &SettingsHandler{reader: reader, updater: updater, logger: logger}
}

// SetSourceReader installs the provenance reader the Update handler
// consults before writing. Must be called at wiring time; nil clears
// the reader and disables the 2013 env_override_readonly guard.
func (h *SettingsHandler) SetSourceReader(r SourceReader) { h.sourceReader = r }

// Get serves GET /api/admin/settings. See admin-api.md for the wire
// shape. All fields are non-secret; db.url is NEVER included.
func (h *SettingsHandler) Get(w http.ResponseWriter, r *http.Request) {
	reqID := api.RequestIDFromContext(r.Context())
	// Admin settings reflect live hot-reloadable config fields and MUST
	// NOT be cached by browsers or intermediaries. Otherwise a second
	// tab loading /admin/settings would see pre-update state after a
	// successful POST /api/admin/settings/update, breaking the operator
	// feedback loop. `no-store` is stronger than `no-cache` because it
	// forbids persisting the response to disk, matching the
	// confidentiality expectations around plugin intents and DSN host
	// metadata.
	w.Header().Set("Cache-Control", "no-store")
	cfg := h.reader.Load()
	if cfg == nil {
		// Gate middleware normally catches setup-pending before the
		// request reaches this handler, but if the router is booted
		// into a weird split-brain state (gate open but live slot
		// not published) we still need to return a structured reply.
		api.WriteBizErr(w, reqID, errcode.SetupRequired,
			errcode.Symbol(errcode.SetupRequired), map[string]any{})
		return
	}
	api.WriteOK(w, reqID, projectSettings(cfg))
}

// Update serves POST /api/admin/settings/update. See admin-api.md.
func (h *SettingsHandler) Update(w http.ResponseWriter, r *http.Request) {
	reqID := api.RequestIDFromContext(r.Context())

	raw, ok := httpio.DecodeJSON[map[string]json.RawMessage](w, r, reqID, SettingsMaxBodyBytes)
	if !ok {
		return
	}
	if raw == nil {
		httpio.WriteMalformedBody(w, reqID)
		return
	}

	patch, vErr := parseAndValidatePatch(raw)
	if vErr != nil {
		api.WriteBizErr(w, reqID, vErr.Code, errcode.Symbol(vErr.Code),
			map[string]any{"field": vErr.Field, "detail": vErr.Msg})
		return
	}

	// Reject any patch key whose live source is an env var. The
	// operator cannot win that race — the next boot re-applies the
	// env overlay and silently reverts the patch — so returning
	// 2013 early preserves the "config.json is authoritative except
	// for env-overlaid keys" contract from data-model.md §Field-
	// level env override precedence. Nil sourceReader degrades to
	// "allow everything" which is the correct default for tests and
	// transitional boots where provenance has not been published.
	if h.sourceReader != nil {
		if srcPtr := h.sourceReader.Load(); srcPtr != nil {
			if field, envVar := envOverriddenPatchField(patch, *srcPtr); envVar != "" {
				api.WriteBizErr(w, reqID, errcode.EnvOverrideReadonly,
					errcode.Symbol(errcode.EnvOverrideReadonly),
					map[string]any{
						"field":   field,
						"env_var": envVar,
						"detail":  fmt.Sprintf("%s is overridden by env var %s; unset the env var to re-enable runtime edits", field, envVar),
					})
				return
			}
		}
	}

	cfg, err := h.updater.Update(patch)
	if err != nil {
		h.logger.Warn("admin settings update write failed",
			"request_id", reqID,
			"error", err,
		)
		api.WriteSysErr(w, reqID, errcode.ConfigWriteFailed,
			errcode.Symbol(errcode.ConfigWriteFailed))
		return
	}
	h.logger.Info("admin settings updated",
		"request_id", reqID,
		"runtime_keys_changed", patch.RuntimeKeys(),
		"plugin_keys_changed", patch.PluginKeys(),
	)
	api.WriteOK(w, reqID, projectSettings(cfg))
}

// projectSettings shapes an effective config into the /api/admin/
// settings wire payload. Separated out so the Update handler can
// return the same shape after a successful write.
//
// data.plugins[] stays empty in 002 by contract (admin-api.md v4.0 —
// only *registered* plugins appear there; 002 ships no concrete plugin
// binaries). data.plugin_intents surfaces the *operator-persisted*
// enabled flag from config.json so the Settings page's "Plugin
// intents" panel (D11) can render the correct switch state after a
// reload. The two concepts are deliberately separate:
//   - plugins[] answers "is this plugin live in this build?"
//   - plugin_intents answers "what did the operator choose while the
//     matching plugin binary is still absent in this build?"
func projectSettings(cfg *config.Config) map[string]any {
	return map[string]any{
		"runtime": map[string]any{
			"log_client_request_body":    cfg.Runtime.LogClientRequestBody,
			"log_upstream_request_body":  cfg.Runtime.LogUpstreamRequestBody,
			"log_upstream_response_body": cfg.Runtime.LogUpstreamResponseBody,
			"log_retention_days":         cfg.Runtime.LogRetentionDays,
			"log_level":                  cfg.Runtime.LogLevel,
		},
		"db":             projectDB(cfg.DB),
		"plugins":        []any{}, // 002 ships no concrete plugins
		"plugin_intents": projectPluginIntents(cfg.Plugins),
		"system":         projectSystem(),
	}
}

// projectPluginIntents exposes the operator's persisted plugin-enable
// choices (D9 + D11). Shape is an ordered list so clients get
// deterministic render order regardless of Go map iteration order.
// Each row is {id, label, enabled}. Rows correspond 1:1 to the known
// plugin sub-structs in config.PluginsConfig; 003/004/005 add new rows
// as they land their PluginsConfig fields (prometheus in 005, etc.).
func projectPluginIntents(p config.PluginsConfig) []map[string]any {
	return []map[string]any{
		{
			"id":      "admin_auth",
			"label":   "Admin authentication",
			"enabled": p.AdminAuth.Enabled,
			"status":  "intent only",
		},
		{
			"id":      "client_keys",
			"label":   "Client API keys",
			"enabled": p.ClientKeys.Enabled,
			"status":  "intent only",
		},
	}
}

// projectDB returns the non-secret identity fields for the db block.
// db.url is NEVER returned — it may contain credentials.
func projectDB(db config.DBConfig) map[string]any {
	out := map[string]any{
		"driver":        db.Driver,
		"host":          "",
		"database_name": "",
	}
	switch db.Driver {
	case "sqlite3":
		out["host"] = "local"
		out["database_name"] = filepath.Base(db.URL)
	case "postgres":
		host, dbName := parsePostgresDSN(db.URL)
		out["host"] = host
		out["database_name"] = dbName
	case "mysql":
		host, dbName := parseMySQLDSN(db.URL)
		out["host"] = host
		out["database_name"] = dbName
	}
	return out
}

// parsePostgresDSN returns ("host:port", "database_name") for
// postgres://user:pass@host:port/db?... or the libpq keyword form
// "host=... port=... dbname=...". On parse failure returns "", "".
// The implementation is defensive: bad DSNs should not crash the
// settings page, they should just leave the identity blank.
func parsePostgresDSN(dsn string) (string, string) {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return "", ""
		}
		host := u.Host
		if host != "" && !strings.Contains(host, ":") {
			// Port defaults to 5432 per admin-api.md §db.host ("port
			// always included, even when the driver default is used").
			host = host + ":5432"
		}
		path := strings.TrimPrefix(u.Path, "/")
		return host, path
	}
	// libpq keyword form — split on whitespace, parse k=v pairs.
	var host, port, dbName string
	for _, tok := range strings.Fields(dsn) {
		eq := strings.IndexByte(tok, '=')
		if eq < 0 {
			continue
		}
		k, v := tok[:eq], tok[eq+1:]
		switch k {
		case "host":
			host = v
		case "port":
			port = v
		case "dbname":
			dbName = v
		}
	}
	if host == "" {
		return "", dbName
	}
	if port == "" {
		port = "5432"
	}
	return host + ":" + port, dbName
}

// parseMySQLDSN parses the go-sql-driver/mysql DSN format:
//
//	user:pass@tcp(host:port)/dbname?opts
//
// Returns ("host:port", "dbname") on success; ("", "") otherwise.
func parseMySQLDSN(dsn string) (string, string) {
	// Split at the first '@' to separate credentials from host.
	at := strings.Index(dsn, "@")
	if at < 0 {
		return "", ""
	}
	rest := dsn[at+1:]
	// rest looks like tcp(host:port)/dbname?opts or unix(socket)/dbname.
	open := strings.Index(rest, "(")
	closeIdx := strings.Index(rest, ")")
	if open < 0 || closeIdx < 0 || closeIdx < open {
		return "", ""
	}
	host := rest[open+1 : closeIdx]
	after := rest[closeIdx+1:]
	// after = "/dbname?opts" or "/dbname" or ""
	after = strings.TrimPrefix(after, "/")
	if i := strings.Index(after, "?"); i >= 0 {
		after = after[:i]
	}
	if host != "" && !strings.Contains(host, ":") {
		host = host + ":3306"
	}
	return host, after
}

// projectSystem returns the `system` block of the settings payload.
// Reads the compile-time buildinfo globals; never inspects env.
func projectSystem() map[string]any {
	return map[string]any{
		"router_version":  buildinfo.Version,
		"router_git_sha":  buildinfo.GitSHA,
		"router_built_at": buildinfo.BuiltAt,
	}
}

// SettingsPatch is the typed partial-patch shape. Every field is a
// pointer so the Update logic can distinguish "not provided" from
// "explicitly set to zero".
type SettingsPatch struct {
	LogClientRequestBody    *bool   // runtime.log_client_request_body
	LogUpstreamRequestBody  *bool   // runtime.log_upstream_request_body
	LogUpstreamResponseBody *bool   // runtime.log_upstream_response_body
	LogRetentionDays        *int    // runtime.log_retention_days
	LogLevel                *string // runtime.log_level
	AdminAuthEnabled        *bool   // plugins.admin_auth.enabled
	ClientKeysEnabled       *bool   // plugins.client_keys.enabled
}

// RuntimeKeys returns the slice of runtime keys actually present in
// the patch. Used for audit logging so operators can see which four
// keys were touched.
func (p SettingsPatch) RuntimeKeys() []string {
	var out []string
	if p.LogClientRequestBody != nil {
		out = append(out, "log_client_request_body")
	}
	if p.LogUpstreamRequestBody != nil {
		out = append(out, "log_upstream_request_body")
	}
	if p.LogUpstreamResponseBody != nil {
		out = append(out, "log_upstream_response_body")
	}
	if p.LogRetentionDays != nil {
		out = append(out, "log_retention_days")
	}
	if p.LogLevel != nil {
		out = append(out, "log_level")
	}
	return out
}

// PluginKeys returns the slice of plugin ids touched by the patch.
func (p SettingsPatch) PluginKeys() []string {
	var out []string
	if p.AdminAuthEnabled != nil {
		out = append(out, "admin_auth")
	}
	if p.ClientKeysEnabled != nil {
		out = append(out, "client_keys")
	}
	return out
}

// IsEmpty reports whether the patch contains no actionable keys.
func (p SettingsPatch) IsEmpty() bool {
	return len(p.RuntimeKeys()) == 0 && len(p.PluginKeys()) == 0
}

// parseAndValidatePatch reads the decoded top-level map, detects
// unknown keys (→ 2012), decodes the two known sub-blocks, and runs
// field-level validation. Returns the typed patch on success.
func parseAndValidatePatch(raw map[string]json.RawMessage) (SettingsPatch, *setup.ValidationError) {
	var patch SettingsPatch

	for k := range raw {
		switch k {
		case "runtime", "plugins":
			// OK
		default:
			return patch, &setup.ValidationError{
				Code:  errcode.UnknownConfigKey,
				Msg:   fmt.Sprintf("config key %q is not patchable in 002", k),
				Field: k,
			}
		}
	}

	if runtimeRaw, ok := raw["runtime"]; ok {
		runtimeMap := map[string]json.RawMessage{}
		if err := json.Unmarshal(runtimeRaw, &runtimeMap); err != nil {
			return patch, &setup.ValidationError{
				Code: errcode.MalformedBody,
				Msg:  "runtime must be a JSON object",
			}
		}
		if vErr := decodeRuntimePatch(runtimeMap, &patch); vErr != nil {
			return patch, vErr
		}
	}

	if pluginsRaw, ok := raw["plugins"]; ok {
		pluginsMap := map[string]json.RawMessage{}
		if err := json.Unmarshal(pluginsRaw, &pluginsMap); err != nil {
			return patch, &setup.ValidationError{
				Code: errcode.MalformedBody,
				Msg:  "plugins must be a JSON object",
			}
		}
		if vErr := decodePluginsPatch(pluginsMap, &patch); vErr != nil {
			return patch, vErr
		}
	}

	return patch, nil
}

// decodeRuntimePatch inspects each present runtime key, enforcing its
// type + range. Unknown runtime keys return 2012 unknown_config_key
// (admin-api.md §Response errors — "any other root-level key" covers
// runtime.* unknowns by extension; dotted fallback is kept symmetric).
func decodeRuntimePatch(raw map[string]json.RawMessage, patch *SettingsPatch) *setup.ValidationError {
	for k, v := range raw {
		switch k {
		case "log_client_request_body":
			b, err := unmarshalBool(v)
			if err != nil {
				return &setup.ValidationError{
					Code:  errcode.MalformedBody,
					Msg:   "log_client_request_body must be a boolean",
					Field: "runtime.log_client_request_body",
				}
			}
			patch.LogClientRequestBody = &b
		case "log_upstream_request_body":
			b, err := unmarshalBool(v)
			if err != nil {
				return &setup.ValidationError{
					Code:  errcode.MalformedBody,
					Msg:   "log_upstream_request_body must be a boolean",
					Field: "runtime.log_upstream_request_body",
				}
			}
			patch.LogUpstreamRequestBody = &b
		case "log_upstream_response_body":
			b, err := unmarshalBool(v)
			if err != nil {
				return &setup.ValidationError{
					Code:  errcode.MalformedBody,
					Msg:   "log_upstream_response_body must be a boolean",
					Field: "runtime.log_upstream_response_body",
				}
			}
			patch.LogUpstreamResponseBody = &b
		case "log_retention_days":
			var n int
			if err := json.Unmarshal(v, &n); err != nil {
				return &setup.ValidationError{
					Code:  errcode.InvalidRetention,
					Msg:   "log_retention_days must be an integer in [1, 365]",
					Field: "runtime.log_retention_days",
				}
			}
			if vErr := setup.NewValidator().ValidateRetention(n); vErr != nil {
				return vErr
			}
			patch.LogRetentionDays = &n
		case "log_level":
			var s string
			if err := json.Unmarshal(v, &s); err != nil {
				return &setup.ValidationError{
					Code:  errcode.InvalidLogLevel,
					Msg:   "log_level must be one of debug, info, warn, error",
					Field: "runtime.log_level",
				}
			}
			if vErr := setup.NewValidator().ValidateLogLevel(s); vErr != nil {
				return vErr
			}
			patch.LogLevel = &s
		default:
			return &setup.ValidationError{
				Code:  errcode.UnknownConfigKey,
				Msg:   fmt.Sprintf("config key %q is not patchable in 002", "runtime."+k),
				Field: "runtime." + k,
			}
		}
	}
	return nil
}

// decodePluginsPatch walks the plugins object. Every key must be a
// known plugin id whose PluginsConfig sub-struct exists in this build
// (002: admin_auth, client_keys); everything else → 2012.
func decodePluginsPatch(raw map[string]json.RawMessage, patch *SettingsPatch) *setup.ValidationError {
	for id, block := range raw {
		sub := map[string]json.RawMessage{}
		if err := json.Unmarshal(block, &sub); err != nil {
			return &setup.ValidationError{
				Code:  errcode.InvalidPluginFlag,
				Msg:   fmt.Sprintf("plugins.%s must be an object {enabled: bool}", id),
				Field: "plugins." + id,
			}
		}
		// Only `enabled` is writable in 002. Any other sub-key → 2012
		// (forward-compat: 003+ extends the allow-list).
		for k := range sub {
			if k != "enabled" {
				return &setup.ValidationError{
					Code: errcode.UnknownConfigKey,
					Msg: fmt.Sprintf("config key %q is not patchable in 002",
						"plugins."+id+"."+k),
					Field: "plugins." + id + "." + k,
				}
			}
		}

		enabledRaw, hasEnabled := sub["enabled"]
		if !hasEnabled {
			// A plugin block with no `enabled` is a no-op; skip.
			continue
		}
		b, err := unmarshalBool(enabledRaw)
		if err != nil {
			return &setup.ValidationError{
				Code:  errcode.InvalidPluginFlag,
				Msg:   fmt.Sprintf("plugins.%s.enabled must be a boolean", id),
				Field: "plugins." + id + ".enabled",
			}
		}

		switch id {
		case "admin_auth":
			patch.AdminAuthEnabled = &b
		case "client_keys":
			patch.ClientKeysEnabled = &b
		default:
			return &setup.ValidationError{
				Code:  errcode.UnknownConfigKey,
				Msg:   fmt.Sprintf("config key %q is not patchable in 002", "plugins."+id),
				Field: "plugins." + id,
			}
		}
	}
	return nil
}

// envOverriddenPatchField scans the patch fields against the
// published SourceMap. Returns (fieldPath, envVar) for the FIRST
// offending key whose provenance starts with "env:". Returns ("", "")
// when no patch field is env-sourced. Iteration order is deterministic
// (runtime first, then plugins) so operators always see the same
// first-failure message when multiple env-overridden keys are sent.
func envOverriddenPatchField(patch SettingsPatch, src config.SourceMap) (string, string) {
	type probe struct {
		present bool
		key     string
	}
	probes := []probe{
		{patch.LogClientRequestBody != nil, "runtime.log_client_request_body"},
		{patch.LogUpstreamRequestBody != nil, "runtime.log_upstream_request_body"},
		{patch.LogUpstreamResponseBody != nil, "runtime.log_upstream_response_body"},
		{patch.LogRetentionDays != nil, "runtime.log_retention_days"},
		{patch.LogLevel != nil, "runtime.log_level"},
		{patch.AdminAuthEnabled != nil, "plugins.admin_auth.enabled"},
		{patch.ClientKeysEnabled != nil, "plugins.client_keys.enabled"},
	}
	for _, p := range probes {
		if !p.present {
			continue
		}
		provenance := src[p.key]
		if strings.HasPrefix(provenance, "env:") {
			return p.key, strings.TrimPrefix(provenance, "env:")
		}
	}
	return "", ""
}

// unmarshalBool decodes a single JSON bool. Returns an error on any
// non-bool token including JSON null.
func unmarshalBool(raw json.RawMessage) (bool, error) {
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		return false, err
	}
	return b, nil
}

// Package setup hosts the 002 setup-wizard backend: the state reader
// (state.go), the per-request gate (gate.go), the brownfield auto-
// materializer (brownfield.go), the DSN probe (probe.go), the wizard
// validator (this file → validator.go), and the Commit transaction
// (commit.go).
//
// Validator contract (T-100 / data-model.md §Validation Rules):
//
//   - Inputs come from JSON that the HTTP layer has already decoded
//     into the CommitRequest / UpdateRequest struct. Validators do NOT
//     parse JSON; they assume a typed input.
//   - The first offending field wins — the validator returns a single
//     (code, msg, field) tuple and stops. The handler is responsible
//     for mapping that tuple onto the response envelope.
//   - Code integers are sourced from internal/api/errcode to keep the
//     registry the single source of truth.
//   - Every error returned from this file MUST appear in
//     docs/error-codes.md §Feature 002 and in contracts/setup-api.md
//     §Response errors — the test `TestValidator_EveryCodeReachable`
//     enforces that every 2002–2008 + 2014 code has at least one
//     reaching test input.
//
// Why a dedicated validator package instead of inline handler logic:
//   - The commit path and the admin-settings-update path both share
//     the `log_retention_days`, `log_level`, and plugin-flag rules.
//     Centralising keeps the two paths from drifting.
//   - The Wizard UI and the CLI (if added later) both need the same
//     error shape for a consistent operator experience.
//   - Unit-testing a pure function is strictly cheaper than driving a
//     full HTTP fixture.
package setup

import (
	"errors"
	"regexp"
	"strings"

	"github.com/user/one-llm-router/internal/api/errcode"
	"github.com/user/one-llm-router/internal/config"
	"github.com/user/one-llm-router/internal/domain"
)

// ValidationError is the structured result of a validation failure.
// It pairs an errcode registry integer with a human-readable message
// and the dotted field path that violated the rule. The handler maps
// this onto WriteBizErr so the envelope stays consistent across both
// setup-wizard and settings-update endpoints.
//
// Field is OPTIONAL — some errors (malformed_body, request_body_too_large)
// do not identify a single field and leave Field="". Clients MUST tolerate
// an empty Field; the envelope's data may still carry a driver hint.
type ValidationError struct {
	Code  int    // errcode.* constant
	Msg   string // short snake_case-ish stable string
	Field string // optional dotted path, e.g. "first_account.name"
}

// Error implements error so callers can `errors.Is`-style discriminate
// if they ever need to treat validator failures like other errors.
// 002 handlers use WriteBizErr(code, msg, data) directly and do not
// rely on this — the method exists purely because linters expect it
// and because a future 003 consumer may want the error interface.
func (ve *ValidationError) Error() string {
	if ve.Field != "" {
		return ve.Msg + " (field=" + ve.Field + ")"
	}
	return ve.Msg
}

// SupportedDrivers lists the DB driver identifiers the 002 wizard
// accepts. Keep in sync with internal/store/dialect_*.go and
// contracts/setup-api.md §POST /api/setup/probe-dsn.
//
// Exported so the /api/setup/status handler can surface the same list
// without re-encoding the magic strings.
var SupportedDrivers = []string{"sqlite3", "postgres", "mysql"}

// SupportedLogLevels is the enum for runtime.log_level. Order matches
// docs/error-codes.md §2014 invalid_log_level. Exported for the admin-
// settings-update path to reuse.
var SupportedLogLevels = []string{"debug", "info", "warn", "error"}

// RetentionMin / RetentionMax bracket runtime.log_retention_days (data-
// model.md §Validation Rules). Kept as exported constants so callers
// can surface them in UI hints rather than hard-coding the numbers.
const (
	RetentionMin = 1
	RetentionMax = 365
)

// DSNMaxLen is the upper bound for db.url after normalization. 4096 is
// large enough for real-world Postgres DSNs with TLS parameters and
// small enough that an accidental copy-paste of a whole log file is
// rejected before we try to open a connection.
const DSNMaxLen = 4096

// APIKeyMaxLen / APIKeyMinLen bracket the seed account api_key.
const (
	APIKeyMinLen = 1
	APIKeyMaxLen = 256
)

// BaseURLMaxLen bounds first_account.base_url; 256 mirrors the upstream
// proxy's base URL cap.
const BaseURLMaxLen = 256

// accountNameRE is the canonical regex for first_account.name — also
// consumed by the admin CRUD endpoints that 003 ships.
var accountNameRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// CommitRequest is the typed shape the setup-commit handler decodes.
// Every field is exported so the HTTP handler can initialize it from
// json.Unmarshal without a second hop. Validator.Commit walks the
// struct.
type CommitRequest struct {
	DB           DBRequestBlock       `json:"db"`
	FirstAccount AccountRequestBlock  `json:"first_account"`
	Plugins      PluginsRequestBlock  `json:"plugins"`
	Runtime      *RuntimeRequestBlock `json:"runtime,omitempty"`
	RawPlugins   map[string]any       `json:"-"` // unknown-key detection, populated by the handler
	RawRuntime   map[string]any       `json:"-"` // partial-runtime detection, populated by the handler
	RawTop       map[string]any       `json:"-"` // top-level unknown keys
}

// DBRequestBlock mirrors the commit payload's `db` sub-object.
type DBRequestBlock struct {
	Driver string `json:"driver"`
	URL    string `json:"url"`
}

// AccountRequestBlock mirrors `first_account`. BaseURL is a pointer so
// missing vs. explicit-null vs. empty-string are distinguishable.
//
// From 2026-04-15 an entirely-empty block (all scalar fields zero and
// BaseURL nil/empty) means "skip account seeding" — the wizard lets
// operators defer account registration to the admin portal so they can
// walk up to a freshly migrated database before wiring upstream keys.
// Partially-filled blocks still fail validation so fat-fingered inputs
// surface a specific error.
type AccountRequestBlock struct {
	Name     string  `json:"name"`
	Provider string  `json:"provider"`
	APIKey   string  `json:"api_key"`
	BaseURL  *string `json:"base_url,omitempty"`
}

// IsEmpty reports whether the caller omitted first_account entirely —
// every scalar is the zero string and BaseURL carries no value. In that
// case the wizard commit skips the account insert (setup-api.md v2.4).
//
// Callers that need to distinguish "omitted" from "partially present"
// use this together with the individual validators: IsEmpty==false but
// APIKey=="" surfaces a normal 2004 invalid_api_key error.
func (a AccountRequestBlock) IsEmpty() bool {
	if a.Name != "" || a.Provider != "" || a.APIKey != "" {
		return false
	}
	if a.BaseURL != nil && *a.BaseURL != "" {
		return false
	}
	return true
}

// PluginsRequestBlock mirrors the two plugin-flag objects the wizard
// records operator intent for. Every plugin here must have a matching
// config.PluginsConfig field — otherwise the handler returns
// `2006 invalid_plugin_flag` during unknown-ID triage.
type PluginsRequestBlock struct {
	AdminAuth  PluginFlagBlock `json:"admin_auth"`
	ClientKeys PluginFlagBlock `json:"client_keys"`
}

// PluginFlagBlock is the `{enabled: bool}` shape used by both the
// wizard and the settings-update endpoint (2026-04-18 v1.4 contract).
type PluginFlagBlock struct {
	Enabled bool `json:"enabled"`
}

// RuntimeRequestBlock is OPTIONAL on commit (the 002 wizard does not
// expose runtime knobs — see setup-api.md v2.2). When present, all
// five sub-fields are required; the handler populates RawRuntime from
// the original JSON so the validator can catch partials.
type RuntimeRequestBlock struct {
	LogClientRequestBody    *bool   `json:"log_client_request_body"`
	LogUpstreamRequestBody  *bool   `json:"log_upstream_request_body"`
	LogUpstreamResponseBody *bool   `json:"log_upstream_response_body"`
	LogRetentionDays        *int    `json:"log_retention_days"`
	LogLevel                *string `json:"log_level"`
}

// Validator is a stateless record. Exposed as a type (rather than
// free-functions) so future overrides (e.g. a test harness) can swap
// rules without editing call sites.
type Validator struct{}

// NewValidator returns a default Validator.
func NewValidator() Validator { return Validator{} }

// Commit validates a CommitRequest. Returns nil on success; otherwise
// a *ValidationError carrying the first violation. The order of checks
// is deterministic (driver → dsn → account → plugins → runtime) so
// the operator sees the earliest fixable issue first.
func (v Validator) Commit(req *CommitRequest) *ValidationError {
	if err := v.DriverEnum(req.DB.Driver); err != nil {
		return err
	}
	if err := v.DSN(req.DB.URL); err != nil {
		return err
	}
	// first_account is optional (setup-api.md v2.4). A fully-empty
	// block means "defer account seeding"; partially-filled blocks
	// still walk the per-field validators so the operator sees a
	// specific error rather than silently being accepted.
	if !req.FirstAccount.IsEmpty() {
		if err := v.AccountName(req.FirstAccount.Name); err != nil {
			return err
		}
		if err := v.AccountProvider(req.FirstAccount.Provider); err != nil {
			return err
		}
		if err := v.APIKey(req.FirstAccount.APIKey); err != nil {
			return err
		}
		if err := v.BaseURL(req.FirstAccount.BaseURL); err != nil {
			return err
		}
	}
	if err := v.PluginsBlock(&req.Plugins); err != nil {
		return err
	}
	// Runtime is optional at commit time. If present, all four fields
	// must be populated (setup-api.md §Validation Rules — "partial
	// runtime objects are rejected with 2008 malformed_body"). The
	// handler surfaces RawRuntime so we can detect partial payloads
	// that a typed struct would silently fill with Go zero values.
	if req.Runtime != nil {
		if err := v.RuntimeRequired(req.Runtime, req.RawRuntime); err != nil {
			return err
		}
		if err := v.RuntimeFields(req.Runtime); err != nil {
			return err
		}
	}
	return nil
}

// DriverEnum validates db.driver against SupportedDrivers.
func (v Validator) DriverEnum(driver string) *ValidationError {
	if driver == "" {
		return &ValidationError{
			Code:  errcode.InvalidDriver,
			Msg:   "driver must be one of: " + strings.Join(SupportedDrivers, ", "),
			Field: "db.driver",
		}
	}
	for _, d := range SupportedDrivers {
		if driver == d {
			return nil
		}
	}
	return &ValidationError{
		Code:  errcode.InvalidDriver,
		Msg:   "driver must be one of: " + strings.Join(SupportedDrivers, ", "),
		Field: "db.driver",
	}
}

// DSN validates db.url length.
func (v Validator) DSN(url string) *ValidationError {
	if url == "" {
		return &ValidationError{
			Code:  errcode.InvalidDSN,
			Msg:   "DSN is empty or exceeds 4096 characters",
			Field: "db.url",
		}
	}
	if len(url) > DSNMaxLen {
		return &ValidationError{
			Code:  errcode.InvalidDSN,
			Msg:   "DSN is empty or exceeds 4096 characters",
			Field: "db.url",
		}
	}
	return nil
}

// AccountName validates first_account.name against the regex.
func (v Validator) AccountName(name string) *ValidationError {
	if !accountNameRE.MatchString(name) {
		return &ValidationError{
			Code:  errcode.InvalidAccountName,
			Msg:   "account name must match ^[A-Za-z0-9_-]{1,64}$",
			Field: "first_account.name",
		}
	}
	return nil
}

// AccountProvider enforces the 002 provider allow-list (currently
// only "openai" per setup-api.md — the MVP has no Anthropic support
// path in the wizard even though domain.ProviderAnthropic exists).
//
// Emits 2015 invalid_account_provider (002-specific) rather than
// 1003 invalid_account_payload (001) so envelope clients can branch
// on the code without parsing msg.
func (v Validator) AccountProvider(provider string) *ValidationError {
	if provider != "openai" {
		return &ValidationError{
			Code:  errcode.InvalidAccountProvider,
			Msg:   "provider must be 'openai' in 002",
			Field: "first_account.provider",
		}
	}
	return nil
}

// APIKey validates first_account.api_key length. The value is NEVER
// echoed in error messages — callers get a length-diagnostic only.
func (v Validator) APIKey(apiKey string) *ValidationError {
	n := len(apiKey)
	if n < APIKeyMinLen || n > APIKeyMaxLen {
		return &ValidationError{
			Code:  errcode.InvalidAPIKey,
			Msg:   "api_key must be 1..256 characters",
			Field: "first_account.api_key",
		}
	}
	return nil
}

// BaseURL validates first_account.base_url when present (nil/empty
// string are both treated as "not supplied"). Delegates to the shared
// domain.ValidateBaseURL so the wizard and the 001 admin CRUD path
// apply identical rules (scheme http/https, no path/query/fragment,
// length ≤256, host required).
//
// Emits 2016 invalid_base_url on any rule failure. The underlying
// *domain.ValidationError's Message is preserved so operators see
// the specific reason (e.g. "base_url must not contain a path").
func (v Validator) BaseURL(baseURL *string) *ValidationError {
	if baseURL == nil || *baseURL == "" {
		return nil
	}
	if err := domain.ValidateBaseURL(*baseURL); err != nil {
		// domain.ValidateBaseURL's Field is "base_url"; we namespace
		// it with "first_account." so the wizard UI highlights the
		// right input.
		msg := err.Error()
		var ve *domain.ValidationError
		if errors.As(err, &ve) {
			msg = ve.Message
		}
		return &ValidationError{
			Code:  errcode.InvalidBaseURL,
			Msg:   msg,
			Field: "first_account.base_url",
		}
	}
	return nil
}

// PluginsBlock currently validates only that the two known plugin
// flags ({admin_auth.enabled, client_keys.enabled}) are present — the
// typed decoder already enforces `enabled` is a bool. The handler's
// RawPlugins check covers unknown plugin keys (returns 2006 per D9).
func (v Validator) PluginsBlock(_ *PluginsRequestBlock) *ValidationError {
	// Reserved hook — currently no synchronous rule. See
	// handler-level unknown-id handling for the 2006 path.
	return nil
}

// RuntimeRequired ensures every runtime sub-field is present when a
// runtime block is supplied. The handler fills RawRuntime from the
// raw JSON; absent keys surface as "partial" errors via 2008.
func (v Validator) RuntimeRequired(rt *RuntimeRequestBlock, raw map[string]any) *ValidationError {
	required := []struct {
		key     string
		present bool
	}{
		{"log_client_request_body", rt.LogClientRequestBody != nil},
		{"log_upstream_request_body", rt.LogUpstreamRequestBody != nil},
		{"log_upstream_response_body", rt.LogUpstreamResponseBody != nil},
		{"log_retention_days", rt.LogRetentionDays != nil},
		{"log_level", rt.LogLevel != nil},
	}
	for _, f := range required {
		if _, ok := raw[f.key]; !ok || !f.present {
			return &ValidationError{
				Code:  errcode.MalformedBody,
				Msg:   "runtime block is partial; all 5 keys are required when runtime is present",
				Field: "runtime." + f.key,
			}
		}
	}
	return nil
}

// RuntimeFields validates each runtime sub-field after RuntimeRequired
// has confirmed they are all present. Split so the admin-settings-
// update path can call just this half (partial patches are allowed
// there).
func (v Validator) RuntimeFields(rt *RuntimeRequestBlock) *ValidationError {
	if rt.LogRetentionDays != nil {
		if *rt.LogRetentionDays < RetentionMin || *rt.LogRetentionDays > RetentionMax {
			return &ValidationError{
				Code:  errcode.InvalidRetention,
				Msg:   "log_retention_days must be an integer in [1, 365]",
				Field: "runtime.log_retention_days",
			}
		}
	}
	if rt.LogLevel != nil {
		if !isValidLogLevel(*rt.LogLevel) {
			return &ValidationError{
				Code:  errcode.InvalidLogLevel,
				Msg:   "log_level must be one of debug, info, warn, error",
				Field: "runtime.log_level",
			}
		}
	}
	return nil
}

// ValidateLogLevel exposes the log-level check as a standalone helper
// for the admin-settings-update path (which validates log_level
// independently of retention).
func (v Validator) ValidateLogLevel(level string) *ValidationError {
	if !isValidLogLevel(level) {
		return &ValidationError{
			Code:  errcode.InvalidLogLevel,
			Msg:   "log_level must be one of debug, info, warn, error",
			Field: "runtime.log_level",
		}
	}
	return nil
}

// ValidateRetention exposes the retention-range check as a standalone
// helper for the admin-settings-update path.
func (v Validator) ValidateRetention(days int) *ValidationError {
	if days < RetentionMin || days > RetentionMax {
		return &ValidationError{
			Code:  errcode.InvalidRetention,
			Msg:   "log_retention_days must be an integer in [1, 365]",
			Field: "runtime.log_retention_days",
		}
	}
	return nil
}

// isValidLogLevel lifts the enum check to avoid re-iterating the
// slice at every call site.
func isValidLogLevel(level string) bool {
	for _, l := range SupportedLogLevels {
		if level == l {
			return true
		}
	}
	return false
}

// ApplyRuntimeDefaults fills absent runtime sub-fields with the 002
// defaults. Used by the commit handler when the wizard omits the
// runtime block entirely (setup-api.md v2.2).
func ApplyRuntimeDefaults(rt *RuntimeRequestBlock) config.RuntimeConfig {
	out := config.DefaultRuntimeConfig()
	if rt == nil {
		return out
	}
	if rt.LogClientRequestBody != nil {
		out.LogClientRequestBody = *rt.LogClientRequestBody
	}
	if rt.LogUpstreamRequestBody != nil {
		out.LogUpstreamRequestBody = *rt.LogUpstreamRequestBody
	}
	if rt.LogUpstreamResponseBody != nil {
		out.LogUpstreamResponseBody = *rt.LogUpstreamResponseBody
	}
	if rt.LogRetentionDays != nil {
		out.LogRetentionDays = *rt.LogRetentionDays
	}
	if rt.LogLevel != nil {
		out.LogLevel = *rt.LogLevel
	}
	return out
}

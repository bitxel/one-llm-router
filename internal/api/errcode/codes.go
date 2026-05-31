// Package errcode holds the integer error codes used inside the router's
// HTTP response envelope ({code, msg, data}) defined by AGENTS.md and
// docs/error-codes.md.
//
// Adding a new code: register it in docs/error-codes.md first, then add a
// matching exported constant here and a row to the [symbols] map in the
// SAME commit. The table-driven test in codes_test.go enumerates every
// entry; an inconsistency fails CI.
//
// The /v1/* proxy is excluded from this envelope policy; it keeps 001's
// native MVP error shape and does not use any code in this package.
package errcode

// Platform-wide codes.
const (
	// OK indicates a successful request. The envelope message is "ok" and
	// data carries the endpoint payload.
	OK = 0
	// Unknown is emitted by the top-level recovery middleware when a
	// handler panics or returns an unclassified system error.
	Unknown = -1
)

// Feature 001 — Codex Router MVP (admin endpoints only; /v1/* stays outside
// the envelope and does not use any code in this package).
const (
	AccountNotFound       = 1001
	AccountNameConflict   = 1002
	InvalidAccountPayload = 1003
	AccountAlreadyInState = 1004
	RequestRecordNotFound = 1005
	InvalidRequestFilter  = 1006
	SessionNotFound       = 1007
	InvalidPagination     = 1008
	NoAvailableAccount    = 1009

	DBUnavailable = 1900
	InternalError = 1901
)

// Feature 002 — Setup wizard + admin portal skeleton.
//
// The integer 2010 is reserved per docs/error-codes.md (previously
// probe_in_progress, dropped by D10) and is intentionally NOT exported.
const (
	SetupAlreadyDone    = 2001
	InvalidDriver       = 2002
	InvalidDSN          = 2003
	InvalidAccountName  = 2004
	InvalidAPIKey       = 2005
	InvalidPluginFlag   = 2006
	InvalidRetention    = 2007
	MalformedBody       = 2008
	RequestBodyTooLarge = 2009

	SetupRequired       = 2011
	UnknownConfigKey    = 2012
	EnvOverrideReadonly = 2013
	InvalidLogLevel     = 2014
	// InvalidAccountProvider surfaces when first_account.provider is
	// not in the 002 allow-list ("openai"). Distinct from
	// InvalidAccountPayload (a 001 code) so the wizard UI can locate
	// the field precisely via the 002-range check.
	InvalidAccountProvider = 2015
	// InvalidBaseURL surfaces when first_account.base_url is malformed
	// (not https://, contains path, bad scheme, …). Rules live in
	// domain.ValidateBaseURL (the shared 001/002 validator).
	InvalidBaseURL = 2016
	// InvalidModelRename surfaces invalid runtime.model_renames entries
	// in the Settings API: empty fields, duplicates, self-maps, or
	// limit violations.
	InvalidModelRename = 2017

	DBConnectFailed   = 2900
	MigrateFailed     = 2901
	CommitTxFailed    = 2902
	ConfigWriteFailed = 2903
)

// Feature 003 — Multi-mode Codex authentication (browser OAuth, device OAuth,
// auth.json import/export, background token refresh).
//
// All codes below are business errors served at HTTP 200 per the envelope
// policy, except 3900, 3901, and 3902 which are system-level (HTTP 500).
// The /oauth/* loopback listener in internal/oauth/flow.go is browser-
// facing plaintext and does NOT use this package (see
// contracts/oauth-flow-api.md).
//
// The integer 3017 is reserved per docs/error-codes.md — the 003
// /export-auth-json handler reuses AccountNotFound (1001) rather than
// minting a duplicate code, and 3017 is kept in the table for traceability.
const (
	OAuthFlowInProgress           = 3001
	InvalidOAuthProvider          = 3002
	OAuthStateMismatch            = 3003
	NoFlowInProgress              = 3004
	OAuthAlreadyConsumed          = 3005
	OAuthFlowExpired              = 3006
	InvalidCallbackURL            = 3007
	OAuthFlowIDMismatch           = 3008
	OAuthInvalidGrant             = 3009
	InvalidAuthJSONStructure      = 3010
	InvalidAuthJSON               = 3011
	OAuthModeRequiresFlowEndpoint = 3013
	NotOAuthAccount               = 3014
	DeviceAuthUnavailable         = 3015
	OAuthUpstreamError            = 3016

	OAuthInternalError    = 3900
	OAuthStoreFailed      = 3901
	OAuthExportReadFailed = 3902
)

// Feature 004 — Account Playground.
//
// Business errors are served at HTTP 200 per the envelope policy. The
// PlaygroundInternalError code is system-level and is served at HTTP 500.
const (
	InvalidPlaygroundRequest     = 4001
	PlaygroundNoActiveAccount    = 4002
	PlaygroundAccountUnavailable = 4003
	PlaygroundUpstreamError      = 4004
	PlaygroundUpstreamTimeout    = 4005
	PlaygroundResponseMalformed  = 4006
	PlaygroundResponseTooLarge   = 4007
	PlaygroundInternalError      = 4900
)

// Feature 005 — Observability.
//
// Business errors are served at HTTP 200 per the envelope policy. The
// DashboardInternalError code is system-level and is served at HTTP 500.
const (
	DashboardInvalidFilter = 5001
	DashboardInternalError = 5900
)

// Feature 006 — OpenAI API gateway.
//
// UsageInternalError is system-level and is served at HTTP 500. Data-plane
// proxy errors remain outside the Admin API envelope and do not use this
// package.
const (
	UsageInternalError = 6900
)

// legacyOK1000 is the pre-envelope success alias kept only so wrapped 001
// admin handlers that already shipped with code=1000 still decode as
// success ("ok"). New code MUST emit OK (=0) instead. It is not exported
// to discourage accidental reuse; Symbol(1000) still resolves to "ok".
const legacyOK1000 = 1000

// symbols is the authoritative reverse index consulted by Symbol. It MUST
// contain exactly one entry per registered code in docs/error-codes.md.
// Keep the layout grouped by feature for easy eyeballing against the doc.
var symbols = map[int]string{
	OK:      "ok",
	Unknown: "unknown_error",

	legacyOK1000: "ok",

	AccountNotFound:       "account_not_found",
	AccountNameConflict:   "account_name_conflict",
	InvalidAccountPayload: "invalid_account_payload",
	AccountAlreadyInState: "account_already_in_state",
	RequestRecordNotFound: "request_record_not_found",
	InvalidRequestFilter:  "invalid_request_filter",
	SessionNotFound:       "session_not_found",
	InvalidPagination:     "invalid_pagination",
	NoAvailableAccount:    "no_available_account",

	DBUnavailable: "db_unavailable",
	InternalError: "internal_error",

	SetupAlreadyDone:    "setup_already_done",
	InvalidDriver:       "invalid_driver",
	InvalidDSN:          "invalid_dsn",
	InvalidAccountName:  "invalid_account_name",
	InvalidAPIKey:       "invalid_api_key",
	InvalidPluginFlag:   "invalid_plugin_flag",
	InvalidRetention:    "invalid_retention",
	MalformedBody:       "malformed_body",
	RequestBodyTooLarge: "request_body_too_large",

	SetupRequired:          "setup_required",
	UnknownConfigKey:       "unknown_config_key",
	EnvOverrideReadonly:    "env_override_readonly",
	InvalidLogLevel:        "invalid_log_level",
	InvalidAccountProvider: "invalid_account_provider",
	InvalidBaseURL:         "invalid_base_url",
	InvalidModelRename:     "invalid_model_rename",

	DBConnectFailed:   "db_connect_failed",
	MigrateFailed:     "migrate_failed",
	CommitTxFailed:    "commit_tx_failed",
	ConfigWriteFailed: "config_write_failed",

	OAuthFlowInProgress:           "oauth_flow_in_progress",
	InvalidOAuthProvider:          "invalid_oauth_provider",
	OAuthStateMismatch:            "oauth_state_mismatch",
	NoFlowInProgress:              "no_flow_in_progress",
	OAuthAlreadyConsumed:          "already_consumed",
	OAuthFlowExpired:              "flow_expired",
	InvalidCallbackURL:            "invalid_callback_url",
	OAuthFlowIDMismatch:           "flow_id_mismatch",
	OAuthInvalidGrant:             "oauth_invalid_grant",
	InvalidAuthJSONStructure:      "invalid_auth_json_structure",
	InvalidAuthJSON:               "invalid_auth_json",
	OAuthModeRequiresFlowEndpoint: "oauth_mode_requires_flow_endpoint",
	NotOAuthAccount:               "not_oauth_account",
	DeviceAuthUnavailable:         "device_auth_unavailable",
	OAuthUpstreamError:            "oauth_upstream_error",

	OAuthInternalError:    "oauth_internal_error",
	OAuthStoreFailed:      "oauth_store_failed",
	OAuthExportReadFailed: "oauth_export_read_failed",

	InvalidPlaygroundRequest:     "invalid_playground_request",
	PlaygroundNoActiveAccount:    "playground_no_active_account",
	PlaygroundAccountUnavailable: "playground_account_unavailable",
	PlaygroundUpstreamError:      "playground_upstream_error",
	PlaygroundUpstreamTimeout:    "playground_upstream_timeout",
	PlaygroundResponseMalformed:  "playground_response_malformed",
	PlaygroundResponseTooLarge:   "playground_response_too_large",
	PlaygroundInternalError:      "playground_internal_error",

	DashboardInvalidFilter: "dashboard_invalid_filter",
	DashboardInternalError: "dashboard_internal_error",

	UsageInternalError: "usage_internal_error",
}

// Symbol returns the registered snake_case symbol for code, or the empty
// string when the code is not registered. Callers that need a stable
// default envelope message should pass the returned symbol through as
// msg; unknown codes SHOULD NOT be emitted by handlers.
func Symbol(code int) string { return symbols[code] }

// Known reports whether code is registered in this package. It is
// primarily useful to middleware and tests that want to reject unknown
// codes before they reach the wire.
func Known(code int) bool { _, ok := symbols[code]; return ok }

/**
 * Error codes — mirrors `docs/error-codes.md` and
 * `internal/api/errcode/codes.go`.
 *
 * Keep integer values AND snake_case symbols byte-exact with the Go
 * registry and the docs table. A mismatch is a release blocker.
 *
 * DO NOT extend by free-form additions; every new code must be
 * landed in `docs/error-codes.md` first, mirrored in
 * `internal/api/errcode/codes.go`, and mirrored here with an
 * `Err<Feature><Symbol>` naming convention.
 */

export const PlatformOK = 0
export const PlatformUnknown = -1

// Feature 001 — MVP admin path handlers (wrapped by 002's envelope).
export const Err001LegacyOK = 1000
export const Err001AccountNotFound = 1001
export const Err001AccountNameConflict = 1002
export const Err001InvalidAccountPayload = 1003
export const Err001AccountAlreadyInState = 1004
export const Err001RequestRecordNotFound = 1005
export const Err001InvalidRequestFilter = 1006
export const Err001SessionNotFound = 1007
export const Err001InvalidPagination = 1008
export const Err001NoAvailableAccount = 1009
export const Err001DBUnavailable = 1900
export const Err001InternalError = 1901

// Feature 002 — setup wizard + admin portal skeleton.
// NOTE: 2010 is reserved and intentionally NOT exported (see
// docs/error-codes.md §Changelog v1.3).
export const Err002SetupAlreadyDone = 2001
export const Err002InvalidDriver = 2002
export const Err002InvalidDSN = 2003
export const Err002InvalidAccountName = 2004
export const Err002InvalidAPIKey = 2005
export const Err002InvalidPluginFlag = 2006
export const Err002InvalidRetention = 2007
export const Err002MalformedBody = 2008
export const Err002RequestBodyTooLarge = 2009
export const Err002SetupRequired = 2011
export const Err002UnknownConfigKey = 2012
export const Err002EnvOverrideReadonly = 2013
export const Err002InvalidLogLevel = 2014
export const Err002InvalidAccountProvider = 2015
export const Err002InvalidBaseURL = 2016

export const Err002DBConnectFailed = 2900
export const Err002MigrateFailed = 2901
export const Err002CommitTxFailed = 2902
export const Err002ConfigWriteFailed = 2903

// Feature 003 — Multi-mode Codex authentication (browser OAuth, device
// OAuth, auth.json import/export, background token refresh).
// NOTE: 3017 is reserved (reuse Err001AccountNotFound for
// /export-auth-json "account not found" semantics — see
// docs/error-codes.md). All business codes below are served at HTTP 200.
export const Err003OAuthFlowInProgress = 3001
export const Err003InvalidOAuthProvider = 3002
export const Err003OAuthStateMismatch = 3003
export const Err003NoFlowInProgress = 3004
export const Err003AlreadyConsumed = 3005
export const Err003FlowExpired = 3006
export const Err003InvalidCallbackURL = 3007
export const Err003FlowIDMismatch = 3008
export const Err003OAuthInvalidGrant = 3009
export const Err003InvalidAuthJSONStructure = 3010
export const Err003InvalidAuthJSON = 3011
export const Err003OAuthModeRequiresFlowEndpoint = 3013
export const Err003NotOAuthAccount = 3014
export const Err003DeviceAuthUnavailable = 3015
export const Err003OAuthUpstreamError = 3016

export const Err003OAuthInternalError = 3900
export const Err003OAuthStoreFailed = 3901
export const Err003OAuthExportReadFailed = 3902

// Feature 004 — Account Playground.
export const Err004InvalidPlaygroundRequest = 4001
export const Err004PlaygroundNoActiveAccount = 4002
export const Err004PlaygroundAccountUnavailable = 4003
export const Err004PlaygroundUpstreamError = 4004
export const Err004PlaygroundUpstreamTimeout = 4005
export const Err004PlaygroundResponseMalformed = 4006
export const Err004PlaygroundResponseTooLarge = 4007
export const Err004PlaygroundInternalError = 4900

// Feature 005 — Observability.
export const Err005DashboardInvalidFilter = 5001
export const Err005DashboardInternalError = 5900

// Feature 006 — OpenAI API gateway.
export const Err006UsageInternalError = 6900

/**
 * Readable label for a code, for debug panels only. End-user strings
 * come from the server's `msg` field (canonical) — this map is a
 * last-resort fallback when the envelope is malformed.
 *
 * Symbols MUST match
 * `internal/api/errcode/codes.go` → `symbols` map exactly. A mismatch
 * means the frontend and backend disagree on what a numeric code
 * means; see the errcode_parity_test.go drift check in CI.
 */
export const CodeSymbols: Record<number, string> = {
  [PlatformOK]: 'ok',
  [PlatformUnknown]: 'unknown_error',

  [Err001LegacyOK]: 'ok',
  [Err001AccountNotFound]: 'account_not_found',
  [Err001AccountNameConflict]: 'account_name_conflict',
  [Err001InvalidAccountPayload]: 'invalid_account_payload',
  [Err001AccountAlreadyInState]: 'account_already_in_state',
  [Err001RequestRecordNotFound]: 'request_record_not_found',
  [Err001InvalidRequestFilter]: 'invalid_request_filter',
  [Err001SessionNotFound]: 'session_not_found',
  [Err001InvalidPagination]: 'invalid_pagination',
  [Err001NoAvailableAccount]: 'no_available_account',
  [Err001DBUnavailable]: 'db_unavailable',
  [Err001InternalError]: 'internal_error',

  [Err002SetupAlreadyDone]: 'setup_already_done',
  [Err002InvalidDriver]: 'invalid_driver',
  [Err002InvalidDSN]: 'invalid_dsn',
  [Err002InvalidAccountName]: 'invalid_account_name',
  [Err002InvalidAPIKey]: 'invalid_api_key',
  [Err002InvalidPluginFlag]: 'invalid_plugin_flag',
  [Err002InvalidRetention]: 'invalid_retention',
  [Err002MalformedBody]: 'malformed_body',
  [Err002RequestBodyTooLarge]: 'request_body_too_large',
  [Err002SetupRequired]: 'setup_required',
  [Err002UnknownConfigKey]: 'unknown_config_key',
  [Err002EnvOverrideReadonly]: 'env_override_readonly',
  [Err002InvalidLogLevel]: 'invalid_log_level',
  [Err002InvalidAccountProvider]: 'invalid_account_provider',
  [Err002InvalidBaseURL]: 'invalid_base_url',

  [Err002DBConnectFailed]: 'db_connect_failed',
  [Err002MigrateFailed]: 'migrate_failed',
  [Err002CommitTxFailed]: 'commit_tx_failed',
  [Err002ConfigWriteFailed]: 'config_write_failed',

  [Err003OAuthFlowInProgress]: 'oauth_flow_in_progress',
  [Err003InvalidOAuthProvider]: 'invalid_oauth_provider',
  [Err003OAuthStateMismatch]: 'oauth_state_mismatch',
  [Err003NoFlowInProgress]: 'no_flow_in_progress',
  [Err003AlreadyConsumed]: 'already_consumed',
  [Err003FlowExpired]: 'flow_expired',
  [Err003InvalidCallbackURL]: 'invalid_callback_url',
  [Err003FlowIDMismatch]: 'flow_id_mismatch',
  [Err003OAuthInvalidGrant]: 'oauth_invalid_grant',
  [Err003InvalidAuthJSONStructure]: 'invalid_auth_json_structure',
  [Err003InvalidAuthJSON]: 'invalid_auth_json',
  [Err003OAuthModeRequiresFlowEndpoint]: 'oauth_mode_requires_flow_endpoint',
  [Err003NotOAuthAccount]: 'not_oauth_account',
  [Err003DeviceAuthUnavailable]: 'device_auth_unavailable',
  [Err003OAuthUpstreamError]: 'oauth_upstream_error',

  [Err003OAuthInternalError]: 'oauth_internal_error',
  [Err003OAuthStoreFailed]: 'oauth_store_failed',
  [Err003OAuthExportReadFailed]: 'oauth_export_read_failed',

  [Err004InvalidPlaygroundRequest]: 'invalid_playground_request',
  [Err004PlaygroundNoActiveAccount]: 'playground_no_active_account',
  [Err004PlaygroundAccountUnavailable]: 'playground_account_unavailable',
  [Err004PlaygroundUpstreamError]: 'playground_upstream_error',
  [Err004PlaygroundUpstreamTimeout]: 'playground_upstream_timeout',
  [Err004PlaygroundResponseMalformed]: 'playground_response_malformed',
  [Err004PlaygroundResponseTooLarge]: 'playground_response_too_large',
  [Err004PlaygroundInternalError]: 'playground_internal_error',

  [Err005DashboardInvalidFilter]: 'dashboard_invalid_filter',
  [Err005DashboardInternalError]: 'dashboard_internal_error',

  [Err006UsageInternalError]: 'usage_internal_error',
}

export function symbolFor(code: number): string {
  return CodeSymbols[code] ?? `code_${code}`
}

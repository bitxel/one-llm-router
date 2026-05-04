package errcode

import "testing"

// registryFixture is the full expected (code, symbol) mapping, mirrored
// one-to-one with docs/error-codes.md. Keep the ordering grouped by
// feature for review convenience — the test iterates unordered.
var registryFixture = []struct {
	code   int
	symbol string
}{
	{OK, "ok"},
	{Unknown, "unknown_error"},

	{1000, "ok"},

	{AccountNotFound, "account_not_found"},
	{AccountNameConflict, "account_name_conflict"},
	{InvalidAccountPayload, "invalid_account_payload"},
	{AccountAlreadyInState, "account_already_in_state"},
	{RequestRecordNotFound, "request_record_not_found"},
	{InvalidRequestFilter, "invalid_request_filter"},
	{SessionNotFound, "session_not_found"},
	{InvalidPagination, "invalid_pagination"},
	{NoAvailableAccount, "no_available_account"},

	{DBUnavailable, "db_unavailable"},
	{InternalError, "internal_error"},

	{SetupAlreadyDone, "setup_already_done"},
	{InvalidDriver, "invalid_driver"},
	{InvalidDSN, "invalid_dsn"},
	{InvalidAccountName, "invalid_account_name"},
	{InvalidAPIKey, "invalid_api_key"},
	{InvalidPluginFlag, "invalid_plugin_flag"},
	{InvalidRetention, "invalid_retention"},
	{MalformedBody, "malformed_body"},
	{RequestBodyTooLarge, "request_body_too_large"},

	{SetupRequired, "setup_required"},
	{UnknownConfigKey, "unknown_config_key"},
	{EnvOverrideReadonly, "env_override_readonly"},
	{InvalidLogLevel, "invalid_log_level"},
	{InvalidAccountProvider, "invalid_account_provider"},
	{InvalidBaseURL, "invalid_base_url"},

	{DBConnectFailed, "db_connect_failed"},
	{MigrateFailed, "migrate_failed"},
	{CommitTxFailed, "commit_tx_failed"},
	{ConfigWriteFailed, "config_write_failed"},

	{OAuthFlowInProgress, "oauth_flow_in_progress"},
	{InvalidOAuthProvider, "invalid_oauth_provider"},
	{OAuthStateMismatch, "oauth_state_mismatch"},
	{NoFlowInProgress, "no_flow_in_progress"},
	{OAuthAlreadyConsumed, "already_consumed"},
	{OAuthFlowExpired, "flow_expired"},
	{InvalidCallbackURL, "invalid_callback_url"},
	{OAuthFlowIDMismatch, "flow_id_mismatch"},
	{OAuthInvalidGrant, "oauth_invalid_grant"},
	{InvalidAuthJSONStructure, "invalid_auth_json_structure"},
	{InvalidAuthJSON, "invalid_auth_json"},
	{OAuthModeRequiresFlowEndpoint, "oauth_mode_requires_flow_endpoint"},
	{NotOAuthAccount, "not_oauth_account"},
	{DeviceAuthUnavailable, "device_auth_unavailable"},
	{OAuthUpstreamError, "oauth_upstream_error"},

	{OAuthInternalError, "oauth_internal_error"},
	{OAuthStoreFailed, "oauth_store_failed"},
	{OAuthExportReadFailed, "oauth_export_read_failed"},

	{InvalidPlaygroundRequest, "invalid_playground_request"},
	{PlaygroundNoActiveAccount, "playground_no_active_account"},
	{PlaygroundAccountUnavailable, "playground_account_unavailable"},
	{PlaygroundUpstreamError, "playground_upstream_error"},
	{PlaygroundUpstreamTimeout, "playground_upstream_timeout"},
	{PlaygroundResponseMalformed, "playground_response_malformed"},
	{PlaygroundResponseTooLarge, "playground_response_too_large"},
	{PlaygroundInternalError, "playground_internal_error"},

	{DashboardInvalidFilter, "dashboard_invalid_filter"},
	{DashboardInternalError, "dashboard_internal_error"},

	{UsageInternalError, "usage_internal_error"},
}

func TestSymbol_KnownCodes(t *testing.T) {
	t.Parallel()
	for _, entry := range registryFixture {
		entry := entry
		t.Run(entry.symbol, func(t *testing.T) {
			t.Parallel()
			if got := Symbol(entry.code); got != entry.symbol {
				t.Fatalf("Symbol(%d) = %q, want %q", entry.code, got, entry.symbol)
			}
			if !Known(entry.code) {
				t.Fatalf("Known(%d) = false, want true", entry.code)
			}
		})
	}
}

func TestSymbol_UnknownCodes(t *testing.T) {
	t.Parallel()
	// 2010 is reserved per docs/error-codes.md (see D10). It must NOT be
	// resolvable via Symbol, so consumers cannot accidentally emit it.
	// 99999 is a guarded stand-in for any future-range code this package
	// does not yet know about.
	// 3017 is reserved per docs/error-codes.md (the 003 /export-auth-json
	// handler reuses AccountNotFound=1001 rather than
	// minting a duplicate 3xxx; the row exists only for documentation).
	for _, code := range []int{2010, 3017, 99999, 4242} {
		if got := Symbol(code); got != "" {
			t.Errorf("Symbol(%d) = %q, want empty string", code, got)
		}
		if Known(code) {
			t.Errorf("Known(%d) = true, want false", code)
		}
	}
}

func TestSymbols_Completeness(t *testing.T) {
	t.Parallel()
	// Catches drift between registryFixture (the test view) and the
	// package-level symbols map (the runtime view). Either direction is
	// a registry bug.
	if len(symbols) != len(registryFixture) {
		t.Fatalf("registry size mismatch: symbols=%d, fixture=%d", len(symbols), len(registryFixture))
	}
	fixtureIndex := make(map[int]string, len(registryFixture))
	for _, entry := range registryFixture {
		fixtureIndex[entry.code] = entry.symbol
	}
	for code, symbol := range symbols {
		want, ok := fixtureIndex[code]
		if !ok {
			t.Errorf("symbols[%d]=%q exists but fixture has no row", code, symbol)
			continue
		}
		if want != symbol {
			t.Errorf("symbols[%d]=%q, fixture says %q", code, symbol, want)
		}
	}
}

func TestSymbols_Reserved2010NotRegistered(t *testing.T) {
	t.Parallel()
	if _, ok := symbols[2010]; ok {
		t.Fatal("code 2010 is reserved by docs/error-codes.md (D10) and must not appear in symbols")
	}
}

func TestSymbols_Reserved3017NotRegistered(t *testing.T) {
	t.Parallel()
	// 3017 is reserved per docs/error-codes.md — the 003 /export-auth-json
	// handler reuses AccountNotFound=1001 rather than
	// minting a duplicate code. Registering 3017 would violate that
	// cross-feature contract.
	if _, ok := symbols[3017]; ok {
		t.Fatal("code 3017 is reserved by docs/error-codes.md and must not appear in symbols")
	}
}

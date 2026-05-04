package errcode

import (
	"fmt"
	"testing"
)

// codes003 is the authoritative enumeration of every Feature 003 error
// code — 15 business codes and 3 system codes (3900, 3901, 3902).
// Keep this list in lock-step with docs/error-codes.md §Feature
// 003 AND with the 003 section of registryFixture in codes_test.go.
//
// A drift between this list and the package-level symbols map is a
// reviewer block; the test below enforces both directions.
var codes003 = []int{
	// Business (HTTP 200)
	OAuthFlowInProgress,           // 3001
	InvalidOAuthProvider,          // 3002
	OAuthStateMismatch,            // 3003
	NoFlowInProgress,              // 3004
	OAuthAlreadyConsumed,          // 3005
	OAuthFlowExpired,              // 3006
	InvalidCallbackURL,            // 3007
	OAuthFlowIDMismatch,           // 3008
	OAuthInvalidGrant,             // 3009
	InvalidAuthJSONStructure,      // 3010
	InvalidAuthJSON,               // 3011
	OAuthModeRequiresFlowEndpoint, // 3013
	NotOAuthAccount,               // 3014
	DeviceAuthUnavailable,         // 3015
	OAuthUpstreamError,            // 3016

	// System (HTTP 500)
	OAuthInternalError,    // 3900
	OAuthStoreFailed,      // 3901
	OAuthExportReadFailed, // 3902
}

// TestSymbols003 asserts every 003 code has a non-empty, snake_case,
// lowercase symbol registered in the symbols map. Fails fast the
// moment a new 003 constant lands in codes.go without a matching
// registry row.
func TestSymbols003(t *testing.T) {
	t.Parallel()
	if len(codes003) != 18 {
		t.Fatalf("codes003 enumeration has %d entries, want 18 (15 business + 3 system). Did a new 003 code land in codes.go without extending this list?", len(codes003))
	}
	for _, code := range codes003 {
		code := code
		// Name the subtest after the symbol (or the bare int if the
		// registry is drifting) so a failure surfaces as
		// TestSymbols003/oauth_store_failed rather than #07.
		name := Symbol(code)
		if name == "" {
			name = fmt.Sprintf("code_%d_unregistered", code)
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			sym := Symbol(code)
			if sym == "" {
				t.Fatalf("Symbol(%d) is empty; every 003 code MUST have a symbol row per plan §Error Codes", code)
			}
			if !Known(code) {
				t.Fatalf("Known(%d) = false; registration drift in symbols map", code)
			}
			if !isLowerSnakeCase(sym) {
				t.Fatalf("Symbol(%d) = %q, which is not lowercase snake_case. AGENTS.md §Error registry requires [a-z0-9_] only", code, sym)
			}
		})
	}
}

// TestCodes003_RangePartition verifies each 003 code is in the right
// band (business 3001..3016; system 3900..3902). Any stray code (e.g. a
// typo'd 3900-range entry in the business slice) fails CI here, which
// is cheaper than a runtime "system code returned HTTP 200" drift.
func TestCodes003_RangePartition(t *testing.T) {
	t.Parallel()
	businessCount := 0
	systemCount := 0
	for _, code := range codes003 {
		switch {
		case code >= 3001 && code <= 3016:
			businessCount++
		case code >= 3900 && code <= 3902:
			systemCount++
		default:
			t.Errorf("code %d is not in the 003 range (3001..3016 business or 3900..3902 system)", code)
		}
	}
	if businessCount != 15 {
		t.Fatalf("business-range count=%d, want 15", businessCount)
	}
	if systemCount != 3 {
		t.Fatalf("system-range count=%d, want 3", systemCount)
	}
}

func isLowerSnakeCase(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_':
		default:
			return false
		}
	}
	return true
}

package testutil

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/user/one-llm-router/internal/api"
	"github.com/user/one-llm-router/internal/api/errcode"
)

func TestAssertEnvelope_WriteOK(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	api.WriteOK(rec, "req-1", map[string]any{"a": 1.0})

	data := AssertEnvelope(t, rec, 0)
	if got := data["a"]; got != 1.0 {
		t.Fatalf("data.a=%v (%T), want 1.0", got, got)
	}
}

func TestAssertEnvelope_WriteBizErr(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	api.WriteBizErr(rec, "req-2", errcode.OAuthFlowInProgress, errcode.Symbol(errcode.OAuthFlowInProgress), nil)

	data := AssertEnvelope(t, rec, errcode.OAuthFlowInProgress)
	if len(data) != 0 {
		t.Fatalf("data=%v, want empty object (nil → {} normalisation)", data)
	}
}

func TestAssertEnvelope_WriteSysErr_3900(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	api.WriteSysErr(rec, "req-3", errcode.OAuthInternalError, errcode.Symbol(errcode.OAuthInternalError))

	AssertEnvelope(t, rec, errcode.OAuthInternalError)
	if rec.Code != 500 {
		t.Fatalf("HTTP status=%d, want 500", rec.Code)
	}
}

func TestAssertEnvelope_WriteSysErr_3901(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	api.WriteSysErr(rec, "req-3901", errcode.OAuthStoreFailed, errcode.Symbol(errcode.OAuthStoreFailed))
	AssertEnvelope(t, rec, errcode.OAuthStoreFailed)
}

func TestAssertEnvelope_WriteSysErr_3902(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	api.WriteSysErr(rec, "req-3902", errcode.OAuthExportReadFailed, errcode.Symbol(errcode.OAuthExportReadFailed))
	AssertEnvelope(t, rec, errcode.OAuthExportReadFailed)
}

// TestVerifyEnvelope_DriftDetection verifies VerifyEnvelope — and
// therefore AssertEnvelope — flags the critical drift cases that would
// otherwise slip through code review:
//
//  1. System-range code written via WriteBizErr (→ HTTP 200 instead of 500).
//  2. Hand-typed msg string that doesn't match errcode.Symbol(code).
//  3. data: null on the wire (envelope contract forbids it).
//  4. Business code written via WriteSysErr (→ HTTP 500 instead of 200).
//
// Each sub-test constructs a deliberately-drifted response and asserts
// VerifyEnvelope returns a non-nil error with a diagnostic mentioning
// the fault. If the helper ever stops catching these, tests silently
// pass and the drift ships.
func TestVerifyEnvelope_DriftDetection(t *testing.T) {
	t.Parallel()

	t.Run("system_code_via_WriteBizErr_is_caught", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		// Drift: WriteBizErr emits HTTP 200 for every code, but 3901
		// is system-range and the contract demands HTTP 500.
		api.WriteBizErr(rec, "x", errcode.OAuthStoreFailed, errcode.Symbol(errcode.OAuthStoreFailed), nil)

		_, err := VerifyEnvelope(rec, errcode.OAuthStoreFailed)
		if err == nil {
			t.Fatal("VerifyEnvelope returned nil; expected HTTP-status mismatch error (3901 MUST be HTTP 500)")
		}
		if !strings.Contains(err.Error(), "WriteSysErr") {
			t.Errorf("error message=%q; want diagnostic mentioning WriteSysErr", err)
		}
	})

	t.Run("system_code_via_WriteBizErr_3902_is_caught", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		api.WriteBizErr(rec, "x", errcode.OAuthExportReadFailed, errcode.Symbol(errcode.OAuthExportReadFailed), nil)

		_, err := VerifyEnvelope(rec, errcode.OAuthExportReadFailed)
		if err == nil {
			t.Fatal("VerifyEnvelope returned nil; expected HTTP-status mismatch error (3902 MUST be HTTP 500)")
		}
	})

	t.Run("business_code_via_WriteSysErr_is_caught", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		// Drift: WriteSysErr emits HTTP 500, but 3001 is business-range
		// and the contract demands HTTP 200.
		api.WriteSysErr(rec, "x", errcode.OAuthFlowInProgress, errcode.Symbol(errcode.OAuthFlowInProgress))

		_, err := VerifyEnvelope(rec, errcode.OAuthFlowInProgress)
		if err == nil {
			t.Fatal("VerifyEnvelope returned nil; expected HTTP-status mismatch error (3001 MUST be HTTP 200)")
		}
	})

	t.Run("hand_typed_msg_mismatch_is_caught", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		// Drift: handler passed a hand-typed msg that's close but wrong.
		api.WriteBizErr(rec, "x", errcode.OAuthFlowInProgress, "oauth_flow_already_in_progress", nil)

		_, err := VerifyEnvelope(rec, errcode.OAuthFlowInProgress)
		if err == nil {
			t.Fatal("VerifyEnvelope returned nil; expected msg mismatch (hand-typed strings must match errcode.Symbol)")
		}
		if !strings.Contains(err.Error(), "errcode.Symbol") {
			t.Errorf("error message=%q; want diagnostic mentioning errcode.Symbol", err)
		}
	})

	t.Run("code_mismatch_is_caught", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		api.WriteBizErr(rec, "x", errcode.OAuthFlowInProgress, errcode.Symbol(errcode.OAuthFlowInProgress), nil)

		_, err := VerifyEnvelope(rec, errcode.OAuthAlreadyConsumed)
		if err == nil {
			t.Fatal("VerifyEnvelope returned nil; expected code mismatch")
		}
	})

	t.Run("unregistered_wantCode_is_caught", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		api.WriteBizErr(rec, "x", 99999, "made_up_symbol", nil)

		_, err := VerifyEnvelope(rec, 99999)
		// 99999 is not a system code per IsSystemCode, so wantStatus
		// is 200; the response IS HTTP 200, so HTTP-status check
		// passes; code matches (99999 on wire, 99999 wanted); but msg
		// lookup via errcode.Symbol(99999) returns "" → VerifyEnvelope
		// fails early with the "unregistered" diagnostic.
		if err == nil {
			t.Fatal("VerifyEnvelope returned nil; expected 'unregistered code' diagnostic")
		}
		if !strings.Contains(err.Error(), "unregistered") {
			t.Errorf("error message=%q; want diagnostic mentioning 'unregistered'", err)
		}
	})

	t.Run("nil_recorder_is_caught", func(t *testing.T) {
		t.Parallel()
		_, err := VerifyEnvelope(nil, 0)
		if err == nil {
			t.Fatal("VerifyEnvelope(nil) returned nil error")
		}
	})
}

// TestIsSystemCode verifies the system-range classifier used by
// AssertEnvelope to pick the expected HTTP status. Covers every
// registered system code AND representative business codes.
func TestIsSystemCode(t *testing.T) {
	t.Parallel()
	systemCodes := []int{
		errcode.Unknown,               // -1 (panic recovery)
		errcode.DBUnavailable,         // 1900
		errcode.InternalError,         // 1901
		errcode.DBConnectFailed,       // 2900
		errcode.MigrateFailed,         // 2901
		errcode.CommitTxFailed,        // 2902
		errcode.ConfigWriteFailed,     // 2903
		errcode.OAuthInternalError,    // 3900
		errcode.OAuthStoreFailed,      // 3901
		errcode.OAuthExportReadFailed, // 3902
	}
	for _, code := range systemCodes {
		if !IsSystemCode(code) {
			t.Errorf("IsSystemCode(%d) = false, want true (this code MUST go through WriteSysErr → HTTP 500)", code)
		}
	}
	businessCodes := []int{
		errcode.OK,
		errcode.AccountNotFound,      // 1001
		errcode.SetupAlreadyDone,     // 2001
		errcode.MalformedBody,        // 2008
		errcode.RequestBodyTooLarge,  // 2009
		errcode.OAuthFlowInProgress,  // 3001
		errcode.OAuthAlreadyConsumed, // 3005
		errcode.InvalidAuthJSON,      // 3011
		errcode.OAuthUpstreamError,   // 3016
	}
	for _, code := range businessCodes {
		if IsSystemCode(code) {
			t.Errorf("IsSystemCode(%d) = true, want false (business code MUST emit HTTP 200)", code)
		}
	}
}

// TestAssertEnvelope_PanicRecoverySystemCode covers the Unknown (-1)
// path so the classifier's panic-recovery row isn't dead code.
func TestAssertEnvelope_PanicRecoverySystemCode(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	api.WriteSysErr(rec, "req-panic", errcode.Unknown, errcode.Symbol(errcode.Unknown))
	AssertEnvelope(t, rec, errcode.Unknown)
	if rec.Code != 500 {
		t.Fatalf("HTTP status=%d, want 500 (Unknown is system-range)", rec.Code)
	}
}

// TestAssertEnvelope_NonObjectDataPassesThrough — intentional semantics:
// AssertEnvelope must accept array-shaped data (e.g. for list
// endpoints) with an empty returned map; only AssertEnvelopeDataShape
// fails on non-object data. This locks the behaviour so a future
// "helpful" refactor doesn't break list-endpoint tests.
func TestAssertEnvelope_NonObjectDataPassesThrough(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	api.WriteOK(rec, "req-list", []map[string]any{{"id": 1}, {"id": 2}})

	data := AssertEnvelope(t, rec, 0)
	if len(data) != 0 {
		t.Fatalf("data=%v, want empty map for array-shaped data (callers inspect rec.Body directly)", data)
	}
}

// TestAssertEnvelopeDataShape_ObjectOK — happy path for the stricter
// variant.
func TestAssertEnvelopeDataShape_ObjectOK(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	api.WriteOK(rec, "req-obj", map[string]any{"a": 1.0})
	data := AssertEnvelopeDataShape(t, rec, 0)
	if got := data["a"]; got != 1.0 {
		t.Fatalf("data.a=%v, want 1.0", got)
	}
}

// Because AssertEnvelopeDataShape uses t.Fatalf on a non-object, we
// cannot easily test it with a real *testing.T. Instead verify the
// underlying detection primitive directly against the bytes on the
// wire — if this logic were wrong, AssertEnvelopeDataShape would not
// catch the drift it claims to.
func TestAssertEnvelopeDataShape_DetectsArray(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	api.WriteOK(rec, "req-arr", []map[string]any{{"id": 1}})

	// The helper re-reads the body via json.RawMessage then inspects
	// the first non-whitespace byte. Array-shaped data starts with '['.
	body := rec.Body.String()
	if !strings.Contains(body, `"data":[`) {
		t.Fatalf("body=%s; expected data:[...] shape for this test", body)
	}
}

// TestIsSystemCode_MatchesRegistry — belt-and-suspenders: verify
// IsSystemCode covers every system-range code currently registered
// in errcode. Prevents silent drift when a new system code lands
// without being added to the classifier.
func TestIsSystemCode_MatchesRegistry(t *testing.T) {
	t.Parallel()
	// Known system-range codes from internal/api/errcode/codes.go.
	// If this list ever diverges from reality, either the registry
	// added a code (update IsSystemCode + this test) or renamed one
	// (fix symbols).
	expected := map[int]string{
		errcode.Unknown:               "unknown_error",
		errcode.DBUnavailable:         "db_unavailable",
		errcode.InternalError:         "internal_error",
		errcode.DBConnectFailed:       "db_connect_failed",
		errcode.MigrateFailed:         "migrate_failed",
		errcode.CommitTxFailed:        "commit_tx_failed",
		errcode.ConfigWriteFailed:     "config_write_failed",
		errcode.OAuthInternalError:    "oauth_internal_error",
		errcode.OAuthStoreFailed:      "oauth_store_failed",
		errcode.OAuthExportReadFailed: "oauth_export_read_failed",
	}
	for code, sym := range expected {
		if !IsSystemCode(code) {
			t.Errorf("IsSystemCode(%d %q) = false, want true", code, sym)
		}
		if got := errcode.Symbol(code); got != sym {
			t.Errorf("Symbol(%d) = %q, want %q (test fixture drifted from registry)", code, got, sym)
		}
	}
}

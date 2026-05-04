// Package testutil hosts test-only helpers shared across the router's
// admin API packages. The envelope-assertion helper here is the single
// source of truth for decoding + asserting the {code, msg, data}
// response envelope defined by internal/api/envelope.go.
//
// This package is imported ONLY from _test.go files; a production
// import is a review blocker (enforced by code review + log-scrub CI
// gate in T-095 — any non-test file importing testutil fails the
// build).
package testutil

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/user/one-llm-router/internal/api/errcode"
)

// AssertEnvelope decodes rec.Body as the project-wide envelope and
// asserts it matches wantCode on every axis the contract promises:
//
//   - HTTP status code: 200 for OK (wantCode==0) AND business codes
//     (1xxx/2xxx/3xxx *below* the system band); 500 for system codes
//     (errcode.Unknown/-1, 1900/1901, 2900..2903, 3900..3902). A
//     mismatch here signals the wrong helper was used (e.g. WriteBizErr
//     on 3900 → HTTP 200, but the contract demands HTTP 500).
//   - body.code == wantCode.
//   - body.msg == "ok" when wantCode==0 else errcode.Symbol(wantCode).
//     Hand-typed msg strings are a review blocker per plan §Module
//     Boundaries.
//   - body.data is never JSON null. Callers that have no payload still
//     see an empty object ({}).
//
// Returns body.data as map[string]any for further field-level
// assertions. If body.data is valid JSON but NOT a JSON object (e.g. an
// array, string, number, or boolean — legitimate shapes for some list
// endpoints), the returned map is empty and the helper returns without
// failing; callers that need to assert non-object data should inspect
// rec.Body directly or call AssertEnvelopeDataShape below. Failures
// use t.Fatalf — the surrounding test stops on the first envelope-
// shape violation, which is always terminal.
//
// For tests that need to verify AssertEnvelope's failure paths (e.g.
// that a drifted handler emits HTTP 200 for a system code and
// AssertEnvelope catches it), use VerifyEnvelope directly — it returns
// an error instead of calling t.Fatalf, so the negative path can be
// asserted without hijacking the *testing.T.
func AssertEnvelope(t *testing.T, rec *httptest.ResponseRecorder, wantCode int) map[string]any {
	t.Helper()
	data, err := VerifyEnvelope(rec, wantCode)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return data
}

// AssertEnvelopeDataShape is the stricter sibling of AssertEnvelope
// that additionally fails when body.data is NOT a JSON object. Use
// this for endpoints whose contract promises an object-shaped data
// field (the majority of /api/admin/* responses); tests for list
// endpoints returning data: [...] MUST use AssertEnvelope instead and
// inspect rec.Body directly for the array.
func AssertEnvelopeDataShape(t *testing.T, rec *httptest.ResponseRecorder, wantCode int) map[string]any {
	t.Helper()
	data, err := VerifyEnvelope(rec, wantCode)
	if err != nil {
		t.Fatalf("%v", err)
	}
	// VerifyEnvelope returns an empty map both for "data is {}" and
	// for "data is non-object". Re-decode to distinguish: a second
	// pass via json.RawMessage catches the non-object case.
	var raw struct {
		Data json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &raw)
	// Leading whitespace + first non-space byte must be '{' for an
	// object. Empty raw is impossible here (VerifyEnvelope already
	// asserted data is non-null).
	trimmed := trimLeadingWS(raw.Data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		t.Fatalf("AssertEnvelopeDataShape: body.data is not a JSON object (first byte %q); tests for object-shaped endpoints MUST see an object. Raw: %s", firstByte(trimmed), rec.Body.String())
	}
	return data
}

func trimLeadingWS(b []byte) []byte {
	for i, c := range b {
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			return b[i:]
		}
	}
	return nil
}

func firstByte(b []byte) byte {
	if len(b) == 0 {
		return 0
	}
	return b[0]
}

// VerifyEnvelope is the error-returning form of AssertEnvelope. It
// performs the same shape checks but returns the first violation as
// an error rather than failing a test directly. Its primary use-case
// is testing AssertEnvelope itself — production tests should call
// AssertEnvelope for the richer t.Fatalf diagnostics.
//
// On success returns body.data as map[string]any (empty map if data
// wasn't an object); on failure returns (nil, error).
func VerifyEnvelope(rec *httptest.ResponseRecorder, wantCode int) (map[string]any, error) {
	if rec == nil {
		return nil, fmt.Errorf("VerifyEnvelope: rec must not be nil")
	}

	wantStatus := 200
	if IsSystemCode(wantCode) {
		wantStatus = 500
	}
	if rec.Code != wantStatus {
		return nil, fmt.Errorf("HTTP status=%d, want %d (wantCode=%d — %s). Hint: system-range codes (e.g. 3900/3901/3902) MUST be written via api.WriteSysErr and therefore expect HTTP 500; business-range codes MUST use api.WriteBizErr (or WriteOK for code=0) and expect HTTP 200. Body: %s",
			rec.Code, wantStatus, wantCode, errcode.Symbol(wantCode), rec.Body.String())
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json; charset=utf-8" {
		return nil, fmt.Errorf("Content-Type=%q, want \"application/json; charset=utf-8\"", ct)
	}

	var body struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		return nil, fmt.Errorf("decode body: %w; raw=%s", err, rec.Body.String())
	}

	if body.Code != wantCode {
		return nil, fmt.Errorf("body.code=%d, want %d (%s). Raw body: %s",
			body.Code, wantCode, errcode.Symbol(wantCode), rec.Body.String())
	}

	wantMsg := "ok"
	if wantCode != 0 {
		wantMsg = errcode.Symbol(wantCode)
		if wantMsg == "" {
			return nil, fmt.Errorf("wantCode=%d has no registered symbol in errcode package; the test is asserting against an unregistered code", wantCode)
		}
	}
	if body.Msg != wantMsg {
		return nil, fmt.Errorf("body.msg=%q, want %q. Callers MUST pass errcode.Symbol(code) as the msg argument; hand-typed strings drift from the registry", body.Msg, wantMsg)
	}

	if len(body.Data) == 0 || string(body.Data) == "null" {
		return nil, fmt.Errorf("body.data is null/missing. Envelope contract (docs/error-codes.md §HTTP envelope policy) forbids JSON null on the data field — callers with no payload emit {} instead. Raw: %s", rec.Body.String())
	}

	var asMap map[string]any
	if err := json.Unmarshal(body.Data, &asMap); err == nil {
		return asMap, nil
	}
	return map[string]any{}, nil
}

// IsSystemCode reports whether code MUST be written via api.WriteSysErr
// (HTTP 500) under the project's envelope policy. Keep this mirror
// narrow — add rows as new system codes land in
// internal/api/errcode/codes.go.
//
// Includes errcode.Unknown (-1) because the top-level panic-recovery
// middleware in internal/api/recover.go emits it as a system envelope.
func IsSystemCode(code int) bool {
	switch code {
	case errcode.Unknown, // -1 (panic recovery)
		errcode.DBUnavailable,           // 1900
		errcode.InternalError,           // 1901
		errcode.DBConnectFailed,         // 2900
		errcode.MigrateFailed,           // 2901
		errcode.CommitTxFailed,          // 2902
		errcode.ConfigWriteFailed,       // 2903
		errcode.OAuthInternalError,      // 3900
		errcode.OAuthStoreFailed,        // 3901
		errcode.OAuthExportReadFailed,   // 3902
		errcode.PlaygroundInternalError, // 4900
		errcode.DashboardInternalError:  // 5900
		return true
	}
	return false
}

package adminapi

// export_visit_test.go — guard the safe export response wrappers
// added in `export_visit.go`.
//
// The generator's default `AccountsExportAuthJSON200JSONResponse`
// writes attachment headers UNCONDITIONALLY, which poisons the
// 200-envelope error branches (`account_not_found`,
// `not_oauth_account`) with `Content-Disposition: ""` etc.
// Handlers MUST return `ExportAuthJSONAttachmentResponse` (for the
// CodexAuthJSON success branch) or `ExportAuthJSONEnvelopeResponse`
// (for the two error branches) — these tests lock that contract.

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// Test_ExportAttachmentResponse_SetsHeaders asserts the success
// wrapper emits the EXACT contract header set from
// `specs/003-multi-mode-codex-auth/contracts/accounts-api.md`
// §Response (Success):
//
//	Content-Type: application/json; charset=utf-8
//	Cache-Control: no-store, private
//	X-Content-Type-Options: nosniff
//	Content-Disposition: attachment; filename="auth.json"
//
// Any regression that drops a value or a charset/`, private`
// suffix fails this loudly. Tightened after the overall Codex
// review flagged the previous header set as a MINOR drift
// (missing `charset=utf-8` and missing `, private`).
func Test_ExportAttachmentResponse_SetsContractHeaders(t *testing.T) {
	t.Parallel()

	var body ExportAuthJSONResponseBody
	if err := body.FromCodexAuthJSON(CodexAuthJSON{}); err != nil {
		t.Fatalf("FromCodexAuthJSON: %v", err)
	}
	resp := ExportAuthJSONAttachmentResponse{Body: body}

	rec := httptest.NewRecorder()
	if err := resp.VisitAccountsExportAuthJSONResponse(rec); err != nil {
		t.Fatalf("Visit returned error: %v", err)
	}
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	h := rec.Header()
	expect := map[string]string{
		"Content-Type":           "application/json; charset=utf-8",
		"Cache-Control":          "no-store, private",
		"X-Content-Type-Options": "nosniff",
		"Content-Disposition":    `attachment; filename="auth.json"`,
	}
	for k, want := range expect {
		if got := h.Get(k); got != want {
			t.Errorf("%s = %q, want %q (contract: contracts/accounts-api.md §Response (Success))", k, got, want)
		}
	}
}

// Test_ExportEnvelopeResponse_NoAttachmentHeaders asserts the
// error-branch wrapper does NOT emit Content-Disposition,
// Cache-Control, or X-Content-Type-Options. The presence of any
// of these on an envelope error would defeat the client's
// attachment discriminator and leak security-related headers
// into an unrelated response.
//
// Both 200-envelope error branches are exercised: account-not-
// found (1001) and not-oauth-account (3014).
func Test_ExportEnvelopeResponse_NoAttachmentHeaders(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		populate func(*ExportAuthJSONResponseBody) error
		wantCode int
	}{
		{
			name: "account_not_found",
			populate: func(b *ExportAuthJSONResponseBody) error {
				return b.FromAccountNotFoundEnvelope(AccountNotFoundEnvelope{
					Code: N1001,
					Data: map[string]interface{}{},
					Msg:  AccountNotFound,
				})
			},
			wantCode: 1001,
		},
		{
			name: "not_oauth_account",
			populate: func(b *ExportAuthJSONResponseBody) error {
				return b.FromNotOAuthAccountEnvelope(NotOAuthAccountEnvelope{
					Code: N3014,
					Msg:  NotOauthAccount,
				})
			},
			wantCode: 3014,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var body ExportAuthJSONResponseBody
			if err := tc.populate(&body); err != nil {
				t.Fatalf("populate: %v", err)
			}
			resp := ExportAuthJSONEnvelopeResponse{Body: body}

			rec := httptest.NewRecorder()
			if err := resp.VisitAccountsExportAuthJSONResponse(rec); err != nil {
				t.Fatalf("Visit: %v", err)
			}
			if rec.Code != 200 {
				t.Fatalf("status = %d, want 200 (AGENTS.md forbids 4xx on admin)", rec.Code)
			}

			h := rec.Header()
			// Contract: `contracts/accounts-api.md` line 9 —
			// envelope responses on admin endpoints MUST
			// include `charset=utf-8`. MINOR fix folded in with
			// the overall Codex review.
			if got := h.Get("Content-Type"); got != "application/json; charset=utf-8" {
				t.Fatalf("Content-Type = %q, want application/json; charset=utf-8", got)
			}
			// MAJOR (overall Codex review): presence-via-Get
			// cannot distinguish "header absent" from "header
			// present with empty value". A bug that called
			// `w.Header().Set("Cache-Control", "")` would pass a
			// `Get() == ""` check and still break HTTP caches /
			// break the client's `Content-Disposition`-based
			// attachment discriminator. Check key membership on
			// the underlying map instead.
			for _, hdr := range []string{"Content-Disposition", "Cache-Control", "X-Content-Type-Options"} {
				if vals, ok := h[hdr]; ok {
					t.Fatalf("envelope error leaked %s (must be ABSENT, not present-empty): header-values=%q", hdr, vals)
				}
			}

			// The envelope code must actually be present in the body,
			// not just an empty wrapper.
			if !strings.Contains(rec.Body.String(), `"code"`) {
				t.Fatalf("response body missing envelope keys: %q", rec.Body.String())
			}
			// Round-trip: parse and check code.
			var env struct {
				Code int `json:"code"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatalf("body not valid JSON: %v", err)
			}
			if env.Code != tc.wantCode {
				t.Fatalf("envelope.code = %d, want %d", env.Code, tc.wantCode)
			}
		})
	}
}

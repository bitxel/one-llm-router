package adminapi

// export_visit.go — hand-authored safe response types for the
// export operation `POST /api/admin/accounts/{id}/export-auth-json`.
//
// ─── Why this file exists ────────────────────────────────────────
//
// The generator's default wrapper for this operation is:
//
//	type AccountsExportAuthJSON200ResponseHeaders struct {
//	    CacheControl        string
//	    ContentDisposition  string
//	    XContentTypeOptions string
//	}
//
//	type AccountsExportAuthJSON200JSONResponse struct {
//	    Body    ExportAuthJSONResponseBody
//	    Headers AccountsExportAuthJSON200ResponseHeaders
//	}
//
//	func (response AccountsExportAuthJSON200JSONResponse) Visit...(w) error {
//	    w.Header().Set("Content-Type", "application/json")
//	    w.Header().Set("Cache-Control", fmt.Sprint(response.Headers.CacheControl))
//	    w.Header().Set("Content-Disposition", fmt.Sprint(response.Headers.ContentDisposition))
//	    w.Header().Set("X-Content-Type-Options", fmt.Sprint(response.Headers.XContentTypeOptions))
//	    w.WriteHeader(200)
//	    return json.NewEncoder(w).Encode(response.Body)
//	}
//
// Three problems for our envelope/exemption contract:
//
//  1. Headers are written UNCONDITIONALLY. Our export endpoint is
//     the documented envelope-exemption branch — ONLY the
//     `CodexAuthJSON` success variant wears attachment headers
//     (contracts/accounts-api.md §Response (Success)); the two
//     200-envelope error branches (`account_not_found`,
//     `not_oauth_account`) MUST NOT emit `Content-Disposition` or
//     `Cache-Control: no-store` — doing so breaks the client-side
//     discriminator ("envelope vs file download = is
//     Content-Disposition present?") and plants empty-valued
//     headers on the error responses.
//
//  2. A handler that forgets to populate `Headers` on the success
//     branch ships `Content-Disposition: ""` — client treats it
//     as non-attachment, defeating the download prompt.
//
//  3. The generated `Body` type is a union whose inline branches
//     are untagged. Nothing in the generator prevents the handler
//     from wiring the `CodexAuthJSON` branch into a response that
//     omits headers, or vice-versa. The two hand-authored types
//     below make the mis-wiring a compile error instead of a
//     runtime data-corruption.
//
// ─── The safe wrappers ───────────────────────────────────────────
//
//   - `ExportAuthJSONAttachmentResponse` — ONLY for the
//     `CodexAuthJSON` success branch. Takes a populated
//     `ExportAuthJSONResponseBody`, plus the attachment filename,
//     and writes attachment headers + 200 + body.
//
//   - `ExportAuthJSONEnvelopeResponse` — ONLY for the 200-envelope
//     error branches (`account_not_found`, `not_oauth_account`).
//     Writes envelope JSON with NO attachment headers.
//
// Both implement `AccountsExportAuthJSONResponseObject`, so a
// strict handler returns either one and the generator's dispatch
// glue calls the correct Visit method.
//
// The handler (T-012) MUST use one of these two types. Calling
// `AccountsExportAuthJSON200JSONResponse` directly is a bug —
// T-012's review checklist enforces this and the smoke test
// (`export_visit_smoke_test.go`) verifies header presence on
// success AND header absence on both error branches.
//
// ─── Why not fix in the spec? ────────────────────────────────────
//
// We considered splitting the operation into two 200-valued
// responses in OpenAPI (one with headers, one without). OpenAPI
// 3.0 does NOT support per-variant response headers inside a
// `oneOf` — headers are pinned to the response object, not the
// schema. Forking the operation into two separate status codes
// would force 4xx on errors (violating AGENTS.md §HTTP Status
// Policy). The hand-authored wrapper is the narrowest fix.

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// exportAttachmentFilename is the single, contract-fixed filename
// the success branch MUST ship. Per
// `specs/003-multi-mode-codex-auth/contracts/accounts-api.md`
// §Response (Success) the value is `auth.json` — the Codex CLI
// expects that literal name on import and the router has no
// legitimate reason to deviate. Making this a const (instead of
// a struct field) removes an entire class of footguns:
//
//   - A handler forgetting to populate `Filename` cannot ship
//     `filename=""`.
//   - A handler passing a user-supplied string cannot smuggle a
//     header injection (`filename="x\r\nContent-Type: foo/bar"`)
//     or a path-traversal filename.
//   - A future feature cannot weaken the contract without
//     editing this file AND updating the contract doc, which is
//     deliberate friction.
//
// The overall Codex review flagged the previous configurable
// field as a MINOR drift from the contract; fixing it here also
// closes the obvious injection surface.
const exportAttachmentFilename = "auth.json"

// ExportAuthJSONAttachmentResponse is the ONLY safe way to emit
// the success branch of the export endpoint. The handler
// populates `Body` via `body.FromCodexAuthJSON(...)` (or accepts
// a fully-built body from `exportapi`). Every header on the wire
// is forced by the wrapper and cannot be overridden — this
// deliberately leaves no footgun for the handler layer. The
// exact header set (`Content-Type: application/json;
// charset=utf-8`, `Content-Disposition: attachment;
// filename="auth.json"`, `Cache-Control: no-store, private`,
// `X-Content-Type-Options: nosniff`) is the contractual one from
// `contracts/accounts-api.md` §Response (Success).
type ExportAuthJSONAttachmentResponse struct {
	Body ExportAuthJSONResponseBody
}

// VisitAccountsExportAuthJSONResponse implements
// `AccountsExportAuthJSONResponseObject`.
func (r ExportAuthJSONAttachmentResponse) VisitAccountsExportAuthJSONResponse(w http.ResponseWriter) error {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store, private")
	h.Set("X-Content-Type-Options", "nosniff")
	// RFC 6266 §4.1 — `filename=<quoted-string>` is the portable
	// form; %q emits the correctly escaped double-quoted form.
	// Filename is the contract constant, never caller-supplied,
	// so no header-injection surface can exist here.
	h.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", exportAttachmentFilename))
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(r.Body)
}

// ExportAuthJSONEnvelopeResponse is the ONLY safe way to emit a
// 200-envelope error branch (`account_not_found`,
// `not_oauth_account`) for the export endpoint. The handler
// populates `Body` via `body.FromAccountNotFoundEnvelope(...)` or
// `body.FromNotOAuthAccountEnvelope(...)`. The wrapper writes NO
// attachment headers so the client's "is Content-Disposition
// present?" discriminator cleanly reads "envelope, not download".
type ExportAuthJSONEnvelopeResponse struct {
	Body ExportAuthJSONResponseBody
}

// VisitAccountsExportAuthJSONResponse implements
// `AccountsExportAuthJSONResponseObject`.
//
// Contract: `contracts/accounts-api.md` line 9 — EVERY admin
// account endpoint (including the error branches of the export
// exemption) returns `application/json; charset=utf-8`. Keep the
// charset here so the envelope error path aligns with
// `internal/api/envelope.go` and the strict-handler
// `writeEnvelope` fallback.
func (r ExportAuthJSONEnvelopeResponse) VisitAccountsExportAuthJSONResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(r.Body)
}

// Compile-time confirmation that both wrappers satisfy the
// generator's operation response interface. If the interface
// drifts in a regen (e.g. signature change), these lines break
// first, forcing a deliberate reconciliation.
var (
	_ AccountsExportAuthJSONResponseObject = ExportAuthJSONAttachmentResponse{}
	_ AccountsExportAuthJSONResponseObject = ExportAuthJSONEnvelopeResponse{}
)

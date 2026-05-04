// Package httpio provides the request-side HTTP plumbing for the
// /api/admin/* JSON surface: body-cap enforcement, content-type
// validation, and uniform envelope responses on malformed / oversized
// bodies. 003+ handlers and the OpenAPI-migrated settings update route
// use DecodeJSON; setup still uses its 002 bespoke parser until that
// surface is formally migrated. New handlers MUST use DecodeJSON.
//
// Dependency policy (per plan.md §Module Boundaries): this package
// imports only stdlib + internal/api + internal/api/errcode. Handler
// packages depend on httpio — NEVER the other direction.
//
// Exemption: the auth.json multipart import (T-065) uses its own
// dual-layer cap (16 KiB inner part + 64 KiB outer envelope — see
// research.md Decision 7) and therefore does NOT call DecodeJSON; it
// uses WriteMalformedBody for parse-failure envelopes.
package httpio

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"

	"github.com/user/one-llm-router/internal/api"
	"github.com/user/one-llm-router/internal/api/errcode"
)

// DefaultAdminBodyCap is the default request body size limit for
// admin-facing JSON endpoints (8 KiB), matching the cap that
// internal/api/setup already locked in (setup.MaxBodyBytes = 8 KiB).
// NOTE: /api/admin/settings uses its own 16 KiB cap
// (adminapi.SettingsMaxBodyBytes) because of the larger plugin /
// runtime config payload — handlers on that endpoint pass 16*1024
// explicitly to DecodeJSON rather than taking the default. 003
// handlers without a larger-payload justification should use this
// default. The multipart T-065 import path does not go through
// DecodeJSON; its dual-layer cap (64 KiB outer envelope + 16 KiB
// inner part) is wired directly into http.MaxBytesReader +
// io.LimitReader per research.md Decision 7.
const DefaultAdminBodyCap int64 = 8 * 1024

// DecodeJSON enforces the body cap + content-type + JSON-decode path
// for a single /api/admin/* handler call and returns the decoded value
// plus an ok flag.
//
// On any failure (wrong Content-Type, body too large, empty body,
// malformed JSON, unexpected EOF) it writes the canonical envelope via
// api.WriteBizErr (HTTP 200) and returns (zero, false). The caller's
// entire error-handling surface is a single `if !ok { return }`.
//
// Handlers MUST NOT write their own envelope on decoder errors —
// DecodeJSON is the only writer on the false branch. Leaking the raw
// error up to the handler would re-open the drift that 003 is
// designed to eliminate.
//
// maxBytes MUST be >= 0. A maxBytes of 0 means "no body allowed" —
// any non-empty body is treated as request_body_too_large so the
// response is still envelope-compliant instead of silently dropping
// data. A negative maxBytes is a programmer error (http.MaxBytesReader
// silently becomes a no-op for n <= 0 — that would let an unbounded
// body slip through the gate), so DecodeJSON panics in that case to
// surface the misuse at unit-test time rather than in production.
func DecodeJSON[T any](w http.ResponseWriter, r *http.Request, reqID string, maxBytes int64) (T, bool) {
	var zero T

	if maxBytes < 0 {
		panic("httpio.DecodeJSON: maxBytes must be >= 0 (got negative; http.MaxBytesReader treats n<=0 as no-op so a negative cap would disable body-size enforcement)")
	}

	// Content-Type check runs BEFORE decode so a text/plain body of
	// valid JSON still 2008s rather than sneaking past the JSON
	// handler chain. An omitted header is also 2008 — the /api/admin/*
	// contract requires a positive application/json declaration; we
	// do NOT silently treat empty-CT as JSON (that would let curl
	// users accidentally bypass the gate and is counter to how the
	// existing 002 handlers already behave).
	//
	// We use mime.ParseMediaType (stdlib, RFC-9110 compliant) rather
	// than a HasPrefix on "application/json" because a loose prefix
	// match accepts `application/jsonp`, `application/json-seq`,
	// `application/json5`, and friends — all distinct media types
	// whose parsers do NOT agree with encoding/json, so accepting
	// them would weaken the single-decoder contract this package is
	// designed to enforce. ParseMediaType strips whitespace and
	// normalises case, and returns the canonical bare type + params;
	// we demand an EXACT "application/json" on the bare type and
	// ignore parameters (charset=utf-8 is allowed; any other param
	// is also allowed since encoding/json doesn't care about it).
	// A well-formed request sends exactly one Content-Type header.
	// Go's net/http accepts repeated headers without joining them,
	// and Header.Get would only return the first value — that means
	// a request like `Content-Type: application/json` + a second
	// `Content-Type: text/plain` would sail past this gate while a
	// downstream proxy or logger might read the second value. We
	// refuse that ambiguity upfront: zero-or-more-than-one values is
	// malformed_body.
	cts := r.Header.Values("Content-Type")
	if len(cts) != 1 {
		WriteMalformedBody(w, reqID)
		return zero, false
	}
	mediaType, _, err := mime.ParseMediaType(cts[0])
	if err != nil || mediaType != "application/json" {
		WriteMalformedBody(w, reqID)
		return zero, false
	}

	// http.MaxBytesReader returns a *http.MaxBytesError when the
	// limit is exceeded; Decode() bubbles that up through its own
	// error path.
	body := http.MaxBytesReader(w, r.Body, maxBytes)
	defer func() { _ = body.Close() }()

	dec := json.NewDecoder(body)
	var out T
	err = dec.Decode(&out)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			api.WriteBizErr(w, reqID, errcode.RequestBodyTooLarge,
				errcode.Symbol(errcode.RequestBodyTooLarge),
				map[string]any{
					"scope":       "envelope",
					"limit_bytes": maxBytes,
				})
			return zero, false
		}
		WriteMalformedBody(w, reqID)
		return zero, false
	}

	// Trailing bytes after the first JSON value (e.g. a second object)
	// are caller error and should also 2008 — the envelope contract is
	// "one JSON document per admin request".
	if dec.More() {
		WriteMalformedBody(w, reqID)
		return zero, false
	}

	return out, true
}

// WriteMalformedBody emits the canonical `code:2008 malformed_body`
// envelope for handlers that decoded their body through a non-generic
// path (multipart forms, custom parsers, streaming decoders) and need
// to share the wire shape with DecodeJSON callers.
//
// Passing through this helper — rather than calling api.WriteBizErr
// directly — keeps the msg / data shape a single source-of-truth.
// Callers pass nil data; any diagnostic detail lives in the paired
// observability log, NOT the envelope (docs/error-codes.md §Data
// fields are operator-facing).
func WriteMalformedBody(w http.ResponseWriter, reqID string) {
	api.WriteBizErr(w, reqID,
		errcode.MalformedBody,
		errcode.Symbol(errcode.MalformedBody),
		nil)
}

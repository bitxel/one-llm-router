package adminapi

// strict_handler.go — hand-authored blessed entrypoint for wiring
// the generated ServerInterface / StrictServerInterface into a
// router with envelope-aware error handling.
//
// ─── Why this file exists ────────────────────────────────────────
//
// The generator emits THREE distinct error paths that all default
// to non-envelope responses, and every one of them violates our
// non-negotiable HTTP API style (AGENTS.md §HTTP API Style):
//
//  1. `ServerInterfaceWrapper.ErrorHandlerFunc` (outer layer) —
//     fires when path/query parameter binding fails
//     (`RequiredParamError`, `InvalidParamFormatError`,
//     `TooManyValuesForParamError`, `UnmarshalingParamError`). The
//     generator default is `http.Error(w, err.Error(), 400)` which
//     leaks `400 text/plain` before the strict layer ever runs.
//  2. `strictHandler.options.RequestErrorHandlerFunc` — fires when
//     the strict layer's `json.Decode` / `r.MultipartReader()`
//     fails. Default is also `400 text/plain`.
//  3. `strictHandler.options.ResponseErrorHandlerFunc` — fires
//     when the handler returns a non-nil error or an unexpected
//     response type. Default is `500 text/plain`.
//
// Leaving ANY of them live leaks non-envelope bodies to SPA /
// Codex clients. This file replaces all three with envelope-aware
// handlers and exposes a blessed top-level constructor,
// `HandlerFromMuxWithEnvelope`, that wires them correctly. The
// split between the outer (path/query) and strict (body) layers
// was missed in the initial T-006 cut — the overall Codex review
// caught it before merge and the fix closes both surfaces.
//
// ─── Additional guarantees layered in this file ─────────────────
//
//   - ROUTE-AWARE CODE MAPPING. `POST
//     /api/admin/accounts/import-auth-json` is contractually
//     bound to emit `3010 invalid_auth_json_structure` for every
//     "cannot parse the multipart auth.json" failure, not the
//     generic `2008 malformed_body` (see
//     `specs/003-multi-mode-codex-auth/contracts/accounts-api.md`
//     §POST /accounts/import-auth-json). `envelopeRequestError`
//     consults `r.URL.Path` so the transport-layer fallback stays
//     contract-accurate.
//   - CONTENT-TYPE GATE. The generator's strict layer decodes
//     body bytes via `json.NewDecoder` without consulting
//     `Content-Type`, so a client sending
//     `Content-Type: text/plain` with a JSON-shaped body would
//     reach business logic with zero protection. The
//     `contentTypeGate` middleware rejects mismatched content
//     types before any decode happens, using the same envelope
//     codes that the strict layer would eventually emit
//     (3010 for multipart, 2008 for JSON).
//   - STRUCTURED OBSERVABILITY. Both error handlers emit
//     `slog.Warn` / `slog.Error` records with `method`, `path`,
//     and the wrapped error so operators can grep the failure
//     even though `msg` is intentionally non-localised and the
//     client response never echoes the internal error string
//     (security — avoid leaking Go type names / stack hints).
//
// ─── Canonical usage ─────────────────────────────────────────────
//
//	ssi := myStrictServerImpl{...}
//	mux := http.NewServeMux()
//	root := adminapi.HandlerFromMuxWithEnvelope(ssi, mux)
//	srv := &http.Server{Handler: root, ...}
//
// Handlers MUST use this constructor (or
// `HandlerWithEnvelopeOptions` for advanced wiring). Direct calls
// to `NewStrictHandler` / `NewStrictHandlerWithOptions` /
// `HandlerFromMux` from non-test code bypass the envelope
// contract and are considered a violation; the T-009+ admin
// wiring will add an import-visibility test that forbids them
// outside the `adminapi` package itself.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
)

// ─── Envelope codes used by the transport-layer handlers ─────────
//
// These mirror the canonical registry in `docs/error-codes.md`.
// Keep the constants centralised so `envelopeRequestError`,
// `envelopeResponseError`, and `contentTypeGate` cannot drift
// from each other or from the error-code registry.
const (
	// envCodeMalformedBody — generic transport-layer fallback for
	// request body decode failures on JSON endpoints.
	envCodeMalformedBody = 2008
	envMsgMalformedBody  = "malformed_body"

	envCodeRequestBodyTooLarge = 2009
	envMsgRequestBodyTooLarge  = "request_body_too_large"

	envCodeInvalidRequestFilter = 1006
	envMsgInvalidRequestFilter  = "invalid_request_filter"

	envCodeDashboardInvalidFilter = 5001
	envMsgDashboardInvalidFilter  = "dashboard_invalid_filter"

	// envCodeUnknownError — generic transport-layer fallback for
	// response-side failures (handler returned a non-nil error, or
	// the returned type did not implement the expected response
	// interface). Mirrors the reserved `-1` entry in
	// `docs/error-codes.md` §"Reserved codes".
	envCodeUnknownError = -1
	envMsgUnknownError  = "unknown_error"

	// envCodeInvalidAuthJSONStructure — route-specific mapping
	// for `POST /api/admin/accounts/import-auth-json`. Per
	// `contracts/accounts-api.md` this code covers EVERY
	// structural-parse failure for the multipart wrapper: wrong
	// `Content-Type`, missing `auth_json` part, non-JSON body,
	// or valid-JSON-but-not-an-object. Keeping these as 3010
	// (not 2008) means the SPA can match on a single code for
	// its "import failed — check your auth.json file" toast.
	envCodeInvalidAuthJSONStructure = 3010
	envMsgInvalidAuthJSONStructure  = "invalid_auth_json_structure"

	// routeImportAuthJSON is the single static route whose
	// transport-layer failures deserve route-specific code
	// mapping. All other POST routes take the generic 2008
	// bucket — the strict-layer handler decodes the body into a
	// typed Go struct, so there is no ambiguity about what a
	// "malformed" body means.
	routeImportAuthJSON = "/api/admin/accounts/import-auth-json"
	routeSettingsUpdate = "/api/admin/settings/update"
	routePlaygroundRun  = "/api/admin/playground/run"
	routeDashboard      = "/api/admin/dashboard"
	routeRequests       = "/api/admin/requests"
	routeRequestsOpts   = "/api/admin/requests/options"

	// 003-wide default JSON admin envelope body cap. This mirrors
	// T-005b / T-098 and the registry entry for 2009
	// request_body_too_large.
	jsonRouteBodyLimitBytes int64 = 8 * 1024

	// 004 Playground accepts 16,000 Unicode characters of operator
	// prompt text, so the transport cap is route-specific rather
	// than the 003 default 8 KiB JSON cap.
	playgroundJSONRouteBodyLimitBytes int64 = 96 * 1024

	// 002 Settings uses a 16 KiB JSON cap per its original admin
	// contract. The route is now in OpenAPI, so the strict-handler
	// body guard needs the same cap for generated-route consumers.
	settingsJSONRouteBodyLimitBytes int64 = 16 * 1024
)

// ─── JSON envelope primitive ────────────────────────────────────

// envelope is the JSON shape every admin response must emit. We
// deliberately do NOT reuse `internal/api.Envelope` because that
// package imports the strict handler — the import must be kept
// one-way (generator → api, never the reverse) or we introduce a
// dependency cycle.
type envelope struct {
	Code int            `json:"code"`
	Msg  string         `json:"msg"`
	Data map[string]any `json:"data"`
}

func writeEnvelope(w http.ResponseWriter, status, code int, msg string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	// Contract: `specs/003-multi-mode-codex-auth/contracts/accounts-api.md`
	// line 9 — EVERY `/api/admin/accounts/*` endpoint returns
	// `application/json; charset=utf-8`. Keep the charset in
	// sync here so transport-layer envelope errors do not drift
	// from the top-level envelope writer (`internal/api/envelope.go`)
	// and satisfy `testutil/envelope_assert.go` which round-trips
	// a strict equality check on the header.
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(envelope{
		Code: code,
		Msg:  msg,
		Data: data,
	})
}

// ─── Route → expected request-body content-type ─────────────────

// expectedContentType returns the authoritative `Content-Type`
// for the admin operation at (method, path). An empty return
// means "no content-type check applies" — either the method is
// bodyless (GET) or the route has no request body (currently
// only `POST /accounts/{id}/export-auth-json` among the 003-
// onboarded operations).
//
// Table sourced from `openapi/admin.yaml` and MUST be kept in
// lock-step: when a new 003+ operation lands, add it here AND to
// the adminapi union-smoke test (`union_smoke_test.go`) so the
// coverage lock fires if either side drifts. A single admin.yaml
// edit with a stale table here is a silent security hole (a
// client sending `Content-Type: text/plain` with a JSON body
// would reach business logic unchecked), so the pattern is a
// deliberate belt-and-braces.
func expectedContentType(method, path string) string {
	if method != http.MethodPost {
		return ""
	}
	switch {
	case path == routeImportAuthJSON:
		return "multipart/form-data"
	case strings.HasPrefix(path, "/api/admin/accounts/") && strings.HasSuffix(path, "/export-auth-json"):
		// No request body — `{id}` is a path parameter, not a
		// body. Content-Type is client-dictated and irrelevant.
		return ""
	default:
		// All other POST routes in admin.yaml carry a JSON body:
		// /oauth/browser/manual-callback, /oauth/browser/start,
		// /oauth/cancel, /oauth/device/start, /playground/run,
		// /settings/update.
		return "application/json"
	}
}

func jsonBodyLimitBytes(path string) int64 {
	if path == routeSettingsUpdate {
		return settingsJSONRouteBodyLimitBytes
	}
	if path == routePlaygroundRun {
		return playgroundJSONRouteBodyLimitBytes
	}
	return jsonRouteBodyLimitBytes
}

// hasRequestBody reports whether the request carries a body that
// the transport layer needs to decode. Empty bodies skip the
// content-type check and are rejected by the empty-body guard for
// JSON routes, which keeps every JSON route on the same
// `2008 malformed_body` envelope.
func hasRequestBody(r *http.Request) bool {
	if r.ContentLength > 0 {
		return true
	}
	// Chunked transfers may set ContentLength == -1. Treat any
	// such encoding as "body present" — we have no cheap way to
	// know the size without reading it.
	if r.Header.Get("Transfer-Encoding") != "" {
		return true
	}
	return false
}

// mediaTypeOf parses the top-level media type from Content-Type,
// stripping params like `boundary=xxx` or `charset=utf-8`. Uses
// net/mime so malformed Content-Type headers (which should also
// be treated as "content-type mismatch") fall through to an
// empty return.
func mediaTypeOf(contentType string) string {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return ""
	}
	return mt
}

// ─── Transport-layer error handlers ──────────────────────────────

// requestErrorCode returns the envelope (code, msg) pair for a
// request-side failure on the given path. Centralises the
// route-aware mapping so every call site (outer wrapper,
// strict layer, content-type gate) returns the same envelope for
// the same route.
func requestErrorCode(path string) (int, string) {
	if path == routeImportAuthJSON {
		return envCodeInvalidAuthJSONStructure, envMsgInvalidAuthJSONStructure
	}
	if path == routeRequests || path == routeRequestsOpts || strings.HasPrefix(path, routeRequests+"/") {
		return envCodeInvalidRequestFilter, envMsgInvalidRequestFilter
	}
	if path == routeDashboard {
		return envCodeDashboardInvalidFilter, envMsgDashboardInvalidFilter
	}
	return envCodeMalformedBody, envMsgMalformedBody
}

// envelopeRequestError handles:
//
//   - Outer wrapper failures (path/query parameter binding via
//     the `RequiredParamError` family defined in server.gen.go).
//   - Strict-layer failures (JSON decode, multipart decode).
//   - Content-type gate rejects (see `contentTypeGate`).
//
// The chosen mapping is route-aware:
//
//   - `POST /api/admin/accounts/import-auth-json` → 3010
//     `invalid_auth_json_structure` (contract requirement).
//   - All other routes → 2008 `malformed_body` (generic fallback).
//
// We deliberately do NOT echo `err.Error()` into the envelope
// body (security + AGENTS.md §HTTP API Style "msg is never
// localised"), but we DO log it as a structured `slog.Warn`
// record so operators can grep the failure in stderr.
//
// `data` carries the offending parameter name when available
// (extracted via `errors.As` on the generated error types) so
// frontends can surface a targeted error message
// (e.g. "the `id` path param is not a valid integer"). For
// generic body-decode failures `data` is `{}`.
func envelopeRequestError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Warn("adminapi.request_error",
		"method", r.Method,
		"path", r.URL.Path,
		"err", err,
	)

	if err == nil {
		code, msg := requestErrorCode(r.URL.Path)
		writeEnvelope(w, http.StatusOK, code, msg, nil)
		return
	}

	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		writeEnvelope(w, http.StatusOK, envCodeRequestBodyTooLarge, envMsgRequestBodyTooLarge, map[string]any{
			"scope":       "envelope",
			"limit_bytes": maxErr.Limit,
		})
		return
	}

	code, msg := requestErrorCode(r.URL.Path)

	var reqErr *RequiredParamError
	var hdrErr *RequiredHeaderError
	var fmtErr *InvalidParamFormatError
	var tooMany *TooManyValuesForParamError
	var unmarshalErr *UnmarshalingParamError
	switch {
	case errors.As(err, &reqErr):
		writeEnvelope(w, http.StatusOK, code, msg, map[string]any{"param": reqErr.ParamName})
	case errors.As(err, &hdrErr):
		writeEnvelope(w, http.StatusOK, code, msg, map[string]any{"header": hdrErr.ParamName})
	case errors.As(err, &fmtErr):
		writeEnvelope(w, http.StatusOK, code, msg, map[string]any{"param": fmtErr.ParamName})
	case errors.As(err, &tooMany):
		writeEnvelope(w, http.StatusOK, code, msg, map[string]any{"param": tooMany.ParamName})
	case errors.As(err, &unmarshalErr):
		writeEnvelope(w, http.StatusOK, code, msg, map[string]any{"param": unmarshalErr.ParamName})
	default:
		writeEnvelope(w, http.StatusOK, code, msg, nil)
	}
}

// envelopeResponseError handles response-side failures from the
// generated `strictHandler.*` operation middleware:
//
//  1. The StrictServerInterface method returned a non-nil error
//     (the generator assumes a system-level fault — the handler
//     could not produce ANY response at all, e.g. DB handle
//     closed, OS I/O failure).
//  2. The returned response value did not implement the expected
//     `<Op>ResponseObject` interface (a programming mistake
//     inside the handler — returned the wrong type).
//
// Both collapse to `500 / -1 unknown_error` because (a) they are
// indistinguishable from the client's perspective, and (b) the
// alternative of echoing `err.Error()` into `msg` or `data` is a
// security-sensitive information leak (could surface stack hints,
// file paths, internal type names). We keep the client-facing
// body stable while preserving operator visibility via `slog`.
//
// Panic recovery is NOT handled here — it lives in the
// application-level recoverer middleware (`internal/api/recover.go`)
// which wraps ALL handlers (including the admin surface) at the
// outermost layer. A handler that panics therefore produces a
// recoverer-emitted envelope (code 1900 / system-level) rather
// than a raw Go runtime trace, matching AGENTS.md expectations.
func envelopeResponseError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("adminapi.response_error",
		"method", r.Method,
		"path", r.URL.Path,
		"err", err,
	)
	writeEnvelope(w, http.StatusInternalServerError, envCodeUnknownError, envMsgUnknownError, nil)
}

// ─── Response Content-Type charset normaliser ────────────────────

// charsetResponseWriter wraps an `http.ResponseWriter` so the
// outgoing `Content-Type: application/json` header is upgraded to
// `application/json; charset=utf-8` just before the status line
// is flushed. The upgrade is applied to ANY `application/json`
// header whose existing parameters do NOT already include a
// `charset` — i.e. it is IDEMPOTENT (writers that already set
// the full header, like our own `writeEnvelope`, are left alone).
//
// Why this exists: the generator's `Visit*` methods in
// `server.gen.go` (generated from `openapi/admin.yaml`) call
// `w.Header().Set("Content-Type", "application/json")` with NO
// charset. Our spec contract
// (`specs/003-multi-mode-codex-auth/contracts/accounts-api.md`
// line 9) mandates `application/json; charset=utf-8` for EVERY
// `/api/admin/accounts/*` response, and
// `internal/api/testutil/envelope_assert.go`
// VerifyEnvelope does a strict equality check on the header —
// without this normaliser, T-007+ handler tests that use
// VerifyEnvelope would fail on the bare `application/json` the
// generator emits.
//
// We refuse to edit `server.gen.go` (AGENTS.md: NEVER hand-edit
// generated code). We refuse to fork oapi-codegen for a single-
// character fix. The ResponseWriter wrapper is the narrowest
// surgical fix: zero invasive touches, no dependency on a
// specific generator version, and it composes cleanly with the
// existing content-type gate on the request side.
type charsetResponseWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

// upgradeContentType mutates the header map in-place to append
// `charset=utf-8` when (and only when) the current header is a
// bare JSON media type without charset. Extracted so unit tests
// can exercise the mutation path without spinning up a full HTTP
// handler.
func upgradeContentType(h http.Header) {
	ct := h.Get("Content-Type")
	if ct == "" {
		return
	}
	mt, params, err := mime.ParseMediaType(ct)
	if err != nil {
		return
	}
	if !strings.EqualFold(mt, "application/json") {
		return
	}
	if _, hasCharset := params["charset"]; hasCharset {
		return
	}
	if params == nil {
		params = map[string]string{}
	}
	params["charset"] = "utf-8"
	h.Set("Content-Type", mime.FormatMediaType(mt, params))
}

func (w *charsetResponseWriter) WriteHeader(status int) {
	if !w.wroteHeader {
		upgradeContentType(w.Header())
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(status)
}

// Write delegates to WriteHeader(200) on first call, matching the
// stdlib contract that writing without an explicit WriteHeader
// is equivalent to `WriteHeader(200)`. The generator does call
// `w.WriteHeader(...)` explicitly in every Visit method, so in
// practice this branch is only exercised by hand-authored
// writers that forget — but the safety net is free.
func (w *charsetResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap exposes the wrapped ResponseWriter for tooling that
// needs to reach through (e.g. `http.ResponseController.Unwrap`
// in Go 1.20+). We do NOT proactively implement Flusher /
// Hijacker / Pusher: the admin surface is strictly JSON
// request/response (no streaming, no WebSockets, no HTTP/2
// server push), so the surface area is intentionally minimal
// and any future streaming endpoint that lands here would break
// compile and force an explicit reconciliation.
func (w *charsetResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// charsetMiddleware installs `charsetResponseWriter` for every
// request so downstream handlers (generated Visit methods or
// hand-authored wrappers) that emit a bare `application/json`
// header are transparently upgraded to the contract shape.
// Placed in the outer middleware chain so it runs AFTER the
// content-type gate rejects (the gate uses our own
// `writeEnvelope`, which already ships charset, so double-
// upgrading is a no-op thanks to the idempotence guard).
func charsetMiddleware() MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(&charsetResponseWriter{ResponseWriter: w}, r)
		})
	}
}

// ─── Content-Type gate (outer-layer middleware) ──────────────────

// contentTypeGate rejects requests whose `Content-Type` does not
// match the OpenAPI-declared type for the route. Placed OUTSIDE
// the strict layer so that:
//
//   - Multipart-expected routes never reach the strict layer
//     with a JSON body (otherwise the client's `application/json`
//     Content-Type plus a JSON body would be rejected with a
//     confusing `multipart reader failed` error instead of the
//     contract-mandated 3010).
//   - JSON-expected routes never reach the strict layer with a
//     `text/plain` body that happens to parse as JSON (the
//     generator's `json.NewDecoder` does NOT consult
//     Content-Type, so this would otherwise be accepted —
//     caught by the overall Codex review).
//
// Skips: method != POST, requests with no body (handled by the
// empty-body guard), and routes with no declared Content-Type
// expectation (currently only export-auth-json).
func contentTypeGate() MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			expected := expectedContentType(r.Method, r.URL.Path)
			if expected == "" {
				next.ServeHTTP(w, r)
				return
			}
			cts := r.Header.Values("Content-Type")
			if len(cts) != 1 {
				envelopeRequestError(w, r,
					fmt.Errorf("expected exactly one Content-Type header, got %d", len(cts)))
				return
			}
			got := mediaTypeOf(cts[0])
			if !strings.EqualFold(got, expected) {
				envelopeRequestError(w, r,
					fmt.Errorf("content-type %q does not match expected %q", cts[0], expected))
				return
			}
			if strings.EqualFold(expected, "application/json") {
				if !hasRequestBody(r) {
					envelopeRequestError(w, r, io.EOF)
					return
				}
				r.Body = http.MaxBytesReader(w, r.Body, jsonBodyLimitBytes(r.URL.Path))
				if err := normalizeJSONRequestBody(r); err != nil {
					envelopeRequestError(w, r, err)
					return
				}
			}
			if strings.EqualFold(expected, "multipart/form-data") && !hasRequestBody(r) {
				envelopeRequestError(w, r, io.EOF)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func normalizeJSONRequestBody(r *http.Request) error {
	if r == nil || r.Body == nil {
		return io.EOF
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return io.EOF
	}
	if trimmed[0] != '{' {
		return errors.New("can't decode JSON body: expected top-level object")
	}

	dec := json.NewDecoder(bytes.NewReader(body))
	var payload json.RawMessage
	if err := dec.Decode(&payload); err != nil {
		return fmt.Errorf("can't decode JSON body: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return errors.New("can't decode JSON body: trailing data after first JSON value")
		}
		return fmt.Errorf("can't decode JSON body: %w", err)
	}

	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	return nil
}

// ─── Blessed constructors ────────────────────────────────────────

// NewEnvelopeStrictHandler wraps the generated strict server
// interface with envelope-aware request/response error handlers.
// Exposed separately from `HandlerFromMuxWithEnvelope` so
// advanced callers can compose their own routing while still
// reusing the strict-layer envelope policy (e.g. for tests that
// drive individual operations without spinning up a mux).
func NewEnvelopeStrictHandler(ssi StrictServerInterface, middlewares []StrictMiddlewareFunc) ServerInterface {
	return NewStrictHandlerWithOptions(ssi, middlewares, StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  envelopeRequestError,
		ResponseErrorHandlerFunc: envelopeResponseError,
	})
}

// HandlerFromMuxWithEnvelope is THE blessed entry point for
// wiring the admin API into a router. It installs ALL three
// envelope-aware handlers (outer wrapper, strict-layer request,
// strict-layer response) and prepends the content-type gate
// middleware, so every non-success path produces a conforming
// envelope.
//
// Prefer this over `HandlerFromMux` in application code.
// The only reason to reach for the lower-level constructors is
// (a) in this package's own tests, or (b) an application that
// deliberately wants a non-envelope contract — which currently
// has no use case inside this repo (`/v1/*` pass-through routes
// bypass the admin surface entirely and are registered directly
// on the outer mux by `internal/app`, not via the codegen).
func HandlerFromMuxWithEnvelope(ssi StrictServerInterface, m ServeMux) http.Handler {
	return HandlerWithEnvelopeOptions(ssi, nil, StdHTTPServerOptions{BaseRouter: m})
}

// HandlerWithEnvelopeOptions is the full-options variant.
// `strictMiddlewares` are layered inside the strict server (they
// see the typed request object); `options.Middlewares` are
// layered at the outer wrapper (they see the raw
// `*http.Request`). The content-type gate is prepended to
// `options.Middlewares` so it runs BEFORE any caller-supplied
// middleware — a panicking caller middleware will never reach a
// request with a mismatched Content-Type.
//
// Callers MUST NOT override `options.ErrorHandlerFunc`; the
// envelope contract requires the generator-wrapper to use
// `envelopeRequestError`. Any value provided by the caller is
// overwritten.
func HandlerWithEnvelopeOptions(ssi StrictServerInterface, strictMiddlewares []StrictMiddlewareFunc, options StdHTTPServerOptions) http.Handler {
	options.ErrorHandlerFunc = envelopeRequestError
	// Middleware order (outermost first on the wire, innermost
	// last — `Middlewares[0]` is installed OUTERMOST by the
	// generator, so it runs FIRST on requests and LAST on
	// responses):
	//
	//  1. charsetMiddleware — wraps the ResponseWriter so every
	//     downstream `Content-Type: application/json` is
	//     upgraded to include `charset=utf-8`. Outermost so the
	//     wrapper is in place before ANY later middleware or
	//     generated handler writes a header.
	//  2. contentTypeGate — rejects request-side Content-Type
	//     mismatches before decode. Runs AFTER charsetMiddleware
	//     on the request path, which is fine — the gate consults
	//     the REQUEST Content-Type, never the response.
	//  3. caller-supplied middlewares.
	options.Middlewares = append(
		[]MiddlewareFunc{charsetMiddleware(), contentTypeGate()},
		options.Middlewares...,
	)
	si := NewEnvelopeStrictHandler(ssi, strictMiddlewares)
	return HandlerWithOptions(si, options)
}

package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/user/one-llm-router/internal/api/errcode"
)

// Response envelope for every router-owned JSON endpoint EXCEPT /v1/*.
//
// Policy (mirrors docs/error-codes.md + AGENTS.md §HTTP API Style):
//   - HTTP 200 for success AND business errors (the business outcome lives
//     in body.code; 0 = ok, non-zero = registered business error).
//   - HTTP 500 for system errors (panics, corruption, upstream-agnostic
//     infrastructure failures).
//   - Content-Type is always "application/json; charset=utf-8".
//   - X-Request-Id is set on the response header if reqID is non-empty,
//     and is NEVER duplicated into the body.
//   - data is always present and non-null: callers that have no payload
//     still see `"data":{}`. This keeps the client-side TS shape stable.
//
// The /v1/* proxy uses WriteRouterError (native MVP shape, native HTTP
// 5xx) and MUST NOT use any of the Write* helpers below.
//
// Canonical usage pattern (enforced by review; see plan.md §Module
// Boundaries and docs/error-codes.md §HTTP envelope policy):
//
//   - Success:    api.WriteOK(w, reqID, data)                               — data MUST be non-nil.
//   - Business:   api.WriteBizErr(w, reqID, code, errcode.Symbol(code), data) — nil data auto-normalises to {}.
//   - System:     api.WriteSysErr(w, reqID, code, errcode.Symbol(code))     — HTTP 500; data is always {}.
//
// The msg argument MUST always come from errcode.Symbol(code); a
// hand-typed snake_case string is a review blocker because it drifts
// from the registry the moment the code is renamed. Handlers that
// parse JSON bodies SHOULD funnel through internal/api/httpio.DecodeJSON
// rather than calling these helpers directly on malformed/oversized
// input, so the 2008 / 2009 envelopes stay byte-identical across every
// handler that opts into httpio. The setup surface still owns a bespoke
// 002 decode path and should move to httpio when it is formally migrated
// into OpenAPI.

// envelope is the on-wire JSON shape. Kept private because no caller
// outside this package should construct one directly — they go through
// WriteOK / WriteBizErr / WriteSysErr.
type envelope struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data any    `json:"data"`
}

// emptyData is the sentinel used when a caller passes nil for the data
// parameter on WriteBizErr / WriteSysErr. Using a package-level value
// avoids a per-call allocation; struct{}{} marshals to `{}` which is
// the default success shape the contracts promise on error.
var emptyData = struct{}{}

// fallbackSysErrBody is written verbatim when json.Marshal fails on the
// caller-provided data (typically a channel or a map with non-string
// keys). Emitting a canned response keeps the wire contract intact and
// avoids recursing back into Write* (which would risk infinite loops).
var fallbackSysErrBody = []byte(`{"code":-1,"msg":"unknown_error","data":{}}`)

// WriteOK writes a 200 OK envelope with code=0 and msg="ok".
//
// Data MUST be non-nil; passing nil is a programmer error and panics so
// it is caught in unit tests long before reaching production. Callers
// with no payload should pass an explicit empty value (e.g. struct{}{}
// or map[string]any{}).
func WriteOK(w http.ResponseWriter, reqID string, data any) {
	if data == nil {
		panic("api.WriteOK: data must not be nil; pass an explicit empty value instead")
	}
	writeEnvelope(w, reqID, http.StatusOK, envelope{
		Code: errcode.OK,
		Msg:  "ok",
		Data: data,
	})
}

// WriteBizErr writes a 200 OK envelope carrying a registered business
// error code (see docs/error-codes.md). Data may be nil; nil is
// normalised to an empty object so the response is still well-formed.
func WriteBizErr(w http.ResponseWriter, reqID string, code int, msg string, data any) {
	if data == nil {
		data = emptyData
	}
	writeEnvelope(w, reqID, http.StatusOK, envelope{
		Code: code,
		Msg:  msg,
		Data: data,
	})
}

// WriteSysErr writes a 500 Internal Server Error envelope carrying a
// registered system error code (see docs/error-codes.md). Data is always
// an empty object — system errors intentionally carry no drill-down
// payload so handlers cannot leak internal state by accident.
func WriteSysErr(w http.ResponseWriter, reqID string, code int, msg string) {
	writeEnvelope(w, reqID, http.StatusInternalServerError, envelope{
		Code: code,
		Msg:  msg,
		Data: emptyData,
	})
}

// writeEnvelope is the single point of serialization. Keeping it
// package-private enforces the AGENTS.md constraint that only this file
// chooses the HTTP status / headers for envelope-bearing responses.
func writeEnvelope(w http.ResponseWriter, reqID string, status int, env envelope) {
	body, err := json.Marshal(env)
	if err != nil {
		// Use the "error" slog attr to match 001 admin/proxy logging (not "err").
		slog.Error("envelope marshal failed",
			"code", env.Code,
			"msg", env.Msg,
			"error", err,
		)
		setHeaders(w, reqID)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write(fallbackSysErrBody)
		return
	}
	setHeaders(w, reqID)
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func setHeaders(w http.ResponseWriter, reqID string) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	if reqID != "" {
		h.Set("X-Request-Id", reqID)
	}
}

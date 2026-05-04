package adminapi

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/user/one-llm-router/internal/api"
	"github.com/user/one-llm-router/internal/api/admin"
	"github.com/user/one-llm-router/internal/api/errcode"
)

// WrappedHandler adapts the 001 admin handlers onto the 002 envelope
// policy. The 001 *admin.Handler writes native JSON like
// `{"status":"enabled"}` or `{"error":"..."}` with native HTTP status
// codes (200/400/404/409/500). Clients in 002 are required to see the
// same `{code,msg,data}` shape on every /api/admin/* endpoint
// (admin-api.md §Routing decision table), so we replay each 001 call
// through a small adapter that translates its response.
//
// The adapter strategy (documented in plan.md §D8): the 001 handler
// writes into a buffering ResponseWriter; the adapter inspects the
// captured status + body and emits the envelope on the real writer.
// This keeps 001's logic untouched (it was reviewed and shipped as
// part of feature 001) and constrains 002's responsibility to wire
// translation only.
//
// The health endpoint is special — its 001 response is already a
// structured JSON and 002 needs to surface an additive `setup_state`
// field (admin-api.md §Health endpoint). The health adapter injects
// that field after captured-response decode.
type WrappedHandler struct {
	inner  *admin.Handler
	gate   setupGate
	logger *slog.Logger
}

// setupGate is the narrow interface WrappedHandler needs to fold
// `setup_state` into the health payload. *setup.Gate satisfies it;
// tests pass fakes.
type setupGate interface {
	ProbeState() (open bool, probeErr error)
}

// NewWrappedHandler wires a WrappedHandler.
func NewWrappedHandler(inner *admin.Handler, gate setupGate, logger *slog.Logger) *WrappedHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &WrappedHandler{inner: inner, gate: gate, logger: logger}
}

// captureWriter is a tiny http.ResponseWriter that buffers status +
// body in memory so the envelope adapter can inspect them. Does NOT
// implement Flusher / Hijacker — the 001 admin handlers do not use
// streaming / hijacking (those are proxy-only concerns).
type captureWriter struct {
	status int
	body   []byte
	header http.Header
}

func newCaptureWriter() *captureWriter {
	return &captureWriter{header: http.Header{}}
}

func (cw *captureWriter) Header() http.Header { return cw.header }
func (cw *captureWriter) WriteHeader(s int)   { cw.status = s }
func (cw *captureWriter) Write(b []byte) (int, error) {
	if cw.status == 0 {
		cw.status = http.StatusOK
	}
	cw.body = append(cw.body, b...)
	return len(b), nil
}

// errMapFunc chooses (code, msg) given a captured (status, body).
type errMapFunc func(status int, body []byte) (int, string)

// wrap runs the 001 inner handler, captures its response, and emits
// the envelope translation back to the real writer.
func (ww *WrappedHandler) wrap(w http.ResponseWriter, r *http.Request, inner http.HandlerFunc, errMap errMapFunc) {
	reqID := api.RequestIDFromContext(r.Context())
	cw := newCaptureWriter()
	inner(cw, r)

	status := cw.status
	if status == 0 {
		status = http.StatusOK
	}

	if status >= 200 && status < 300 {
		var data any = map[string]any{}
		if len(cw.body) > 0 {
			if err := json.Unmarshal(cw.body, &data); err != nil {
				ww.logger.Warn("wrapped admin handler returned non-json",
					"request_id", reqID, "status", status, "error", err,
				)
				api.WriteSysErr(w, reqID, errcode.InternalError,
					errcode.Symbol(errcode.InternalError))
				return
			}
		}
		api.WriteOK(w, reqID, data)
		return
	}

	code, msg := errMap(status, cw.body)
	if status >= 500 {
		api.WriteSysErr(w, reqID, code, msg)
		return
	}
	// Surface the 001 handler's `error` string (if any) in the
	// envelope's `data.detail` slot so admin-UI error banners retain
	// the precise 001 message (e.g. "account name conflicts with
	// existing active account") instead of only seeing the envelope
	// symbol. Missing / unparseable bodies fall back to `{}` so the
	// envelope contract ("data is always an object, never null") is
	// preserved.
	api.WriteBizErr(w, reqID, code, msg, detailFromNativeBody(cw.body))
}

// detailFromNativeBody extracts the human-readable `error` field a
// 001 admin handler writes on failure and returns it as an envelope
// data payload. Non-JSON or missing `error` returns an empty map so
// the envelope never contains `null` or leaks raw internals.
func detailFromNativeBody(body []byte) map[string]any {
	if len(body) == 0 {
		return map[string]any{}
	}
	var shape struct {
		Error       string   `json:"error"`
		Field       string   `json:"field"`
		AllowedHere []string `json:"allowed_here"`
		Got         string   `json:"got"`
	}
	if err := json.Unmarshal(body, &shape); err != nil {
		return map[string]any{}
	}
	if len(shape.AllowedHere) > 0 && shape.Got != "" {
		return map[string]any{
			"allowed_here": shape.AllowedHere,
			"got":          shape.Got,
		}
	}
	if shape.Field != "" {
		return map[string]any{"field": shape.Field}
	}
	if shape.Error == "" {
		return map[string]any{}
	}
	return map[string]any{"detail": shape.Error}
}

// defaultAccountErrMap translates wrapped account-handler responses onto the
// envelope. 003 extends the create path with field-specific validation and the
// oauth_mode_requires_flow_endpoint business error while preserving the 001
// mappings for get/list/enable/disable/delete.
func defaultAccountErrMap(status int, body []byte) (int, string) {
	switch status {
	case http.StatusBadRequest:
		switch nativeAccountErrorCode(body) {
		case errcode.InvalidAPIKey:
			return errcode.InvalidAPIKey, errcode.Symbol(errcode.InvalidAPIKey)
		case errcode.OAuthModeRequiresFlowEndpoint:
			return errcode.OAuthModeRequiresFlowEndpoint, errcode.Symbol(errcode.OAuthModeRequiresFlowEndpoint)
		default:
			return errcode.InvalidAccountPayload, errcode.Symbol(errcode.InvalidAccountPayload)
		}
	case http.StatusNotFound:
		return errcode.AccountNotFound, errcode.Symbol(errcode.AccountNotFound)
	case http.StatusConflict:
		return errcode.AccountAlreadyInState, errcode.Symbol(errcode.AccountAlreadyInState)
	case http.StatusInternalServerError:
		return errcode.InternalError, errcode.Symbol(errcode.InternalError)
	default:
		return errcode.InternalError, errcode.Symbol(errcode.InternalError)
	}
}

func nativeAccountErrorCode(body []byte) int {
	if len(body) == 0 {
		return errcode.InvalidAccountPayload
	}
	var shape struct {
		Error       string   `json:"error"`
		Field       string   `json:"field"`
		AllowedHere []string `json:"allowed_here"`
		Got         string   `json:"got"`
	}
	if err := json.Unmarshal(body, &shape); err != nil {
		return errcode.InvalidAccountPayload
	}
	if len(shape.AllowedHere) > 0 && shape.Got != "" {
		return errcode.OAuthModeRequiresFlowEndpoint
	}
	switch shape.Field {
	case "api_key":
		return errcode.InvalidAPIKey
	default:
		return errcode.InvalidAccountPayload
	}
}

// defaultRequestsErrMap translates 001 /admin/requests responses.
//
//	400 → 1006 invalid_request_filter
//	500 → 1901 internal_error
func defaultRequestsErrMap(status int, _ []byte) (int, string) {
	switch status {
	case http.StatusBadRequest:
		return errcode.InvalidRequestFilter, errcode.Symbol(errcode.InvalidRequestFilter)
	case http.StatusInternalServerError:
		return errcode.InternalError, errcode.Symbol(errcode.InternalError)
	default:
		return errcode.InternalError, errcode.Symbol(errcode.InternalError)
	}
}

// defaultSessionErrMap translates /admin/sessions/resolve errors:
//
//	400 → 1006 invalid_request_filter
//	503 → 1009 no_available_account
//	500 → 1901 internal_error
func defaultSessionErrMap(status int, _ []byte) (int, string) {
	switch status {
	case http.StatusBadRequest:
		return errcode.InvalidRequestFilter, errcode.Symbol(errcode.InvalidRequestFilter)
	case http.StatusServiceUnavailable:
		return errcode.NoAvailableAccount, errcode.Symbol(errcode.NoAvailableAccount)
	case http.StatusInternalServerError:
		return errcode.InternalError, errcode.Symbol(errcode.InternalError)
	default:
		return errcode.InternalError, errcode.Symbol(errcode.InternalError)
	}
}

// CreateAccount wraps POST /api/admin/accounts.
func (ww *WrappedHandler) CreateAccount(w http.ResponseWriter, r *http.Request) {
	ww.wrap(w, r, ww.inner.CreateAccount, defaultAccountErrMap)
}

// ListAccounts wraps GET /api/admin/accounts.
func (ww *WrappedHandler) ListAccounts(w http.ResponseWriter, r *http.Request) {
	ww.wrap(w, r, ww.inner.ListAccounts, defaultAccountErrMap)
}

// GetAccount wraps GET /api/admin/accounts/{id}.
func (ww *WrappedHandler) GetAccount(w http.ResponseWriter, r *http.Request) {
	ww.wrap(w, r, ww.inner.GetAccount, defaultAccountErrMap)
}

// EnableAccount wraps POST /api/admin/accounts/{id}/enable.
func (ww *WrappedHandler) EnableAccount(w http.ResponseWriter, r *http.Request) {
	ww.wrap(w, r, ww.inner.EnableAccount, defaultAccountErrMap)
}

// DisableAccount wraps POST /api/admin/accounts/{id}/disable.
func (ww *WrappedHandler) DisableAccount(w http.ResponseWriter, r *http.Request) {
	ww.wrap(w, r, ww.inner.DisableAccount, defaultAccountErrMap)
}

// DeleteAccount wraps POST /api/admin/accounts/{id}/delete.
func (ww *WrappedHandler) DeleteAccount(w http.ResponseWriter, r *http.Request) {
	ww.wrap(w, r, ww.inner.DeleteAccount, defaultAccountErrMap)
}

// QueryRequests wraps GET /api/admin/requests.
func (ww *WrappedHandler) QueryRequests(w http.ResponseWriter, r *http.Request) {
	ww.wrap(w, r, ww.inner.QueryRequests, defaultRequestsErrMap)
}

// ResolveSession wraps GET /api/admin/sessions/resolve.
func (ww *WrappedHandler) ResolveSession(w http.ResponseWriter, r *http.Request) {
	ww.wrap(w, r, ww.inner.ResolveSession, defaultSessionErrMap)
}

// GetHealth wraps GET /api/admin/health and injects data.setup_state.
// admin-api.md §Health endpoint: the 001 handler returns HTTP 200
// with a structured health payload; 002 adds `setup_state` so the
// SPA shell can display a banner.
//
// Unlike the other adapters, this one ALWAYS emits code=0 + HTTP 200
// irrespective of the underlying health state (admin-api.md: "code
// stays 0 for all three states"); the liveness verdict lives in
// data.status.
func (ww *WrappedHandler) GetHealth(w http.ResponseWriter, r *http.Request) {
	reqID := api.RequestIDFromContext(r.Context())
	cw := newCaptureWriter()
	ww.inner.GetHealth(cw, r)

	var health map[string]any
	if len(cw.body) == 0 {
		health = map[string]any{}
	} else if err := json.Unmarshal(cw.body, &health); err != nil {
		ww.logger.Warn("health body not JSON", "request_id", reqID, "error", err)
		api.WriteSysErr(w, reqID, errcode.InternalError,
			errcode.Symbol(errcode.InternalError))
		return
	}

	setupState := "done"
	if open, _ := ww.gate.ProbeState(); !open {
		setupState = "pending"
	}
	health["setup_state"] = setupState

	api.WriteOK(w, reqID, health)
}

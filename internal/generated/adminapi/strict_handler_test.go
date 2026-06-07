package adminapi

// strict_handler_test.go — envelope + content-type + error-path
// regression harness for the blessed constructors in
// `strict_handler.go`. See that file's header for WHY these
// replacements are non-negotiable: the generator's default
// handlers leak raw 400/500 `text/plain` responses that violate
// AGENTS.md §HTTP API Style, and the overall Codex review showed
// the initial T-006 cut missed the outer-wrapper path-param leak
// and the content-type bypass. Both surfaces are now covered.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ─── Stub StrictServerInterface ─────────────────────────────────

// stubServer is a minimal StrictServerInterface implementation.
// Tests set the response/error pair on the operation they are
// exercising; unreached operations panic so a future test that
// accidentally drives them fails loudly instead of silently
// returning nil.
type stubServer struct {
	importAuthResp any
	importAuthErr  error
}

func (s stubServer) AccountsImportAuthJSON(ctx context.Context, req AccountsImportAuthJSONRequestObject) (AccountsImportAuthJSONResponseObject, error) {
	_ = ctx
	_ = req
	if s.importAuthErr != nil {
		return nil, s.importAuthErr
	}
	if s.importAuthResp == nil {
		return nil, nil
	}
	//lint:ignore SA9002 intentional type-mismatch for test
	return s.importAuthResp.(AccountsImportAuthJSONResponseObject), nil
}

func (stubServer) AccountsExportAuthJSON(context.Context, AccountsExportAuthJSONRequestObject) (AccountsExportAuthJSONResponseObject, error) {
	panic("stubServer.AccountsExportAuthJSON not wired")
}
func (stubServer) OauthBrowserManualCallback(context.Context, OauthBrowserManualCallbackRequestObject) (OauthBrowserManualCallbackResponseObject, error) {
	panic("stubServer.OauthBrowserManualCallback not wired")
}
func (stubServer) OauthBrowserStart(context.Context, OauthBrowserStartRequestObject) (OauthBrowserStartResponseObject, error) {
	panic("stubServer.OauthBrowserStart not wired")
}
func (stubServer) OauthCancel(context.Context, OauthCancelRequestObject) (OauthCancelResponseObject, error) {
	panic("stubServer.OauthCancel not wired")
}
func (stubServer) OauthDeviceStart(context.Context, OauthDeviceStartRequestObject) (OauthDeviceStartResponseObject, error) {
	panic("stubServer.OauthDeviceStart not wired")
}
func (stubServer) OauthFlowStatus(context.Context, OauthFlowStatusRequestObject) (OauthFlowStatusResponseObject, error) {
	panic("stubServer.OauthFlowStatus not wired")
}
func (stubServer) PlaygroundRun(context.Context, PlaygroundRunRequestObject) (PlaygroundRunResponseObject, error) {
	panic("stubServer.PlaygroundRun not wired")
}
func (stubServer) DashboardGet(context.Context, DashboardGetRequestObject) (DashboardGetResponseObject, error) {
	panic("stubServer.DashboardGet not wired")
}
func (stubServer) UsageGet(context.Context, UsageGetRequestObject) (UsageGetResponseObject, error) {
	panic("stubServer.UsageGet not wired")
}
func (stubServer) RequestsList(context.Context, RequestsListRequestObject) (RequestsListResponseObject, error) {
	panic("stubServer.RequestsList not wired")
}
func (stubServer) RequestsOptions(context.Context, RequestsOptionsRequestObject) (RequestsOptionsResponseObject, error) {
	panic("stubServer.RequestsOptions not wired")
}
func (stubServer) RequestsGet(context.Context, RequestsGetRequestObject) (RequestsGetResponseObject, error) {
	panic("stubServer.RequestsGet not wired")
}
func (stubServer) SettingsGet(context.Context, SettingsGetRequestObject) (SettingsGetResponseObject, error) {
	panic("stubServer.SettingsGet not wired")
}
func (stubServer) SettingsUpdate(context.Context, SettingsUpdateRequestObject) (SettingsUpdateResponseObject, error) {
	panic("stubServer.SettingsUpdate not wired")
}
func (stubServer) AccountModelsList(context.Context, AccountModelsListRequestObject) (AccountModelsListResponseObject, error) {
	panic("stubServer.AccountModelsList not wired")
}
func (stubServer) AccountModelAdd(context.Context, AccountModelAddRequestObject) (AccountModelAddResponseObject, error) {
	panic("stubServer.AccountModelAdd not wired")
}
func (stubServer) AccountModelRefresh(context.Context, AccountModelRefreshRequestObject) (AccountModelRefreshResponseObject, error) {
	panic("stubServer.AccountModelRefresh not wired")
}
func (stubServer) AccountModelRemove(context.Context, AccountModelRemoveRequestObject) (AccountModelRemoveResponseObject, error) {
	panic("stubServer.AccountModelRemove not wired")
}

// decodeEnvelope decodes the recorded response body into the
// blessed envelope shape. Fails the test with a readable message
// if either the body is not JSON or a required field is absent.
// Returns the decoded envelope so tests can make further
// assertions on `.Data`.
func decodeEnvelope(t *testing.T, rec *httptest.ResponseRecorder) envelope {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type = %q, want application/json prefix", ct)
	}
	bodyBytes := rec.Body.Bytes()
	// Contract: `data` is NEVER `null`. Lock this at the bytes
	// level because JSON unmarshaling into `map[string]any` would
	// silently coerce `null` → nil → `{}` in Go and hide the
	// violation.
	if bytes.Contains(bodyBytes, []byte(`"data":null`)) {
		t.Fatalf("envelope emitted data:null (AGENTS.md forbids): %s", bodyBytes)
	}
	var got envelope
	if err := json.Unmarshal(bodyBytes, &got); err != nil {
		t.Fatalf("response body is not a valid envelope: %v (body=%q)", err, bodyBytes)
	}
	if got.Data == nil {
		t.Fatalf("envelope.data is nil after unmarshal (should be `{}`): body=%q", bodyBytes)
	}
	return got
}

// ─── Strict-layer: request-side multipart failure ───────────────

// Sending plain text to the multipart-only import-auth-json route
// MUST return HTTP 200 + envelope `3010 invalid_auth_json_structure`
// (NOT the generic `2008 malformed_body`). Contract source:
// `specs/003-multi-mode-codex-auth/contracts/accounts-api.md`
// §POST /accounts/import-auth-json — 3010 covers every
// structural-parse failure on that route.
//
// This test was tightened after the overall Codex review caught
// the previous mapping (2008) as a contract violation.
func Test_EnvelopeStrictHandler_Import_InvalidAuthJSONStructure_NotMultipart(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	h := HandlerFromMuxWithEnvelope(stubServer{}, mux)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/admin/accounts/import-auth-json",
		strings.NewReader("this is not multipart"),
	)
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (AGENTS.md §HTTP Status Policy forbids 4xx on admin)", rec.Code)
	}
	got := decodeEnvelope(t, rec)
	if got.Code != envCodeInvalidAuthJSONStructure {
		t.Fatalf("envelope.code = %d, want %d (invalid_auth_json_structure)", got.Code, envCodeInvalidAuthJSONStructure)
	}
	if got.Msg != envMsgInvalidAuthJSONStructure {
		t.Fatalf("envelope.msg = %q, want %q", got.Msg, envMsgInvalidAuthJSONStructure)
	}
}

// Multipart content-type with NO `boundary=` parameter passes the
// content-type gate (the media type itself is `multipart/form-data`)
// but fails inside the strict layer when `r.MultipartReader()`
// rejects the missing boundary. This MUST still map to 3010 —
// the "it's a multipart request but we cannot parse it" bucket
// owns this case per `contracts/accounts-api.md`. This test
// distinguishes the gate (pre-decode) from the strict-layer
// (`RequestErrorHandlerFunc`) failure path so a future reviewer
// can trust both surfaces.
func Test_EnvelopeStrictHandler_Import_InvalidAuthJSONStructure_MissingBoundary(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	h := HandlerFromMuxWithEnvelope(stubServer{}, mux)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/admin/accounts/import-auth-json",
		strings.NewReader("--something\r\nContent-Disposition: form-data; name=\"auth_json\"\r\n\r\n{}\r\n--something--\r\n"),
	)
	// Note: no `boundary=...` parameter — r.MultipartReader() fails.
	req.Header.Set("Content-Type", "multipart/form-data")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got := decodeEnvelope(t, rec)
	if got.Code != envCodeInvalidAuthJSONStructure {
		t.Fatalf("envelope.code = %d, want %d (strict-layer multipart failure must still map to 3010)", got.Code, envCodeInvalidAuthJSONStructure)
	}
}

// ─── Strict-layer: response-side handler error ──────────────────

// When the handler returns a non-nil error, the transport layer
// MUST emit HTTP 500 + envelope `-1 unknown_error`, never a raw
// `text/plain` stack trace / error string (AGENTS.md §Security —
// avoid leaking internals).
func Test_EnvelopeStrictHandler_ResponseError_InternalError(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	h := HandlerFromMuxWithEnvelope(stubServer{
		importAuthErr: errors.New("simulated internal failure with /etc/secret/path hint"),
	}, mux)

	body := &bytes.Buffer{}
	body.WriteString("--xxx\r\nContent-Disposition: form-data; name=\"auth_json\"\r\n\r\n{}\r\n--xxx--\r\n")
	req := httptest.NewRequest(http.MethodPost, "/api/admin/accounts/import-auth-json", body)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=xxx")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (system error per AGENTS.md)", rec.Code)
	}
	got := decodeEnvelope(t, rec)
	if got.Code != envCodeUnknownError {
		t.Fatalf("envelope.code = %d, want %d (unknown_error)", got.Code, envCodeUnknownError)
	}
	if got.Msg != envMsgUnknownError {
		t.Fatalf("envelope.msg = %q, want %q", got.Msg, envMsgUnknownError)
	}
	// Security guarantee: the raw error message (including any
	// stack hints / file paths) MUST NOT leak into the envelope
	// body. We asserted the exact `code` / `msg` above; now lock
	// the body bytes against the suspicious substring so a
	// future regression that starts echoing `err.Error()` fails
	// this test.
	if bytes.Contains(rec.Body.Bytes(), []byte("/etc/secret/path hint")) {
		t.Fatalf("envelope leaked internal error message: %s", rec.Body.String())
	}
}

// ─── Outer-wrapper: path-param parse failure ────────────────────

// BLOCKER #2 from the overall Codex review: path parameter parse
// failures on routes like `/accounts/{id}/export-auth-json` fire from the
// OUTER wrapper (`ServerInterfaceWrapper.ErrorHandlerFunc`),
// which the initial T-006 cut did NOT override. The generator
// default is `http.Error(..., 400)`, which leaks a raw
// `400 text/plain`. `HandlerFromMuxWithEnvelope` must install an
// envelope-aware outer handler so this path returns 200 + 2008.
func Test_EnvelopeStrictHandler_OuterWrapper_PathParamParseError(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	h := HandlerFromMuxWithEnvelope(stubServer{}, mux)

	req := httptest.NewRequest(http.MethodPost, "/api/admin/accounts/not-an-int/export-auth-json", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (AGENTS.md §HTTP Status Policy forbids 4xx on admin)", rec.Code)
	}
	got := decodeEnvelope(t, rec)
	if got.Code != envCodeMalformedBody {
		t.Fatalf("envelope.code = %d, want %d (malformed_body for non-import routes)", got.Code, envCodeMalformedBody)
	}
	if paramVal, ok := got.Data["param"].(string); !ok || paramVal != "id" {
		t.Fatalf("envelope.data.param = %v, want \"id\" (parse failure should report the offending param)", got.Data["param"])
	}
}

// ─── Content-Type gate ──────────────────────────────────────────

// Sending `text/plain` with a valid JSON body to a JSON-only
// route MUST be rejected at the gate with envelope `2008
// malformed_body` — the generator's strict layer would otherwise
// happily decode the body because it does not consult
// `Content-Type`. Caught by the overall Codex review as MAJOR #2.
func Test_EnvelopeStrictHandler_ContentTypeGate_JSONRoute_RejectsTextPlain(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	h := HandlerFromMuxWithEnvelope(stubServer{}, mux)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/admin/oauth/browser/start",
		strings.NewReader(`{"provider":"openai"}`),
	)
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got := decodeEnvelope(t, rec)
	if got.Code != envCodeMalformedBody {
		t.Fatalf("envelope.code = %d, want %d (content-type gate should emit 2008 for JSON routes)", got.Code, envCodeMalformedBody)
	}
	if got.Msg != envMsgMalformedBody {
		t.Fatalf("envelope.msg = %q, want %q", got.Msg, envMsgMalformedBody)
	}
}

func Test_EnvelopeStrictHandler_ContentTypeGate_JSONRoute_RejectsTrailingSecondDocument(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	h := HandlerFromMuxWithEnvelope(stubServer{}, mux)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/admin/oauth/browser/start",
		strings.NewReader(`{"provider":"openai"}{"provider":"bogus"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got := decodeEnvelope(t, rec)
	if got.Code != envCodeMalformedBody {
		t.Fatalf("envelope.code = %d, want %d", got.Code, envCodeMalformedBody)
	}
}

func Test_EnvelopeStrictHandler_ContentTypeGate_JSONRoute_RejectsTrailingGarbage(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	h := HandlerFromMuxWithEnvelope(stubServer{}, mux)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/admin/oauth/browser/start",
		strings.NewReader(`{"provider":"openai"} garbage`),
	)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got := decodeEnvelope(t, rec)
	if got.Code != envCodeMalformedBody {
		t.Fatalf("envelope.code = %d, want %d", got.Code, envCodeMalformedBody)
	}
}

func Test_EnvelopeStrictHandler_ContentTypeGate_JSONRoute_RejectsTopLevelNull(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	h := HandlerFromMuxWithEnvelope(stubServer{}, mux)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/admin/oauth/browser/start",
		strings.NewReader(`null`),
	)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got := decodeEnvelope(t, rec)
	if got.Code != envCodeMalformedBody {
		t.Fatalf("envelope.code = %d, want %d", got.Code, envCodeMalformedBody)
	}
}

// Sending JSON to the multipart-only import route MUST be
// rejected at the gate with 3010 (the route-specific code),
// NOT the generic 2008.
func Test_EnvelopeStrictHandler_ContentTypeGate_MultipartRoute_RejectsJSON(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	h := HandlerFromMuxWithEnvelope(stubServer{}, mux)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/admin/accounts/import-auth-json",
		strings.NewReader(`{"auth_json":{"tokens":{}}}`),
	)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got := decodeEnvelope(t, rec)
	if got.Code != envCodeInvalidAuthJSONStructure {
		t.Fatalf("envelope.code = %d, want %d (multipart route must map to 3010 even at the gate)", got.Code, envCodeInvalidAuthJSONStructure)
	}
}

// The content-type gate MUST pass through when Content-Type
// matches and the media-type has parameters (e.g. `charset=utf-8`
// on application/json, or `boundary=xxx` on multipart). A
// too-strict string-compare would reject legitimate clients.
func Test_EnvelopeStrictHandler_ContentTypeGate_ParameterisedMediaType_Allowed(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	h := HandlerFromMuxWithEnvelope(stubServer{
		importAuthErr: errors.New("expected — we just need to get past the gate"),
	}, mux)

	body := &bytes.Buffer{}
	body.WriteString("--xxx\r\nContent-Disposition: form-data; name=\"auth_json\"\r\n\r\n{}\r\n--xxx--\r\n")
	req := httptest.NewRequest(http.MethodPost, "/api/admin/accounts/import-auth-json", body)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=xxx")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	// Must reach the handler (which then returns an error →
	// response-side path → 500 / -1). If we were stopped at the
	// gate we would see 200 / 3010 instead.
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("parameterised media-type was blocked by the gate — expected to pass through. status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// Empty-body POSTs on JSON-body admin routes are malformed in 003.
// The content-type/body-cap gate owns the rejection so every route
// gets the same HTTP 200 + `2008 malformed_body` envelope shape.
func Test_EnvelopeStrictHandler_ContentTypeGate_EmptyBody_Rejected(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	h := HandlerFromMuxWithEnvelope(stubServer{}, mux)

	req := httptest.NewRequest(http.MethodPost, "/api/admin/oauth/browser/start", http.NoBody)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)
	got := decodeEnvelope(t, rec)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if got.Code != envCodeMalformedBody {
		t.Fatalf("empty-body request code=%d want %d", got.Code, envCodeMalformedBody)
	}
}

func Test_EnvelopeStrictHandler_ContentTypeGate_PlaygroundRun_UsesRouteSpecificBodyCap(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	h := HandlerFromMuxWithEnvelope(stubPlaygroundRunOK{}, mux)

	payload := `{"selection_mode":"auto","model":"gpt-5.4-mini","text":"` + strings.Repeat("a", 9_000) + `"}`
	if len(payload) <= int(jsonRouteBodyLimitBytes) {
		t.Fatalf("test payload len=%d must exceed default JSON cap %d", len(payload), jsonRouteBodyLimitBytes)
	}
	if len(payload) >= int(playgroundJSONRouteBodyLimitBytes) {
		t.Fatalf("test payload len=%d must stay under Playground cap %d", len(payload), playgroundJSONRouteBodyLimitBytes)
	}

	req := httptest.NewRequest(http.MethodPost, routePlaygroundRun, strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	got := decodeEnvelope(t, rec)
	if got.Code != 0 {
		t.Fatalf("playground request over default JSON cap should reach handler; got code=%d body=%s", got.Code, rec.Body.String())
	}
}

func Test_EnvelopeStrictHandler_ContentTypeGate_PlaygroundRun_RejectsOverRouteSpecificBodyCap(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	h := HandlerFromMuxWithEnvelope(stubPlaygroundRunOK{}, mux)

	payload := `{"selection_mode":"auto","model":"gpt-5.4-mini","text":"` + strings.Repeat("a", 99_000) + `"}`
	if len(payload) <= int(playgroundJSONRouteBodyLimitBytes) {
		t.Fatalf("test payload len=%d must exceed Playground cap %d", len(payload), playgroundJSONRouteBodyLimitBytes)
	}

	req := httptest.NewRequest(http.MethodPost, routePlaygroundRun, strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	got := decodeEnvelope(t, rec)
	if got.Code != envCodeRequestBodyTooLarge {
		t.Fatalf("playground oversize code=%d want %d", got.Code, envCodeRequestBodyTooLarge)
	}
	if got.Msg != envMsgRequestBodyTooLarge {
		t.Fatalf("playground oversize msg=%q want %q", got.Msg, envMsgRequestBodyTooLarge)
	}
	if got.Data["scope"] != "envelope" {
		t.Fatalf("playground oversize scope=%v want envelope", got.Data["scope"])
	}
	limit, ok := got.Data["limit_bytes"].(float64)
	if !ok || int64(limit) != playgroundJSONRouteBodyLimitBytes {
		t.Fatalf("playground oversize limit_bytes=%v want %d", got.Data["limit_bytes"], playgroundJSONRouteBodyLimitBytes)
	}
}

func Test_EnvelopeStrictHandler_ContentTypeGate_SettingsUpdate_UsesRouteSpecificBodyCap(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	h := HandlerFromMuxWithEnvelope(stubSettingsUpdateOK{}, mux)

	payload := `{"runtime":{"log_level":"` + strings.Repeat("a", 9_000) + `"}}`
	if len(payload) <= int(jsonRouteBodyLimitBytes) {
		t.Fatalf("test payload len=%d must exceed default JSON cap %d", len(payload), jsonRouteBodyLimitBytes)
	}
	if len(payload) >= int(settingsJSONRouteBodyLimitBytes) {
		t.Fatalf("test payload len=%d must stay under Settings cap %d", len(payload), settingsJSONRouteBodyLimitBytes)
	}

	req := httptest.NewRequest(http.MethodPost, routeSettingsUpdate, strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	got := decodeEnvelope(t, rec)
	if got.Code != 0 {
		t.Fatalf("settings request over default JSON cap should reach handler; got code=%d body=%s", got.Code, rec.Body.String())
	}
}

func Test_EnvelopeStrictHandler_ContentTypeGate_SettingsUpdate_RejectsOverRouteSpecificBodyCap(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	h := HandlerFromMuxWithEnvelope(stubSettingsUpdateOK{}, mux)

	payload := `{"runtime":{"log_level":"` + strings.Repeat("a", 17_000) + `"}}`
	if len(payload) <= int(settingsJSONRouteBodyLimitBytes) {
		t.Fatalf("test payload len=%d must exceed Settings cap %d", len(payload), settingsJSONRouteBodyLimitBytes)
	}

	req := httptest.NewRequest(http.MethodPost, routeSettingsUpdate, strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	got := decodeEnvelope(t, rec)
	if got.Code != envCodeRequestBodyTooLarge {
		t.Fatalf("settings oversize code=%d want %d", got.Code, envCodeRequestBodyTooLarge)
	}
	if got.Data["scope"] != "envelope" {
		t.Fatalf("settings oversize scope=%v want envelope", got.Data["scope"])
	}
	limit, ok := got.Data["limit_bytes"].(float64)
	if !ok || int64(limit) != settingsJSONRouteBodyLimitBytes {
		t.Fatalf("settings oversize limit_bytes=%v want %d", got.Data["limit_bytes"], settingsJSONRouteBodyLimitBytes)
	}
}

type stubPlaygroundRunOK struct{ stubServer }

func (stubPlaygroundRunOK) PlaygroundRun(context.Context, PlaygroundRunRequestObject) (PlaygroundRunResponseObject, error) {
	statusCode := 200
	omitted := NotRequested
	var body PlaygroundRunResponseBody
	if err := body.FromPlaygroundRunSuccessEnvelope(PlaygroundRunSuccessEnvelope{
		Code: PlaygroundRunSuccessEnvelopeCodeN0,
		Msg:  PlaygroundRunSuccessEnvelopeMsgOk,
		Data: PlaygroundRunSuccessData{
			Run: PlaygroundRun{
				SelectionMode: PlaygroundRunSelectionModeAuto,
				Outcome:       Success,
				LatencyMs:     12,
			},
			Account: PlaygroundAccountSummary{
				Id:         1,
				Name:       "playground-smoke",
				Provider:   "openai",
				AuthMethod: ApiKey,
				Status:     PlaygroundAccountSummaryStatusActive,
			},
			Upstream: PlaygroundUpstreamSummary{
				StatusCode:   &statusCode,
				ResponseMode: PlaygroundUpstreamSummaryResponseModeJson,
			},
			Output: PlaygroundOutput{
				Text:                     "ok",
				TextAvailable:            true,
				RawResponseAvailable:     false,
				RawResponseOmittedReason: &omitted,
			},
			Usage: PlaygroundUsage{},
		},
	}); err != nil {
		return nil, err
	}
	return PlaygroundRun200JSONResponse(body), nil
}

type stubSettingsUpdateOK struct{ stubServer }

func (stubSettingsUpdateOK) SettingsUpdate(context.Context, SettingsUpdateRequestObject) (SettingsUpdateResponseObject, error) {
	var body SettingsUpdateResponseBody
	env := SettingsEnvelope{
		Code: SettingsEnvelopeCodeN0,
		Msg:  SettingsEnvelopeMsgOk,
		Data: sampleSettingsPayload(),
	}
	if err := body.FromSettingsEnvelope(env); err != nil {
		return nil, err
	}
	return SettingsUpdate200JSONResponse(body), nil
}

func sampleSettingsPayload() SettingsPayload {
	return SettingsPayload{
		Runtime: RuntimeSettings{
			LogClientRequestBody:    false,
			LogUpstreamRequestBody:  false,
			LogUpstreamResponseBody: false,
			LogRetentionDays:        30,
			LogLevel:                RuntimeSettingsLogLevelInfo,
		},
		Db: DBSettings{
			Driver:       "sqlite3",
			Host:         "local",
			DatabaseName: "router.db",
		},
		Plugins: []PluginSummary{},
		PluginIntents: []PluginIntent{
			{
				Id:      AdminAuth,
				Label:   "Admin authentication",
				Enabled: false,
				Status:  "intent only",
			},
		},
		System: SystemSummary{
			RouterVersion: "0.4.0",
			RouterGitSha:  "deadbeef",
			RouterBuiltAt: "2026-04-25T00:00:00Z",
		},
	}
}

// ─── Envelope primitive: data:null guard ────────────────────────

// Even when a caller passes nil Data to `writeEnvelope`, the
// output MUST be `"data":{}`, never `"data":null`. Guards
// AGENTS.md §API Contract and docs/error-codes.md §"data MUST
// always be present and MUST NEVER be JSON null".
func Test_EnvelopeStrictHandler_DataNeverNull(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	writeEnvelope(rec, http.StatusOK, envCodeMalformedBody, envMsgMalformedBody, nil)

	body, _ := io.ReadAll(rec.Body)
	s := string(body)
	if strings.Contains(s, `"data":null`) {
		t.Fatalf("envelope emitted data:null, contract forbids it: %s", s)
	}
	if !strings.Contains(s, `"data":{}`) {
		t.Fatalf("envelope missing `data:{}` sentinel (nil input must become `{}`): %s", s)
	}
}

// ─── Route table drift lock ─────────────────────────────────────

// expectedContentType is a hand-authored table that MUST be
// kept in lock-step with openapi/admin.yaml. This test pins the
// expected mapping for every route that exists today so a drift
// (new route added without a matching table entry, or the wrong
// expected content-type) fails at `go test ./...`.
//
// When a new 003+ operation is added in admin.yaml, update both
// this table and the `expectedContentType` implementation.
func Test_ExpectedContentType_Table(t *testing.T) {
	t.Parallel()

	cases := []struct {
		method, path string
		want         string
	}{
		{http.MethodPost, "/api/admin/accounts/import-auth-json", "multipart/form-data"},
		{http.MethodPost, "/api/admin/accounts/123/export-auth-json", ""},
		{http.MethodPost, "/api/admin/oauth/browser/manual-callback", "application/json"},
		{http.MethodPost, "/api/admin/oauth/browser/start", "application/json"},
		{http.MethodPost, "/api/admin/oauth/cancel", "application/json"},
		{http.MethodPost, "/api/admin/oauth/device/start", "application/json"},
		{http.MethodPost, "/api/admin/playground/run", "application/json"},
		{http.MethodPost, "/api/admin/settings/update", "application/json"},
		{http.MethodGet, "/api/admin/oauth/flow", ""},
		{http.MethodGet, "/api/admin/settings", ""},
		// GET methods should never trigger the gate.
		{http.MethodGet, "/api/admin/accounts/import-auth-json", ""},
	}

	for _, tc := range cases {
		if got := expectedContentType(tc.method, tc.path); got != tc.want {
			t.Errorf("expectedContentType(%q, %q) = %q, want %q", tc.method, tc.path, got, tc.want)
		}
	}
}

// ─── charsetMiddleware / upgradeContentType ─────────────────────

// Test_UpgradeContentType_Table locks the contract-compliance
// mapping for every input the middleware can see in production.
// The logic is IDEMPOTENT (already-correct headers pass through
// untouched) and MEDIA-TYPE-SCOPED (non-JSON types are left
// alone — a future streaming endpoint that emits
// `application/octet-stream` must not silently grow a charset
// param). Table-driven to lock both the positive and negative
// cases against future accidental relaxations.
func Test_UpgradeContentType_Table(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"bare_json_upgraded", "application/json", "application/json; charset=utf-8"},
		{"already_utf8_unchanged", "application/json; charset=utf-8", "application/json; charset=utf-8"},
		{"already_latin1_preserved", "application/json; charset=iso-8859-1", "application/json; charset=iso-8859-1"},
		{"mixed_case_json_upgraded", "Application/JSON", "application/json; charset=utf-8"},
		{"other_media_type_left_alone", "application/octet-stream", "application/octet-stream"},
		{"multipart_left_alone", "multipart/form-data; boundary=x", "multipart/form-data; boundary=x"},
		{"malformed_left_alone", "not a content type", "not a content type"},
		{"empty_left_alone", "", ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := http.Header{}
			if tc.in != "" {
				h.Set("Content-Type", tc.in)
			}
			upgradeContentType(h)
			if got := h.Get("Content-Type"); got != tc.want {
				t.Fatalf("upgradeContentType(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// Test_CharsetMiddleware_UpgradesGeneratedHeader simulates the
// generator's bare-`application/json` Visit behaviour and
// verifies the middleware upgrades it end-to-end. Without this
// middleware, downstream handler tests that rely on
// `testutil/envelope_assert.go` VerifyEnvelope (strict equality
// on the Content-Type header) would fail. This pins the upgrade
// at the HTTP layer, not just the pure function.
func Test_CharsetMiddleware_UpgradesGeneratedHeader(t *testing.T) {
	t.Parallel()

	// Simulates what server.gen.go Visit methods do.
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{}}`))
	})

	mw := charsetMiddleware()
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/admin/oauth/flow?flow_id=fl_x", nil))

	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want application/json; charset=utf-8 (middleware should upgrade bare JSON)", got)
	}
}

// Test_CharsetMiddleware_Idempotent verifies a handler that
// already sets the full header is not double-upgraded (no
// `charset=utf-8; charset=utf-8` fiascos).
func Test_CharsetMiddleware_Idempotent(t *testing.T) {
	t.Parallel()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})

	mw := charsetMiddleware()
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want application/json; charset=utf-8 (must be idempotent)", got)
	}
	if vs := rec.Header().Values("Content-Type"); len(vs) != 1 {
		t.Fatalf("Content-Type header has %d values %q, want exactly 1", len(vs), vs)
	}
}

// Test_CharsetMiddleware_NonJSONPreserved verifies non-JSON
// responses (e.g. a future streaming endpoint that emits
// `text/event-stream`) are NOT touched by the middleware. The
// charset-upgrade policy is scoped to `application/json` only.
func Test_CharsetMiddleware_NonJSONPreserved(t *testing.T) {
	t.Parallel()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`data: ping\n\n`))
	})

	mw := charsetMiddleware()
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream (non-JSON must not be touched)", got)
	}
}

// Test_HandlerFromMuxWithEnvelope_UpgradesCharsetOnEnvelopeError
// exercises the FULL middleware stack from the top — path-param
// parsing failure triggers envelopeRequestError, writes the
// envelope, and the wire response MUST carry charset=utf-8 so
// testutil VerifyEnvelope downstream passes.
func Test_HandlerFromMuxWithEnvelope_UpgradesCharsetOnEnvelopeError(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	root := HandlerFromMuxWithEnvelope(stubServer{}, mux)

	req := httptest.NewRequest(http.MethodPost, "/api/admin/accounts/not-an-int/export-auth-json", nil)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	root.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200 (AGENTS.md forbids 4xx)", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want application/json; charset=utf-8 (contract: accounts-api.md line 9)", got)
	}
}

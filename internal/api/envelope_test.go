package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/user/one-llm-router/internal/api/errcode"
)

// parsedEnvelope mirrors the on-wire shape for test decoding. Keep Code
// typed as int (not string) — the contract guarantees integer codes.
type parsedEnvelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) parsedEnvelope {
	t.Helper()
	var env parsedEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v (body=%q)", err, rec.Body.String())
	}
	return env
}

func assertCommonHeaders(t *testing.T, rec *httptest.ResponseRecorder, wantReqID string) {
	t.Helper()
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want application/json; charset=utf-8", got)
	}
	if got := rec.Header().Get("X-Request-Id"); got != wantReqID {
		t.Errorf("X-Request-Id = %q, want %q", got, wantReqID)
	}
}

func TestEnvelopeOK_HappyPath(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	WriteOK(rec, "req_abc", map[string]any{"state": "done"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	assertCommonHeaders(t, rec, "req_abc")

	env := decode(t, rec)
	if env.Code != errcode.OK {
		t.Errorf("code = %d, want 0", env.Code)
	}
	if env.Msg != "ok" {
		t.Errorf("msg = %q, want ok", env.Msg)
	}
	if string(env.Data) != `{"state":"done"}` {
		t.Errorf("data = %s, want {\"state\":\"done\"}", env.Data)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte(`"request_id"`)) {
		t.Error("body must not carry request_id; that belongs on the header")
	}
}

func TestEnvelopeOK_PanicsOnNilData(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("WriteOK(nil data) did not panic; contract requires panic")
		}
	}()
	rec := httptest.NewRecorder()
	WriteOK(rec, "req_nil", nil)
}

func TestEnvelopeBizErr_NormalisesNilData(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	WriteBizErr(rec, "req_x", errcode.SetupAlreadyDone, "setup_already_done", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (business error keeps 200)", rec.Code)
	}
	assertCommonHeaders(t, rec, "req_x")

	env := decode(t, rec)
	if env.Code != errcode.SetupAlreadyDone {
		t.Errorf("code = %d, want %d", env.Code, errcode.SetupAlreadyDone)
	}
	if env.Msg != "setup_already_done" {
		t.Errorf("msg = %q, want setup_already_done", env.Msg)
	}
	if string(env.Data) != `{}` {
		t.Errorf("data = %s, want {}", env.Data)
	}
}

func TestEnvelopeBizErr_CarriesData(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	WriteBizErr(rec, "req_y", errcode.InvalidDSN, "invalid_dsn", map[string]any{
		"latency_ms": 42,
		"hint":       map[string]any{"sqlstate": "28000"},
	})

	env := decode(t, rec)
	var data struct {
		LatencyMs int                    `json:"latency_ms"`
		Hint      map[string]interface{} `json:"hint"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if data.LatencyMs != 42 {
		t.Errorf("latency_ms = %d, want 42", data.LatencyMs)
	}
	if data.Hint["sqlstate"] != "28000" {
		t.Errorf("hint.sqlstate = %v, want 28000", data.Hint["sqlstate"])
	}
}

func TestEnvelopeSysErr_Returns500WithEmptyData(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	WriteSysErr(rec, "req_z", errcode.Unknown, "unknown_error")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	assertCommonHeaders(t, rec, "req_z")

	env := decode(t, rec)
	if env.Code != errcode.Unknown {
		t.Errorf("code = %d, want -1", env.Code)
	}
	if env.Msg != "unknown_error" {
		t.Errorf("msg = %q, want unknown_error", env.Msg)
	}
	if string(env.Data) != `{}` {
		t.Errorf("data = %s, want {}", env.Data)
	}
}

func TestEnvelope_OmitsHeaderWhenReqIDEmpty(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	WriteBizErr(rec, "", errcode.MalformedBody, "malformed_body", nil)

	if got := rec.Header().Get("X-Request-Id"); got != "" {
		t.Errorf("X-Request-Id = %q, want empty (no header set)", got)
	}
	if _, hadHeader := rec.Header()["X-Request-Id"]; hadHeader {
		t.Error("X-Request-Id header should not be present when reqID is empty")
	}
}

// unmarshalable triggers json.Marshal failure by containing an
// unexported channel field (json cannot serialize channels).
type unmarshalable struct {
	Ch chan int `json:"ch"`
}

func TestEnvelope_FallbackOnMarshalFailure(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	WriteOK(rec, "req_marshal", unmarshalable{Ch: make(chan int)})

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 on marshal failure", rec.Code)
	}
	assertCommonHeaders(t, rec, "req_marshal")

	env := decode(t, rec)
	if env.Code != errcode.Unknown {
		t.Errorf("code = %d, want -1 (fallback envelope)", env.Code)
	}
	if env.Msg != "unknown_error" {
		t.Errorf("msg = %q, want unknown_error", env.Msg)
	}
	if string(env.Data) != `{}` {
		t.Errorf("data = %s, want {}", env.Data)
	}
}

func TestEnvelope_AllSuccessShapesRetainData(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		data any
		want string
	}{
		{"empty_map", map[string]any{}, `{}`},
		{"empty_slice", []string{}, `[]`},
		{"nested_object", map[string]any{"a": map[string]any{"b": 1}}, `{"a":{"b":1}}`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			WriteOK(rec, "req", tc.data)
			env := decode(t, rec)
			if string(env.Data) != tc.want {
				t.Errorf("data = %s, want %s", env.Data, tc.want)
			}
		})
	}
}

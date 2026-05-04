package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/user/one-llm-router/internal/api/errcode"
)

// captureLogs swaps slog.Default with a text handler writing to buf for
// the duration of fn. Keeps tests hermetic from each other.
func captureLogs(t *testing.T, fn func()) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	fn()
	return &buf
}

func TestRecoverHandler_HappyPath_Passthrough(t *testing.T) {
	h := RecoverHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Test", "ok")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("body"))
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/anything", nil)

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want 418", rec.Code)
	}
	if got := rec.Header().Get("X-Test"); got != "ok" {
		t.Fatalf("X-Test = %q, want ok", got)
	}
	if rec.Body.String() != "body" {
		t.Fatalf("body = %q, want body", rec.Body.String())
	}
}

func TestRecoverHandler_AdminPanic_WritesEnvelope500(t *testing.T) {
	var buf *bytes.Buffer
	rec := httptest.NewRecorder()
	buf = captureLogs(t, func() {
		h := RecoverHandler(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			panic("boom")
		}))
		req := httptest.NewRequest(http.MethodGet, "/admin/foo", nil)
		req = req.WithContext(WithRequestID(req.Context(), "req_trace_1"))
		h.ServeHTTP(rec, req)
	})

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want application/json; charset=utf-8", got)
	}
	if got := rec.Header().Get("X-Request-Id"); got != "req_trace_1" {
		t.Errorf("X-Request-Id = %q, want req_trace_1", got)
	}

	var env struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Code != errcode.Unknown {
		t.Errorf("code = %d, want -1", env.Code)
	}
	if env.Msg != "unknown_error" {
		t.Errorf("msg = %q, want unknown_error", env.Msg)
	}

	logs := buf.String()
	for _, needle := range []string{"panic recovered", "panic_preview=boom", "path=/admin/foo", "request_id=req_trace_1", "stack="} {
		if !strings.Contains(logs, needle) {
			t.Errorf("log output missing %q; got %q", needle, logs)
		}
	}
}

// TestRecoverHandler_V1PathPanicPropagates verifies the boundary case:
// a panic on /v1/* must NOT be transformed into the router envelope. The
// stdlib's net/http will recover later and close the connection; in
// this unit test we install an outer recover to observe that our
// middleware did not consume the panic.
func TestRecoverHandler_DataPlanePathPanicPropagates(t *testing.T) {
	h := RecoverHandler(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("boom")
	}))

	for _, path := range []string{
		"/v1/models",
		"/v1",
		"/backend-api",
		"/backend-api/codex/responses",
	} {
		path := path
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("%s: expected panic to propagate, got none", path)
				}
				if r != "boom" {
					t.Fatalf("%s: unexpected recovered value %v", path, r)
				}
				if rec.Body.Len() != 0 {
					t.Errorf("%s: body should be empty, got %q", path, rec.Body.String())
				}
			}()
			h.ServeHTTP(rec, req)
		})
	}
}

// TestRecoverHandler_ErrAbortHandlerRepanics verifies we never swallow
// http.ErrAbortHandler even on non-/v1 paths: the stdlib relies on the
// re-panic to close the connection silently.
func TestRecoverHandler_ErrAbortHandlerRepanics(t *testing.T) {
	h := RecoverHandler(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/foo", nil)

	defer func() {
		// recover() returns any; identity check against the sentinel
		// re-panicked by RecoverHandler is the exact behaviour we test.
		if r := recover(); r != http.ErrAbortHandler { //nolint:errorlint
			t.Fatalf("expected http.ErrAbortHandler to re-propagate, got %v", r)
		}
		if rec.Body.Len() != 0 {
			t.Errorf("body must be empty on ErrAbortHandler, got %q", rec.Body.String())
		}
	}()
	h.ServeHTTP(rec, req)
}

func TestIsDataPlanePath_Cases(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"/v1":                   true,
		"/v1/":                  true,
		"/v1/models":            true,
		"/v1/chat/completions":  true,
		"/backend-api":          true,
		"/backend-api/codex":    true,
		"/api/codex":            false,
		"/api/codex/usage":      false,
		"/api/v1/something":     false,
		"/v1foo":                false,
		"/backend-apix":         false,
		"/api/codexish":         false,
		"/admin/v1/passthrough": false,
		"/":                     false,
		"":                      false,
	}
	for input, want := range cases {
		input, want := input, want
		t.Run(input+"=", func(t *testing.T) {
			t.Parallel()
			if got := isDataPlanePath(input); got != want {
				t.Fatalf("isDataPlanePath(%q) = %v, want %v", input, got, want)
			}
		})
	}
}

// TestPanicPreview_TruncatesLongPayloads guards the 256-byte bound on
// the panic payload rendered into the recover log line. The cap keeps
// secret-carrying panic messages (e.g. "failed to open dsn postgres://
// user:password@host/db") from flowing unbounded into log sinks.
func TestPanicPreview_TruncatesLongPayloads(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("a", panicPreviewMax+50)
	got := panicPreview(long)
	// Assert the exact truncation boundary, not only max length + suffix.
	wantPrefix := strings.Repeat("a", panicPreviewMax)
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("preview = %q, want prefix %q…", got, wantPrefix)
	}
	if len(got) != len(wantPrefix)+len("…") {
		t.Errorf("len = %d, want %d (prefix + ellipsis)", len(got), len(wantPrefix)+len("…"))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated output must end with '…', got %q", got[len(got)-4:])
	}
}

// TestPanicPreview_StripsNewlines guards that a multi-line panic
// payload collapses to a single log line. Slog's text handler would
// otherwise split the record across lines and break log shippers
// that key on first-line-start.
func TestPanicPreview_StripsNewlines(t *testing.T) {
	t.Parallel()
	got := panicPreview("first\nsecond\r\nthird")
	if strings.ContainsAny(got, "\r\n") {
		t.Errorf("preview must not contain CR/LF, got %q", got)
	}
	for _, needle := range []string{"first", "second", "third"} {
		if !strings.Contains(got, needle) {
			t.Errorf("preview must preserve %q, got %q", needle, got)
		}
	}
}

// TestPanicPreview_HandlesNonStringPayload verifies that panic(err)
// (where err is a typed value) renders via %v just like a string.
// Asserts the EXACT %v form so a future drift from fmt.Sprint("%v", …)
// to a different verb (%+v / %q / custom) is caught — T-010 tightened
// this from a Contains check that would have silently accepted any
// output containing "typed".
func TestPanicPreview_HandlesNonStringPayload(t *testing.T) {
	t.Parallel()
	payload := errTyped{msg: "typed"}
	want := fmt.Sprintf("%v", payload)
	got := panicPreview(payload)
	if got != want {
		t.Errorf("preview = %q, want %q (exact %%v render)", got, want)
	}

	// Also cover a non-error struct to prove the helper does NOT
	// special-case the error interface — anything printable through
	// %v must round-trip identically.
	type plain struct{ N int }
	payload2 := plain{N: 7}
	want2 := fmt.Sprintf("%v", payload2)
	got2 := panicPreview(payload2)
	if got2 != want2 {
		t.Errorf("struct preview = %q, want %q", got2, want2)
	}
}

// errTyped is a minimal typed value the panic-preview test panics
// with. Kept private to recover_test.go.
type errTyped struct{ msg string }

func (e errTyped) Error() string { return e.msg }

// TestRecoverHandler_NoRequestIDWhenMissing verifies graceful behaviour
// when the request never hit RequestIDMiddleware (e.g. pathological
// test-only flow). The envelope still renders with no X-Request-Id
// header.
func TestRecoverHandler_NoRequestIDWhenMissing(t *testing.T) {
	h := RecoverHandler(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("ghost")
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/ghost", nil)
	req = req.WithContext(context.Background()) // explicit: no request_id on ctx

	_ = captureLogs(t, func() { h.ServeHTTP(rec, req) })

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if _, present := rec.Header()["X-Request-Id"]; present {
		t.Error("X-Request-Id should be absent when no id is on the context")
	}
}

package api

import (
	"bufio"
	"bytes"
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestLoggingMiddleware_LogsStatusAndLatency(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("hello"))
	})

	mw := RequestLoggingMiddleware(logger)(inner)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/admin/health", nil)
	mw.ServeHTTP(w, r)

	assert.Equal(t, http.StatusTeapot, w.Code)
	assert.Equal(t, "hello", w.Body.String())

	out := buf.String()
	assert.Contains(t, out, "msg=\"http request\"")
	assert.Contains(t, out, "method=GET")
	assert.Contains(t, out, "path=/admin/health")
	assert.Contains(t, out, "status=418")
	assert.Contains(t, out, "bytes=5")
	assert.Contains(t, out, "latency_ms=")
}

func TestRequestLoggingMiddleware_DefaultsTo200(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	mw := RequestLoggingMiddleware(logger)(inner)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/x", nil)
	mw.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, strings.Contains(buf.String(), "status=200"))
}

func TestRequestLoggingMiddleware_DoubleWriteHeaderIgnored(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("ok"))
	})

	mw := RequestLoggingMiddleware(logger)(inner)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/x", nil)
	mw.ServeHTTP(w, r)

	assert.Equal(t, http.StatusAccepted, w.Code)
	assert.Contains(t, buf.String(), "status=202")
}

type flusherSpy struct{ flushed bool }

func (f *flusherSpy) Header() http.Header         { return http.Header{} }
func (f *flusherSpy) Write(b []byte) (int, error) { return len(b), nil }
func (f *flusherSpy) WriteHeader(_ int)           {}
func (f *flusherSpy) Flush()                      { f.flushed = true }

func TestRequestLoggingMiddleware_PreservesFlusher(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	})

	spy := &flusherSpy{}
	mw := RequestLoggingMiddleware(logger)(inner)
	r := httptest.NewRequest("GET", "/x", nil)
	mw.ServeHTTP(spy, r)

	assert.True(t, spy.flushed, "middleware must not hide http.Flusher so SSE keeps working")
}

// R5: RequestIDMiddleware must generate an ID when none is supplied, echo
// it back on the response, and expose it via RequestIDFromContext so nested
// handlers log and forward the same ID.
func TestRequestIDMiddleware_GeneratesWhenMissing(t *testing.T) {
	var seenID string
	var seenClientIP string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenID = RequestIDFromContext(r.Context())
		seenClientIP = ClientIPFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	mw := RequestIDMiddleware()(inner)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/x", nil)
	r.RemoteAddr = "203.0.113.44:54321"
	mw.ServeHTTP(w, r)

	assert.NotEmpty(t, seenID, "handler must see generated request id on context")
	assert.Equal(t, "203.0.113.44", seenClientIP)
	assert.Equal(t, seenID, w.Header().Get("X-Request-Id"), "response header must echo the same id")
}

// Inbound X-Request-Id MUST be preserved (tracing continuity across proxies).
func TestRequestIDMiddleware_PrefersInboundHeader(t *testing.T) {
	var seenID string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenID = RequestIDFromContext(r.Context())
	})

	mw := RequestIDMiddleware()(inner)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/x", nil)
	r.Header.Set("X-Request-Id", "caller-supplied-id")
	mw.ServeHTTP(w, r)

	assert.Equal(t, "caller-supplied-id", seenID)
	assert.Equal(t, "caller-supplied-id", w.Header().Get("X-Request-Id"))
}

// Legacy "Request-Id" (no X- prefix) is accepted when X-Request-Id is absent.
func TestRequestIDMiddleware_AcceptsLegacyHeader(t *testing.T) {
	var seenID string
	mw := RequestIDMiddleware()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seenID = RequestIDFromContext(r.Context())
	}))
	r := httptest.NewRequest("GET", "/x", nil)
	r.Header.Set("Request-Id", "legacy-id")
	mw.ServeHTTP(httptest.NewRecorder(), r)
	assert.Equal(t, "legacy-id", seenID)
}

// Logging middleware must emit the same request_id so proxy + admin +
// middleware logs can be joined on a single identifier.
func TestRequestLoggingMiddleware_IncludesRequestID(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	chain := RequestIDMiddleware()(RequestLoggingMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	r := httptest.NewRequest("GET", "/x", nil)
	r.Header.Set("X-Request-Id", "trace-42")
	chain.ServeHTTP(httptest.NewRecorder(), r)

	assert.Contains(t, buf.String(), "request_id=trace-42")
}

// RequestIDFromContext returns "" for a nil / unseeded context.
func TestRequestIDFromContext_Defaults(t *testing.T) {
	assert.Equal(t, "", RequestIDFromContext(context.Background()))
	//nolint:staticcheck // exercise the nil-ctx defensive branch inside RequestIDFromContext
	assert.Equal(t, "", RequestIDFromContext(nil))
	assert.Equal(t, "abc", RequestIDFromContext(WithRequestID(context.Background(), "abc")))
}

// statusResponseWriter.Hijack must forward to the underlying hijackable
// writer and fail cleanly when the underlying writer does not support it.
type hijackableRecorder struct {
	*httptest.ResponseRecorder
	hijacked bool
}

func (h *hijackableRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h.hijacked = true
	return nil, nil, nil
}

func TestStatusResponseWriter_HijackForwards(t *testing.T) {
	underlying := &hijackableRecorder{ResponseRecorder: httptest.NewRecorder()}
	sw := &statusResponseWriter{ResponseWriter: underlying}

	hj, ok := interface{}(sw).(http.Hijacker)
	require.True(t, ok, "statusResponseWriter must implement http.Hijacker")
	_, _, err := hj.Hijack()
	require.NoError(t, err)
	assert.True(t, underlying.hijacked, "Hijack call must reach the underlying writer")
}

func TestStatusResponseWriter_HijackUnsupported(t *testing.T) {
	sw := &statusResponseWriter{ResponseWriter: httptest.NewRecorder()}
	hj := interface{}(sw).(http.Hijacker)
	_, _, err := hj.Hijack()
	require.Error(t, err, "non-hijackable underlying writer must surface an error")
}

package api

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/user/one-llm-router/internal/clientip"
	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/requestid"
)

// RequestIDFromContext returns the request ID stored by the middleware, or an
// empty string if none is present. Handlers that generate their own ID should
// fall back to domain.NewRequestID() only when this returns "".
func RequestIDFromContext(ctx context.Context) string {
	return requestid.FromContext(ctx)
}

// WithRequestID returns a ctx carrying id. Exported so tests (and the proxy)
// can seed a context when bypassing the HTTP middleware.
func WithRequestID(ctx context.Context, id string) context.Context {
	return requestid.WithContext(ctx, id)
}

// ClientIPFromContext returns the router-observed client IP stored by the
// middleware, or "" if the request did not pass through the HTTP chain.
func ClientIPFromContext(ctx context.Context) string {
	return clientip.FromContext(ctx)
}

// statusResponseWriter wraps http.ResponseWriter to capture the status code
// and bytes-written count. It preserves http.Flusher so SSE streaming still
// works through the middleware chain, and forwards http.Hijacker so future
// WebSocket/upgrade paths (Phase 3) are not silently broken by the wrapper.
type statusResponseWriter struct {
	http.ResponseWriter
	status       int
	bytesWritten int
	wroteHeader  bool
}

func (w *statusResponseWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.status = code
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytesWritten += n
	return n, err
}

func (w *statusResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack forwards to the underlying ResponseWriter if it supports hijacking
// (needed for Phase 3 protocol upgrades). Returns http.ErrNotSupported
// otherwise so callers can detect unsupported transports.
func (w *statusResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("response writer does not support hijacking")
}

// RequestIDMiddleware installs a per-request identifier on every inbound
// request. Priority order (mirrors codex-lb):
//  1. Inbound X-Request-Id header (downstream proxy / tracing systems).
//  2. Inbound Request-Id header (legacy).
//  3. Freshly generated UUID via domain.NewRequestID().
//
// The ID is attached to the request context via WithRequestID and reflected
// back on the response header so clients can correlate failures.
func RequestIDMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get("X-Request-Id")
			if id == "" {
				id = r.Header.Get("Request-Id")
			}
			if id == "" {
				id = domain.NewRequestID()
			}
			w.Header().Set("X-Request-Id", id)
			ctx := WithRequestID(r.Context(), id)
			ctx = clientip.WithContext(ctx, r.RemoteAddr)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequestLoggingMiddleware emits a single structured log entry per HTTP
// request covering method, path, status, latency, bytes and request_id. This
// satisfies the AGENTS.md boundary rule "preserve request logging for routed
// traffic" and gives admin routes observability parity with the proxy path.
func RequestLoggingMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusResponseWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)
			logger.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", sw.status,
				"bytes", sw.bytesWritten,
				"latency_ms", time.Since(start).Milliseconds(),
				"remote_addr", r.RemoteAddr,
				"request_id", RequestIDFromContext(r.Context()),
			)
		})
	}
}

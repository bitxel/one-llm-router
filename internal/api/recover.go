package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"

	"github.com/user/one-llm-router/internal/api/errcode"
)

// panicPreviewMax caps the stringified panic payload included in the
// recovered log line. Panic messages often carry formatted errors
// that MAY include DSN fragments, SQL statements, or caller-supplied
// secrets. Truncating to a bounded preview keeps the recovery log
// useful for triage while avoiding unbounded secret leakage.
const panicPreviewMax = 256

// RecoverHandler catches panics from downstream handlers and converts them
// into the `{code,msg,data}` envelope with code = -1 and HTTP 500. The
// panic payload and Goroutine stack are emitted via slog.Error along
// with the correlation id so operators can triage from the logs.
//
// Placement: compose AFTER RequestIDMiddleware (so the correlation id is
// already on the context) and AFTER RequestLoggingMiddleware (so the log
// line still fires even when the handler panics). A suggested stack is:
//
//	root := RequestIDMiddleware()(
//	  RequestLoggingMiddleware(logger)(
//	    RecoverHandler(mux),
//	  ),
//	)
//
// Some paths are intentionally NOT recovered here:
//
//   - /v1/* and selected /backend-api/* — the data
//     plane is excluded from the admin envelope entirely (see
//     AGENTS.md §HTTP API Style). A panic there propagates to
//     net/http's stdlib handler, which closes the connection and
//     preserves provider-compatible client semantics.
//   - http.ErrAbortHandler — the stdlib's sentinel for "abort the
//     request without logging". Per standard Go convention a recovery
//     wrapper MUST re-panic so net/http can still close the connection
//     cleanly. We honour it on every path, /v1 or not.
func RecoverHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isDataPlanePath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			// recover() returns any, not error; this is a panic-value
			// identity check against the net/http sentinel, not an
			// error-chain match.
			if rec == http.ErrAbortHandler { //nolint:errorlint
				panic(rec)
			}
			reqID := RequestIDFromContext(r.Context())
			slog.Error("panic recovered",
				"panic_preview", panicPreview(rec),
				"method", r.Method,
				"path", r.URL.Path,
				"request_id", reqID,
				"stack", string(debug.Stack()),
			)
			WriteSysErr(w, reqID, errcode.Unknown, "unknown_error")
		}()
		next.ServeHTTP(w, r)
	})
}

// isDataPlanePath reports whether the given URL path is owned by the
// provider-compatible data plane rather than the router-owned admin
// JSON APIs.
func isDataPlanePath(path string) bool {
	return path == "/v1" ||
		strings.HasPrefix(path, "/v1/") ||
		path == "/backend-api" ||
		strings.HasPrefix(path, "/backend-api/")
}

// panicPreview renders a bounded, newline-stripped summary of a
// recovered panic payload for logging. Keeping the rendering logic
// central lets ops tweak the bound or the redaction policy in one
// place.
func panicPreview(rec any) string {
	s := fmt.Sprintf("%v", rec)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	if len(s) > panicPreviewMax {
		s = s[:panicPreviewMax] + "…"
	}
	return s
}

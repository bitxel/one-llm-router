package plugin

import (
	"context"
	"net/http"
	"time"
)

// AdminAuth authenticates requests hitting admin JSON endpoints. The
// app invokes Authenticate exactly once per inbound /api/admin/*
// request (SPA static assets at /admin/* and all /api/setup/*
// endpoints are allow-listed in the middleware chain per the
// path-convention split — see plan.md §Routing topology).
//
// Authenticate returns the authenticated admin's stable identifier
// plus a nil error on success. A non-nil error aborts the request
// with 401/403; the specific HTTP code is chosen by the caller
// (BuildApp) based on the error's sentinel category, not by the
// plugin.
//
// Activated in 003. 002 invokes NONE of this interface.
type AdminAuth interface {
	Plugin
	Authenticate(r *http.Request) (adminID string, err error)
}

// ClientKeyAuth authenticates clients hitting proxied endpoints. The
// app invokes Authenticate exactly once per inbound /v1/* request.
//
// Returning a non-nil error aborts the request with 401 and the 001
// native error shape ({"error":{...}}) — /v1/* NEVER uses the 002
// JSON envelope.
//
// Activated in 004. 002 invokes NONE of this interface.
type ClientKeyAuth interface {
	Plugin
	Authenticate(r *http.Request) (clientKeyID int64, err error)
}

// ProxyHook observes proxied requests after they complete. Once
// wired (005), it is invoked once per completed /v1/* request,
// regardless of outcome (2xx, 4xx, 5xx, connection errors).
//
// Implementations MUST be non-blocking. The intended 005 dispatch
// model (NOT yet implemented in 002) is: BuildApp fans out every
// enabled ProxyHook via a goroutine + bounded queue; slow hooks
// drop events rather than slow the proxy. 005's load suite will
// enforce that contract; 002 ships only the interface so observers
// can compile against a stable type.
//
// Unlike AdminAuth and ClientKeyAuth (which are singletons),
// ProxyHook is MULTI-INSTANCE — once 005 wires dispatch, the app
// invokes every enabled plugin that implements it. The Prometheus
// exporter (005) and a hypothetical OTel exporter can coexist.
//
// Activated in 005. 002 invokes NONE of this interface.
type ProxyHook interface {
	Plugin
	OnProxyComplete(ctx context.Context, ev ProxyCompleteEvent)
}

// ProxyCompleteEvent is the immutable record passed to ProxyHook
// implementations. It is additive-only across feature versions: new
// fields may be appended without a major bump, but existing fields
// MUST NOT be renamed or retyped.
//
// Keep field docstrings concise — this type lives in the plugin API
// surface and is read by every observability plugin author.
type ProxyCompleteEvent struct {
	// RequestID is the X-Request-Id the middleware attached. Always
	// non-empty (the request-id middleware generates a UUID if the
	// client did not supply one).
	RequestID string

	// APIKeyID identifies the client key that authenticated the
	// request. Zero when client-key auth is disabled (pre-004 world)
	// or when authentication was bypassed via a special route.
	APIKeyID int64

	// AccountID identifies the upstream account the selector chose.
	// Non-zero on a successful proxy; zero if selection failed.
	AccountID int64

	// SessionID is the session key the request was sticky-routed by,
	// empty for sessionless requests.
	SessionID string

	// Method and Path describe the inbound request. Path is normalised
	// to its template form where possible (e.g. "/v1/responses"),
	// NOT the raw URL-with-query.
	Method string
	Path   string

	// StatusCode is the HTTP status code sent back to the client.
	// Zero if the connection dropped before any status was emitted.
	StatusCode int

	// LatencyNS is the wall-clock duration from inbound-request-
	// accepted to last-byte-written. Nanoseconds keep the type wire-
	// compatible with slog's duration formatting.
	LatencyNS int64

	// BytesIn / BytesOut are the counted body bytes. They are -1 if
	// the proxy did not (or could not) count (e.g. streaming body
	// larger than the cap).
	BytesIn  int64
	BytesOut int64

	// UpstreamErr is the non-protocol error returned by the upstream
	// client, if any. Nil for protocol-level errors (4xx/5xx are NOT
	// upstream errors — they are valid responses).
	UpstreamErr error

	// CompletedAt is the wall-clock time the request finished, in UTC.
	CompletedAt time.Time
}

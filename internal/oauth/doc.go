// Package oauth owns Feature 003's OAuth state machine for browser-PKCE
// and device-code flows. It is the single authority over the non-wire
// lifecycle of an upstream OAuth account:
//
//   - Coordinator — top-level façade for admin handlers; starts flows,
//     refreshes tokens, and persists successful exchanges via internal/store.
//   - Flow — the short-lived, in-memory record of one in-flight flow
//     (browser PKCE or device-code). Owns an atomic Consumed flag so
//     the browser flow's loopback and manual-paste rails race without
//     duplicating the code exchange.
//   - Provider — interface over the upstream OAuth server; the concrete
//     openai implementation lives in exchange.go.
//   - Clock — stdlib-only clock interface for deterministic tests.
//
// Module boundaries (see specs/003-multi-mode-codex-auth/plan.md):
//
//   - Depends on internal/domain (UpstreamAccount) and internal/store
//     (account persistence); MUST NOT depend on any internal/api package.
//   - internal/api/oauthapi consumes Coordinator; internal/api/exportapi
//     consumes store directly. Neither package imports this one's
//     unexported types.
//
// Secrets policy: token bytes (access/refresh/id) never cross the
// slog boundary. Helpers in this package log flow_id, method, provider,
// and error_code only — the log-scrub CI gate in T-095 enforces it.
package oauth

// Package oauthapi hosts the admin-facing HTTP handlers for Feature
// 003's OAuth browser and device flows — everything under the
// /api/admin/oauth/* prefix:
//
//   - POST /api/admin/oauth/browser/start             — new browser-PKCE flow
//   - POST /api/admin/oauth/browser/manual-callback   — Rail B paste submission
//   - POST /api/admin/oauth/device/start              — new device-code flow
//   - GET  /api/admin/oauth/flow                      — poll current flow state
//   - POST /api/admin/oauth/cancel                    — cancel current flow
//
// All routes live behind the admin-auth plugin and speak the
// project-wide JSON envelope (HTTP 200 with {code, msg, data} per
// docs/error-codes.md — no HTTP 4xx leakage on /api/admin/*).
//
// Scope boundary — loopback callback is NOT served from this package:
// the browser-facing GET /auth/callback handler (Rail A of the dual-rail
// flow) lives inside internal/oauth — it is wired into the ephemeral
// *http.Server that oauth.Coordinator.StartBrowser binds on
// 127.0.0.1:1455. That handler emits plaintext / minimal HTML to the
// operator's browser tab and deliberately bypasses the envelope; see
// specs/003-multi-mode-codex-auth/plan.md §Data Flow US-1 and
// contracts/oauth-flow-api.md §GET /auth/callback. oauthapi NEVER
// registers /auth/callback on the admin router.
//
// Module boundaries:
//
//   - Imports internal/oauth (Coordinator / Provider / Flow types).
//   - Implements generatedadminapi.StrictServerInterface and is wrapped by
//     generatedadminapi.HandlerFromMuxWithEnvelope so envelope rendering
//     stays byte-identical with the OpenAPI codegen surface.
//   - MUST NOT import internal/store, internal/api/adminapi, or any
//     other handler package — routing dependencies flow inward.
//
// Registration is the one public seam. Phase 3 exposes
// RegisterBrowserHandlers; Phase 5 adds RegisterDeviceHandlers.
// Both use the same shared http.ServeMux + handler-chain shape
// (per AGENTS.md §Plugin Model — plugins declare capabilities, not
// URL mounts).
package oauthapi

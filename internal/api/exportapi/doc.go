// Package exportapi hosts the single admin endpoint
// POST /api/admin/accounts/{id}/export-auth-json, which streams a
// Codex CLI-compatible auth.json file for one OAuth upstream account.
//
// It lives in its own package rather than sharing adminapi because its
// success response is the SECOND documented envelope exemption in 003:
// the success body is the raw 5-key auth.json payload served as an
// application/json attachment (Content-Disposition filename=auth.json),
// NOT the project envelope. Keeping it physically separate from
// adminapi/ guarantees the envelope-parity CI gate (T-095a) can carve
// it out cleanly, and that no future hand-edit accidentally wraps the
// success branch in WriteOK.
//
// Error responses DO wear the envelope and route through
// internal/api (WriteBizErr / WriteSysErr) + internal/api/errcode
// just like every other /api/admin/* route.
//
// Module boundaries:
//
//   - Imports internal/store (account lookup), internal/api, and
//     internal/api/errcode.
//   - MUST NOT import internal/api/adminapi or internal/api/oauthapi —
//     handler packages are peers, never siblings.
//   - Every success response emits an oauth_auth_json_exported WARN
//     audit event via slog per contracts/accounts-api.md §Logs —
//     callers MUST carry operator_id through request context and this
//     package is responsible for the final structured log line.
package exportapi

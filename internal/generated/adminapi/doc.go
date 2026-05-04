// Package adminapi is the code-generated Go mirror of the admin API
// OpenAPI schema at openapi/admin.yaml.
//
// The `.gen.go` files in this directory MUST NOT be hand-edited.
// The CI freshness gate (specs/003-multi-mode-codex-auth/tasks.md
// T-008) fails any PR where `bash scripts/codegen-go.sh` produces a
// diff against the committed tree for those files. Hand-authored
// companion files are allowed (and enumerated below) — they are
// explicitly excluded from the freshness check because they are
// never regenerated.
//
// Contents:
//
//   - types.gen.go        — GENERATED. Request/response/schema
//     models emitted by `openapi/codegen-types.yaml`.
//   - server.gen.go       — GENERATED. net/http handler +
//     strict-server interface emitted by
//     `openapi/codegen-server.yaml`; implementers satisfy either
//     the regular ServerInterface or the stricter
//     StrictServerInterface to wire up handlers.
//   - doc.go              — HAND-AUTHORED. This file. Kept so the
//     package still builds a doc header even if the generated
//     files are removed, and so engineers have a single place to
//     learn how to regenerate.
//   - union_marshal.go    — HAND-AUTHORED. Per-operation
//     MarshalJSON shims that restore the serialisation semantics
//     of oneOf-backed response wrappers. Required because
//     oapi-codegen emits `type Op200JSONResponse Union` (a
//     defined type, not a type alias), and Go does NOT inherit
//     MarshalJSON across `type X Y`. Without these shims the
//     generated `Visit<Op>Response` would reflect-encode the
//     wrapper and emit `{}` instead of the envelope. See the
//     file's header for the full rationale.
//   - union_smoke_test.go — HAND-AUTHORED. Per-branch regression
//     test that asserts every oneOf response body's
//     `As<Branch>() / From<Branch>() / Merge<Branch>()` helper
//     compiles AND that both direct `json.Marshal(wrapper)` and
//     the generated `Visit<Op>Response` method emit the correct
//     envelope (never `{}` or `null`). Also locks the total
//     branch count so a spec change without a matching test
//     update fails loud at `go test ./...`.
//   - strict_handler.go   — HAND-AUTHORED. Blessed entrypoint
//     `NewEnvelopeStrictHandler(ssi, middlewares)` that wires the
//     generator's StrictServerInterface through envelope-aware
//     request/response error handlers. The generator-default
//     `NewStrictHandler` leaks `400/500 text/plain` on
//     decode/handler failures which violates the non-negotiable
//     envelope contract (AGENTS.md §HTTP API Style); handlers
//     MUST use the blessed constructor. Paired with
//     `strict_handler_test.go`.
//   - export_visit.go     — HAND-AUTHORED. Safe response wrappers
//     `ExportAuthJSONAttachmentResponse` (success branch only —
//     writes attachment headers) and `ExportAuthJSONEnvelopeResponse`
//     (200-envelope error branches only — no attachment headers).
//     Exists because the generator's default
//     `AccountsExportAuthJSON200JSONResponse` writes attachment
//     headers UNCONDITIONALLY, which poisons the error branches
//     with empty-valued `Content-Disposition` and defeats the
//     client-side "is Content-Disposition present?" discriminator
//     that identifies the file-download exemption. Paired with
//     `export_visit_test.go`.
//
// Regeneration:
//
//	bash scripts/codegen-go.sh
//
// See `openapi/README.md` §Codegen pipeline for the end-to-end
// contract (admin.yaml → Go + TS, CI freshness gate, pin strategy).
package adminapi

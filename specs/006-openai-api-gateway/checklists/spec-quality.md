# Spec Quality Checklist: Full OpenAI API Gateway

**Feature**: 006-openai-api-gateway
**Date**: 2026-04-27
**Status**: Pass

## Content Quality

- [x] User value is explicit for each scenario.
- [x] Scope boundaries are documented.
- [x] Assumptions are documented.
- [x] No unresolved clarification markers remain.
- [x] Requirement language is testable.

## Requirement Quality

- [x] P0 endpoint groups are listed.
- [x] Transport shapes are identified.
- [x] API-key vs OAuth account behavior is explicit.
- [x] Operation-level coverage matrix is explicit for the initial 006 route set.
- [x] Backend routing logic by client path and upstream credential type is explicit.
- [x] Provider-compatible data-plane error behavior is explicit.
- [x] Security constraints cover secrets, binary payloads, and admin surfaces.
- [x] Administration API scope is resolved.
- [x] Realtime WebRTC/SIP scope is resolved.
- [x] Beta/legacy/new platform API priority is resolved.
- [x] `/v1/audio/transcriptions` deferred status is resolved.
- [x] Initial unsupported Platform groups are resolved.
- [x] Selected `/backend-api/*` compatibility scope is resolved.
- [x] codex-lb cross-check additions are reflected without enabling `/v1/audio/transcriptions` or codex-lb-shaped usage aliases.

## Coverage Scan Results

| Category | Status |
|---|---|
| Functional Scope | Clear |
| Domain & Data | Clear |
| UX Flow | Clear |
| Non-Functional | Clear |
| Edge Cases | Clear |
| Constraints | Clear |

## SDD Gates

- [x] Spec-first gate started.
- [x] PLAN gate unblocked; clarification markers are resolved.
- [x] Test-first path identified: local compatibility tests before implementation behavior.
- [x] Traceability path established from endpoint groups to FRs.

## Notes

Resolved on 2026-04-26:

- OpenAI Administration API is out of scope for 006.
- OpenAI Realtime server-to-server WebSocket is deferred from initial 006. WebRTC/SIP proxying is out of scope.
- Current/common OpenAI Platform APIs are P0; newer product-specific and legacy groups are P1/deferred unless a concrete client workload requires promotion.
- `/v1/audio/transcriptions` is not supported in 006 for either API-key or OAuth accounts.
- Embeddings, Moderations, Images, Audio, Videos, Files, Uploads, Vector Stores, Batches, Fine-tuning, Evals, and OpenAI Realtime WebSocket are deferred from initial 006.
- Selected Codex-native `/backend-api/*` paths are included; arbitrary `/backend-api/*` proxying is out of scope.
- Backend routing logic is captured as a dedicated matrix covering API-key direct forwarding, OAuth Codex adapters/mappings, and unsupported-route outcomes.
- Operation-level coverage is captured for supported P0 method/path operations and for deferred, blocked, and unsupported operation families.
- codex-lb cross-check additions `WS /v1/responses` and OAuth `GET /v1/models` are included; `/v1/audio/transcriptions` remains unsupported in 006.
- Router-local usage observability is modeled as `GET /api/admin/usage` with the Admin API envelope/OpenAPI contract. `GET /api/codex/usage` and `GET /api/codex/usage/` are not supported data-plane endpoints.

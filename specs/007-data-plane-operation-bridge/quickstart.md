# Quickstart: Data-Plane Operation Bridge

**Feature**: 007-data-plane-operation-bridge
**Spec**: `specs/007-data-plane-operation-bridge/spec.md`
**Created**: 2026-04-27
**Status**: Implemented

Smoke validation scenarios after implementation. This is not the complete task matrix; implementation tasks must use the full operation/bridge/credential matrix in `plan.md`.

1. **API-key direct Responses path**
   - Configure an API-key account with a mock upstream.
   - Call `POST /v1/responses`.
   - Verify the request resolves to `op.openai.responses.create` and `bridge.openai.responses.direct`.
   - Verify raw request body, path, query, status, and response body are provider-compatible.

2. **OAuth Responses facade path**
   - Configure an OAuth account with a mock Codex upstream.
   - Call `POST /v1/responses` with `stream:false`.
   - Verify the request resolves to `op.openai.responses.create` and `bridge.openai.responses.to_codex`.
   - Verify upstream uses the Codex responses contract and downstream returns OpenAI Responses JSON.

3. **OAuth Chat Completions facade path**
   - Configure an OAuth account with a mock Codex upstream.
   - Call `POST /v1/chat/completions` with one user message.
   - Verify the request resolves to `op.openai.chat_completions.create` and `bridge.openai.chat_completions.to_codex`.
   - Verify downstream response is Chat Completions-compatible.

4. **Codex-native responses path**
   - Configure an OAuth account with a mock Codex upstream.
   - Call `POST /backend-api/codex/responses`.
   - Verify the request resolves to `op.codex_native.responses.create` and `bridge.codex_native.responses.direct`.
   - Verify downstream response remains Codex-native and is not collected into an OpenAI facade.

5. **Admin usage boundary**
   - Call `GET /api/admin/usage`.
   - Verify the response remains a standard Admin API envelope.
   - Verify no data-plane OpID or BridgeID is assigned to the Admin usage request.

6. **Unsupported route rejection**
   - Call `POST /v1/audio/transcriptions` with an unreadable or oversized body test double.
   - Verify no OpID and no bridge are selected.
   - Verify no account selection, OAuth refresh, or upstream call occurs.

7. **WebSocket bridge path**
   - Connect to `WS /backend-api/codex/responses` through a mock upstream.
   - Verify the request resolves to `op.codex_native.responses.websocket` and `bridge.codex_native.responses.websocket.direct`.
   - Verify frame payloads are relayed but not logged or persisted.

Recommended command sequence:

```bash
go test ./internal/api ./internal/provider/openai ./internal/core
go test ./internal/api ./internal/provider/openai -run TestBridgeSelectionOverhead -bench BridgeSelection -benchtime=100x
make data-plane-compat
go test ./...
go build ./...
```

Optional live smoke remains opt-in:

```bash
make live-upstream-compat
```

Deterministic local verification completed during implementation on 2026-04-27:

- `go test ./internal/provider/openai ./internal/api ./internal/api/adminapi ./internal/store ./internal/core`
- `go test ./internal/api ./internal/provider/openai -run TestBridgeSelectionOverhead -bench BridgeSelection -benchtime=100x` (`BenchmarkBridgeSelection`: 368.8 ns/op, 416 B/op, 7 allocs/op on local Darwin arm64 after metadata construction was included)
- `golangci-lint run`
- `go test ./...`
- `go build ./...`
- `bash scripts/coverage-floor.sh`
- `make go-build && cd frontend && pnpm playwright test tests/e2e/data-plane-compat.spec.ts`
- `make data-plane-compat`

Live upstream smoke was not required for 007 because this feature introduces no new live-only operation behavior.

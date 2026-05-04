# Quickstart: Full OpenAI API Gateway

**Feature**: 006-openai-api-gateway
**Status**: Ready

This quickstart is for implementing and validating the 006 plan. It assumes the repository root is `~/app/project/one-llm-router`.

## 1. Start With Red Tests

Write tests before changing gateway behavior:

```bash
go test ./internal/api ./internal/provider/openai ./internal/core ./internal/app
```

Initial red test targets:

- route classifier supports every operation in the 006 supported matrix;
- `/v1/audio/transcriptions`, embeddings, moderations, files, realtime, and admin paths reject before upstream;
- setup-pending returns native `setup_required` for `/v1/*`, `/backend-api`, and `/backend-api/*`;
- API-key accounts no longer wildcard-forward unsupported `/v1/*`;
- OAuth accounts are eligible only for explicit Codex mappings, including the `GET /v1/models` facade;
- `GET /api/codex/usage` and `GET /api/codex/usage/` are not supported data-plane routes;
- `GET /api/admin/usage` returns enveloped router-local usage observability without upstream calls;
- WebSocket relay tests exist and fail before adding `github.com/coder/websocket@v1.8.14` and implementing relay code.

## 2. Implement Classifier First

Do not add new forwarding behavior until classifier tests are red.

Expected early validation:

```bash
go test ./internal/api -run 'Test.*Gateway.*Classif|Test.*Unsupported|Test.*SetupGate'
```

## 3. Validate API-Key Direct Forwarding

Use local mock upstreams only. Required checks:

- exact path and query preservation;
- inbound client `Authorization` never reaches upstream;
- upstream API key is applied;
- provider JSON/SSE/error responses are not enveloped;
- unsupported routes make zero upstream calls.

```bash
go test ./internal/api ./internal/provider/openai -run 'Test.*APIKey|Test.*Direct|Test.*Unsupported'
```

## 4. Validate OAuth Codex Compatibility

Use a mock ChatGPT Codex backend. Required checks:

- `POST /v1/responses` maps to `/backend-api/codex/responses`;
- `POST /v1/responses/compact` maps to `/backend-api/codex/responses/compact`;
- `/backend-api/codex/*` routes use OAuth bearer and `chatgpt-account-id` when present;
- `POST /backend-api/transcribe` preserves multipart semantics and disables body capture;
- `POST /v1/chat/completions` goes through the adapter, not direct OpenAI Platform forwarding.

```bash
go test ./internal/provider/openai ./internal/api -run 'Test.*OAuth|Test.*Codex|Test.*Transcribe|Test.*ChatCompletion'
```

## 5. Validate WebSocket Relay

Dependency approval is resolved: use `github.com/coder/websocket@v1.8.14`. Start with focused red tests:

```bash
go test ./internal/api ./internal/provider/openai -run 'Test.*WebSocket|Test.*TurnState'
```

Then add the dependency in the WebSocket implementation task and commit `go.mod` and `go.sum` with the relay code:

```bash
go get github.com/coder/websocket@v1.8.14
go mod tidy
```

Required assertions:

- downstream accept includes generated/reused `x-codex-turn-state`;
- upstream receives OAuth credential headers only;
- upstream receives `OpenAI-Beta: responses_websockets=2026-02-06`;
- frames relay both directions;
- close and abort paths record outcomes;
- frame payloads are not logged or persisted.

Do not mark WebSocket scope complete, and do not treat skipped WebSocket tests as passing 006, unless these focused tests pass and log scrub confirms frame payloads are not captured.

## 6. Validate Admin Usage Surface

```bash
go test ./internal/core ./internal/api/adminapi -run 'Test.*Usage'
```

Expected Admin API response:

`GET /api/admin/usage`:

```json
{
  "code": 0,
  "msg": "ok",
  "data": {
    "request_count": 0,
    "total_tokens": 0,
    "cached_input_tokens": 0,
    "total_cost_usd": 0,
    "limits": [],
    "codex": {
      "plan_type": "guest",
      "rate_limit": null,
      "credits": null,
      "additional_rate_limits": []
    }
  }
}
```

## 7. Run Local Compatibility E2E

When backend tests pass, run the Playwright compatibility matrix against local mock upstreams:

```bash
cd frontend
pnpm playwright test tests/e2e/data-plane-compat.spec.ts
```

The matrix must cover:

- API-key Responses JSON and SSE;
- API-key Chat Completions JSON/SSE;
- API-key Models list/retrieve;
- OAuth `GET /v1/models` facade over the Codex model list;
- Codex WebSocket handshake/close behavior;
- `/backend-api/transcribe` multipart forwarding with body capture disabled;
- provider error propagation;
- router unsupported route for `/v1/audio/transcriptions`;
- Admin usage endpoint;
- token-safe request records.

## 8. Run Full Gates

If `openapi/admin.yaml` changed, regenerate clients before tests and builds:

```bash
go generate ./internal/generated/...
cd frontend && pnpm openapi-ts
```

```bash
go test ./...
go build ./...
golangci-lint run
```

If frontend or E2E files changed:

```bash
cd frontend
pnpm biome check .
pnpm tsc --noEmit
pnpm vitest run
pnpm build
pnpm playwright test tests/e2e/data-plane-compat.spec.ts
```

Run log scrub after collecting test output:

```bash
bash scripts/log-scrub.sh test-output/
```

## 9. Optional Live Smoke

Live smoke is opt-in and must not run in default PR CI.

```bash
OPENAI_API_KEY=sk-... \
ROUTER_BASE_URL=http://127.0.0.1:8080 \
bash scripts/run-openai-sdk-compat-smoke.sh
```

Required live smoke scope:

- low-cost non-streaming Responses;
- low-cost streaming Responses;
- low-cost non-streaming Chat Completions;
- low-cost streaming Chat Completions;
- Models list;
- unsupported-route parse check;
- no credential material in logs.

Do not run live smoke without explicit credentials and an intentional local router target.

# Research: Account Model Routing (008)

## Key Findings

1. **Model name is not checked during routing** — confirmed by code review. `classifyGatewayRoute` only checks path+method+WebSocket, not model. A `deepseek-v4-flash-free` request CAN be routed to OAuth accounts.

2. **OAuth accounts always pass `HasCapabilityFor`** — confirmed by domain.go:176 `HasCapabilityFor` always returns `true` for OAuth accounts.

3. **`GET /v1/models` returns the union** — confirmed by proxy_models.go. It calls all eligible accounts' upstream `/v1/models` and merges without filtering.

4. **`DisableCompression: true`** in client.go:53 — cannot change to false; would break SSE streaming.

5. **`copyForwardHeaders`** in forwarder.go copies `Accept-Encoding` from client to upstream — this is the root cause of the garbled content issue (separate fix in progress).

## Error Code Allocation

- Feature 008: `8000–8999` (reserved in error-codes.md)
- Codes: `8001 model_not_supported`, `8002 account_model_duplicate`, `8003 account_model_refresh_failed`, `8900 account_model_internal_error`

## OAuth Model Refresh URL

- API-key accounts: `{base_url}/v1/models` (OpenAI Platform-compatible)
- OAuth accounts: `{CodexBackendBaseURL}/codex/models` (ChatGPT Codex backend)

## Performance

- Batch lookup via `AccountsWithModel()` avoids N+1 pattern
- 2 total queries per routing decision regardless of account count
- `account_models` table is small (indexed integers + short strings)

# Contract: Codex Compatibility Data Plane

**Feature**: 006-openai-api-gateway
**Status**: Ready

This contract covers selected Codex-native data-plane paths outside the Admin API envelope.

## Supported Operations

| Operation | API-Key Account | OAuth Account | Behavior |
|---|---|---|---|
| `POST /backend-api/codex/responses` | Unsupported | Supported | Map to ChatGPT Codex responses upstream; JSON or SSE |
| `WS /backend-api/codex/responses` | Unsupported | Supported | Relay ChatGPT Codex Responses WebSocket |
| `POST /backend-api/codex/responses/compact` | Unsupported | Supported | Map to ChatGPT Codex compact upstream |
| `GET /backend-api/codex/models` | Unsupported | Supported | Map to ChatGPT Codex models upstream |
| `GET /v1/models` | Direct Platform forwarding | Supported | Return OpenAI-compatible model list facade backed by the Codex model list; no Platform upstream call for OAuth |
| `POST /backend-api/transcribe` | Unsupported | Supported | Map multipart audio upload to ChatGPT Codex `/transcribe` |

Arbitrary `/backend-api/*` proxying is excluded. `/api/codex/*` is not a data-plane prefix in 006; router-local usage observability belongs to `GET /api/admin/usage`.

## HTTP Request Normalization

Applies to OAuth-backed Codex upstream calls for `POST /backend-api/codex/responses`, `POST /backend-api/codex/responses/compact`, the OAuth `/v1/responses` and `/v1/responses/compact` mappings, and the OAuth Chat Completions adapter after it has converted the client request into a Responses-shaped payload.

Responses request structure:

- `model`: required non-empty string.
- `input`: required string or array. String input is normalized to one user `input_text` item.
- `instructions`: optional string; omitted/null becomes `""`.
- `tools`, `tool_choice`, `parallel_tool_calls`, `reasoning`, `include`, `service_tier`, `conversation`, `previous_response_id`, `prompt_cache_key`, and `text`: accepted when structurally valid for the local Codex request model.
- `store`: omitted/null/`false` only. `true` rejects before upstream. Upstream payload uses `store:false`.
- `stream`: accepted only as boolean/null. HTTP Responses upstream payload uses `stream:true` because the ChatGPT Codex backend expects streaming transport.

Compatibility aliases are normalized before upstream: `reasoningEffort` -> `reasoning.effort`, `reasoningSummary` -> `reasoning.summary`, `textVerbosity`/top-level `verbosity` -> `text.verbosity`, `promptCacheKey` -> `prompt_cache_key`, and `service_tier:"fast"` -> `service_tier:"priority"`.

Input items are normalized for Codex backend compatibility: interleaved reasoning payloads are removed, assistant text-like content parts become `output_text`, tool-role input items become `function_call_output` with `call_id`, and `input_file.file_id` is rejected. Tool definitions normalize `web_search_preview` to `web_search`, reject unsupported Codex tool types, and use stable ordering before upstream.

The shared Codex upstream normalizer strips fields known to be rejected by the ChatGPT Codex backend: `temperature`, `max_output_tokens`, `prompt_cache_retention`, and `safety_identifier`. This stripping is an explicit transport compatibility rule, not a router-owned success fallback.

Native response routes and OpenAI-compatible facade routes differ only in downstream semantics:

- `POST /backend-api/codex/responses` normalizes the request and forwards to `/codex/responses` without OpenAI facade collection. SSE stays SSE.
- `POST /v1/responses` normalizes the request and may collect upstream SSE into downstream JSON when the client did not request streaming.
- `POST /backend-api/codex/responses/compact` and `POST /v1/responses/compact` use the compact request structure; compact upstream payload strips `store` in addition to the unsupported upstream fields.

## WebSocket Contract

Applies to:

- `WS /v1/responses`
- `WS /backend-api/codex/responses`

Handshake:

- Route is OAuth-only.
- If inbound `x-codex-turn-state` is present and non-empty, reuse it.
- Otherwise generate a new turn state and include it in the downstream accept response as `x-codex-turn-state`.
- Upstream request uses OAuth bearer token and `chatgpt-account-id` when the selected account has it.
- Upstream request uses the fixed Codex CLI user agent already used by existing OAuth HTTP mappings.
- Upstream request MUST include `OpenAI-Beta: responses_websockets=2026-02-06`. If an inbound or internally-built `OpenAI-Beta` header already exists, append this token without dropping existing beta tokens.

Relay:

- Bidirectional frames are relayed without buffering a full conversation.
- Implementation uses `github.com/coder/websocket@v1.8.14`; protocol details stay delegated to the library.
- Ping/pong and close frames are handled by the WebSocket library.
- Client abort closes upstream promptly.
- Upstream close propagates downstream close where possible.
- Frame payloads are never logged or persisted.

Request record:

```json
{
  "method": "WS",
  "path": "/v1/responses",
  "response_mode": "websocket",
  "outcome": "success",
  "error_code": null
}
```

## Multipart Transcribe Contract

Client route: `POST /backend-api/transcribe`

Required request shape:

- `Content-Type: multipart/form-data`
- `file`: required file part
- `prompt`: optional form field

Rules:

- OAuth-only.
- API-key accounts are ineligible and must not be selected.
- The route maps to the ChatGPT Codex transcribe upstream, not OpenAI Platform `/v1/audio/transcriptions`.
- `/v1/audio/transcriptions` remains unsupported for every account type in 006.
- Multipart/audio payload bytes are never captured in request records or logs.
- Request body size is bounded by the configured data-plane body limit.

## Setup-Pending Behavior

When setup is incomplete:

- `/backend-api` and `/backend-api/*` return native HTTP 503 `setup_required`.
- Setup-pending native error `type` is `service_unavailable`, not `router_error`.
- `/api/codex` and `/api/codex/*` are not data-plane prefixes in 006.
- Responses are never admin envelopes.

## Native Router Errors

Representative router-generated errors:

| Status | Code | When |
|---|---|---|
| 400 | `invalid_request` | Malformed JSON, invalid multipart, adapter validation failure |
| 404 | `unsupported_endpoint` | Unlisted `/backend-api/*` route |
| 503 | `no_available_account` | No active eligible OAuth account for an OAuth-only route |
| 503 | `setup_required` | Setup is incomplete; native error `type` is `service_unavailable` |
| 502 | `upstream_connect_failed` | Upstream connection failed |
| 502 | `upstream_response_invalid` | Upstream response could not be safely relayed/adapted |
| 504 | `upstream_timeout` | Upstream response header/connection timeout |

All errors include `X-Request-Id` and the native router error body.

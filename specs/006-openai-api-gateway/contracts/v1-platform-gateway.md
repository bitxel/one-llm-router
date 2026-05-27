# Contract: `/v1/*` OpenAI Platform Gateway

**Feature**: 006-openai-api-gateway
**Status**: Ready

This contract covers client-facing `/v1/*` routes. These routes are data-plane routes, not router-owned Admin APIs, and never use the `{code,msg,data}` envelope.

## Common Rules

- `X-Request-Id` is set as a response header for every router-handled response.
- API-key account routes direct-forward to `account.EffectiveBaseURL() + original path/query`.
- OAuth accounts are eligible only for explicit Codex compatibility rows listed below.
- Hop-by-hop headers, inbound `Authorization`, cookies, admin tokens, CSRF headers, and upstream `Set-Cookie` are stripped according to existing proxy rules.
- Provider 4xx/5xx responses are copied with provider-compatible status, content type, and body.
- Router-generated errors use native data-plane shape:

```json
{
  "error": {
    "type": "router_error",
    "code": "unsupported_endpoint",
    "message": "endpoint is not supported by this router"
  }
}
```

`X-Request-Id` is response-header only and is never duplicated into the body.

## Supported API-Key Operations

| Operation | Behavior |
|---|---|
| `POST /v1/responses` | Direct forward, JSON or SSE according to provider response/request |
| `GET /v1/responses/{response_id}` | Direct forward |
| `DELETE /v1/responses/{response_id}` | Direct forward |
| `POST /v1/responses/{response_id}/cancel` | Direct forward |
| `GET /v1/responses/{response_id}/input_items` | Direct forward with query preservation |
| `POST /v1/responses/input_tokens` | Direct forward |
| `POST /v1/responses/compact` | Direct pass-through; router does not emulate unsupported upstreams |
| `POST /v1/conversations` | Direct forward |
| `GET /v1/conversations/{conversation_id}` | Direct forward |
| `POST /v1/conversations/{conversation_id}` | Direct forward |
| `DELETE /v1/conversations/{conversation_id}` | Direct forward |
| `POST /v1/conversations/{conversation_id}/items` | Direct forward |
| `GET /v1/conversations/{conversation_id}/items` | Direct forward with query preservation |
| `GET /v1/conversations/{conversation_id}/items/{item_id}` | Direct forward |
| `DELETE /v1/conversations/{conversation_id}/items/{item_id}` | Direct forward |
| `POST /v1/chat/completions` | Direct forward, JSON or SSE |
| `GET /v1/chat/completions` | Direct forward |
| `GET /v1/chat/completions/{completion_id}` | Direct forward |
| `POST /v1/chat/completions/{completion_id}` | Direct forward |
| `DELETE /v1/chat/completions/{completion_id}` | Direct forward |
| `GET /v1/chat/completions/{completion_id}/messages` | Direct forward |
| `GET /v1/models` | Contribute real model-list data to the strict union response |
| `GET /v1/models/{model}` | Direct forward |

## Supported OAuth Operations

| Operation | Behavior |
|---|---|
| `POST /v1/responses` | Map to ChatGPT Codex `/backend-api/codex/responses`; preserve JSON/SSE downstream semantics |
| `WS /v1/responses` | Map to ChatGPT Codex Responses WebSocket; WebSocket relay with `x-codex-turn-state` compatibility |
| `POST /v1/responses/compact` | Map to ChatGPT Codex `/backend-api/codex/responses/compact` |
| `POST /v1/chat/completions` | Convert Chat Completions request to Codex Responses request and reconstruct Chat Completions-compatible JSON/SSE |
| `GET /v1/models` | Contribute a codex-lb-compatible OpenAI model list facade backed by the Codex model list to the strict union response; no OpenAI Platform upstream call |

All other `/v1/*` operations are unsupported for OAuth accounts in 006, including `GET /v1/models/{model}`.

## `GET /v1/models` Union And OAuth Facade

`GET /v1/models` is a router-level discovery operation over every active route-eligible upstream account. It returns one OpenAI-compatible list response whose `data` is deduplicated by model `id`.

API-key accounts contribute their real OpenAI Platform-compatible `GET /v1/models` response. OAuth accounts use the facade rules below and contribute the adapted Codex model list. The operation is strict fail-fast: if any eligible account cannot prepare credentials, connect, or return a valid successful model list, the whole request fails with native data-plane error semantics rather than silently returning a partial list.

OAuth facade rules:

Rules:

- The response uses OpenAI-compatible model list shape.
- The model source is the same Codex model inventory used by `GET /backend-api/codex/models`.
- The route does not contact OpenAI Platform `/v1/models` with a ChatGPT OAuth token.
- `GET /v1/models/{model}` remains unsupported for OAuth accounts in 006.
- Unknown model metadata is omitted or set only from real local/Codex model metadata; implementation must not fabricate provider ownership, capabilities, or timestamps.
- Duplicate model ids are represented once; the first account in stable active-account order wins unless a future spec defines conflict merging.

## OAuth Chat Completions Adapter

Applies only to OAuth accounts on `POST /v1/chat/completions`. API-key accounts direct-forward the original request and do not use this adapter.

Request validation is strict. The adapter must reject unsupported or unknown client intent with HTTP 400 native router error `invalid_request`; it must not silently drop fields. After successful Chat Completions-to-Responses conversion, the shared Codex upstream normalizer documented in `contracts/codex-compatibility.md` applies before the ChatGPT Codex backend call, including explicit stripping of Codex-rejected upstream fields.

| Chat Completions Field | OAuth Adapter Behavior |
|---|---|
| `model` | Required; maps to Codex Responses `model`. |
| `messages` | Required non-empty array. `system` and `developer` messages become Responses instructions; `user`, `assistant`, and `tool` messages become Responses input items while preserving tool-call references when present. |
| `stream` | Controls downstream response mode. Upstream may be forced to streaming when required by the Codex backend, but downstream still honors the client's `stream` value. |
| `stream_options.include_usage` | When `stream=true`, emits a terminal Chat Completions usage chunk if upstream Responses usage is available. |
| `tools`, `tool_choice`, `parallel_tool_calls` | Supported for function tools and Codex-supported tool types only; unsupported tool types or malformed tool choices reject before upstream. |
| `temperature`, `top_p`, `stop`, `presence_penalty`, `frequency_penalty`, `seed`, `service_tier` | Mapped to equivalent Responses request fields only when the local Responses request schema accepts them; schema rejection becomes HTTP 400 `invalid_request` before upstream. `temperature` is stripped by the shared Codex upstream normalizer because the ChatGPT Codex backend rejects it. |
| `max_tokens`, `max_completion_tokens` | Validate and map client token-limit intent; if both are set to conflicting values, reject before upstream. The resulting `max_output_tokens` is stripped by the shared Codex upstream normalizer because the ChatGPT Codex backend rejects it. |
| `response_format` | `text`, `json_object`, and `json_schema` map to Responses `text.format`; unsupported formats reject before upstream. |
| `store` | Only omitted, `null`, or `false` is accepted from the client. `true` rejects before upstream. The OAuth adapter forces Codex Responses upstream `store:false` because ChatGPT Codex backend requires an explicit non-stored request even when the Chat Completions client omitted the field. |
| `n` | Only omitted, `null`, or `1` is accepted. Values greater than `1` reject before upstream. |
| `logprobs`, `top_logprobs`, `audio`, `modalities`, `prediction`, legacy `function_call`, unknown top-level fields | Unsupported in 006 OAuth adapter; reject before upstream. |

Response mapping:

- Non-streaming output returns `object: "chat.completion"` with one choice.
- Streaming output returns `object: "chat.completion.chunk"` SSE data and terminates with `data: [DONE]`.
- The OAuth adapter must treat upstream Codex Responses SSE by body shape as SSE even if the upstream `Content-Type` is mislabelled as JSON; downstream `stream=true` clients still receive `text/event-stream`.
- `response.output_text.delta` maps to Chat `message.content` or stream `delta.content`.
- Responses refusal deltas map to Chat `refusal` fields.
- Responses tool-call deltas map to Chat `tool_calls` deltas and final assistant tool calls.
- Finish reason maps to `stop`, `tool_calls`, `length`, or `content_filter` when derivable from the Responses terminal event.
- Responses usage maps to Chat usage fields: `input_tokens` -> `prompt_tokens`, `output_tokens` -> `completion_tokens`, `total_tokens` -> `total_tokens`, cached input tokens -> `prompt_tokens_details.cached_tokens`, reasoning output tokens -> `completion_tokens_details.reasoning_tokens`.
- Use the upstream response id when available. If an upstream stream omits an id required by Chat Completions shape, generate a router-prefixed protocol id and record that it was adapter-generated.
- Upstream provider errors, `response.failed`, malformed terminal events, and adapter serialization failures must not be converted into successful Chat Completions objects.

## Setup-Pending Behavior

When setup is incomplete, `/v1/*` returns HTTP 503 with native error `code: "setup_required"` and `type: "service_unavailable"`. It is never enveloped.

## Unsupported and Blocked `/v1/*`

Representative unsupported/deferred examples:

- `POST /v1/audio/transcriptions`
- `POST /v1/audio/*`
- `POST /v1/embeddings`
- `POST /v1/moderations`
- `/v1/files*`, `/v1/uploads*`, `/v1/vector_stores*`, `/v1/batches*`
- `/v1/fine_tuning*`, `/v1/evals*`
- `/v1/realtime*`

Representative blocked examples:

- `/v1/organization*`
- `/v1/projects/{project_id}/*`

Required behavior:

- no upstream account selection;
- no upstream HTTP/WebSocket attempt;
- no business request-body read or body capture before rejection;
- native data-plane error, not admin envelope;
- request record with null `upstream_account_id`, `outcome=router_error`, and `error_code=unsupported_endpoint` or `blocked_endpoint`.

Router-local usage observability is exposed by the router-owned Admin API `GET /api/admin/usage`.

# Contract: Unsupported, Deferred, and Blocked Data-Plane Routes

**Feature**: 006-openai-api-gateway
**Status**: Ready

Feature 006 is explicit-allowlist based. Any data-plane route outside the supported operation matrix fails before account selection and before upstream contact.

## Error Shape

Router-generated data-plane errors use this native shape:

```json
{
  "error": {
    "type": "router_error",
    "code": "unsupported_endpoint",
    "message": "endpoint is not supported by this router"
  }
}
```

During setup-pending only, the same native shape uses `type: "service_unavailable"` with `code: "setup_required"` and HTTP 503, preserving the 001 MVP setup-required contract.

`X-Request-Id` is set as a response header only and is never duplicated into the body. The response is never `{code,msg,data}`.

## Required No-Upstream Behavior

For every unsupported/deferred/blocked route:

- route classifier returns unsupported or blocked before account selection;
- selector is not called;
- OAuth refresh is not called;
- upstream HTTP/WebSocket client is not called;
- no business request-body read or body capture occurs; classifier must run before JSON decoding, multipart parsing, request recording body capture, or upstream request construction;
- server-level connection/body protection may still terminate pathological requests before routing, but implementation must not read deferred binary/multipart payloads just to decide the route is unsupported;
- request record is written with `upstream_account_id = null`.

## Deferred Families

| Family | Representative Paths | Required Code |
|---|---|---|
| Embeddings | `POST /v1/embeddings` | `unsupported_endpoint` |
| Moderations | `POST /v1/moderations` | `unsupported_endpoint` |
| Images | `/v1/images/generations`, `/v1/images/edits`, `/v1/images/variations` | `unsupported_endpoint` |
| Audio | `/v1/audio/transcriptions`, `/v1/audio/translations`, `/v1/audio/speech`, `/v1/audio/voices`, `/v1/audio/voice_consents*` | `unsupported_endpoint` |
| Videos | `/v1/videos*` | `unsupported_endpoint` |
| Files | `/v1/files*` | `unsupported_endpoint` |
| Uploads | `/v1/uploads*` | `unsupported_endpoint` |
| Vector Stores | `/v1/vector_stores*` | `unsupported_endpoint` |
| Batches | `/v1/batches*` | `unsupported_endpoint` |
| Fine-tuning | `/v1/fine_tuning*` | `unsupported_endpoint` |
| Evals | `/v1/evals*` | `unsupported_endpoint` |
| Realtime | `/v1/realtime*` | `unsupported_endpoint` |
| Legacy Assistants/Threads/Completions | `/v1/assistants*`, `/v1/threads*`, `/v1/completions` | `unsupported_endpoint` |
| Newer resource APIs | `/v1/chatkit*`, `/v1/containers*`, `/v1/skills*` | `unsupported_endpoint` |
| Mutating Models | `DELETE /v1/models/{model}` | `unsupported_endpoint` |

`POST /v1/audio/transcriptions` is explicitly deferred even though codex-lb implements it. The only transcription-related 006 route is OAuth-only `POST /backend-api/transcribe`.

## Blocked Administration Families

| Family | Representative Paths | Required Code |
|---|---|---|
| OpenAI organization admin | `/v1/organization*` | `blocked_endpoint` |
| OpenAI project admin | `/v1/projects/{project_id}/*` | `blocked_endpoint` |

Blocked routes require a separate future spec with admin-key credential class, audit, and permission model. Ordinary upstream API-key accounts must not be used as OpenAI administration credentials.

## Arbitrary Backend/API Proxying

| Route | Required Behavior |
|---|---|
| Unlisted `/backend-api` or `/backend-api/*` | Native `unsupported_endpoint`; no ChatGPT upstream contact |
| `/api/codex` or `/api/codex/*` | Not a 006 data-plane prefix; must not be treated as ChatGPT backend proxying |
| Arbitrary `/api/*` outside `/api/admin/*` and `/api/setup/*` | Must not be treated as data-plane proxy |

## Status Policy

| Status | Code | Meaning |
|---|---|---|
| 404 | `unsupported_endpoint` | Route is not in 006 supported set or is deferred |
| 403 | `blocked_endpoint` | OpenAI administration surface is intentionally blocked |
| 503 | `setup_required` | Setup is incomplete for a data-plane prefix; native error `type` is `service_unavailable` |

These statuses are normative for router-generated pre-upstream failures. The response must also preserve the invariant that it uses the native data-plane shape and makes no upstream attempt.

Setup-pending precedence is deterministic: before setup completes, the setup gate handles `/v1/*`, `/backend-api`, and `/backend-api/*` before the unsupported-route classifier and returns HTTP 503 `setup_required` with native error `type: "service_unavailable"`. After setup completes, the classifier owns unsupported and blocked route outcomes. Router-local usage observability is served by the Admin API `GET /api/admin/usage` and follows the Admin API envelope/status policy instead of this native data-plane error contract.

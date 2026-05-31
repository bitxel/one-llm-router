# Feature Spec: Full OpenAI API Gateway

**ID**: 006-openai-api-gateway
**Created**: 2026-04-26
**Updated**: 2026-04-27
**Status**: Ready

## Overview

Platform operators and internal client developers need one managed endpoint that can act as an OpenAI Platform-compatible gateway, not only a Codex Responses proxy. Feature 006 expands the client-facing data plane so OpenAI-compatible SDKs, HTTP clients, and Codex-native clients can point at the router and use supported OpenAI Platform and ChatGPT Codex backend surfaces through managed upstream accounts.

The feature preserves the control-plane boundary: `/api/admin/*` and `/api/setup/*` remain router-owned enveloped APIs, while `/v1/*` and selected `/backend-api/*` data-plane paths remain native data-plane surfaces and never receive the admin envelope. Router-local usage observability is an admin concern, not an OpenAI-compatible data-plane contract: Feature 006 exposes it through an enveloped `/api/admin/usage` operation and does not support codex-lb-shaped `GET /api/codex/usage` or `GET /api/codex/usage/` client paths. OpenAI Platform gateway compatibility is defined for API-key accounts according to the coverage matrix, with `/v1/audio/transcriptions` explicitly deferred from 006. ChatGPT OAuth accounts support explicit Codex-backed compatibility mappings for `/v1/responses`, `WS /v1/responses`, `/v1/responses/compact`, `/v1/chat/completions`, OAuth `GET /v1/models` facade, and selected Codex-native `/backend-api/*` paths; they are not treated as general OpenAI Platform credentials for arbitrary `/v1/*`, `/backend-api/*`, `/api/*`, or OpenAI Administration API paths.

## User Scenarios

### US-1: Use One Router Base URL for Core OpenAI Model APIs (P0)

As an internal client developer, I want OpenAI-compatible clients to use the router as their OpenAI base URL for model APIs, so that I do not need a different endpoint for each supported OpenAI feature.

**Acceptance Scenarios**:
1. Given an API-key upstream account is active, when a client calls supported Responses, Conversations, Chat Completions, or Models endpoints through `/v1/*`, then the client receives the provider-compatible status, headers, and response body.
2. Given a client uses a first-party OpenAI SDK against the router base URL, when it sends non-streaming calls for P0 endpoint groups, then the SDK parses successful and error responses without router-specific adapters.
3. Given no API-key account is eligible for a requested full-gateway path that has no explicit OAuth compatibility mapping, then the router returns a native data-plane router error and does not silently retry through a ChatGPT OAuth account.
4. Given a ChatGPT OAuth account is eligible and a client calls `/v1/chat/completions`, then the router handles the request through an explicit Codex-backed compatibility adapter and returns a Chat Completions-compatible response.
5. Given a Codex-native client calls a supported `/backend-api/*` data-plane endpoint, then the router handles the request through an explicit compatibility path and does not expose arbitrary ChatGPT backend paths.

**Edge Cases**:
- A path already supported for OAuth Responses, OAuth Chat Completions, or selected Codex-native `/backend-api/*` compatibility remains routed by the explicit account-specific compatibility mapping.
- Unsupported or unrecognized `/v1/*` and `/backend-api/*` paths fail visibly instead of being wrapped in the admin envelope; router-owned usage reads are served only by the Admin API.

### US-2: Preserve OpenAI Wire Semantics Across HTTP Shapes (P0)

As an internal client developer, I want the gateway to preserve OpenAI request and response semantics, so that existing SDKs, retry logic, pagination, and error handling keep working.

**Acceptance Scenarios**:
1. Given a JSON request, when it is routed through the gateway, then method, path, query string, JSON body, content type, and provider response shape are preserved except for credential substitution.
2. Given the in-scope multipart transcribe route is routed through the gateway, then uploaded audio bytes are forwarded without JSON decoding or request-record body capture and the downstream client receives the provider-compatible result.
3. Given a provider returns 4xx or 5xx, then the client receives the provider status and body, not an admin envelope.
4. Given the router itself cannot connect, times out, or rejects an unsupported route, then the client receives a native data-plane router error with `X-Request-Id`.

**Edge Cases**:
- Empty, large, or streaming request bodies are handled without assuming JSON.
- Provider-specific response headers such as request id and rate-limit headers are preserved when safe to forward.

### US-3: Support Streaming and Defer OpenAI Realtime Gateway Traffic (P0)

As an internal client developer, I want streamed model output to work through the router and unsupported OpenAI Realtime traffic to fail clearly, so that latency-sensitive clients do not bypass the managed endpoint by accident.

**Acceptance Scenarios**:
1. Given a client requests SSE streaming for supported Responses or Chat Completions endpoints, then the router forwards incremental events without buffering the full response first.
2. Given a streaming response emits usage or terminal metadata, then the router records observable metrics when extractable without changing the downstream stream.
3. Given a server-to-server OpenAI Realtime WebSocket client connects through the router, then Feature 006 rejects it as deferred rather than silently attempting an unsupported upstream relay.
4. Given a client attempts to use ChatGPT OAuth credentials for Realtime traffic, then the router rejects the request as no eligible route rather than forwarding OAuth tokens to the OpenAI Platform API.
5. Given a client needs Realtime WebRTC or SIP proxying, then Feature 006 treats that transport as out of scope and does not advertise it as supported.

**Edge Cases**:
- Aborted client connections close upstream streams or sockets promptly.
- Malformed upstream SSE or WebSocket close frames are recorded as observable router outcomes without fabricating provider success.

### US-4: Defer File, Upload, Batch, and Long-Running Resource APIs (P1)

As a platform operator, I want deferred OpenAI resource APIs such as files, uploads, vector stores, batches, fine-tuning, and evals to be explicit in the coverage matrix, so that clients know these workflows are not part of the initial 006 support set.

**Acceptance Scenarios**:
1. Given a client calls Files, Uploads, Vector Stores, Batches, Fine-tuning, or Evals in 006, then the router returns a native data-plane unsupported-route outcome.
2. Given a client calls Images, Audio, Videos, Embeddings, Moderations, or OpenAI Realtime in 006, then the router returns a native data-plane unsupported-route outcome.
3. Given a future spec promotes any deferred endpoint group, then it must define transport behavior, logging policy, and compatibility tests before implementation.
4. Given body logging is disabled, then existing supported data-plane paths still avoid storing binary payloads or generated media bytes.

**Edge Cases**:
- Multipart boundaries are preserved.
- Downloaded file or generated media content can be binary and must not be JSON-decoded.
- Batch files may contain nested API requests and must not be rewritten by the router.

### US-5: Keep Operator Observability and Safety Across the Expanded Data Plane (P0)

As a platform operator, I want every routed OpenAI API call to remain observable and safe, so that broad gateway support does not create blind spots or leak credentials.

**Acceptance Scenarios**:
1. Given any supported `/v1/*` or selected `/backend-api/*` data-plane request is routed, then a request record is created with method, path, router-observed client IP, selected account, status, latency, outcome, response mode, and extractable model/usage fields where available.
2. Given request or response bodies contain credentials, bearer tokens, file bytes, audio bytes, or other binary payloads, then logs and request records redact or omit them according to explicit body-capture policy.
3. Given an upstream returns rate-limit headers, then the client can still inspect those headers when the provider exposes them.
4. Given the gateway cannot extract model or token usage from a response, then it records absence explicitly and does not invent usage.
5. Given an operator requests router-local usage observability, then the data is returned through `/api/admin/usage` using the router-owned Admin API envelope and not through a provider-compatible data-plane route.
6. Given an operator configures a `runtime.model_renames` rule, then matching `/v1/*` JSON requests rewrite the exact top-level `model` value before upstream forwarding, while request history preserves the original client model and records the applied rename in router metadata.

**Edge Cases**:
- Non-model resource APIs may not have model or token usage.
- Streaming and WebSocket sessions may have partial observability when the connection closes before terminal metadata.
- In deployments behind a reverse proxy, the recorded client IP is the router's direct TCP peer until a trusted-proxy configuration explicitly allows forwarded headers.

### US-6: Prove Compatibility with Deterministic and Live Tests (P0)

As a protocol adapter maintainer, I want a compatibility test matrix for the expanded OpenAI gateway, so that route additions do not silently regress SDK behavior.

**Acceptance Scenarios**:
1. Given local CI runs, then deterministic mock-upstream compatibility tests cover representative JSON, SSE, Codex WebSocket, Codex transcribe multipart, provider-error, and router-error cases without real provider credentials.
2. Given live smoke is explicitly enabled with real provider credentials, then a small low-cost matrix verifies the highest-risk endpoint groups through real upstream behavior.
3. Given first-party SDK smoke is explicitly enabled, then SDK clients can call the router for representative non-streaming, streaming, models, and unsupported-route cases without custom SDK patches.
4. Given live credentials are absent, then live compatibility tests skip or fail fast according to the documented command contract and do not make network calls.

**Edge Cases**:
- Provider/model capability differences produce clearly reported optional skips only for cases marked optional.
- CI logs are scrubbed for API keys and token-like material.

### US-7: Bound High-Risk and Administrative Surfaces (P1)

As a platform operator, I want high-risk OpenAI administration surfaces to be explicitly governed, so that the gateway does not accidentally allow organization or project mutation through a general client endpoint.

**Acceptance Scenarios**:
1. Given a client calls an OpenAI Administration API endpoint through the router, then Feature 006 rejects it with a native data-plane blocked-route outcome and does not forward it upstream.
2. Given an endpoint is out of scope for 006, then clients receive a native router `unsupported_endpoint` or `blocked_endpoint` style data-plane error rather than an upstream attempt with the wrong credential class.
3. Given an operator later enables an administrative surface, then that scope has its own audit and permission requirements before traffic is allowed.

**Edge Cases**:
- The router's own Admin API must not be confused with OpenAI's Administration API.
- OpenAI admin-key credentials must never be stored in an ordinary upstream API-key account without an explicit future spec.

## Functional Requirements

- **FR-001**: The gateway MUST keep `/v1/*` and selected `/backend-api/*` data-plane paths outside the router-owned admin envelope, and MUST expose router-local usage observability only through router-owned `/api/admin/*` envelope APIs.
- **FR-002**: The gateway MUST support API-key account routing only for OpenAI Platform REST operations marked supported in the initial operation-level coverage matrix.
- **FR-003**: ChatGPT OAuth accounts MUST remain eligible only for operations marked supported for OAuth in the Operation-Level Coverage Matrix; every other `/v1/*`, `/backend-api/*`, or `/api/*` path is ineligible for OAuth data-plane routing.
- **FR-004**: Provider-compatible upstream responses on `/v1/*` and selected `/backend-api/*` data-plane paths MUST preserve the documented HTTP status, body shape, and content type.
- **FR-005**: Router-generated data-plane errors MUST use the native `/v1/*` or `/backend-api/*` data-plane router error shape, not the admin envelope.
- **FR-006**: The gateway MUST support JSON bodies, the in-scope multipart `/backend-api/transcribe` upload, SSE streams, and WebSocket upgrade traffic for in-scope endpoints.
- **FR-007**: The gateway MUST avoid full in-memory buffering for in-scope multipart uploads and long-lived SSE/WebSocket streams.
- **FR-008**: The gateway MUST substitute only the upstream credential and required routing headers; client-visible provider parameters MUST NOT be rewritten unless a spec explicitly defines a compatibility adaptation, such as OAuth Chat Completions or backend-api compatibility over Codex transports.
- **FR-009**: Request logging MUST run for every routed `/v1/*` and selected `/backend-api/*` data-plane request, including non-JSON, streaming, and WebSocket traffic.
- **FR-010**: Body capture MUST be disabled, redacted, or bounded for binary, multipart, audio, image, video, upload, and file-content traffic.
- **FR-011**: Compatibility tests MUST include local mock-upstream coverage before implementation behavior is considered complete.
- **FR-012**: Live compatibility tests MUST remain opt-in and MUST NOT require secrets in default PR CI.
- **FR-013**: The gateway MUST document exact method/path operations for the initial supported 006 route set and deterministic classifier rules for deferred, legacy, beta, blocked, and otherwise unsupported endpoint families.
- **FR-014**: The gateway MUST prioritize current, common, stable OpenAI Platform client APIs as P0; newer product-specific APIs and legacy APIs MUST be P1/deferred unless a concrete client workload requires them before implementation starts.
- **FR-015**: The gateway MUST support only explicitly listed Codex-native `/backend-api/*` compatibility paths and MUST reject arbitrary `/backend-api/*` proxying. `/api/codex/*` MUST NOT be treated as data-plane proxy space in 006.
- **FR-016**: Router-local usage observability MUST be available through an Admin API operation documented in `openapi/admin.yaml`, regenerated into Go and TypeScript clients, wrapped as `{ "code": 0, "msg": "ok", "data": ... }` on success, and covered by Admin API contract tests.
- **FR-017**: Runtime model rename rules MUST be exact, case-sensitive mappings configured through the Admin Settings API. They apply only to supported `/v1/*` JSON request bodies with a top-level string `model`, never to WebSocket, multipart, selected `/backend-api/*`, model-list, or unsupported routes. The router MUST record applied renames in `request_records.router_metadata` and MUST NOT silently rewrite any other request field.

## Endpoint Coverage Target

| Priority | Endpoint Group | Coverage Intent |
|---|---|---|
| P0 | Responses, Conversations, streaming events | Initial API-key Platform-compatible core, including JSON and SSE where documented; Codex/OAuth compatibility also includes `WS /v1/responses`. |
| P0 | Chat Completions | JSON and SSE, including stored completion operations where provider supports them. |
| P0 | Models | `GET /v1/models` returns a strict fail-fast union of every active route-eligible API-key Platform model list and OAuth Codex model-list facade; API-key `GET /v1/models/{model}` remains direct retrieve with provider errors; OAuth retrieve remains unsupported. |
| P0 | Codex-native compatibility | Explicit ChatGPT Codex mappings for `/backend-api/codex/responses`, `WS /backend-api/codex/responses`, `/backend-api/codex/responses/compact`, `/backend-api/codex/models`, `/backend-api/transcribe`, and OAuth `GET /v1/models` facade; no arbitrary backend-api or api proxy. |
| P0 | Router admin usage observability | Router-local usage and plan/status summaries exposed through `/api/admin/usage` with the Admin API envelope and OpenAPI-generated clients; no data-plane usage alias. |
| P1 | Webhooks events | Newer platform event API coverage after core P0 gateway transport is stable; does not mean hosting customer webhook receivers. |
| P1 | ChatKit, Containers, Skills | Newer platform resource APIs after P0 gateway transport is stable. |
| P1 | Legacy Realtime Beta, Assistants, and Completions | Compatibility only if needed by supported SDK/client workloads. |
| Blocked | Administration API | Explicitly excluded from 006; requires a separate admin-key credential, audit, and permission model if added later. |
| Deferred | Embeddings, Moderations | Not supported in initial 006 by user decision; future support requires explicit tests. |
| Deferred | Images, Audio, Videos | Not supported in initial 006, including `/v1/audio/transcriptions`; future support requires explicit media/binary logging policy and tests. |
| Deferred | Files, Uploads | Not supported in initial 006; future support requires upload/download streaming, body-capture, and storage-safety tests. |
| Deferred | Vector Stores, Batches, Fine-tuning, Evals | Not supported in initial 006; future support requires resource lifecycle and pagination tests. |
| Deferred | OpenAI Realtime WebSocket | Not supported in initial 006; future support requires bidirectional relay and abort tests. |
| Out of Scope | Realtime WebRTC/SIP proxying | Not a normal HTTP reverse proxy target; may be direct-to-provider or future work. |

## Operation-Level Coverage Matrix

The OpenAI Platform operation inventory was checked against the official OpenAI API reference/OpenAPI feed on 2026-04-26. The matrix below is the 006 source of truth at method/path granularity for the initial route set. "Supported" means the router must route the client-facing operation in 006. "Deferred", "Blocked", and "Unsupported" mean the router must fail before any upstream attempt with native data-plane unsupported-route behavior, never the admin envelope.

### Initial Supported Operations

| Priority | Client Operation | API-Key Account Behavior | OAuth Codex Account Behavior | Transport / Response Mode | Required 006 Tests |
|---|---|---|---|---|---|
| P0 | `POST /v1/responses` | Supported; direct forward to `account.base_url + original path/query`. | Supported; map to ChatGPT Codex `/backend-api/codex/responses`. | JSON or SSE according to request; provider-compatible status/body/content type. | JSON success, SSE relay, provider error, client abort, request log. |
| P0 | `WS /v1/responses` | Not supported for API-key upstream accounts in 006. | Supported; map to the ChatGPT Codex Responses WebSocket transport, equivalent to `WS /backend-api/codex/responses` with OpenAI-compatible client pathing. | WebSocket bidirectional relay with codex-lb-compatible turn-state handling. | Connect/relay success, generated/reused turn state, upstream close, client abort, token-safe logging. |
| P0 | `GET /v1/responses/{response_id}` | Supported; direct forward. | Not supported in 006. | JSON. | Retrieve success, provider not-found/error, query/header preservation. |
| P0 | `DELETE /v1/responses/{response_id}` | Supported; direct forward. | Not supported in 006. | Provider-compatible delete response. | Delete success, provider error, no synthetic success. |
| P0 | `POST /v1/responses/{response_id}/cancel` | Supported; direct forward. | Not supported in 006. | JSON. | Cancel success, terminal/provider error propagation. |
| P0 | `GET /v1/responses/{response_id}/input_items` | Supported; direct forward. | Not supported in 006. | JSON list with query preservation. | Pagination/query preservation, provider error. |
| P0 | `POST /v1/responses/input_tokens` | Supported; direct forward. | Not supported in 006. | JSON. | Token-count request forwarding, provider validation error. |
| P0 | `POST /v1/responses/compact` | Supported as direct pass-through when the configured API-key upstream supports the path; no router emulation. | Supported; map to ChatGPT Codex `/backend-api/codex/responses/compact`. | JSON. | API-key pass-through, OAuth compact mapping, unsupported upstream/provider error. |
| P0 | `POST /v1/conversations` | Supported; direct forward. | Not supported in 006. | JSON. | Create success, provider validation error, request log. |
| P0 | `GET /v1/conversations/{conversation_id}` | Supported; direct forward. | Not supported in 006. | JSON. | Retrieve success, provider not-found/error. |
| P0 | `POST /v1/conversations/{conversation_id}` | Supported; direct forward. | Not supported in 006. | JSON. | Update success, provider validation error. |
| P0 | `DELETE /v1/conversations/{conversation_id}` | Supported; direct forward. | Not supported in 006. | Provider-compatible delete response. | Delete success, provider error. |
| P0 | `POST /v1/conversations/{conversation_id}/items` | Supported; direct forward. | Not supported in 006. | JSON. | Item create success, provider validation error. |
| P0 | `GET /v1/conversations/{conversation_id}/items` | Supported; direct forward. | Not supported in 006. | JSON list with query preservation. | Pagination/query preservation, provider error. |
| P0 | `GET /v1/conversations/{conversation_id}/items/{item_id}` | Supported; direct forward. | Not supported in 006. | JSON. | Item retrieve success, provider not-found/error. |
| P0 | `DELETE /v1/conversations/{conversation_id}/items/{item_id}` | Supported; direct forward. | Not supported in 006. | Provider-compatible delete response. | Item delete success, provider error. |
| P0 | `POST /v1/chat/completions` | Supported; direct forward to `account.base_url + original path/query`. | Supported through the Chat Completions-to-Codex-Responses compatibility adapter. | JSON or SSE according to request. | Non-stream success, SSE relay, adapter unsupported-field rejection, provider error, client abort. |
| P0 | `GET /v1/chat/completions` | Supported; direct forward. | Not supported in 006. | JSON list with query preservation. | Stored completion list success, pagination/query preservation, provider error. |
| P0 | `GET /v1/chat/completions/{completion_id}` | Supported; direct forward. | Not supported in 006. | JSON. | Stored completion retrieve success, provider not-found/error. |
| P0 | `POST /v1/chat/completions/{completion_id}` | Supported; direct forward. | Not supported in 006. | JSON. | Stored completion update success, provider validation error. |
| P0 | `DELETE /v1/chat/completions/{completion_id}` | Supported; direct forward. | Not supported in 006. | Provider-compatible delete response. | Stored completion delete success, provider error. |
| P0 | `GET /v1/chat/completions/{completion_id}/messages` | Supported; direct forward. | Not supported in 006. | JSON list with query preservation. | Stored completion messages list success, pagination/query preservation. |
| P0 | `GET /v1/models` | Contributes each active API-key account's real `account.base_url + /v1/models` response to a strict union. | Contributes each active OAuth account's Codex model list from the same source as `GET /backend-api/codex/models`, adapted to OpenAI-compatible list shape. | JSON; one client-facing response with `object:"list"` and deduped `data`. Any eligible account failure fails the whole request; no silent partial success. | API-key+OAuth union success, duplicate model de-duplication, provider/Codex mapping error, invalid upstream model list, no eligible account. |
| P0 | `GET /v1/models/{model}` | Supported; direct forward. | Not supported in 006. | JSON. | Model retrieve success, provider not-found/error. |
| P0 | `POST /backend-api/codex/responses` | Not supported for API-key accounts. | Supported; map to ChatGPT Codex responses upstream. | JSON or SSE according to Codex upstream response. | Codex-native JSON/SSE success, provider error, request log. |
| P0 | `WS /backend-api/codex/responses` | Not supported for API-key accounts. | Supported; establish and relay the Codex Responses WebSocket. | WebSocket bidirectional relay. | Connect/relay success, upstream close, client abort, token-safe logging. |
| P0 | `POST /backend-api/codex/responses/compact` | Not supported for API-key accounts. | Supported; map to ChatGPT Codex compact upstream. | JSON. | Compact success, provider error. |
| P0 | `GET /backend-api/codex/models` | Not supported for API-key accounts. | Supported; map to ChatGPT Codex models upstream. | JSON. | Model list success, provider error. |
| P0 | `POST /backend-api/transcribe` | Not supported for API-key accounts. | Supported; map to ChatGPT Codex `/transcribe` upstream for Codex-native clients. | Multipart request upload with bounded forwarding; provider-compatible response recorded as `response_mode=json` when JSON. | Multipart forwarding, body-capture disabled/redacted, provider error, request body limit. |

### Deferred, Blocked, and Unsupported Operation Families

| Status | Client Operation Family | API-Key Account Behavior | OAuth Codex Account Behavior | Required 006 Behavior |
|---|---|---|---|---|
| P1 Future | Webhooks event APIs if concrete provider method/path operations are added to the inventory | Deferred. | Not supported. | Reject in 006; future promotion must add exact operation rows and event tests. |
| P1 Future | `/v1/chatkit/sessions`, `/v1/chatkit/sessions/{session_id}/cancel`, `/v1/chatkit/threads`, `/v1/chatkit/threads/{thread_id}`, `/v1/chatkit/threads/{thread_id}/items` | Deferred. | Not supported. | Reject with native data-plane unsupported-route behavior. |
| P1 Future | `/v1/containers`, `/v1/containers/{container_id}`, `/v1/containers/{container_id}/files`, `/v1/containers/{container_id}/files/{file_id}`, `/v1/containers/{container_id}/files/{file_id}/content` | Deferred. | Not supported. | Reject; future support requires file-content streaming and logging tests. |
| P1 Future | `/v1/skills`, `/v1/skills/{skill_id}`, `/v1/skills/{skill_id}/content`, `/v1/skills/{skill_id}/versions`, `/v1/skills/{skill_id}/versions/{version}`, `/v1/skills/{skill_id}/versions/{version}/content` | Deferred. | Not supported. | Reject; future support requires binary/content logging policy. |
| P1 Future | Legacy `/v1/completions`, Assistants `/v1/assistants*`, and Threads/Runs `/v1/threads*` | Deferred. | Not supported. | Reject unless a future supported SDK/client workload promotes the operations. |
| Deferred | `POST /v1/embeddings`, `POST /v1/moderations` | Deferred by user decision. | Not supported. | Reject; no upstream call. |
| Deferred | Images operations under `/v1/images/generations`, `/v1/images/edits`, `/v1/images/variations` | Deferred. | Not supported. | Reject; future support requires generated-media logging and binary tests. |
| Deferred | Audio operations under `/v1/audio/*`, including `/v1/audio/transcriptions`, `/v1/audio/translations`, `/v1/audio/speech`, `/v1/audio/voices`, and `/v1/audio/voice_consents*` | Deferred. | Not supported. | Reject; `/backend-api/transcribe` remains the only transcription-related 006 path and only for OAuth/Codex-native clients. |
| Deferred | Videos operations under `/v1/videos*` | Deferred. | Not supported. | Reject; future support requires media streaming and body-capture tests. |
| Deferred | Files and Uploads operations under `/v1/files*` and `/v1/uploads*` | Deferred. | Not supported. | Reject; future support requires upload/download streaming and storage-safety tests. |
| Deferred | Vector Stores and Batches operations under `/v1/vector_stores*` and `/v1/batches*` | Deferred. | Not supported. | Reject; future support requires lifecycle, pagination, and file-content tests. |
| Deferred | Fine-tuning and Evals operations under `/v1/fine_tuning*` and `/v1/evals*` | Deferred. | Not supported. | Reject; future support requires lifecycle and long-running operation tests. |
| Deferred | OpenAI Realtime operations under `/v1/realtime`, `/v1/realtime/sessions`, `/v1/realtime/transcription_sessions`, `/v1/realtime/client_secrets`, and `/v1/realtime/calls*` | Deferred for API-key accounts. | Not supported for OAuth accounts. | Reject in 006; future WebSocket support requires bidirectional relay and abort tests. |
| Deferred | Mutating model operation `DELETE /v1/models/{model}` | Deferred; 006 supports read-only model discovery only. | Not supported. | Reject; future support requires explicit destructive-operation decision. |
| Blocked | OpenAI organization/project administration under `/v1/organization*` and `/v1/projects/{project_id}/*` | Blocked. | Blocked. | Always reject; no admin-key credential, audit, or permission model exists in 006. |
| Unsupported | Any unlisted `/v1/*` or arbitrary `/backend-api/*` path | Unsupported. | Unsupported. | Reject with native data-plane unsupported-route behavior; no wildcard backend-api proxying. `/api/codex/*` is not a data-plane prefix in 006. |

## Login Method API Support Matrix

| Upstream Login Method | Credential Class | 006 OpenAI Platform `/v1/*` APIs | Explicit Codex Compatibility Paths | Realtime WebSocket | Explicitly Not Supported in 006 | OpenAI Administration API |
|---|---|---|---|---|---|---|
| API key | OpenAI Platform API key | Supported for `/v1/responses`, `/v1/conversations`, `/v1/chat/completions`, and `/v1/models` | Not supported for `/backend-api/*` | Not supported for OpenAI Platform Realtime or `WS /v1/responses` in initial 006 | `/v1/embeddings`; `/v1/moderations`; Images; Audio including `/v1/audio/transcriptions`; Videos; Files; Uploads; Vector Stores; Batches; Fine-tuning; Evals; `/backend-api/*`; arbitrary `/api/*`; Realtime; WebRTC/SIP | Not supported |
| OAuth browser | ChatGPT/Codex OAuth token | Not supported for arbitrary Platform REST APIs | Supported for `/v1/responses`, `WS /v1/responses`, `/v1/responses/compact`, `/v1/chat/completions`, `GET /v1/models`, `/backend-api/codex/responses`, `WS /backend-api/codex/responses`, `/backend-api/codex/responses/compact`, `/backend-api/codex/models`, and `/backend-api/transcribe` | Not supported for OpenAI Platform Realtime; Codex Responses WebSocket compatibility is supported on `WS /v1/responses` and `WS /backend-api/codex/responses` | `/v1/models/{model}`; `/v1/audio/transcriptions`; all other Platform REST APIs; arbitrary `/backend-api/*`; arbitrary `/api/*`; OpenAI Platform Realtime; Realtime WebRTC/SIP proxying | Not supported |
| OAuth device code | ChatGPT/Codex OAuth token | Not supported for arbitrary Platform REST APIs | Same explicit Codex compatibility paths as browser OAuth | Not supported | Same unsupported set as browser OAuth | Not supported |
| Import `auth.json` | ChatGPT/Codex OAuth token | Not supported for arbitrary Platform REST APIs | Same explicit Codex compatibility paths as browser OAuth | Not supported | Same unsupported set as browser OAuth | Not supported |

## Backend Routing Logic Matrix

| Client-Facing Request | API-Key Upstream Account | Codex OAuth Upstream Account | Backend Logic |
|---|---|---|---|
| `POST /v1/responses` | Supported | Supported | API-key rows forward to `account.base_url + /v1/responses` with the upstream API key. OAuth rows map to the ChatGPT Codex `/backend-api/codex/responses` upstream with the OAuth access token and optional `chatgpt-account-id`. |
| `WS /v1/responses` | Not supported | Supported | OAuth rows establish the ChatGPT Codex Responses WebSocket with OAuth credential headers and relay events while preserving the `/v1/responses` client-facing path for codex-lb-compatible clients. |
| `POST /v1/responses/compact` | Pass-through only if the API-key upstream supports the path | Supported | API-key rows do no compatibility adaptation. OAuth rows map to the ChatGPT Codex `/backend-api/codex/responses/compact` upstream. |
| `POST /v1/chat/completions` | Supported | Supported | API-key rows forward to `account.base_url + /v1/chat/completions`. OAuth rows use the Chat Completions-to-Codex-Responses adapter, then reconstruct Chat Completions-compatible object or SSE output. |
| `/v1/conversations` | Supported | Not supported | API-key rows forward to the matching Platform-compatible upstream path. OAuth rows reject with native data-plane unsupported-route behavior. |
| `GET /v1/models` | Supported | Supported | Returns a strict fail-fast union across every active route-eligible account. API-key rows contribute `account.base_url + /v1/models`; OAuth rows contribute Codex model lists through the OpenAI-compatible facade and do not contact OpenAI Platform `/v1/models`. |
| `POST /backend-api/codex/responses` | Not supported | Supported | API-key rows are never eligible for `/backend-api/*`. OAuth rows map to the ChatGPT Codex responses upstream. |
| `WS /backend-api/codex/responses` | Not supported | Supported | API-key rows are never eligible for `/backend-api/*`. OAuth rows establish the Codex Responses WebSocket with OAuth credential headers and relay events. |
| `POST /backend-api/codex/responses/compact` | Not supported | Supported | API-key rows are never eligible for `/backend-api/*`. OAuth rows map to the ChatGPT Codex compact upstream. |
| `GET /backend-api/codex/models` | Not supported | Supported | API-key rows are never eligible for `/backend-api/*`. OAuth rows return the Codex model list through the explicit Codex models mapping. |
| `POST /backend-api/transcribe` | Not supported | Supported | API-key rows are never eligible for `/backend-api/*`. OAuth rows map to the ChatGPT Codex `/transcribe` upstream for Codex-native clients. |
| `GET /api/admin/usage` | Router-owned Admin API | Router-owned Admin API | Returns router-local usage observability through the Admin API envelope and generated OpenAPI contract. It is not a provider-compatible data-plane request and does not select upstream accounts. |
| `POST /v1/embeddings` | Deferred | Not supported | Both credential classes reject in initial 006 with native data-plane unsupported-route behavior. |
| `POST /v1/moderations` | Deferred | Not supported | Both credential classes reject in initial 006 with native data-plane unsupported-route behavior. |
| Images APIs | Deferred | Not supported | Both credential classes reject in initial 006 with native data-plane unsupported-route behavior. |
| Audio APIs, including `POST /v1/audio/transcriptions` | Deferred | Not supported | Both credential classes reject in initial 006 with native data-plane unsupported-route behavior; `/backend-api/transcribe` remains the only transcription-related supported path and only for OAuth/Codex-native clients. |
| Videos APIs | Deferred | Not supported | Both credential classes reject in initial 006 with native data-plane unsupported-route behavior. |
| Files, Uploads, Vector Stores, Batches, Fine-tuning, Evals | Deferred | Not supported | Both credential classes reject in initial 006 with native data-plane unsupported-route behavior. |
| OpenAI Realtime WebSocket | Deferred | Not supported | API-key Realtime is deferred from initial 006. OAuth tokens are never used for OpenAI Platform Realtime. |
| OpenAI Administration API | Blocked | Blocked | Always reject; 006 does not model admin-key credentials, audit, or permissions for OpenAI organization/project administration. |

## Non-Functional Requirements

| Category | Requirement | Verification |
|---|---|---|
| Compatibility | P0 local compatibility matrix passes for JSON, SSE, `/v1/responses` and backend Codex WebSocket, Codex transcribe multipart, provider-error, and router-error paths | Local E2E/integration tests |
| SDK | First-party SDK smoke covers representative P0 JSON, streaming, models, and unsupported-route cases | Opt-in SDK smoke |
| Performance | For small JSON requests, gateway overhead P95 is <= 50ms over direct upstream in local mock tests | Benchmark or integration timing |
| Streaming | First streamed downstream event is delivered before upstream completion for SSE and WebSocket cases | Stream timing tests |
| Memory | Streaming and Codex WebSocket proxying avoid buffering full responses; Codex transcribe multipart handling is bounded by the configured request body limit | Integration test/code review |
| Security | No upstream API key, bearer token, upload bytes, audio bytes, or generated media bytes appear in logs or request records when body logging is disabled | Log scrub and DB artifact scan |
| Reliability | Aborted client streams/sockets close upstream resources within 5 seconds | Integration tests |
| Observability | Every routed data-plane request has a request record with outcome and latency, and router-local usage observability is available through `/api/admin/usage` | Store/API tests |

## Key Entities

- **Gateway Route**: A client-facing `/v1/*` or selected `/backend-api/*` data-plane path that the router may forward to an OpenAI-compatible or ChatGPT Codex-compatible upstream.
- **Coverage Matrix**: The authoritative list of endpoint groups and method/path operations, including priority, credential behavior, transport shape, status, and test requirement for this feature.
- **Transport Shape**: The request/response mechanics for a route: JSON, multipart, binary, SSE, WebSocket, or long-running resource lifecycle.
- **Provider-Compatible Response**: A downstream response that preserves the provider status, content type, and body semantics.
- **Router-Native Data-Plane Error**: A router-generated `/v1/*` or `/backend-api/*` data-plane error that follows the existing native error shape and includes `X-Request-Id`.
- **Administrative Surface**: OpenAI endpoints that manage organizations, projects, users, groups, roles, service accounts, certificates, API keys, usage, costs, or rate limits.
- **OAuth Codex Account**: A ChatGPT/Codex-authenticated upstream account created by browser OAuth, device OAuth, or `auth.json` import; eligible only for explicit Codex-backed compatibility paths.
- **Codex-Native Backend API**: The selected `/backend-api/*` paths used by Codex-native clients and codex-lb-compatible integrations; not a general ChatGPT backend proxy.
- **Admin Usage Surface**: The router-owned `/api/admin/usage` operation that reports local request/usage observability using the Admin API envelope and generated OpenAPI clients. It is not an OpenAI Platform, ChatGPT Codex, or codex-lb data-plane endpoint.

## Scope

### In Scope

- API-key account gateway compatibility for initial P0 endpoint groups in the coverage target: Responses, Conversations, Chat Completions, and Models.
- OAuth `GET /v1/models` codex-lb-compatible list facade over the Codex model list.
- OAuth account Chat Completions compatibility through a Codex Responses adapter.
- OAuth account `WS /v1/responses` compatibility through the ChatGPT Codex Responses WebSocket.
- Selected Codex-native `/backend-api/*` compatibility paths for Codex clients.
- Router-owned Admin API usage observability through `GET /api/admin/usage`.
- Provider-compatible JSON, multipart, SSE, and WebSocket transport for the initial supported path set.
- Request logging, outcome classification, and safe observability for expanded `/v1/*` and selected `/backend-api/*` traffic.
- Local compatibility tests and opt-in live smoke for representative endpoint groups.
- Documentation of supported/deferred/blocked endpoint groups.

### Out of Scope

- Making ChatGPT OAuth accounts a complete OpenAI Platform gateway credential.
- Initial support for Embeddings, Moderations, Images, Audio, Videos, Files, Uploads, Vector Stores, Batches, Fine-tuning, Evals, or OpenAI Realtime WebSocket.
- Realtime WebRTC or SIP proxying.
- Realtime client-secret workflows whose purpose is direct browser/mobile WebRTC access.
- Provider schema validation beyond what is required for router routing and logging.
- Local model execution, content moderation policy enforcement, prompt filtering, or response rewriting.
- Storing or caching uploaded files, generated media, vector store contents, or batch input/output contents in the router.
- Cost estimation beyond provider-returned usage fields.
- Admin Portal UI for constructing arbitrary OpenAI API calls.
- OpenAI Administration API proxying.
- `/v1/audio/transcriptions`.
- Arbitrary `/backend-api/*` proxying outside the explicitly listed Codex compatibility paths.
- Arbitrary `/api/*` or `/api/codex/*` data-plane proxying.

## Assumptions

- This is core data-plane behavior, not a plugin, because it changes the router's provider-compatible `/v1/*` and selected `/backend-api/*` contracts. Router-local usage observability is a router-owned Admin API concern.
- "Full OpenAI API gateway" means "standard OpenAI Platform client traffic through API-key upstream accounts" unless clarified otherwise.
- The OpenAI API reference as checked on 2026-04-26 is the initial endpoint inventory source; future OpenAI additions require explicit coverage-matrix updates.
- The router should be mostly transparent for API-key traffic and should not attempt to normalize every provider schema.
- Live tests use low-budget API-key credentials and never run by default in PR CI.
- OpenAI Realtime server-to-server WebSocket is deferred from initial 006. WebRTC and SIP require a separate transport decision because they involve browser/mobile media or call-control flows rather than ordinary API-key HTTP/WebSocket forwarding.
- Beta/newer product-specific APIs and legacy APIs are P1/deferred by default. P0 is reserved for common current Platform APIs and every transport shape needed to prove gateway correctness.
- OAuth Chat Completions support means compatibility translation over the ChatGPT Codex backend, not direct forwarding to the OpenAI Platform Chat Completions endpoint with OAuth credentials.
- `/v1/audio/transcriptions` is not supported in 006 for either API-key or OAuth accounts.
- OAuth `/backend-api/transcribe` support means explicit ChatGPT Codex `/transcribe` compatibility mapping for Codex-native clients.
- Selected `/backend-api/*` support means the listed Codex-native client paths only, not arbitrary ChatGPT backend proxying.
- Router-local usage observability belongs to `/api/admin/usage`; `GET /api/codex/usage` and `GET /api/codex/usage/` are not 006 data-plane endpoints.
- OpenAI's official OpenAPI endpoint inventory checked on 2026-04-27 lists usage and cost APIs as organization administration endpoints such as `/v1/organization/usage/*` and `/v1/organization/costs`; they remain blocked with the rest of the OpenAI Administration API in 006.

## Clarifications Resolved

- 2026-04-26: OpenAI Administration API is not included in 006.
- 2026-04-26: OpenAI Realtime server-to-server WebSocket is deferred from initial 006; WebRTC/SIP proxying is out of scope.
- 2026-04-26: Current/common Platform API groups are P0; newer product-specific and legacy API groups are P1/deferred unless a concrete client workload requires promotion.
- 2026-04-26: ChatGPT OAuth browser/device/import accounts must support `/v1/chat/completions` through an explicit Codex-backed compatibility adapter.
- 2026-04-26: `/v1/audio/transcriptions` is not supported in 006 for either API-key or OAuth accounts.
- 2026-04-26: Embeddings, Moderations, Images, Audio, Videos, Files, Uploads, Vector Stores, Batches, Fine-tuning, Evals, and OpenAI Realtime WebSocket are deferred from initial 006.
- 2026-04-26: Selected Codex-native `/backend-api/*` paths are included for compatibility; arbitrary `/backend-api/*` proxying is excluded.
- 2026-04-26: codex-lb cross-check promotes `WS /v1/responses` and OAuth `GET /v1/models` into 006 compatibility scope; `/v1/audio/transcriptions` remains deferred.
- 2026-04-27: Router-local usage observability is moved to the Admin API surface. `GET /api/codex/usage` and `GET /api/codex/usage/` are no longer 006 supported data-plane operations.

## Success Criteria

- At least one first-party SDK can use the router base URL for representative P0 endpoint groups without custom SDK patches.
- Local compatibility tests cover every initial P0 transport shape: JSON, SSE, Codex WebSocket, Codex transcribe multipart, provider error, and router error.
- Live smoke validates a low-cost subset of P0 endpoint groups with real API-key credentials.
- No request log, router log, Playwright artifact, SDK smoke log, or request-record body contains token-like secrets or binary payload bytes when body logging is disabled.
- Existing Codex `/v1/responses`, OAuth Responses, OAuth Chat Completions, and selected `/backend-api/*` behavior remains compatible with Features 003-005 and the new 006 mapping.

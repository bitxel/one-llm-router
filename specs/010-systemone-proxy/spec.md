# 010 — Native System One proxy

## Scope

Add `POST /v1/systemone` and a third, independent account capability
`op.systemone` (UI: Decision / System One). Chat Completions, Responses and
System One may be selected in any combination; there is no account type or
mutual exclusion. Reuse the existing account, API key, model restrictions,
request recorder, egress proxy and HTTP forwarder. No new dependencies,
database migrations, provider-specific adapters, plugins or billing service.

API-key accounts forward to `trimRight(base_url, "/") + "/systemone"`.
Base URL must be explicitly configured for System One. OAuth does not support
this operation. Model names and request bytes are preserved; global model
rename rules do not apply. Each request uses one eligible account and makes
one upstream request; no retries or fallback to other operations/models.

## Contract

- JSON text requests only: model (nonempty string), state (string/object/array),
  questions (nonempty object), with noul/choice/score question types. Upstream
  validates question instructions and criteria. Unknown extensions pass through;
  assets/images/box/stream, query parameters, multipart and request compression
  are explicitly unsupported. No provider-specific limits or model allowlist.
- Validate requests before selecting accounts. A valid request with no eligible
  account returns 503; DB failures remain system errors. Use existing router
  native error bodies, not the admin envelope.
- Upstream JSON success must contain model, answers and nonnegative integer
  input/output usage. Validate before writing response headers; preserve raw
  response bytes. No SSE, probability transformation or invented usage.
- Preserve non-2xx upstream status/body and safe response headers. Router read,
  connect, timeout and malformed-response failures are explicit native errors.
- Reuse body limits and request logging. Bound nonstream upstream execution to
  30 seconds after request body validation; propagate cancellation.
- Headers never forward client credentials, cookies, Codex-specific headers or
  hop-by-hop values. Inject selected upstream API key and router request ID.

## Routing compatibility

Only current operation capability determines inference eligibility. All seven
nonempty capability combinations are legal. System-One-only accounts must not
be selected by existing models/conversations/Playground/session diagnostic
paths. Mixed-capability accounts remain usable on each supported operation.
Preserve legacy empty-capability account semantics on legacy routes.

System-One-only accounts use manual model configuration: skip automatic
models discovery and return existing `account_model_refresh_failed` on manual
refresh with a clear explanation. Mixed-capability accounts retain existing
discovery. Model restrictions remain account-level, not per operation. The
OpenAI models union includes legacy/mixed accounts, excludes System-One-only
accounts, and does not promise each listed model supports every operation.

## Delivery / acceptance

Contract and SQLite integration tests must fail before implementation. Cover
all capability combinations; raw forwarding to arbitrary base URLs; credential
replacement; no model rewrite; no account; request/response validation; upstream
errors; usage recording; mixed vs single capability isolation; model discovery;
create/edit multi-select UI. Regenerate Go/TS after OpenAPI documentation changes.
Run related and full Go/frontend checks plus data-plane compatibility.

Live inference is opt-in and needs an operator-provided API key. Public model
listing and mock tests do not count as live inference. Before rollback, disable
System-One-only accounts and remove the new capability from mixed accounts.

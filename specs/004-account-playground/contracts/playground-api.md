# Account Playground API Contract

**Feature**: 004-account-playground
**Spec**: `specs/004-account-playground/spec.md`
**Created**: 2026-04-23
**Status**: Ready

All operations in this contract are router-owned Admin API operations. They use the standard envelope:

```json
{
  "code": 0,
  "msg": "ok",
  "data": {}
}
```

Success and business errors use HTTP 200. System errors use HTTP 500. `X-Request-Id` is a response header only and must not be duplicated into `data` or `msg`.

---

## POST /api/admin/playground/run

**Description**: Execute one non-streaming single-turn text probe using automatic account selection or one explicit active account.

**Auth**: Same Admin API trust boundary as other `/api/admin/*` routes. When the admin-auth plugin is enabled in a future feature, this route is gated like other admin operations.

**Idempotent**: No. Each valid submit may create one upstream call and one request-history record.

**Provider transport**: This route is router-owned and enveloped, but the upstream call MUST use the same account-specific transport as Feature 003 data-plane forwarding. API-key accounts call `account.base_url + /v1/responses` with an API-key bearer and no `chatgpt-account-id`. OAuth accounts call the ChatGPT Codex backend `/codex/responses` path with the OAuth access token, `chatgpt-account-id` when present, and `User-Agent: codex_cli_rs/0.120.0 (Mac OS 26.2; arm64) dumb`, then normalize the bounded upstream result into this Admin API envelope.

Duplicate-submit protection in P0 is a browser/UI responsibility while a run is pending. The API does not accept a `client_run_id` and does not deduplicate repeated direct HTTP requests.

### Request

Content-Type: `application/json`

| Parameter | Type | Required | Validation | Description |
|-----------|------|----------|------------|-------------|
| `selection_mode` | string | yes | `auto` or `account` | Account selection mode |
| `account_id` | integer | iff `selection_mode=account` | positive integer; account must exist and be active | Explicit account target |
| `session_key` | string | no | 1..128 characters | Sticky auto-selection key |
| `model` | string | yes | 1..128 characters after trimming | Provider model id; UI default is `gpt-5.4-mini` |
| `text` | string | yes | 1..16000 characters after trimming | Single-turn prompt text |
| `max_output_tokens` | integer | no | 1..4096 | Output cap; server default is `1024` when omitted |
| `include_raw_response` | boolean | no | default false | Whether bounded safe raw JSON may be included |

The JSON request body cap is 96 KiB (`98304` bytes). Prompt length is counted as 16,000 trimmed Unicode characters, not UTF-8 bytes.

### Request Example

```json
{
  "selection_mode": "account",
  "account_id": 42,
  "model": "gpt-5.4-mini",
  "text": "Say hello in one sentence.",
  "max_output_tokens": 128,
  "include_raw_response": true
}
```

### Response (Success)

HTTP 200:

```json
{
  "code": 0,
  "msg": "ok",
  "data": {
    "run": {
      "selection_mode": "account",
      "session_key": null,
      "outcome": "success",
      "latency_ms": 1234
    },
    "account": {
      "id": 42,
      "name": "prod-plus",
      "provider": "openai",
      "auth_method": "oauth_browser",
      "status": "active",
      "email": "operator@example.com",
      "plan_type": "chatgpt-plus",
      "plan_type_label": "ChatGPT Plus"
    },
    "upstream": {
      "status_code": 200,
      "response_mode": "json"
    },
    "output": {
      "text": "Hello.",
      "text_available": true,
      "raw_response": {},
      "raw_response_available": true
    },
    "usage": {
      "input": 10,
      "output": 3
    }
  }
}
```

### Response (Success With No Extractable Text)

HTTP 200, still `code:0` because the upstream call succeeded:

```json
{
  "code": 0,
  "msg": "ok",
  "data": {
    "run": {
      "selection_mode": "auto",
      "session_key": "playground-local",
      "outcome": "no_extractable_text",
      "latency_ms": 1100
    },
    "account": {
      "id": 7,
      "name": "api-key-account",
      "provider": "openai",
      "auth_method": "api_key",
      "status": "active"
    },
    "upstream": {
      "status_code": 200,
      "response_mode": "json"
    },
    "output": {
      "text": "",
      "text_available": false,
      "raw_response": {},
      "raw_response_available": true
    },
    "usage": {}
  }
}
```

### Response (Business Errors)

| HTTP | Code | Symbol | Data | When |
|------|------|--------|------|------|
| 200 | 2008 | `malformed_body` | `{}` | Request body is not valid JSON or content type is invalid |
| 200 | 2009 | `request_body_too_large` | `{ "scope": "envelope", "limit_bytes": 98304 }` | JSON body exceeds the 96 KiB Playground request cap |
| 200 | 4001 | `invalid_playground_request` | `{ "field": "text" }` or other field | Empty text, over-limit text, invalid selection mode, missing account id, invalid model, invalid max output |
| 200 | 4002 | `playground_no_active_account` | `{}` | Automatic mode has no active eligible accounts |
| 200 | 4003 | `playground_account_unavailable` | `{ "requested_account_id": 42, "reason": "disabled" }` | Explicit account is missing, deleted, disabled, or otherwise ineligible |
| 200 | 4004 | `playground_upstream_error` | `{ "account_id": 42, "account": { ...safeAccountSummary }, "upstream_status": 401, "provider_error": "invalid_api_key", "provider_message": "..." }` | Provider returns non-2xx |
| 200 | 4005 | `playground_upstream_timeout` | `{ "account_id": 42, "account": { ...safeAccountSummary }, "wait_limit_ms": 30000 }` | Upstream request times out |
| 200 | 4006 | `playground_response_malformed` | `{ "account_id": 42, "account": { ...safeAccountSummary }, "reason": "invalid_json" }` | Upstream success body is not safe JSON for this Admin API response |
| 200 | 4007 | `playground_response_too_large` | `{ "account_id": 42, "account": { ...safeAccountSummary }, "limit_bytes": 1048576 }` | Upstream response exceeds the bounded read cap |
| 500 | 4900 | `playground_internal_error` | `{}` | Unexpected handler/service failure |

### Safe Account Summary

`account` in success and known-account failure responses is a non-secret object:

```json
{
  "id": 42,
  "name": "prod-plus",
  "provider": "openai",
  "auth_method": "oauth_browser",
  "status": "active",
  "email": "operator@example.com",
  "plan_type": "chatgpt-plus",
  "plan_type_label": "ChatGPT Plus"
}
```

It MUST NOT contain API keys, OAuth access tokens, refresh tokens, ID tokens, raw authorization headers, provider bearer values, or derived bearer values.

### Safe Raw Response and Provider Messages

- `include_raw_response=false` by default. When false, `output.raw_response_available=false` and `output.raw_response_omitted_reason="not_requested"` unless a more specific omission reason applies.
- Raw response is capped at 1 MiB before parsing. Over cap returns `4007 playground_response_too_large`.
- Raw response is rendered only when the provider body is valid JSON and the sanitizer can produce a bounded object without credential material.
- The sanitizer MUST redact any key matching secret-like names including `authorization`, `api_key`, `access_token`, `refresh_token`, `id_token`, `token`, `bearer`, `secret`, `password`, and `credential`.
- The sanitizer MUST redact token-like string values even when the key is not secret-like.
- `provider_error` is capped at 128 characters after trimming.
- `provider_message` is capped at 512 characters after trimming and is sanitized with the same token-like value redaction.

### Timeout and Cancellation

- The P0 Playground wait limit is 30 seconds (`wait_limit_ms=30000`).
- Upstream work that exceeds the limit returns `4005 playground_upstream_timeout`.
- If the client cancels while the router waits on upstream, the HTTP response may not be written. The run must still record a `cancelled` outcome when the request context is observed cancelled, and logs/request history must not leak credential material.

### Observability

Every valid submitted run logs/records:

| Field | Required | Description |
|-------|----------|-------------|
| `request_id` | yes | Router correlation id |
| `selection_mode` | yes | `auto` or `account` |
| `account_id` | when known | Selected or requested account |
| `auth_method` | when known | Account auth method |
| `upstream_status` | when upstream reached | Provider status |
| `latency_ms` | yes | Run duration |
| `outcome` | yes | Terminal outcome |
| `error_code` | when failed | Registered symbol |

Prompt and response bodies follow runtime body-logging toggles. Credential material is never logged or returned.

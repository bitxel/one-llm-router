# Contract: OAuth Flow API (003 v1.1)

**Feature**: 003-multi-mode-codex-auth
**Base path**: `/api/admin/oauth/*` (admin-auth gated exactly like 002's `/api/admin/*`)
**Owner package**: `internal/api/oauthapi/` (new in 003) — `handlers.go` hosts all endpoints in this contract; the loopback `CallbackHandler` (the Rail-A receiver bound by `oauth.Coordinator.StartBrowser` onto `localhost:1455`) lives in `internal/oauth/flow.go`, not in `oauthapi/`, because it is mounted on its own `net.Listener`, not on the admin-API mux.

**Machine-readable source of truth**: [`openapi/admin.yaml`](../../../openapi/admin.yaml) (OpenAPI 3.0.3). Per `AGENTS.md §API Contract` rule #1, from 003 onwards the OpenAPI YAML is the canonical spec and this Markdown contract is derived narrative documentation that explains the WHY (rail semantics, observability contract, state-before-cancel ordering) that YAML cannot express. Any disagreement between the two is a bug — the YAML is authoritative for shapes, the Markdown is authoritative for flow prose; both MUST be updated in the same commit.

**Response envelope — non-negotiable**. Every `/api/admin/oauth/*` endpoint returns `Content-Type: application/json; charset=utf-8` and **always** uses the 002 envelope policy defined in `docs/error-codes.md` §HTTP envelope policy and implemented by `internal/api/envelope.go`:

- **HTTP 200** for success AND business errors. The outcome lives in `body.code`:
  - `code: 0, msg: "ok"` → success. `data` carries the endpoint payload.
  - `code: <non-zero>, msg: "<snake_case_symbol>"` → business error. `data` carries drill-down fields (empty object `{}` when there is no detail).
- **HTTP 500** (system error) is reserved for `oauth_internal_error` (3900) and `oauth_store_failed` (3901). Handlers MUST NOT invent any other HTTP 5xx.
- HTTP 4xx is **forbidden** on any `/api/admin/oauth/*` route (the envelope moves business-error signalling into `body.code`). Violating this is a CI failure per the `envelope_parity_test`.

Every body in this contract is shown **already wrapped**. For brevity, nested examples document only the `data` payload when a whole section is purely about data fields.

Two documented exemptions exist in 003, and **neither is in this file**:
- `GET /auth/callback` (below in §`/auth/callback` — browser-facing plaintext, not `/api/*`).
- `POST /api/admin/accounts/{id}/export-auth-json` **success** body (see `contracts/accounts-api.md` §`/export-auth-json` — byte-compatible Codex CLI `auth.json`; its **error** responses still use the envelope).

---

## POST `/api/admin/oauth/browser/start`

Start the browser PKCE OAuth flow (backs US-1). The server always opens **both** callback rails in parallel (FR-012): Rail A = best-effort dual-stack loopback listener on `localhost:1455`; Rail B = `POST /browser/manual-callback` endpoint accepting a pasted URL. Either rail can close the flow; a `Flow.Consumed` atomic CAS guarantees exactly one `/oauth/token` exchange.

**Auth**: admin-auth plugin (when enabled) — same posture as every other `/api/admin/*` mutation in 002.
**Idempotent**: No.

### Request

```json
{ "provider": "openai" }
```

| Parameter | Type | Required | Validation | Description |
|---|---|---|---|---|
| `provider` | string | Yes | `∈ {"openai"}` in 003 | Which upstream provider to authorise against. |

**There is no `mode` / `topology` / `return_redirect_url_only` parameter**, by FR-012 design. The two callback rails are always both open; no operator decision. Clients sending any such field MUST have it ignored (per HTTP `POST` convention for unknown JSON keys — the server does not envelope-reject it, to keep forward compatibility painless).

The callback port is **not** operator-selectable per request — the redirect URI is pinned to `http://localhost:1455/auth/callback` by the OpenAI OAuth app registration (see `research.md` Decision 1). The router does not advertise fallback ports to OpenAI; if 1455 is unavailable, the flow still starts and completes through the manual paste rail.

### Response (Success)

**HTTP 200**:

```json
{
  "code": 0,
  "msg":  "ok",
  "data": {
    "flow_id":        "fl_a1b2c3",
    "authorize_url":  "https://auth.openai.com/oauth/authorize?…",
    "callback_url":   "http://localhost:1455/auth/callback",
    "listener_bound": true,
    "expires_at":     "2026-04-15T10:19:00Z",
    "method":         "browser"
  }
}
```

**One `data` shape, always**. Fields:

| Field | Type | Always present? | Meaning |
|---|---|---|---|
| `flow_id` | string | Yes | Opaque handle used by `/cancel`, `/flow` status poll, and `/manual-callback`. |
| `authorize_url` | string | Yes | URL to open in the operator's browser — carries `code_challenge_method=S256`, `state=…`, `redirect_uri=http://localhost:1455/auth/callback`, `scope=openid profile email offline_access api.connectors.read api.connectors.invoke`, `originator=codex_cli_rs`, `id_token_add_organizations=true`, `codex_cli_simplified_flow=true`. |
| `callback_url` | string | **Yes (always)** | The literal redirect URI baked into `authorize_url`: always `http://localhost:1455/auth/callback`. Present regardless of `listener_bound`: when `listener_bound=false` the URL is what the operator's browser will land on with a connection-refused page or another local process' response; the UI uses it to help the operator recognise the URL they need to copy into the paste textarea. |
| `listener_bound` | bool | Yes | `true` iff the router managed to bind at least one of `{127.0.0.1:1455, [::1]:1455}`. `false` means canonical port 1455 could not be bound (port in use, unprivileged container, etc.) — Rail A is dead, Rail B (paste) is the only path. **Advisory only**: the UI MUST still render the paste textarea regardless (FR-012). `listener_bound` is shown to the operator as an informational badge at most (e.g. "Listening on localhost:1455" vs "Paste-only mode — loopback unavailable"). |
| `expires_at` | ISO8601 | Yes | Hard deadline. After this the flow transitions to `error` and both rails stop accepting input. |
| `method` | string | Yes | Always `"browser"` for this endpoint. |

The OAuth `state` CSRF token is **not** included — it's inside `authorize_url` where only the user-agent needs it. Server-side comparison against the in-memory `OAuthFlow.State` happens on whichever rail closes the flow (FR-006).

### Response (Errors)

| HTTP | `code` | `msg` | When | `data` shape |
|---|---|---|---|---|
| 200 | 3001 | `oauth_flow_in_progress` | Another OAuth flow is already pending (FR-008). | `{ "method": "browser"\|"device", "flow_id": "...", "expires_at": "...", "created_at": "..." }` — describes the in-flight flow so the UI can render the conflict banner without re-polling. |
| 200 | 3002 | `invalid_oauth_provider` | `provider` not in the allow-list (`{"openai"}` only). | `{ "field": "provider" }` |

There is **no** error for loopback bind failure — loopback bind failure is a normal success envelope with `data.listener_bound = false`, not an error. The flow still starts.

---

## POST `/api/admin/oauth/browser/manual-callback`

Rail B of the dual-rail browser flow (FR-012). The UI renders a "Paste callback URL" textarea for every browser flow it starts — the operator copies whatever their address bar ended at (typically `http://localhost:1455/auth/callback?code=…&state=…`, which on remote-router deploys showed them a connection-refused page) and submits it here.

**Auth**: admin-auth plugin.
**Idempotent**: No (consumes the pending flow via CAS).
**No precondition on how the flow was started** — every browser flow accepts both rails. Which rail actually wins is decided by the `Flow.Consumed` CAS inside `oauth.Coordinator`.

### Request

```json
{
  "callback_url": "http://localhost:1455/auth/callback?code=abc…&state=s_xyz…"
}
```

Server-side parses `code` + `state` (and any `error` param) from the URL query string. Processing order is **strictly sequential — every prior step MUST pass before the next runs** (handler implementations MUST follow this order; `envelope_parity_test` verifies it with a matrix of pre-condition failures):

1. **URL prefix validation (first, cheapest — no flow state needed)**. Reject if `callback_url` is not parseable OR `scheme+host+path` does NOT match `http://localhost:1455/auth/callback` exactly (case-sensitive path, literal `"localhost"`). On failure → `code: 3007 invalid_callback_url`, `data.reason = "url_prefix_mismatch"`. Flow stays `pending`.
2. **Required-param check (still no flow state needed)**. If NEITHER `code` NOR `error` query params are present → `code: 3007 invalid_callback_url`, `data.reason = "missing_code_and_error"`. Flow stays `pending`.
3. **Load pending flow**. Read `Coordinator.PendingFlow()` atomically. If `nil` → `code: 3004 no_flow_in_progress`, `data = {}`. No log event (unauthenticated-probe noise would otherwise dominate).
4. **Flow expiry check**. If `Flow.ExpiresAt.Before(now)` → `code: 3006 flow_expired`, `data = {}`. The expiry reaper normally logs `oauth_flow_expired` first; this is the fallback when a rail raced the reaper. Do NOT mutate `Flow.Status` here — the reaper owns that transition.
5. **State validation (before every branch that mutates flow state)**. Constant-time compare `state` against `Flow.State`. Mismatch → `code: 3003 oauth_state_mismatch` (log `oauth_rail_rejected` INFO with `rail=manual_paste`, `error_code=oauth_state_mismatch`); flow stays `pending` — the loopback rail (if still alive) can still close it, or the operator may re-paste the corrected URL. **Applies to both the `error=access_denied` branch and the happy path — no mutation of `Flow.Status` happens without a validated `state`**.
6. **Then branch on params**:
   - **`error=access_denied`** (after state validation passes) → `Flow.Consumed.CompareAndSwap(false, true)`:
     - CAS win → `Flow.Status = error`, `Flow.ConsumedBy = "manual_paste"`, `oauth_flow_cancelled` INFO log with `rail=manual_paste`. Response envelope: `code: 0`, `data = { "status": "cancelled" }`. No `/oauth/token` call.
     - CAS loss → `code: 3005 already_consumed` (the loopback rail already closed the flow first). `data = { "rail_won": "loopback" }` so the UI knows which rail prevailed.
   - **Any other `error=…`** (after state validation passes) → `code: 3016 oauth_upstream_error`, `data = { "provider_error": "<value>", "provider_message": "<?error_description if present>" }`. Flow stays `pending` (the operator may re-paste a corrected URL or cancel). No `/oauth/token` call.
   - **Happy path: `code` present, no `error`** (after state validation passes) → `Flow.Consumed.CompareAndSwap(false, true)`:
     - CAS win → run `/oauth/token` code-exchange; on provider success, persist the row and emit the success envelope below; on provider failure, emit the matching `code` (see error table).
     - CAS loss → `code: 3005 already_consumed`, `data = { "rail_won": "loopback" }` (the loopback rail already exchanged this code; harmless race).

### Response (Success)

**HTTP 200**:

```json
{
  "code": 0,
  "msg":  "ok",
  "data": {
    "account": { /* AccountListItem per data-model.md */ },
    "rail":    "manual_paste",
    "status":  "success"
  }
}
```

For `error=access_denied` with CAS-win:

```json
{
  "code": 0,
  "msg":  "ok",
  "data": {
    "status": "cancelled",
    "rail":   "manual_paste"
  }
}
```

### Response (Errors)

| HTTP | `code` | `msg` | When | `data` shape |
|---|---|---|---|---|
| 200 | 3007 | `invalid_callback_url` | URL prefix mismatch OR missing both `code` and `error` query params. | `{ "reason": "url_prefix_mismatch" \| "missing_code_and_error" }` |
| 200 | 3003 | `oauth_state_mismatch` | `state` query param does not match `Flow.State`. The flow is NOT marked consumed — the loopback rail (if still alive) can still close it, or the operator may re-paste the correct URL. | `{}` |
| 200 | 3004 | `no_flow_in_progress` | No pending flow; operator probably missed the timeout. | `{}` |
| 200 | 3005 | `already_consumed` | `Flow.Consumed` CAS lost — the loopback rail already exchanged this flow's code. Harmless race; the UI's next `GET /flow` poll will report `status=success`. | `{ "rail_won": "loopback" }` |
| 200 | 3006 | `flow_expired` | Flow's `ExpiresAt` already passed before the callback arrived — the expiry reaper usually logs `oauth_flow_expired` first; this is the fallback when the rail raced the reaper. | `{}` |
| 200 | 3009 | `oauth_invalid_grant` | OpenAI's `/oauth/token` returned `invalid_grant` during code exchange (PKCE `code_verifier` mismatch, code reuse, etc.). | `{ "provider_error": "invalid_grant", "provider_message": "<?error_description>" }` |
| 200 | 3016 | `oauth_upstream_error` | Propagated from OpenAI's `/oauth/token` (non-`invalid_grant` 4xx, 5xx, unparseable body, network failure) OR the `error=…` param carried a non-`access_denied` provider error. `data.http_status` omitted when the failure was network-level. | `{ "provider_error": "<str>", "provider_message": "<str>", "http_status": <int?> }` |

---

## GET `/auth/callback` (NOT under `/api/` — envelope-exempt by contract)

The loopback-listener served by the browser flow. **Not** part of the admin API — bound on `127.0.0.1:<ephemeral_port>` only, served by a one-shot `*http.Server` spun up inside the `OAuthFlow`. Present here only for documentation. The response is shown to the browser (not to a program), so it is plaintext / HTML, not envelope-wrapped — this is the **first** of the two documented envelope exemptions for 003.

### Request

`GET /auth/callback?code=…&state=…` — sent by the user's browser after OpenAI redirects. OR `GET /auth/callback?error=access_denied&state=…` on cancel.

### Response (to the browser, **not** to an API client)

- `200 OK` with a minimal success HTML + a `<script>` that closes the tab — happy path only.
- `400 Bad Request` with a plaintext reason — state mismatch, missing `code`, or any other local validation error.
- `502 Bad Gateway` with a plaintext reason — validation passed but the follow-on `POST auth.openai.com/oauth/token` call failed (network error, 4xx, 5xx). The flow `Status` still transitions to `error` and the operator sees the provider message in the admin UI on the next `/flow` poll.
- `404 Not Found` — any path other than `/auth/callback`. The listener exposes exactly one route; this is a belt-and-suspenders check against drive-by scans during the 5-minute window.

The response body is **never** shown to a program — it is meant for the browser tab; the real outcome is persisted in `OAuthFlow.Status` and surfaced via `GET /api/admin/oauth/flow`.

### Post-conditions

- `state` mismatch → browser sees 400, flow `Status` stays `pending` (the paste rail can still close the flow with the correct URL; this rail's hit is simply rejected), `oauth_rail_rejected` INFO log (`error_code=oauth_state_mismatch`, `rail=loopback`), no row written (FR-006). **State validation happens BEFORE every branch including `error=access_denied`** — the loopback rail also never lets an unauthenticated cancel tear down a live flow.
- `error=access_denied` (after `state` validated) → `Flow.Consumed.CompareAndSwap(false,true)`:
  - CAS win → under `Flow.mu`: set `Status=error`, `ConsumedBy="loopback"`; browser sees 200 cancel page; emit `oauth_flow_cancelled` INFO log (`rail=loopback`); no row written (US-1 AC-3).
  - CAS loss → the paste rail already closed this flow. **The loopback handler MUST `Flow.mu.RLock()` and read `Flow.Status` before rendering**, then release — NOT assume any branch. Render is a pure function of the observed terminal status: `success` → 200 success page; `error` → 200 cancel page (parity with the CAS-win cancel branch so the operator's browser never sees a spurious "success" after a cancel). **Per T-037 §silent-Rail-A rule this is the ONLY silent CAS-loss branch** — emit an `oauth_rail_rejected` event at **DEBUG** level with `rail=loopback, error_code=already_consumed` (debug-only so production stays quiet under the expected dual-rail race), NO INFO line, NO extra `/oauth/token` call, NO duplicate row.
- `code` present + `state` valid + `Flow.Consumed` CAS wins → browser sees 200 success page, call `POST {auth.openai.com}/oauth/token` with `grant_type=authorization_code` + `code_verifier`, persist the `upstream_accounts` row, under `Flow.mu` set `Status=success` + `ConsumedBy="loopback"`, listener closed, `oauth_flow_completed` INFO log with `rail=loopback`.
- `code` present + `state` valid + `Flow.Consumed` CAS loses (paste rail got there first) → same silent-Rail-A rule as the cancel branch above: **`Flow.mu.RLock()` → read `Flow.Status` → render accordingly** (`success` ⇒ 200 success page, `error` ⇒ 200 cancel page — paste rail may have landed on either terminal state). Listener closed, NO extra `/oauth/token` call, NO INFO log, DEBUG-only `oauth_rail_rejected` (`error_code=already_consumed`), NO duplicate row.
- `code` present + `state` valid + `/oauth/token` fails → browser sees 502, flow `Status=error`, `oauth_flow_failed` INFO log with the provider-supplied `error_code` + `rail=loopback`, listener closed, no row written.

---

## POST `/api/admin/oauth/device/start`

Start the device-code OAuth flow (backs US-3).

**Auth**: admin-auth plugin.
**Idempotent**: No.

### Request

```json
{ "provider": "openai" }
```

### Response (Success)

**HTTP 200**:

```json
{
  "code": 0,
  "msg":  "ok",
  "data": {
    "flow_id":          "fl_d4e5f6",
    "user_code":        "ABCD-1234",
    "verification_url": "https://auth.openai.com/codex/device",
    "interval_seconds": 5,
    "expires_at":       "2026-04-15T10:29:00Z",
    "method":           "device"
  }
}
```

### Response (Errors)

| HTTP | `code` | `msg` | When | `data` shape |
|---|---|---|---|---|
| 200 | 3001 | `oauth_flow_in_progress` | Same as browser flow. | Same shape as browser. |
| 200 | 3002 | `invalid_oauth_provider` | `provider` not in the allow-list. | `{ "field": "provider" }` |
| 200 | 3015 | `device_auth_unavailable` | Provider returned 404 on `/api/accounts/deviceauth/usercode` (FR-007, matches codex-lb behavior). | `{ "provider": "openai" }` |
| 200 | 3016 | `oauth_upstream_error` | Any other 4xx/5xx from OpenAI's device endpoint, or unparseable body. | `{ "provider_error": "<str>", "provider_message": "<str>", "http_status": <int?> }` |

### Behavior after success

The server immediately kicks off a background polling goroutine that:

1. Waits `interval_seconds`.
2. Calls `POST {auth.openai.com}/api/accounts/deviceauth/token` with `{device_auth_id, user_code}`.
3. On `authorization_pending`: repeats after the current interval.
4. On `slow_down`: **doubles** the current interval for the next tick (per `research.md` Decision 1 §"Device-code poll interval fallback" — matches RFC 8628 §3.5 recommended behaviour), caps at 60s, then repeats. (Earlier drafts said "+5s"; that was an error and is corrected in the `tasks.md` rewrite.)
5. On transport-level HTTP 403 / 404 from the provider: treats as transient, uses the current interval, keeps polling until `expires_at`.
6. On a successful `tokens` response: persists the `upstream_accounts` row, sets `Status=success`, stops polling.
7. On terminal provider error:
   - `access_denied` → `Flow.Status=error`, `oauth_flow_cancelled` INFO log (parity with browser `error=access_denied`), stop polling.
   - `expired_token` → `Flow.Status=error`, `oauth_flow_expired` INFO log (the deadline hit), stop polling.
   - Anything else (`invalid_grant`, repeated `slow_down` past deadline, HTTP errors the poller gave up on) → `Flow.Status=error`, `oauth_flow_failed` INFO log with the provider-supplied `error_code` + `error_message`, stop polling.

The UI polls `GET /api/admin/oauth/flow` to learn the outcome.

---

## GET `/api/admin/oauth/flow`

Status-poll endpoint the UI hits every second while either flow is pending.

**Auth**: admin-auth plugin.
**Idempotent**: Yes.

### Response (Success)

**HTTP 200**. The `data` object is always one of the shapes below; the envelope itself is constant.

```json
// idle
{ "code": 0, "msg": "ok", "data": { "status": "idle" } }

// pending — browser (single shape; UI ALWAYS renders the paste textarea regardless of listener_bound per FR-012)
{
  "code": 0, "msg": "ok",
  "data": {
    "status":             "pending",
    "method":             "browser",
    "flow_id":            "fl_a1b2c3",
    "listener_bound":     true,
    "expires_at":         "2026-04-15T10:19:00Z",
    "created_at":         "2026-04-15T10:14:00Z",
    "target_account_id":  42                        // OPTIONAL — present only for reauth flows (FR-010, `contracts/accounts-api.md` §`/reauth`); OMITTED (not null) for new-account flows
  }
}

// pending — device (includes display bits)
{
  "code": 0, "msg": "ok",
  "data": {
    "status":             "pending",
    "method":             "device",
    "flow_id":            "fl_d4e5f6",
    "user_code":          "ABCD-1234",
    "verification_url":   "https://auth.openai.com/codex/device",
    "expires_at":         "2026-04-15T10:29:00Z",
    "created_at":         "2026-04-15T10:14:00Z",
    "target_account_id":  42                        // OPTIONAL — same semantics as the browser pending shape above
  }
}

// success — one-shot; server transitions back to idle on the NEXT read
{
  "code": 0, "msg": "ok",
  "data": {
    "status":             "success",
    "method":             "browser",
    "flow_id":            "fl_a1b2c3",
    "rail":               "loopback",               // or "manual_paste" — which rail won the CAS (browser flows only; omitted for device)
    "target_account_id":  42,                       // OPTIONAL — echoed on reauth-flow success so the UI can navigate to /admin/accounts/{id}
    "account":            { /* AccountListItem per data-model.md */ }
  }
}

// error — one-shot; server transitions back to idle on the NEXT read
{
  "code": 0, "msg": "ok",
  "data": {
    "status":  "error",
    "method":  "device",
    "flow_id": "fl_d4e5f6",
    "error":   { "code": "access_denied", "message": "User denied consent" }
  }
}
```

**Why `error` sits inside `data` (not in the envelope top-level `code`)**: a `GET /flow` poll is itself successful — it is reporting the status of a past flow, not failing the poll. The envelope `code` MUST be 0 unless the poll itself failed (admin-auth plugin rejected, DB down, etc.). `data.error.code` carries the **symbolic** provider/flow error code for display; the integer error codes in `docs/error-codes.md` §3xxx are for the endpoints that **produced** the error, not for reporting it back through the status poll.

### Response (Errors)

None beyond the admin-auth plugin's own failure modes — the endpoint NEVER returns a system error on "no flow" (it returns `code: 0, data: { "status": "idle" }`). The only non-zero `code` this endpoint can emit is the standard system-error family (`3900` on unrecoverable panic, `1900` on DB unavailable).

---

## POST `/api/admin/oauth/cancel`

Operator cancels the in-flight flow (closes the browser tab, gives up on device entry). Lets the UI tear down the flow cleanly instead of waiting for the hard timeout.

**Auth**: admin-auth plugin.
**Idempotent**: Yes — cancelling an already-idle or already-completed flow is a success envelope with no effect.

### Request

```json
{ "flow_id": "fl_a1b2c3" }
```

### Response (Success)

**HTTP 200**:

```json
{ "code": 0, "msg": "ok", "data": { "status": "idle" } }
```

### Response (Errors)

| HTTP | `code` | `msg` | When | `data` shape |
|---|---|---|---|---|
| 200 | 3008 | `flow_id_mismatch` | `flow_id` doesn't match the currently pending flow; probably a stale UI. | `{ "expected_flow_id": "<current>" \| null }` — `null` when idle. |

---

## Observability contract

Every OAuth endpoint MUST emit these structured log events with `slog`. **No event ever carries token bytes**; token-claims-derived display values (`email`, `plan_type`, `chatgpt_account_id`) ARE allowed in logs.

| Event | Level | When | Fields (mandatory) |
|---|---|---|---|
| `oauth_flow_started` | INFO | `/browser/start` or `/device/start` returns success | `request_id`, `flow_id`, `method`, `provider`, `expires_at`, `listener_bound` (browser only — `true` / `false`; omitted for device) |
| `oauth_flow_completed` | INFO | Account row persisted | `request_id`, `flow_id`, `method`, `provider`, `account_id`, `email`, `plan_type`, `rail` (browser only: `"loopback"` / `"manual_paste"`; omitted for device) |
| `oauth_flow_cancelled` | INFO | `/cancel` hit OR `error=access_denied` arrives on callback (after state validation) | `request_id`, `flow_id`, `method`, `rail` (browser only, when triggered by a callback rather than `/cancel`; omitted otherwise) |
| `oauth_flow_failed` | INFO | Flow transitioned to `Status=error` because `/oauth/token` (browser code-exchange OR device poll) returned a terminal error (`invalid_grant`, provider 4xx/5xx the coordinator gave up on, network failure after retries, or `/oauth/token` payload fails our `OAuthTokens` schema check). **Fires on BOTH `method="browser"` and `method="device"`** — device flows emit this when the background poller gives up on a terminal error (see §`/device/start` Behavior step 7). | `request_id`, `flow_id`, `method`, `error_code`, `error_message` (provider-supplied; MUST NOT be the raw response body), `rail` (browser only: `"loopback"` / `"manual_paste"`; **omitted (not null) for device flows**) |
| `oauth_rail_rejected` | INFO (DEBUG for Rail A `already_consumed` — see T-037 silent-Rail-A rule) | A single callback hit on one rail was rejected **without** transitioning the flow's `Status` out of `pending` — applies to: (1) state-mismatch on Rail A or Rail B, (2) CAS-loss path where a rail tried to consume after the other rail already won (`already_consumed`), (3) **authorize-server `?error=<other>` on a rail** where `<other>` is NOT `access_denied` (`server_error`, `unauthorized_client`, `invalid_scope`, etc. — the rail reports the rejection to its caller, but the flow stays alive so the other rail / a retry can still close it). The flow remains alive. | `request_id`, `flow_id`, `method` (always `"browser"`), `rail`, `error_code` (∈ {`oauth_state_mismatch`, `already_consumed`, `<provider_error_string>`}) |
| `oauth_flow_expired` | INFO | Flow hit `ExpiresAt` without success — applies to both `method="browser"` (neither rail closed it) and `method="device"` (polling deadline hit with no success response). Replaces the old per-method `oauth_device_expired` event. | `request_id`, `flow_id`, `method`, `listener_bound` (browser only) |
| `oauth_refresh_started` | INFO | `oauth.Coordinator.RefreshIfStale` opens a `singleflight.Do` for an account whose current token is past the refresh threshold | `request_id`, `account_id`, `provider`, `last_refresh` |
| `oauth_refresh_ok` | INFO | Refresh attempt completed successfully (new tokens persisted, `last_refresh` bumped) — **one INFO line per single-flight burst**, no matter how many concurrent callers joined the Do. | `request_id`, `account_id`, `provider`, `refresh_elapsed_ms` |
| `oauth_refresh_failed` | WARN | Refresh attempt at request-time returned `invalid_grant`/`account_deactivated`/`invalid_client`; account moving to `status=disabled` (FR-005) | `request_id`, `account_id`, `provider`, `error_code` |
| `oauth_refresh_transient_fallback` | WARN | Refresh attempt at request-time hit a **transient** error (network/timeout/5xx/`temporarily_unavailable`/`slow_down`) — `RefreshIfStale` returns `(acct.AccessToken, nil)` so the forwarder proceeds with the current (stale-but-maybe-valid) bearer; account stays `active`. One WARN line per fallback (NOT per waiter in the single-flight burst). | `request_id`, `account_id`, `provider`, `error_code`, `last_refresh_age_seconds` |
| `oauth_auth_json_exported` | WARN | `/accounts/{id}/export-auth-json` responded success | `request_id`, `account_id`, `operator_id` (from admin-auth plugin; `"anonymous"` if plugin disabled), `email`, `exported_at` |
| `oauth_auth_json_export_rejected` | INFO | `/accounts/{id}/export-auth-json` responded with a business-error envelope (1001 or 3014) | `request_id`, `account_id`, `error_code` |
| `auth_json_import_rejected` | INFO | `/accounts/import-auth-json` responded with a business-error envelope (3010 / 3011 / 2009) | `request_id`, `reason` (never the payload bytes), `error_code`, `scope` (**only when `error_code=2009`**: `"envelope"` when the outer 64 KB `http.MaxBytesReader` tripped — preamble-flood signal; `"part"` when the inner 16 KB `io.LimitReader` tripped — legitimate oversized payload. Omitted on 3010 / 3011.) |

The log-scrub test from 002 (grep CI for `"Bearer "`, `"access_token":`, `"refresh_token":`, `"id_token":` in any log stream) extends in 003 to assert **zero** hits across the full test run — implementing SC-4.

# Quickstart: Multi-mode Codex Authentication

**Feature**: 003-multi-mode-codex-auth
**Spec**: `specs/003-multi-mode-codex-auth/spec.md`
**Purpose**: the ≤10-minute smoke path for verifying 003 after implementation. These scenarios are the canonical signal for `sdd-test` and `sdd-verify`.

Unless a scenario explicitly says otherwise, it assumes:
- A cold `one-llm-router` binary built from 003.
- A fresh SQLite DB (`./router.db`).
- `config.json` already exists and `/admin/*` is open.
- `curl` + a real browser (for US-1) and a separate laptop browser (for US-3).

---

## 1. US-1 — OAuth browser onboard, dual-rail (laptop + remote router)

**One scenario, two rails always open in parallel (FR-012)**. The same code path covers local-router (laptop) and remote-router (server) topologies — the UI shows the same form, the `POST /browser/start` body is the same (`{"provider":"openai"}`), and the backend always best-effort binds the loopback listener AND always accepts a pasted URL. Whichever rail delivers a valid `code+state` first wins via a `Flow.Consumed` CAS.

### 1a. Local-router happy path (Rail A — loopback wins) — under 2 minutes

Exercise when the router binary AND the operator's browser both run on the operator's laptop (admin portal at `http://localhost:8080` or `http://127.0.0.1:8080`).

1. Open `http://localhost:8080/admin/accounts` in the same browser that hosts the router.
2. Click **New account** → pick **OAuth (browser)** → **Start**. There is NO mode selector on the form — only a provider label and the Start button.
3. Verify: `POST /api/admin/oauth/browser/start` request body is exactly `{"provider":"openai"}` (no `mode` field, no `topology` field, no `return_redirect_url_only`). Response is the 002 envelope `HTTP 200 {"code":0,"msg":"ok","data":{...}}`; `data` contains `flow_id`, `authorize_url`, `callback_url` = `"http://localhost:1455/auth/callback"`, `listener_bound`, `expires_at`, `method: "browser"`. Every `/api/admin/oauth/*` endpoint in this quickstart responds HTTP 200 with the envelope shape — a literal `HTTP 4xx` on any such call is a FAIL (envelope policy `docs/error-codes.md`).
4. Verify: the router logs a single `oauth_flow_started method=browser listener_bound=true` INFO line containing `flow_id`, `expires_at`.
5. Verify: immediately after the Start click, the UI opens a new browser tab at `https://auth.openai.com/oauth/authorize?…` (with `code_challenge_method=S256` + `state=s_…` + `id_token_add_organizations=true`) **AND** the admin-portal page renders a textarea labelled "Paste callback URL" (the textarea MUST be visible even though `listener_bound=true` — FR-012).
6. Sign in with a real ChatGPT Plus / Team account, approve.
7. Verify: the new tab lands on `http://localhost:1455/auth/callback?code=…&state=…`, the loopback listener serves a success page that auto-closes, and — within ≤3s (next `/flow` poll) — the paste textarea on the admin page auto-dismisses with a "Completed via browser redirect" badge. The admin table now shows the new account with:
   - `auth_method = OAuth (browser)`
   - `email = <the account's email>`
   - `plan_type_label = "ChatGPT Plus"` (or "ChatGPT Team")
   - `last_refresh` = just now
   - `status = active`
8. Verify: `GET /api/admin/oauth/flow` (one last poll) returns envelope `{"code":0,"msg":"ok","data":{"status":"success","rail":"loopback","account":{…}}}`; the next poll returns `{"code":0,"msg":"ok","data":{"status":"idle"}}`.
9. Run `curl -s "http://localhost:8080/api/admin/health?account_id=<id>" | jq -r ".data.accounts[\"<id>\"].status"`. Value MUST become `active` within 5s of the success page (US-1 AC-2).
10. Issue a non-streaming `POST /v1/responses` that selects this OAuth account. Verify from an upstream fixture or trace log that the router called `https://chatgpt.com/backend-api/codex/responses`, included `chatgpt-account-id` when the row has `chatgpt_account_id`, forced Codex SSE upstream, returned collected JSON to the client, and, when upstream response-body logging is enabled, stored the collected complete JSON in `request_records.upstream_response_body`. A call to `https://api.openai.com/v1/responses` with the OAuth bearer is a FAIL.
11. Verify: grep the full log buffer — `grep -E 'Bearer |access_token.*eyJ|refresh_token' router.log` MUST return 0 lines (NFR "Tokens never surfaced off-host", SC-4).

**Pass**: end-to-end elapsed ≤120s from click-Start to the admin-table row, and `rail=loopback` recorded. **Fail** ≥120s, any token byte in logs, a missing paste textarea at step 5, or rail ≠ loopback.

### 1b. Remote-router happy path (Rail B — paste wins) — under 3 minutes

Exercise when the router runs on a remote host (`https://router.example.com` or `http://10.0.0.12:8080` etc.) and the operator's browser is on a different machine. The operator flow is the same as 1a except the loopback redirect does not round-trip.

1. Open `https://router.example.com/admin/accounts` in the operator's laptop browser.
2. Click **New account** → pick **OAuth (browser)** → **Start**. Same form, same single button.
3. Verify: `POST /api/admin/oauth/browser/start` request body is exactly `{"provider":"openai"}` (identical to 1a — no mode, no topology). Envelope response `data` may contain `listener_bound: true` OR `listener_bound: false` — either is valid on the remote host depending on whether the router could bind 1455. Either way the UI proceeds identically.
4. Verify: the UI opens the authorize tab AND renders the "Paste callback URL" textarea (identical UI state to 1a at this point).
5. Sign in with a real ChatGPT Plus / Team account, approve.
6. Verify: the browser's new tab is at `http://localhost:1455/auth/callback?code=…&state=…` and shows a connection-refused / "site can't be reached" page (expected — the router is not on the laptop). The URL is still visible in the address bar. `/flow` poll still shows `status=pending` on the admin page (the loopback rail is alive-but-never-reached).
7. Copy the entire URL from the address bar. Paste into the admin UI's textarea. Click **Submit**.
8. Verify: `POST /api/admin/oauth/browser/manual-callback` is sent with body `{"callback_url":"http://localhost:1455/auth/callback?code=…&state=…"}`; returns the envelope `HTTP 200 {"code":0,"msg":"ok","data":{"account":{…},"rail":"manual_paste","status":"success"}}` within ≤5s.
9. Verify: the admin table shows the new account with all FR-011a metadata populated identically to 1a. Router logs contain `oauth_flow_completed method=browser rail=manual_paste`.
10. Verify: `GET /api/admin/oauth/flow` returns envelope `{"code":0,"msg":"ok","data":{"status":"success","rail":"manual_paste","account":{…}}}`; the next poll returns `{"code":0,"msg":"ok","data":{"status":"idle"}}`.
11. Verify: the OAuth data-plane transport check from step 10 of 1a passes for this account as well.
12. Verify: the token-leak log grep from step 11 of 1a still returns 0 lines (SC-4).

**Pass**: end-to-end elapsed ≤180s from click-Start through copy-paste to the admin-table row, and `rail=manual_paste` recorded. **Fail** ≥180s, any token byte in logs, a missing paste textarea, a `mode` field anywhere in the request/response wire format, or rail ≠ manual_paste.

**Negative sub-test (state mismatch on paste)**: during step 7 above, mangle the `state` query param (flip one character). Expect envelope `HTTP 200 {"code":3003,"msg":"oauth_state_mismatch","data":{}}`, NO row written, log shows `oauth_rail_rejected error_code=oauth_state_mismatch rail=manual_paste`, flow status remains `pending`. Re-paste the correct URL — flow still closes successfully (the mismatch did NOT abort the flow, per FR-012 rail-isolation).

**Negative sub-test (access_denied on paste without state)**: during step 7 above, paste a URL like `http://localhost:1455/auth/callback?error=access_denied` with NO `state` param. Expect envelope `HTTP 200 {"code":3003,"msg":"oauth_state_mismatch","data":{}}` (state validation runs BEFORE the branch — an unauthenticated cancel MUST NOT tear down the flow; FR-006 + FR-012). Flow status stays `pending`. Re-paste with the correct state param to actually cancel.

**Negative sub-test (invalid callback URL)**: during step 7, paste `https://evil.example.com/auth/callback?code=…&state=<valid>`. Expect envelope `HTTP 200 {"code":3007,"msg":"invalid_callback_url","data":{"reason":"url_prefix_mismatch"}}`. Flow status stays `pending`. Also test missing both `code` and `error` params: expect `code:3007` with `data.reason = "missing_code_and_error"`.

**Negative sub-test (CAS race — both rails fire)**: on a local-router laptop start the flow per 1a; at the success-page step, ALSO copy the callback URL from the address bar and paste it into the admin textarea within the same 2-second window. Expect exactly ONE `upstream_accounts` row, ONE `oauth_flow_completed` log line (with `rail=loopback` OR `rail=manual_paste` — whichever CAS-won), and the loser returns harmlessly (Rail A loser renders the success page anyway; Rail B loser returns envelope `HTTP 200 {"code":3005,"msg":"already_consumed","data":{"rail_won":"loopback"}}` — UI treats it as success because the next `/flow` poll is already `status=success`).

---

## 2. US-1 Edge — Second operator tries to start while a flow is pending

1. On a separate admin session (or in an incognito tab) call:
   ```bash
   curl -sX POST http://localhost:8080/api/admin/oauth/browser/start \
        -H 'Content-Type: application/json' \
        -d '{"provider":"openai"}'
   ```
   while scenario 1 (any sub-scenario) is still in flight.
2. Expect envelope `HTTP 200 {"code":3001,"msg":"oauth_flow_in_progress","data":{"method":"browser","flow_id":"fl_…","expires_at":…,"created_at":…}}`.
3. Verify: no second flow started (log still shows exactly one `oauth_flow_started`).

---

## 3. US-2 — Pure API-key path still works Platform-compatible with 002

1. Call:
   ```bash
   curl -sX POST http://localhost:8080/api/admin/accounts \
        -H 'Content-Type: application/json' \
        -d '{"name":"staging-apikey","provider":"openai","api_key":"sk-test-…","auth_method":"api_key"}'
   ```
2. Expect envelope `HTTP 200 {"code":0,"msg":"ok","data":{"account":{...}}}` where `data.account.auth_method = "api_key"` and **no** `email` / `plan_type` / `plan_type_label` / `chatgpt_account_id` / `last_refresh` fields (FR-011a — those keys MUST be omitted, not null). This preserves the 002 account-create semantics while adding the explicit `auth_method` discriminator.
3. Run the 002 proxy end-to-end smoke: `curl -sX POST http://localhost:8080/v1/responses -d …` — MUST forward to `account.base_url + /v1/responses` with the API key bearer and no `chatgpt-account-id` header. This path remains Platform-compatible with 002 for API-key rows.

---

## 4. US-3 — Onboard from a headless box via device code

1. On a headless server, hit:
   ```bash
   curl -sX POST http://localhost:8080/api/admin/oauth/device/start \
        -d '{"provider":"openai"}'
   ```
2. Response contains `user_code` (8 chars, formatted `XXXX-YYYY`), `verification_url`, `interval_seconds`, `expires_at`.
3. On your laptop's browser, open the `verification_url`, type the `user_code`, approve.
4. Poll from the server: `curl -s http://localhost:8080/api/admin/oauth/flow` — within ≤2× `interval_seconds` after approval the status transitions from `pending` → `success` and returns the new account (US-3 AC-2).
5. Verify the admin table row: `auth_method = OAuth (device)`, full metadata rendered per FR-011a.

---

## 5. US-4 — Auto-refresh before request forwarding

Precondition: a successfully onboarded OAuth account from scenarios 1 or 4. Also requires that the DB's `last_refresh` is old enough to trigger refresh — the easiest way is to manually mutate the row:

```sql
UPDATE upstream_accounts SET last_refresh = datetime('now','-30 days') WHERE id = <oauth_id>;
```

1. Issue a Codex-protocol request that will pick this account:
   ```bash
   curl -sX POST http://localhost:8080/v1/responses \
        -H 'Authorization: Bearer <client_key>' \
        -d '{"model":"gpt-5","input":[{"role":"user","content":"ping"}]}'
   ```
2. Verify: the router logs a single `oauth_refresh_ok` INFO line for that `account_id` (single-flight held), and the response from the ChatGPT Codex backend is a normal 2xx surfaced through the `/v1/responses` client-facing route.
3. Verify: re-reading the row shows `last_refresh` bumped to within the last second and `access_token` / `refresh_token` bytes **changed** (they rotated).
4. **Race test** (SC-3 synthetic proxy of this scenario in Go tests): fire 50 concurrent `/v1/responses` at the same aged-token account. The router MUST call `/oauth/token` exactly ONCE for the whole burst (metric assertion + single `oauth_refresh_ok` log).

---

## 6. US-4 Edge — Permanent refresh failure disables the account

1. Revoke the account's refresh token on the OpenAI side (or simulate by overwriting `refresh_token` with random bytes in SQLite).
2. Issue another `/v1/responses`.
3. Verify: single `oauth_refresh_failed` WARN log with `error_code=invalid_grant`, the row transitions to `status='disabled'`, and the next selector call MUST NOT pick this account (`/api/admin/health` reports `degraded` if no other active account remains — US-1 AC-2).

---

## 7. US-5 — Rotate/re-auth keeps the row identity

1. **API-key branch**: open `/admin/accounts/1`, paste a new `sk-...` key into the re-auth form, submit.
   Verify via `SELECT * FROM upstream_accounts WHERE id=1`:
   - `id` unchanged, `created_at` unchanged, `updated_at` advanced.
   - `access_token`, `refresh_token`, `id_token` stay `NULL`.
   - no new row was created.
2. **OAuth browser branch**: from the admin UI, click **Re-authenticate** on the browser-OAuth row from scenario 1.
   Complete a new browser OAuth flow.
   Verify via `SELECT * FROM upstream_accounts WHERE id=<same_id>`:
   - `id` unchanged, `name` unchanged, `provider` unchanged, `created_at` unchanged.
   - `access_token`, `refresh_token`, `id_token` all rotated.
   - `updated_at`, `last_refresh`, and `access_expires_at` advanced.
   - `auth_method` remains `oauth_browser`.
3. **OAuth device branch**: click **Re-authenticate** on the device-OAuth row from scenario 4.
   Approve the new device code and wait for the page to converge back to the same detail route.
   Verify via `SELECT * FROM upstream_accounts WHERE id=<same_id>`:
   - same row id, same `auth_method='oauth_device'`.
   - token trio rotated, timestamps advanced.
   - no new row was created.
4. Negative: on an `oauth_browser` row, paste an API key into the inline re-auth field.
   Verify the UI shows `auth_method_mismatch` copy: "This account was onboarded via OAuth — paste-key re-auth is not allowed", and the row remains unchanged.
5. Abandon a rotation mid-flow: start another `reauth` then click `Cancel` in the flow. Verify the row is unchanged from the previous successful state.

---

## 8. US-6 — Import a local Codex CLI `auth.json`

1. On your laptop, locate `~/.codex/auth.json`.
2. Open `http://localhost:8080/admin/accounts` → **New account** → pick **Import auth.json**, select the file via the picker (or drag-drop), submit. The UI sends a `multipart/form-data` POST with a single `auth_json` file part (NO `name` / `provider` fields — aligned with codex-lb).
3. Verify: envelope `HTTP 200 {"code":0,"msg":"ok","data":{"account":{…}}}` + the new row renders with `auth_method = OAuth (import)`, `name` derived from the id_token's `email` claim, `provider = "openai"`, and FR-011a metadata populated.
4. Issue a `/v1/responses` against this account — MUST succeed (US-6 AC-2).
5. **Validator path**: upload a JSON that is missing `tokens.refresh_token`. Expect envelope `HTTP 200 {"code":3011,"msg":"invalid_auth_json","data":{"missing_fields":["tokens.refresh_token"]}}`; **no** row written; log shows `auth_json_import_rejected` INFO **without** the payload bytes.
6. **Structure path**: upload a file whose body is `"not an object"`. Expect envelope `HTTP 200 {"code":3010,"msg":"invalid_auth_json_structure","data":{}}`.
7. **Missing-part path**: send the request with `Content-Type: application/json` instead of `multipart/form-data`. Expect envelope `HTTP 200 {"code":3010,"msg":"invalid_auth_json_structure","data":{}}`.
8. **Size path (inner cap)**: upload a file whose `auth_json` part body is 32 KB. Expect envelope `HTTP 200 {"code":2009,"msg":"request_body_too_large","data":{"scope":"part","limit_bytes":16384}}` (reuses 002's registered code; inner `io.LimitReader` trip).

8b. **Size path (outer cap)**: upload a 100 KB multipart body whose `auth_json` part is only 2 KB but the preamble/boundary junk totals 95 KB. Expect envelope `HTTP 200 {"code":2009,"msg":"request_body_too_large","data":{"scope":"envelope","limit_bytes":65536}}` (outer `http.MaxBytesReader` trip — preamble-flood defence). On a non-malicious client this path is **not reachable** with any real multipart library.
9. **Duplicate account**: import a second `auth.json` whose `chatgpt_account_id` matches an existing row's. Expect envelope `HTTP 200 {"code":0,...}` (dupe allowed — US-6 edge case 2); the UI surfaces a non-fatal "looks like a duplicate" warning.

---

## 9. FR-014 — Export auth.json from an OAuth account

1. From the admin UI, open the OAuth account's detail panel. Verify:
   - Metadata (email, plan_type, auth_method, status, last_refresh) is rendered WITHOUT an **Export auth.json** button auto-rendering tokens.
2. Click **Export auth.json**.
3. Verify: the response headers contain `Content-Disposition: attachment; filename="auth.json"`, and the browser downloads a file.
4. `cat ~/Downloads/auth.json | jq '.tokens | keys'` MUST list exactly `["access_token","account_id","id_token","refresh_token"]`.
5. Verify: the router log now contains a single `oauth_auth_json_exported` **WARN** line with `request_id`, `account_id`, `email`, `exported_at`, and **no token bytes**.
6. Import that same exported file into another (fresh) router's `POST /accounts/import-auth-json`. MUST land a functional account (round-trip fidelity — Decision 8 in research.md).
7. Click **Export auth.json** on an API-key row: the UI MUST not show the button at all; calling the endpoint directly MUST return envelope `HTTP 200 {"code":3014,"msg":"not_oauth_account","data":{"auth_method":"api_key"}}`. Export against a non-existent id MUST return envelope `HTTP 200 {"code":1001,"msg":"account_not_found","data":{}}` (reuses 001's registered code per `docs/error-codes.md` §Feature 003).

---

## 10. FR-013 — setup wizard preserves 002 commit semantics while exposing all four auth modes

1. Delete `config.json`, restart the router, and open `/setup/`.
2. Wizard MUST NOT offer a "Probe" step for SQLite (002's relaxation preserved).
3. On the *Upstream* step, verify exactly four auth-method choices exist in this order:
   - `api_key`
   - `oauth_browser`
   - `oauth_device`
   - `oauth_import`
4. Verify `api_key` is selected by default and still shows the inline `name/provider/api_key/base_url` seed fields.
5. Switch to each deferred mode (`oauth_browser`, `oauth_device`, `oauth_import`) and verify the inline API-key fields disappear; the review step must describe the mode as deferred and the setup commit still succeeds without inventing a fake `first_account`.
6. Complete the wizard with `oauth_browser` selected. After `POST /api/setup/commit` succeeds, the router MUST land directly on `/admin/accounts/new-oauth?from=setup`.
7. Repeat with `oauth_device`. After commit success, the router MUST land on `/admin/accounts/new-oauth-device?from=setup`.
8. Repeat with `oauth_import`. After commit success, the router MUST land on `/admin/accounts/new-import?from=setup`.
9. Repeat with `skip first_account`. The wizard must still commit successfully, land on `/admin/`, and show the degraded-shell path until an account is added later.

---

## Pass/Fail matrix

| # | Scenario | P0? | Pass criteria |
|---|---|---|---|
| 1a | OAuth browser dual-rail — local router, loopback wins (US-1 AC-1) | P0 | ≤120s end-to-end, no token bytes in logs, `rail=loopback`, paste textarea always visible until auto-dismiss on success, OAuth `/v1/responses` uses ChatGPT Codex backend + `chatgpt-account-id` + `User-Agent: codex_cli_rs/0.120.0 (Mac OS 26.2; arm64) dumb` |
| 1b | OAuth browser dual-rail — remote router, paste wins (US-1 AC-1) | **P0** | ≤180s end-to-end, no token bytes in logs, `rail=manual_paste`, `{"provider":"openai"}` body (no `mode` / `topology` field anywhere), state-mismatch rejected with envelope `code:3003` without terminating the flow, state-before-cancel validated on `error=access_denied` without `state`, invalid callback URL rejected with envelope `code:3007`, CAS race resolved with exactly one row + one `oauth_flow_completed`, OAuth `/v1/responses` uses ChatGPT Codex backend + `User-Agent: codex_cli_rs/0.120.0 (Mac OS 26.2; arm64) dumb` |
| 2 | Concurrent flow blocked by envelope `code:3001` | P0 | HTTP 200 + envelope with `method` + `flow_id` + `expires_at` |
| 3 | API-key path regression | P0 | API-key `/v1/*` path remains Platform-compatible with 002 and never sends `chatgpt-account-id` |
| 4 | Device-code happy path | P1 | Flow converges within ≤2× provider interval |
| 5 | Auto-refresh + single-flight | P0 | Exactly one `/oauth/token` call per aged-token burst |
| 6 | Permanent refresh fail → disable | P0 | `status=disabled`, WARN log with `invalid_grant` |
| 7 | Re-auth preserves row identity | P1 | Same `id` + `created_at` post-rotation |
| 8 | auth.json import | P1 | Metadata rendered, `/v1/responses` succeeds, validator rejects malformed payloads |
| 9 | auth.json export | P1 | Tokens leave only via the downloaded file + WARN audit per call; API-key rows hide the button |
| 10 | Setup wizard parity + four-mode handoff | P0 | SQLite probe skipped, four auth modes rendered in setup, deferred modes hand off directly after commit, skip-account still works |

All 10 MUST pass for the `sdd-verify` closed-loop gate. Any HTTP 4xx on any `/api/admin/*` path in any scenario above is an automatic FAIL — every `/api/admin/*` endpoint in 003 MUST emit HTTP 200 with the `{code,msg,data}` envelope (business-error `code` non-zero lives in the body, not in the HTTP status). HTTP 500 is ONLY legal for `oauth_internal_error` (3900) / `oauth_store_failed` (3901) / earlier-feature system codes.

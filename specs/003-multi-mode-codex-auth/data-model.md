# Data Model: Multi-mode Codex Authentication

**Feature**: 003-multi-mode-codex-auth
**Spec**: `specs/003-multi-mode-codex-auth/spec.md`
**Created**: 2026-04-15
**Status**: Reviewed

> 003 is **additive** on top of 002's schema. No column is removed, renamed, or re-typed. Every existing API-key row keeps working byte-for-byte.

---

## Entity: `UpstreamAccount` (extends 002)

The existing table `upstream_accounts` gains **eight** nullable columns. Every new column is nullable so the migration does NOT require data backfill — existing 002 rows end up with all NULLs in the new columns and `auth_method = 'api_key'` (defaulted at read-time, see below).

### Final column set (post-003)

| Attribute | Type | Constraints | Added in | Description |
|---|---|---|---|---|
| `id` | INTEGER (SQLite) / BIGSERIAL (PG) / BIGINT AUTO_INCREMENT (MySQL) | PK | 001 | |
| `name` | TEXT | NOT NULL, UNIQUE per-operator | 001 | |
| `provider` | TEXT | NOT NULL, default `'openai'` | 001 | |
| `api_key` | TEXT | **NULL-able (relaxed in 003)** | 001 / relaxed 003 | Plaintext API key for `auth_method='api_key'` rows. NULL for OAuth rows. |
| `base_url` | TEXT | NULL | 001 | |
| `status` | TEXT | NOT NULL, default `'active'`, ∈ {active, disabled, deleted} | 001 | |
| `created_at` | DATETIME / TIMESTAMPTZ | NOT NULL, default `CURRENT_TIMESTAMP` | 001 | |
| `updated_at` | DATETIME / TIMESTAMPTZ | NOT NULL, default `CURRENT_TIMESTAMP`, bumped on row update | 001 | |
| **`auth_method`** | TEXT | NOT NULL, default `'api_key'`, ∈ {api_key, oauth_browser, oauth_device, oauth_import} | **003** | Discriminator between the two credential shapes. |
| **`access_token`** | BLOB / BYTEA / VARBINARY(8192) | NULL | **003** | Sealed-bytes-ready; plaintext JWT in 003 per FR-003. NULL for `auth_method='api_key'`. |
| **`refresh_token`** | BLOB / BYTEA / VARBINARY(8192) | NULL | **003** | Sealed-bytes-ready; plaintext JWT in 003 per FR-003. NULL for `auth_method='api_key'`. |
| **`id_token`** | BLOB / BYTEA / VARBINARY(8192) | NULL | **003** | Sealed-bytes-ready; plaintext JWT in 003 per FR-003. NULL for `auth_method='api_key'`. |
| **`last_refresh`** | TIMESTAMP(6) / TIMESTAMPTZ / TEXT (ISO8601 UTC) | NULL | **003** | Wall-clock at last successful refresh (or at onboard if never refreshed). NULL for `auth_method='api_key'`. Timezone-aware on disk in PG/MySQL (see §Timestamp types); SQLite stores as ISO-8601 text with explicit `Z` suffix. |
| **`access_expires_at`** | TIMESTAMP(6) / TIMESTAMPTZ / TEXT (ISO8601 UTC) | NULL | **003** | Absolute wall-clock at which `access_token` is known (per the most-recent provider response) to cease being accepted by upstream. Computed as `last_refresh + provider_returned_expires_in_seconds` at the moment of persist; **never** `now() + 45min`-style client fallback unless the provider flatly refuses to return `expires_in` (see §Derivation fallback). Drives FR-004's "refresh when older than half of `expires_in`" rule — concretely, `RefreshIfStale` decides "is a refresh needed now?" via `clock.Now().After(access_expires_at.Add(-coord.safetyMargin))` where `safetyMargin = (access_expires_at - last_refresh) / 2`. Storing the absolute time (not the remaining-lifetime integer) makes the decision stable across router restarts — a restart does NOT reset the clock, so the staleness judgement survives. NULL for `auth_method='api_key'`. |
| **`email`** | TEXT | NULL | **003** | Decoded from the `id_token` `email` claim (RFC 7519 standard claim). NULL for `auth_method='api_key'`. |
| **`plan_type`** | TEXT | NULL | **003** | Decoded from the id_token's OpenAI-custom claim `claims["https://api.openai.com/auth"]["plan_type"]` (URL-style custom claim whose value is an object). If either the URL-style claim or the inner `plan_type` key is absent, falls through to the top-level `auth.plan_type` subobject (codex-lb compatibility), otherwise NULL. NULL for `auth_method='api_key'`. |
| **`chatgpt_account_id`** | TEXT | NULL | **003** | Derivation probes three paths in order, first-hit-wins (matching `contracts/accounts-api.md` §`POST /accounts/import-auth-json`): (1) `id_token` claim `claims["https://api.openai.com/auth"]["chatgpt_account_id"]`, (2) `id_token` claim `claims["auth"]["chatgpt_account_id"]` (codex-lb compatibility), (3) for `auth_method='oauth_import'` only, the top-level `tokens.account_id` field of the imported `auth.json` payload. NULL when none of the three paths resolves. NULL for `auth_method='api_key'`. |

### Row-level invariants (enforced in `internal/domain/account.go`, NOT in SQL)

1. **Exactly-one credential shape per row**:
   - `auth_method = 'api_key'` ⇒ `api_key IS NOT NULL` AND **all** of `{access_token, refresh_token, id_token, last_refresh, access_expires_at, email, plan_type, chatgpt_account_id}` MUST be NULL.
   - `auth_method ∈ {'oauth_browser', 'oauth_device', 'oauth_import'}` ⇒ `api_key IS NULL` AND **at minimum** `{access_token, refresh_token, id_token, last_refresh, access_expires_at}` MUST be NOT-NULL. (`email`, `plan_type`, `chatgpt_account_id` MAY be NULL if the provider happens not to return them in the id_token — we tolerate partial metadata, not partial credentials.)
2. **Defence-in-depth enum check**: `auth_method` is CHECK-constrained in **both PostgreSQL and MySQL 8.0.16+** (which is the minimum MySQL version targeted by `AGENTS.md`; MySQL 8.0.16 is the release that first enforces `CHECK` constraints rather than silently ignoring them). SQLite is left with plain TEXT + no CHECK because the 003 migration already uses `PRAGMA writable_schema` to relax the `api_key NOT NULL` constraint in place, and mixing a `PRAGMA writable_schema` CREATE-statement rewrite with an added CHECK on the same column would require a second round of surgical regex on `sqlite_master` and is not worth the fragility cost. `status` remains uncheck-constrained everywhere — the Go domain layer is the single source of truth for valid values, and the DB-level CHECK on `auth_method` is purely belt-and-suspenders so a future code path that bypasses `domain.Validate()` still fails loudly at INSERT/UPDATE rather than silently corrupting rows.
3. **`auth_method` is immutable within a row**: US-5 rotation never changes `auth_method` — an API-key row stays API-key, an OAuth row stays OAuth. Moving between shapes requires a new row.

### Timestamp types (`last_refresh`, `access_expires_at`)

The two OAuth-only timestamp columns are deliberately **timezone-aware** on every dialect so that `RefreshIfStale` comparisons remain correct across router restarts and under session-timezone drift:

| Dialect | Physical type | Stored as | Notes |
|---|---|---|---|
| PostgreSQL | `TIMESTAMPTZ` | UTC | Native tz-aware type; PG converts from the client session zone on write, always returns UTC. |
| MySQL 8.0+ | `TIMESTAMP(6)` | UTC | MySQL's only tz-aware type. Converts session-local to UTC on write, UTC back to session-local on read. Microsecond precision matches 001's `DATETIME(6)`. `explicit_defaults_for_timestamp=ON` (MySQL 8.0 default) keeps these columns NULL-defaultable — we state `NULL DEFAULT NULL` explicitly to survive a server configured otherwise. The 2038 TIMESTAMP ceiling is irrelevant: `last_refresh` is rewritten on every refresh and `access_expires_at` is provider-capped at a few hours/days. |
| SQLite | `DATETIME` (TEXT affinity) | ISO-8601 text with explicit `Z` suffix | SQLite types are purely advisory. xorm marshals `time.Time` to/from ISO-8601; our store helpers ensure `.UTC()` is applied before every write so the stored string always ends `Z` and round-trips deterministically. |

**Contrast with 001's `created_at` / `updated_at`**: those pre-date 003 and stay `DATETIME(6)` (MySQL) / `TIMESTAMPTZ` (PG) for backward compatibility. They are only ever written as `CURRENT_TIMESTAMP(6)` at the server, so the dialect mismatch is harmless. The 003 OAuth timestamps differ: Go code writes them as explicit `time.Time` values computed from provider responses, so we need the DB to normalise to UTC rather than trust the session zone.

**Implication for Go callers**: treat any `time.Time` returned from `internal/store/accounts.go` as UTC (use `.UTC()` defensively before formatting), and always pass `.UTC()` times on the write side. A dedicated test `TestAccountsStore_UpdateCredentials_OAuthRefresh` asserts round-trip precision.

### `access_expires_at` derivation fallback (for `auth_method='oauth_import'`)

The three provider-driven rows (`oauth_browser`, `oauth_device`) always receive `expires_in` on `/oauth/token` / `/deviceauth/token` responses, so the coordinator computes `access_expires_at = response_received_wallclock + expires_in_seconds` and writes it in the same store call that lands the tokens.

`oauth_import` uploads an `auth.json` file that was written by the Codex CLI — that file does **not** carry an `expires_in` integer. The import handler MUST derive `access_expires_at` from one of these sources, first-hit-wins:
1. **`id_token` `exp` claim** (RFC 7519 standard) — typically ~1h out. Preferred because it reflects the provider's own notion of "this token is valid until T".
2. **Last-resort conservative fallback**: `now() + 5 minutes`. This forces a refresh on the next request (triggering US-4's code path), which is the safest behaviour when we have no upstream signal for validity — a stale 5-min window that forces a real refresh can never be more wrong than pretending the token is good for an hour.

The fallback choice is logged as `auth_json_import_last_refresh_fallback` WARN (reason `id_token_exp_absent` → fell through to fallback 2) so operators can see that the import row will refresh aggressively until the first real refresh re-anchors the column.

`RefreshIfStale` treats rows with `access_expires_at IS NULL` as a hard error (domain invariant violation) — the migration + store helpers must guarantee every OAuth row gets a non-NULL value even in edge cases.

### Indexes added in 003

- **`idx_upstream_accounts_auth_method` on `upstream_accounts(auth_method)`** — non-unique, plain B-tree. Added in all three `000002_multi_mode_auth.up.sql` migrations (SQLite, PostgreSQL, MySQL). The admin UI's "filter accounts by auth_method" control (US-1, US-6) does `SELECT … WHERE status='active' AND auth_method=?` at page load on the accounts list, so we index the discriminator column at the migration point rather than waiting for a slow-query report. Cardinality is low (≤ 4 distinct values) but index scan + status-index bitmap-AND is still cheaper than a full table scan as the accounts count grows, and the write cost on a row that is inserted once and rarely updated is negligible. If a future spec deletes the filter control, the index is safe to drop in a later migration.

### Why no *other* new indexes

- Every hot read **other than the auth_method filter** is by `id` (selector lookup) or `status = 'active'` (filter already indexed in 001).
- `email`, `plan_type`, `chatgpt_account_id` are **display-only** in 003 (no dedupe query, no login-by-email). If a future spec adds "find by email" or "find by chatgpt_account_id" we add the index then.
- Tokens (`access_token`, `refresh_token`, `id_token`) MUST NOT be indexed — they are opaque blobs and indexing them would leak token material into the DB's index metadata.

---

## Entity: `OAuthFlow` (in-memory only — NEVER persisted)

Held on `*app.App` as an `atomic.Pointer[oauth.Flow]`. At most one non-nil value exists per router process (FR-008).

| Attribute | Type | Lifetime | Description |
|---|---|---|---|
| `Method` | `"browser"` \| `"device"` | flow start → completion/timeout | Which user-visible flow is running. |
| `ListenerBound` | `bool` | browser flow only | `true` iff `Coordinator.StartBrowser` successfully bound at least one of `{127.0.0.1:1455, [::1]:1455}` (best-effort, dual-stack). `false` means canonical port 1455 was unavailable (port in use, unprivileged container, capability-restricted runtime, etc.) — the flow still starts; only Rail B (manual-paste) can close it. Teardown paths (`ReleaseFlow`, `Cancel`, timeout) use this bit to decide whether `CallbackServer.Close()` needs calling. Surfaced to the UI via `/browser/start` response and `GET /flow` poll for informational display only (FR-012 — UI never branches on it for rendering the paste textarea). |
| `Consumed` | `atomic.Bool` (CAS target) | browser flow only | First-wins race winner between Rail A (loopback) and Rail B (manual-paste). A rail MUST `CompareAndSwap(false, true)` before calling `/oauth/token`; CAS-loss path returns `ErrAlreadyConsumed` (→ envelope `code:3005 already_consumed` at HTTP 200 on Rail B; silent success-page HTML on Rail A, envelope-exempt). Guarantees exactly one upstream code-exchange per flow under sub-second race (FR-012 first-wins CAS invariant). |
| `ConsumedBy` | `"loopback"` \| `"manual_paste"` \| `""` (a.k.a. `RailUnknown`) | browser flow only | Observability label. **Written under `Flow.mu.Lock()` by the CAS winner** immediately after a successful `Consumed.CompareAndSwap(false, true)` — i.e. BEFORE the `/oauth/token` round-trip — so that it is populated even when `/oauth/token` fails and the flow ends in `Status=error`. **Read by CAS losers via `Flow.mu.RLock()`** (NOT a raw field read — the happens-before edge for `ConsumedBy` is through `flow.mu`, not `Consumed`'s CAS). Emitted as the `rail` field on `oauth_flow_completed` (success path), `oauth_flow_failed` (CAS-winner's token exchange failed), and `oauth_flow_cancelled` (only when the cancel was triggered by `error=access_denied` arriving on a rail; `/cancel` hits omit `rail`). Empty string until a rail wins; never cleared once set. Also echoed in `GET /flow` `success` payload. |
| `State` | `string` (43 chars) | browser flow only | CSRF token for the authorize → callback round-trip. Generated as 32 bytes from `crypto/rand.Read` then `base64.RawURLEncoding.EncodeToString` → 43-char URL-safe string. Compared constant-time on whichever rail fires (FR-006). |
| `CodeVerifier` | `string` (43 chars) | browser flow only | PKCE verifier; same generator as `State` (32 bytes crypto/rand → base64url, 43 chars; within RFC 7636 §4.1 `43..128`). `code_challenge = base64url(sha256(verifier))` is sent on `/oauth/authorize` with `code_challenge_method=S256`. Used identically by either rail on code-exchange. |
| `DeviceAuthID` | `string` | device flow only | Handed back to OpenAI on every `…/deviceauth/token` poll. |
| `UserCode` | `string` | device flow only | Shown to the operator so they can type it on their laptop. |
| `VerificationURL` | `string` | device flow only | URL the operator opens on their laptop. |
| `ExpiresAt` | `time.Time` | both flows | Hard deadline — on hit, the flow is GC'd and reset to `idle`. Default 5 min (browser) / 15 min (device). |
| `PollInterval` | `time.Duration` | device flow only | Provider-returned interval; the poller `time.Tick`s on this. |
| `CallbackServer` | `*http.Server` \| `nil` | browser flow when `ListenerBound=true` | Loopback listener wired to `127.0.0.1:1455` and/or `[::1]:1455` (dual-stack). Closed on success / timeout / cancel. **NIL when `ListenerBound=false`** — no listener was bound, nothing to close. |
| `Status` | `"pending"` \| `"success"` \| `"error"` \| `"idle"` | full flow | Reported on the `GET /api/admin/oauth/flow` status endpoint. Mutations are serialised through `Coordinator` (see *Concurrency model* below); only the CAS-winner or the coordinator's reaper/cancel path writes this field. |
| `CreatedAt` | `time.Time` | full flow | Used to compute "already running, started 27s ago" in the envelope `code:3001 oauth_flow_in_progress` response body (HTTP 200). |
| `TargetAccountID` | `int64` (0 = none) | full flow | Non-zero iff the flow was opened by `POST /api/admin/accounts/{id}/reauth` (US-5, FR-010). When non-zero the success-path `store` call is a **credential-only UPDATE** against this `id` (keeping `name`/`provider`/`base_url`/routing stats untouched); when zero the success-path is an `InsertUpstreamAccount`. Never exposed on the wire except as an echo in the HTTP 200 envelope `data.target_account_id` field of `/reauth` (see `contracts/accounts-api.md`). |

### Concurrency model (how mutations are serialised)

Five goroutines can touch a live Flow simultaneously: (1) Rail A loopback handler, (2) Rail B `/browser/manual-callback` handler, (3) device-code poller, (4) expiry reaper (goroutine `time.AfterFunc(ExpiresAt-time.Now())`), (5) `/cancel` handler. They coordinate through exactly two synchronisation primitives — both owned by the `oauth.Coordinator`:

1. **`Coordinator.flowPtr atomic.Pointer[Flow]`** — guards *whether a flow exists*. `TryStartFlow` is the only writer that sets nil → non-nil; `ReleaseFlow` is the only writer that sets non-nil → nil. Readers (e.g. `/flow` status handler) `.Load()` and operate on the returned pointer; a `nil` load ⇒ `status=idle`.
2. **Per-flow `sync.Mutex` (field `Flow.mu`, unexported)** — guards every **field write on a live Flow except `Consumed`**. Holders: whichever goroutine is mutating `Status`, `ConsumedBy`, `CallbackServer`, or persisting the resulting row. Readers that need a multi-field snapshot (the `/flow` handler projecting `{status, rail, account}`) acquire `mu.RLock` (promote to `sync.RWMutex` if profile shows lock contention — 003 traffic is tiny; start with `Mutex`).

**`Flow.Consumed atomic.Bool` is the ONLY field mutated without `Flow.mu`.** Its CAS is the serialisation point between Rail A and Rail B; the CAS winner then acquires `Flow.mu` to set `Status=success` + `ConsumedBy=<rail>` + close the listener, all under the same lock, guaranteeing readers see all three transitions together.

**Invariant**: a goroutine that loses the `Consumed` CAS MUST NOT subsequently acquire `Flow.mu` to mutate the Flow — its only job is to return `ErrAlreadyConsumed` to its caller. This keeps the winner's write path contention-free.

**Reaper / cancel ordering**: the expiry reaper and `/cancel` handler both attempt `Flow.Consumed.CompareAndSwap(false, true)` before transitioning to `Status=error`. If a rail has already CAS-won, the reaper/cancel short-circuits to a no-op (the winner's success persist has already happened or is in-flight under `Flow.mu`; cancelling a mid-`/oauth/token` call is best-effort per `plan.md` §Risk table — we do not interrupt the in-flight HTTP round-trip).

### State transitions

```
idle --start(browser)--------------> pending --rail_A_loopback_CAS_win------> success --persist_row--> idle
                                            \-rail_B_manual_paste_CAS_win--> success --persist_row--> idle
idle --start(device)----------------> pending --device_poll_ok--------------> success --persist_row--> idle
pending --timeout------------------------------> error ------------------------------------------> idle
pending --cancel-------------------------------> error ------------------------------------------> idle
pending --rail_state_mismatch (browser)--------> pending  (this rail's hit rejected; other rail can still close;
                                                          oauth_rail_rejected INFO with error_code=oauth_state_mismatch + rail=…)
pending --rail_CAS_loses (browser)-------------> pending  (the other rail already CAS-won; this rail short-circuits
                                                          to its callback UX: loopback → success-page HTML, paste → envelope code:3005 already_consumed;
                                                          NOT a state transition — flow is already heading to success)
pending --refresh_http_5xx (device poll)-------> pending  (retry until ExpiresAt)
```

### What is NOT persisted anywhere — ever

These bytes live only inside the `OAuthFlow` struct while Status=`pending`. A router restart drops the struct; no disk trace remains.
- `CodeVerifier`
- `State`
- `DeviceAuthID` (only the *resulting tokens* land on disk)
- `UserCode`
- The raw body of any imported `auth.json` file (only the extracted columns land)

---

## Migration Strategy

One migration file per dialect, under `internal/store/migrations/{sqlite,postgres,mysql}/000002_multi_mode_auth.up.sql` (+ `.down.sql`).

### Forward migration (SQLite — template)

```sql
-- 002 → 003: add multi-mode auth columns to upstream_accounts.
-- Every new column is nullable. Zero backfill: existing rows end up
-- auth_method='api_key' via the DEFAULT, and the seven new columns
-- stay NULL (which, per domain invariant 1, is the legal shape for
-- api_key rows).

-- SQLite ≤3.34 can only ALTER TABLE ADD COLUMN one at a time:
ALTER TABLE upstream_accounts ADD COLUMN auth_method         TEXT    NOT NULL DEFAULT 'api_key';
ALTER TABLE upstream_accounts ADD COLUMN access_token        BLOB    NULL;
ALTER TABLE upstream_accounts ADD COLUMN refresh_token       BLOB    NULL;
ALTER TABLE upstream_accounts ADD COLUMN id_token            BLOB    NULL;
ALTER TABLE upstream_accounts ADD COLUMN last_refresh        DATETIME NULL;
ALTER TABLE upstream_accounts ADD COLUMN access_expires_at   DATETIME NULL;
ALTER TABLE upstream_accounts ADD COLUMN email               TEXT    NULL;
ALTER TABLE upstream_accounts ADD COLUMN plan_type           TEXT    NULL;
ALTER TABLE upstream_accounts ADD COLUMN chatgpt_account_id  TEXT    NULL;

-- Relax api_key from NOT NULL to NULL. SQLite doesn't support
-- ALTER COLUMN directly; the migrator uses the table-rename dance:
-- rename old -> create new with new schema -> copy data -> drop old.
-- See internal/store/migrations/sqlite/000002_multi_mode_auth.up.sql
-- for the full ceremony.
```

### Forward migration (PostgreSQL)

```sql
ALTER TABLE upstream_accounts
    ADD COLUMN auth_method         TEXT    NOT NULL DEFAULT 'api_key',
    ADD COLUMN access_token        BYTEA   NULL,
    ADD COLUMN refresh_token       BYTEA   NULL,
    ADD COLUMN id_token            BYTEA   NULL,
    ADD COLUMN last_refresh        TIMESTAMPTZ NULL,
    ADD COLUMN access_expires_at   TIMESTAMPTZ NULL,
    ADD COLUMN email               TEXT    NULL,
    ADD COLUMN plan_type           TEXT    NULL,
    ADD COLUMN chatgpt_account_id  TEXT    NULL,
    ADD CONSTRAINT chk_auth_method CHECK (auth_method IN ('api_key','oauth_browser','oauth_device','oauth_import'));

ALTER TABLE upstream_accounts ALTER COLUMN api_key DROP NOT NULL;
```

### Forward migration (MySQL)

```sql
ALTER TABLE upstream_accounts
    ADD COLUMN auth_method         VARCHAR(32) NOT NULL DEFAULT 'api_key',
    ADD COLUMN access_token        VARBINARY(8192) NULL,
    ADD COLUMN refresh_token       VARBINARY(8192) NULL,
    ADD COLUMN id_token            VARBINARY(8192) NULL,
    ADD COLUMN last_refresh        TIMESTAMP(6) NULL DEFAULT NULL,
    ADD COLUMN access_expires_at   TIMESTAMP(6) NULL DEFAULT NULL,
    ADD COLUMN email               VARCHAR(320) NULL,
    ADD COLUMN plan_type           VARCHAR(64) NULL,
    ADD COLUMN chatgpt_account_id  VARCHAR(128) NULL,
    ADD CONSTRAINT chk_auth_method
        CHECK (auth_method IN ('api_key','oauth_browser','oauth_device','oauth_import')),
    MODIFY COLUMN api_key TEXT NULL;
```

### Rollback

Symmetric `DROP COLUMN` (Postgres/MySQL) / table-rename dance (SQLite). Any OAuth row written under 003 is **lost on rollback** — the whole token triplet is the feature and there is nowhere it can survive the rollback. This is acceptable because:
- 003 is additive and opt-in (no operator auto-upgrades to OAuth).
- The rollback is explicitly a destructive operation the operator invoked via `one-llm-router migrate down`; the rollback message warns them first.
- Rolling forward after a rollback requires the operator to re-run OAuth per account — a minor inconvenience, not data loss of anything they didn't choose to roll back.

### Data compatibility

- **Read-side (pre-003 clients)**: impossible — the 003 binary is the only reader; 002 binaries never see the new columns.
- **Read-side (003 binary against a non-migrated 002 DB)**: blocked. `one-llm-router migrate up` runs at every boot (002 behavior); if the operator somehow skipped it, the 003 binary refuses to start with a clear error ("upstream_accounts missing required column `auth_method`").
- **Zero-regression for 002 API-key rows**: existing rows appear post-migration with `auth_method='api_key'`, all new columns NULL. Every 002 code path — the selector, `ForwardRequest`, `/api/admin/accounts` — reads them identically to 002.

---

## Derived read-side projection: `AccountListItem` (admin API response)

This is what `GET /api/admin/accounts` returns **per row** (contract details in `contracts/accounts-api.md`):

```json
{
  "id": 42,
  "name": "prod-plus",
  "provider": "openai",
  "auth_method": "oauth_browser",
  "status": "active",
  "base_url": "https://api.openai.com",
  "created_at": "2026-04-15T09:14:00Z",
  "updated_at": "2026-04-15T10:02:17Z",

  "email":              "alice@example.com",   // omit for api_key rows
  "plan_type":          "chatgpt-plus",         // raw; UI applies human-readable label per FR-011a
  "plan_type_label":    "ChatGPT Plus",         // server-side computed human label, so every UI agrees
  "chatgpt_account_id": "org_7f2b9a3e",         // omit for api_key rows; NOT redacted here because the API response is already behind admin auth
  "last_refresh":       "2026-04-15T10:02:17Z", // omit for api_key rows
  "access_expires_at":  "2026-04-15T11:02:17Z"  // omit for api_key rows; absolute wall-clock per FR-004
}
```

**Non-returned fields** (FR-003 + FR-011): `api_key`, `access_token`, `refresh_token`, `id_token`. These are gated **server-side** inside the repo's `ListForAdminAPI` query — they are `SELECT`ed and dropped before JSON-marshal; no request-handler-level dance. The one legitimate exit path for token bytes is the dedicated `POST /api/admin/accounts/{id}/export-auth-json` endpoint (see `contracts/accounts-api.md`).

---

## Derived read-side projection: `AuthJSON` (export-auth-json response)

This is the **exact** shape the FR-014 `POST /api/admin/accounts/{id}/export-auth-json` endpoint returns, matching the on-disk `~/.codex/auth.json` format that the Codex CLI itself produces:

```json
{
  "OPENAI_API_KEY": null,
  "tokens": {
    "id_token":       "eyJhbGciOi…",
    "access_token":   "eyJhbGciOi…",
    "refresh_token":  "rt_…",
    "account_id":     "org_7f2b9a3e"
  },
  "last_refresh": "2026-04-15T10:02:17Z"
}
```

Rationale: an operator who exports `auth.json` on router A and drops it into router B's `auth.json` import (US-6) MUST get byte-compatible onboarding. Matching the Codex CLI schema verbatim is the only way that works.

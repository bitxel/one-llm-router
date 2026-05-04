# Research: Multi-mode Codex Authentication

**Feature**: 003-multi-mode-codex-auth
**Spec**: `specs/003-multi-mode-codex-auth/spec.md`
**Created**: 2026-04-15
**Status**: Reviewed

Reference implementations: the current official `openai/codex` browser/device auth flow is authoritative for live OAuth parameters; `~/app/project/codex-lb` remains a compatibility reference for imported `auth.json` shape and account metadata extraction. 003 is an additive **port** into Go, not a re-derivation.

---

## Decision 1: Reuse the current Codex CLI OpenAI OAuth endpoint surface verbatim

- **Decision**: The Go OAuth client in 003 will call the *exact* endpoints, `client_id`, `scope`, `originator`, and redirect URI shape that the current official Codex CLI calls. No renegotiation with OpenAI; no "let's try a different client_id".
- **Rationale**:
  - The OpenAI OAuth surface is not a generic public OAuth integration. Matching the first-party Codex CLI keeps the router on the same server-side allow-list semantics as the tool it centralises.
  - Re-deriving it from scratch buys nothing except risk of landing on a deprecated client_id or missing the `id_token_add_organizations=true` param (which is what unlocks `chatgpt_account_id` in the id_token — required by FR-002 / FR-011a).
  - `auth.json` file imports (US-6) remain compatible because the router still stores and refreshes the same access/refresh/id-token triplet.
- **Concrete endpoint bag** (checked against `openai/codex` `codex-rs/login/src/server.rs` and `codex-rs/login/src/auth/default_client.rs`, 2026-04-23):

| Field | Value | Source |
|---|---|---|
| `auth_base_url` | `https://auth.openai.com` | `server.rs` default issuer |
| Authorize URL | `GET  {auth_base_url}/oauth/authorize` | `server.rs` `build_authorize_url` |
| Token URL (code exchange + refresh) | `POST {auth_base_url}/oauth/token` (form-url-encoded) | `server.rs` `exchange_code_for_tokens`; current refresh path sends no explicit `scope` |
| Device-code request URL | `POST {auth_base_url}/api/accounts/deviceauth/usercode` (JSON) | current Codex CLI device auth |
| Device-code poll URL | `POST {auth_base_url}/api/accounts/deviceauth/token` (JSON) | current Codex CLI device auth |
| Device `verification_url` (shown to operator) | `{auth_base_url}/codex/device` | current Codex CLI device auth |
| `client_id` | `app_EMoamEEZ73f0CkXaXp7hrann` | `server.rs` `ServerOptions.client_id` default |
| `originator` | `codex_cli_rs` | `default_client.rs` `DEFAULT_ORIGINATOR` |
| `scope` | `openid profile email offline_access api.connectors.read api.connectors.invoke` | `server.rs` `build_authorize_url` |
| `redirect_uri` (default) | `http://localhost:1455/auth/callback` | `server.rs` default callback port; router pins to the canonical registered redirect URI |
| Extra query params on `/oauth/authorize` | `id_token_add_organizations=true`, `codex_cli_simplified_flow=true`, `code_challenge_method=S256`, `response_type=code` | `server.rs` `build_authorize_url` |
| Refresh payload | form-url-encoded, `grant_type=refresh_token`, `client_id=…`, `refresh_token=…`; no explicit `scope` field | current Codex CLI refresh path |
| Device-code poll "pending" signal | Error code `authorization_pending` or `slow_down`, OR status `pending`/`authorization_pending` — poller keeps going | current Codex CLI-compatible behavior |
| Device-code `interval` fallback | When the provider's `usercode` response returns `interval = 0` or omits the field, poll every **5 seconds** per RFC 8628 §3.5 default. `slow_down` responses from the poll double the next interval until the next successful poll (also RFC 8628 §3.5). | RFC 8628 §3.5; codex-lb returns 0 on null and uses a provider-side default |

- **Alternatives Considered**:

  | Alternative | Pros | Cons | Why Rejected |
  |---|---|---|---|
  | Use the PlatformAPI `https://api.openai.com` OAuth path (generic developer OAuth app) | First-party-documented | Does NOT issue a ChatGPT-plan `chatgpt_account_id`; the issued tokens can't call `/v1/responses` against Plus/Team accounts | Defeats US-1's whole reason to exist |
  | Register a NEW `one-llm-router` OAuth client_id with OpenAI | "Looks more legit" to OpenAI | Subscription-backed OAuth apps are gated; requesting a new one is weeks of paperwork and doesn't change runtime behavior | Out of scope for shipping 003 |
  | Proxy the OAuth dance through a central hosted service | Hides the client_id | Adds a new service the operator must trust; violates "single-process, self-hosted" posture of 001/002 | Violates Constitution Simplicity Gate |

---

## Decision 2: HTTP callback server pattern (US-1) — loopback-only, canonical port, 5-minute budget

- **Decision**: The OAuth browser flow **best-effort** starts a **loopback-only** listener on canonical port `1455` — this is Rail A of the FR-012 dual-rail. If 1455 is busy OR the host is capability-restricted (unprivileged container, pledged runtime, etc.), binding silently fails, `Flow.ListenerBound=false`, and the flow continues on Rail B (manual-paste via `POST /browser/manual-callback`) alone. There is **no 500 error on bind failure** — loopback unavailability is a normal operating state, not an exception. When bound, the listener serves **exactly one** meaningful path (`GET /auth/callback`); any other path returns `404 Not Found` with no banner. It receives the authorization `code` + `state`, hands them to `Coordinator.ConsumeCode(rail="loopback")` (which CAS-guards the code-exchange path against the paste rail), writes a minimal success HTML back to the browser, and shuts down. The listener's total lifetime is capped at 5 minutes; if the callback never arrives, the flow times out, the in-memory state is cleared, and the listener is `Close`d.
- **Listener binding detail**: the OAuth callback uses the same loopback URI shape as Codex CLI's registered default: `http://localhost:1455/auth/callback` — `localhost`, not `127.0.0.1`, and no fallback port in either `/oauth/authorize` or `/oauth/token`. To tolerate hosts where `localhost` resolves to IPv6 first (`::1`) as well as IPv4-first hosts, the Go implementation opens **two** `net.Listener`s wired to one `*http.Server`: `127.0.0.1:1455` **and** `[::1]:1455`. Binding succeeds if EITHER listener binds (both is the happy path). `Flow.ListenerBound` records whether ANY family on 1455 succeeded. The `callback_url` exposed over the admin API — and baked into the authorize URL we send to OpenAI — is always `http://localhost:1455/auth/callback`, regardless of whether the listener actually bound (the operator's browser will either land on the live listener, another local process' response, or see connection-refused and use the paste rail).
- **Rationale**:
  - Matches `codex-lb`'s + the Codex CLI's behavior — operators already know "log in on your laptop, browser lands on `localhost:1455/auth/callback`". Going off-pattern here means retraining users for zero benefit.
  - Loopback-only eliminates the CSRF / SSRF attack surface: nothing external can hit the listener, and the OAuth `state` token covers tab-hijack by the local browser.
  - Pinning the port avoids sending an unregistered fallback redirect URI to OpenAI, which can fail before the callback reaches the router. On port-busy (or container-without-cap) the FR-012 dual-rail guarantees no operator gets wedged — the paste rail closes the flow with zero operator retraining (they still copy the URL from their address bar just like they would on a remote-router deploy).
  - Dual `127.0.0.1:1455` + `[::1]:1455` bind prevents the "IPv6-first host → browser resolves `localhost` to `::1` → connection refused because the listener was bound only on 127.0.0.1" foot-gun that Codex CLI has occasionally hit.
- **Alternatives Considered**:

  | Alternative | Pros | Cons | Why Rejected |
  |---|---|---|---|
  | Register a public `https://router.example.com/auth/callback` | Works for remote-SSH out of the box | Requires operator-owned HTTPS cert; leaks the `state`/`code` through proxies; can't be the default | Ship device-code (US-3) + manual-paste (FR-012) instead |
  | Write the callback into a shared pipe / FIFO | No port bind needed | Browser can't POST to a pipe; requires a relay anyway | Off-protocol |
  | Long-poll from the browser to the router's admin API | Reuses existing admin auth | OpenAI insists on redirecting to an HTTP endpoint; you can't substitute a long-poll | Doesn't match the RFC |

---

## Decision 3: Single-flight refresh — per-account `singleflight.Group` keyed on `account_id`

- **Decision**: Use `golang.org/x/sync/singleflight.Group` keyed on the `UpstreamAccount.ID` for the refresh call. The refresh path is called **before** the forwarder constructs the upstream `Authorization` header, inside the Codex protocol adapter's "select account → refresh if stale → forward" sequence (FR-009 forbids adapter-side caching of the bearer).
- **Rationale**:
  - Spec FR-004 mandates single-flight; `singleflight` is the stdlib-adjacent Go idiom that already ships with every Go project (it lives under `golang.org/x/sync`, a de-facto stdlib extension). No new third-party dep beyond that.
  - Keying on `account_id` (not a mutex per selector) means we don't serialize *unrelated* accounts; only duplicate refreshes on the same account collapse.
  - `singleflight.Do` returns the shared result to every waiter, which is exactly what FR-004 describes ("both requests see the fresh token").
- **Alternatives Considered**:

  | Alternative | Pros | Cons | Why Rejected |
  |---|---|---|---|
  | `sync.Mutex` per account, held across the refresh HTTP call | Dead simple | Blocks every concurrent request for the duration of the refresh (~700ms); defeats the point | Bad latency distribution |
  | Central refresh goroutine pulling from a channel | Decouples producers from consumers | Operator pain during restart — the channel is lost; also makes error propagation ugly | Over-engineered |
  | No dedup; idempotent refresh at upstream | Zero client code | OpenAI's token endpoint is NOT idempotent — a concurrent `grant_type=refresh_token` can return *different* tokens and invalidate the older one; we'd see random 401s | Violates SC-2 |

---

## Decision 4: One router-wide `OAuthFlow` state — in-memory, no persistence

- **Decision**: Exactly **one** `OAuthFlow` may be in flight per router process (FR-008). The in-flight flow is held on `*App` as `atomic.Pointer[oauth.Flow]`; a second start returns envelope `code:3001 oauth_flow_in_progress` (HTTP 200 per project-wide envelope policy) with `method` + `expires_at` in `data`. On router restart, the in-memory state is lost — any mid-flight OAuth is dropped; the operator retries.
- **Rationale**:
  - Consistent with the 001/002 "single-process, one router instance" model.
  - Persistence would require thinking about leaking `code_verifier` + `state` to disk — both of which the spec explicitly says NOT to persist (Storage Layout §"What is NOT persisted").
  - A second start is an operator mistake 99% of the time (they clicked *Start* twice). Returning `code:3001 oauth_flow_in_progress` (HTTP 200 + envelope) with the in-flight flow's expiry lets the UI show "already running, expires in 4m32s".
- **Alternatives Considered**:

  | Alternative | Pros | Cons | Why Rejected |
  |---|---|---|---|
  | Per-operator flow (N parallel flows) | More UX-forgiving with multiple admins | The loopback callback server is process-singleton; N parallel flows would conflict on port / state | Port conflict is architectural |
  | DB-persisted OAuthFlow row | Survives restart | `code_verifier` / `state` must land in DB; enlarges the "tokens on disk" trust boundary; also impossible to safely resume a browser-bound flow whose browser already closed | Violates FR-003's redaction posture |

---

## Decision 5: Token columns are **BLOB / BYTEA / VARBINARY**, not TEXT

- **Decision**: The three token columns `access_token`, `refresh_token`, `id_token` are typed as opaque-bytes in every dialect:
  - SQLite: `BLOB NULL`
  - PostgreSQL: `BYTEA NULL`
  - MySQL: `VARBINARY(8192) NULL` (access-token length is well under 4KB; 8KB ceiling gives 2× headroom)
  - Go-side: `[]byte` (not `string`).
- **Rationale** (mandated by spec FR-003 + NFR "sealed-bytes-ready column shape"):
  - In 003 the bytes are plaintext UTF-8 JWTs. That is allowed — 002 took the same posture for `api_key`.
  - In the future key-vault plugin, the same column will hold AEAD-sealed bytes (nonce + ciphertext + tag). Sealed output is NOT valid UTF-8; a `TEXT`/`VARCHAR` column would corrupt it under some drivers.
  - Typing as bytes from day 1 means the vault plugin lands **without** a schema migration and without a read-side branch on the column type.
- **Alternatives Considered**:

  | Alternative | Pros | Cons | Why Rejected |
  |---|---|---|---|
  | `TEXT` columns (simpler SQL) | Easier to eyeball in `sqlite3` CLI during debugging | Must migrate later — and we've already committed to sealed-bytes-ready shape in FR-003 | Violates FR-003 explicitly |
  | `TEXT` with base64 encoding | Still eyeball-friendly | Wastes 33% size and bakes an encoding choice into the schema | Pointless layer |

---

## Decision 6: `id_token` claims extraction is JWT-decode **without** signature verification

- **Decision**: To surface `email` / `plan_type` / `chatgpt_account_id` (FR-002, FR-011a), the router splits the `id_token` on `"."`, **base64url-decodes** (`base64.RawURLEncoding`, i.e. URL-safe alphabet `A-Za-z0-9-_` with NO padding — per RFC 7515 §3) the middle segment, and `encoding/json` parses it into the claim struct defined in Decision 9's table. It does NOT verify the JWT signature.
- **Rationale**:
  - The `id_token` was just handed to us over TLS by `auth.openai.com` inside the very same OAuth exchange. We already trust the channel. Re-verifying the signature would require fetching + caching OpenAI's JWKS and pinning a verification key — two new failure modes.
  - `codex-lb` takes the identical shortcut (`extract_id_token_claims` — no signature verify). This behavior is production-battle-tested.
  - In 003 the router NEVER forwards the `id_token` to any downstream service, so a spoofed `id_token` has no authority — it could only mislead the operator's UI about which email they bound (which they will immediately notice the next time a `/v1/responses` call against that account fails auth upstream).
- **Alternatives Considered**:

  | Alternative | Pros | Cons | Why Rejected |
  |---|---|---|---|
  | Full JWKS-backed `id_token` verification with key rotation | Textbook-correct | Adds a new HTTP dependency (JWKS endpoint) + key cache + offline-edge failure case; no security uplift given the channel-of-receipt trust | Cost > benefit |

---

## Decision 7: `auth.json` import (US-6) has a dual-layer body cap — outer 64 KB envelope + inner 16 KB `auth_json` part

- **Decision**: The `POST /api/admin/accounts/import-auth-json` endpoint uses a **dual-layer** body cap for its `multipart/form-data` payload:
  - **Outer cap**: `http.MaxBytesReader(w, r.Body, 64*1024)` — wraps the entire request body BEFORE `r.ParseMultipartForm`, so a preamble-flood attack (attacker stuffs 2 MB of junk before the boundary) is rejected in O(1) memory instead of parsed into memory. 64 KB is sized **generously** so legitimate multipart clients NEVER trip it — a single `auth_json` part's framing is 300–500 bytes, leaving ~48 KB of preamble/boundary-padding headroom before the OOM defence kicks in. The response envelope carries `data={scope:"envelope", limit_bytes:65536}`.
  - **Inner cap**: `io.LimitReader(part, 16*1024+1)` on the `auth_json` part content specifically, where the extra `+1` lets us detect "read exactly `16*1024+1` bytes" as an overflow sentinel (`io.EOF` at N = tolerable; N+1 = overflow). The response envelope carries `data={scope:"part", limit_bytes:16384}`.
  - This is a deliberate relaxation of 002's 8 KB admin cap for this one route — both the inner (semantic payload) and outer (attacker-defence) layers are strictly larger.
- **Rationale**:
  - A real `~/.codex/auth.json` from a live Codex CLI session hovers ~4–6 KB (three JWTs + `last_refresh` + a few metadata keys); the 99th-percentile is ~10–12 KB (multi-org claim + large plan metadata). 002's 8 KB global cap is uncomfortably close to the median, not the 99th percentile.
  - **16 KB inner cap** gives 2× headroom on the observed maximum (12 KB), leaving margin for near-term claim growth (OpenAI adding another organization tier, etc.) without over-sizing the semantic budget we promise operators.
  - **64 KB outer cap** is defence-only: it exists solely to bound memory consumption of `mime/multipart` against preamble-flood. Anything smaller than 32 KB risks legitimate clients tripping it on long multipart boundaries (RFC-permitted up to 70 chars) or extra part headers; anything larger than 128 KB opens excess OOM surface. 64 KB is the balanced midpoint.
  - Both caps MUST be in place: inner cap alone is insufficient (preamble-flood would still OOM); outer cap alone would let a tight-packed-but-oversized `auth_json` part sneak through.
  - Overflow on EITHER layer returns the envelope `code:2009 request_body_too_large` + HTTP 200 (per project policy — NOT 413) with `data.scope` discriminating which layer tripped, so ops dashboards can distinguish preamble-flood attacks (`envelope`) from oversized legitimate payloads (`part`). This is exactly what US-6's edge case #3 specifies.
  - This relaxation is **scoped to this one endpoint** — not the global middleware. No other endpoint gets the bigger cap.
- **Alternatives Considered**:

  | Alternative | Pros | Cons | Why Rejected |
  |---|---|---|---|
  | Keep the 8 KB global cap | Consistency | Real files would occasionally bounce — bad onboarding UX | Fails real-world sizes |
  | Raise the global cap to 16 KB | One fewer knob | Expands the body-size attack surface on *every* admin endpoint for no benefit | Unjustified |
  | Inner-only cap (`io.LimitReader` on the `auth_json` part, no outer `http.MaxBytesReader`) | Simpler | Vulnerable to preamble-flood: attacker sends 10 MB of junk before the multipart boundary; `ParseMultipartForm` reads all of it into memory before reaching the inner cap | Insufficient for adversarial inputs |
  | Outer-only cap (`http.MaxBytesReader` on the full request, no inner `io.LimitReader` on the part) | Simpler | Doesn't attribute overflow to the `auth_json` part specifically; any part (name / provider / junk) going over the 64 KB outer cap would trip the same error without a useful `data.scope`/`data.limit_bytes` discriminator for the caller | Weak observability |
  | Tight 20 KB outer cap (originally proposed) | Smaller attack surface for preamble-flood | Too tight: framing overhead (200–500 bytes) + long RFC-permitted boundaries (70 chars) + hand-rolled clients can exceed it with <4 KB of headroom, causing legitimate users to see `code:2009 scope=envelope` despite sub-16 KB payloads | Legitimate-client false positives |
  | Use HTTP 413 status instead of HTTP 200 + `code:2009` | Semantic fit with RFC 9110 | Violates project-wide envelope policy (`AGENTS.md` §HTTP API Style: `/api/admin/*` NEVER returns 4xx) | Contradicts project invariant |

---

## Decision 8: Export-auth-json (FR-014) is `POST`, not `GET`

- **Decision**: The export endpoint is `POST /api/admin/accounts/{id}/export-auth-json`. It returns `Content-Type: application/json` + `Content-Disposition: attachment; filename="auth.json"`. No query string / no URL-level idempotency.
- **Rationale**:
  - `POST` keeps the call out of browser-history / proxy-access-log URLs (a `GET` would log the path + `account_id` into any proxy in between).
  - `POST` disables browser prefetching / link-hover previews — a rogue extension can't accidentally trigger it.
  - The WARN audit log (FR-014) captures every hit regardless of verb; `POST` just makes the action unambiguously intentional and non-idempotent semantics match the "fresh download, always logged" posture.
- **Alternatives Considered**:

  | Alternative | Pros | Cons | Why Rejected |
  |---|---|---|---|
  | `GET /…/export-auth-json` | Works with `curl -O` | Would logprint the account_id in access logs + prefetchable | Violates "never-off-host unless WARN-audited" posture |

---

## Decision 9: No new Go third-party dependencies; only two additions to existing imports

- **Decision**: 003 adds **zero** new Go modules. It uses:

  | Usage | Package | Already present? |
  |---|---|---|
  | JSON / base64 / `net/http` / `crypto/rand` / `crypto/sha256` | stdlib | yes |
  | JWT decode (`base64.RawURLEncoding` + `encoding/json` only, no signature verify) | stdlib | yes — no `jwt-go` needed |
  | PKCE verifier / `state` generation — **32 bytes** from `crypto/rand.Read` → `base64.RawURLEncoding.EncodeToString` → 43-character URL-safe string (satisfies RFC 7636 §4.1 "43..128 chars" for the verifier; the `state` uses the same generator so only one primitive is reviewed) | `crypto/rand` + `encoding/base64` | yes |
  | Single-flight refresh dedup | `golang.org/x/sync/singleflight` | Needs verification — see Dependency Versions below |
  | HTTP client to `auth.openai.com` | stdlib `net/http` + the project's own `httpclient` helper (see 002) | yes |
  | ID-token claim shape — RFC 7519 standard `email`; OpenAI-custom object at `claims["https://api.openai.com/auth"]` holding `chatgpt_account_id` + `plan_type`, with a top-level `auth` subobject as the codex-lb-compatibility fallback path | defined as a plain Go struct next to the decoder | new code, no new dep |

- **Rationale**: The Constitution's Anti-Abstraction Gate says "use framework features directly"; the OAuth surface we need is small enough that a JWT library or an "OAuth framework" would add more abstraction than it removes. Stdlib + one thin `oauth` package in `internal/` is the right ratio.

---

## Decision 10: OAuth data-plane requests use ChatGPT Codex backend transport

- **Decision**: `/v1/*` remains the client-facing data-plane namespace and stays outside the router-owned Admin API envelope, but upstream transport is selected by account auth method:
  - `auth_method=api_key`: preserve 002 behavior. Forward the original `/v1/*` path and query to `account.EffectiveBaseURL()` (default `https://api.openai.com`) with `Authorization: Bearer <api_key>`.
  - `auth_method in {oauth_browser, oauth_device, oauth_import}`: treat the access token as a ChatGPT/Codex token, not an OpenAI Platform key. Route `/v1/responses` to `https://chatgpt.com/backend-api/codex/responses` and `/v1/responses/compact` to `/codex/responses/compact`, set `Authorization: Bearer <access_token>`, set `chatgpt-account-id` when the row has a non-empty `chatgpt_account_id`, and override inbound `User-Agent` with `codex_cli_rs/0.120.0 (Mac OS 26.2; arm64) dumb`.
  - OAuth accounts are eligible only for the two Codex Responses paths above. Other `/v1/*` paths must be served by API-key accounts or fail with the existing no-capacity router error when no eligible API-key account exists.
  - For non-streaming `/v1/responses`, normalize the client JSON into the Codex backend request shape, request SSE upstream, return the collected complete JSON downstream, and use the same collected terminal `response.completed` event to populate request-history `upstream_response_body` when body logging is enabled. For streaming requests, keep SSE as SSE.
- **Rationale**:
  - ChatGPT subscription OAuth tokens are accepted by the ChatGPT Codex backend, not by the OpenAI Platform `https://api.openai.com/v1/responses` endpoint. Sending them to Platform produces missing-scope failures such as `api.responses.write`.
  - Codex clients still call the router through the familiar `/v1/responses` surface. The router owns the account-specific provider transport behind that facade.
  - Preserving the API-key Platform path keeps existing 002 deployments working and avoids changing service-account traffic.
  - Restricting OAuth eligibility to the known Codex Responses paths avoids pretending a ChatGPT subscription token can satisfy arbitrary Platform API endpoints.
- **Alternatives Considered**:

  | Alternative | Pros | Cons | Why Rejected |
  |---|---|---|---|
  | Send OAuth access tokens to `https://api.openai.com/v1/responses` | Reuses 002 forwarder unchanged | Fails for ChatGPT subscription accounts because the token lacks Platform scopes such as `api.responses.write` | Does not meet the feature goal |
  | Treat every `/v1/*` path as OAuth-eligible and map unknown paths unchanged to ChatGPT backend | Broadest path coverage | Fabricates compatibility for endpoints the ChatGPT Codex backend may not implement; confusing failures | Fail-fast and path-explicit is safer |
  | Make the client call `https://chatgpt.com/backend-api/codex/*` directly | No router adaptation | Breaks the managed endpoint contract and exposes ChatGPT backend details to clients | Violates product boundary |

---

## Dependency Versions (Verified 2026-04-15)

| Package | Verified Version | Registry | Notes |
|---|---|---|---|
| `golang.org/x/sync` | `v0.19.0` | `go list -m golang.org/x/sync` — 2026-04-15 | Already present in `go.sum` (transitive). Only the `singleflight` sub-package is imported; no `go get` required. |

No other new Go modules. No npm deltas — the frontend re-uses the 002 stack (React 19 + Vite 6 + TanStack Router/Query + shadcn/ui + Tailwind v4 + React Hook Form + Zod + Biome + Vitest + Playwright) verbatim.

> The versions in the 002 plan's `Technical Context` (Node 22 LTS, pnpm 9, Go 1.25, etc.) are inherited unchanged. 003 does NOT float any 002-pinned version.

---

## Open Items (none are blockers, all have defaults that will be noted in `plan.md`)

- **OpenAI OAuth 401 subcode stability**: OpenAI has historically returned `invalid_grant` for both "refresh token revoked" and "refresh token expired". Spec FR-005 lumps both into `status = disabled`, which is correct. No action needed — just be ready for that error-code collapse.
- **`plan_type` vocabulary drift**: OpenAI has been observed to add new `plan_type` values (`chatgpt-pro`, `chatgpt-business`) without prior notice. FR-011a already handles this: unmapped values fall back to the raw string. No code change needed when a new plan appears — the UI just renders the raw name until someone updates the label map.
- **SQLite BLOB NULL semantics**: The SQLite dialect treats `BLOB NULL` and `TEXT NULL` interchangeably at row-read time, but `xorm` needs `BLOB` to map cleanly to `[]byte`. The existing `internal/store/dialect_sqlite.go` already has `BLOB` support — no dialect edit needed. Confirmed in plan phase.

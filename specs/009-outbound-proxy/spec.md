# Feature Spec: Outbound Proxy (方案 2 精简版)

**ID**: 009-outbound-proxy
**Created**: 2026-08-29
**Updated**: 2026-08-29
**Status**: Ready

## Overview

Operators deploying the router in mainland China need an HTTP(S)/SOCKS5 proxy to reach OpenAI upstreams (auth.openai.com during OAuth bind, chatgpt.com / api.openai.com on the data plane). Today the router has zero outbound-proxy capability and inconsistent environment-variable behavior across its three egress families. This feature adds ONE global proxy setting (`network.proxy_url` in config.json, managed via the existing Settings API) and ONE per-account opt-in switch (`use_proxy`, default false = direct). Environment variables (`HTTP_PROXY`/`HTTPS_PROXY`/...) stop influencing outbound traffic entirely — the settings value is the single source of truth.

Design authority: /tmp/opencode/009-outbound-proxy-plan.md (v2, simplified after over-engineering review).

## User Scenarios

### US-1: Configure the Global Proxy (P0)

As a platform operator, I want to set one outbound proxy URL in the admin settings so that all proxy-aware traffic can reach OpenAI upstreams from a restricted network.

**Acceptance Scenarios**:
1. Given setup is done, when I `POST /api/admin/settings/update` with `{"network": {"proxy_url": "socks5://127.0.0.1:7890"}}`, then config.json persists it and the response payload's `network` block shows `proxy_configured=true` with a masked URL (`socks5://127.0.0.1:7890` — no credentials present).
2. Given a proxy URL with credentials (`socks5://user:secret@10.0.0.1:1080`), when the settings GET projection is read, then the raw password NEVER appears on the wire; the projection shows `proxy_has_auth=true` and a masked URL.
3. Given an invalid proxy URL (unknown scheme, unparsable, empty host), when I patch it, then I receive `invalid_proxy_url` (code 9001) and config.json is unchanged.
4. Given a proxy URL is configured, when I patch `network.proxy_url` with `""`, then it is cleared (no referential check by design — see US-4 edge cases).

**Edge Cases**:
- An invalid `proxy_url` on disk at boot makes startup fail fast (refuse to boot), never silently degrade to direct.
- Clearing the URL while accounts still have `use_proxy=true` is allowed; runtime behavior is governed by US-4's fail-fast defense (no silent fallback).

### US-2: Egress Follows the Account Switch (P0)

As a platform operator, I want each account to opt in to the global proxy so that accounts pointing at domestically reachable endpoints stay direct while OpenAI-bound accounts go through the proxy.

**Acceptance Scenarios**:
1. Given the global proxy is configured and an active account has `use_proxy=true`, when a data-plane request selects that account, then the outbound connection dials through the configured proxy.
2. Given the global proxy is configured and an account has `use_proxy=false` (default), when a data-plane request selects it, then the outbound connection is direct (no proxy, no environment variables).
3. Given `use_proxy=true` on an OAuth account, when `RefreshIfStale` triggers a token refresh, then the refresh request to auth.openai.com goes through the proxy.
4. Given no global proxy is configured, when I `POST /api/admin/accounts/{id}/proxy/set` with `{"use_proxy": true}`, then I receive `proxy_url_required` (code 9002).
5. Given the endpoint `POST /api/admin/accounts/{id}/proxy/set`, when called for any of the four auth methods (api_key, oauth_browser, oauth_device, oauth_import), then the toggle persists (the toggle is not a credential and is not subject to UpdateDetails' OAuth immutability rule).
6. Given the same value is set twice, when the second call completes, then it succeeds idempotently (absolute-value assignment, not a state transition).

**Edge Cases**:
- Non-existent or soft-deleted account ids reuse the existing account status error semantics (1001 / deleted).
- The response projections of account list/detail gain `use_proxy` for ALL auth methods.

### US-3: OAuth Bind Traffic Follows the Global Proxy (P0)

As a platform operator in China, I want OAuth bind flows (browser callback exchange, device code request/poll) to go through the configured proxy so that the very first authentication can succeed behind a firewall.

**Acceptance Scenarios**:
1. Given the global proxy is configured, when a browser or device OAuth flow exchanges tokens, then the outbound call to auth.openai.com goes through the proxy (no account exists yet to consult, so the global setting governs).
2. Given no proxy is configured, when a bind flow runs, then it dials direct.

**Edge Cases**:
- OAuth start/cancel/callback admin API request/response shapes are unchanged (no new parameters).
- Imported accounts (auth.json) get `use_proxy=false` like every other account.

### US-4: Fail-Fast Misconfiguration (P0)

As an internal client developer, I want a misconfigured account (opted into a proxy that no longer exists) to fail loudly instead of silently going direct or fabricating success.

**Acceptance Scenarios**:
1. Given an account with `use_proxy=true` and the global proxy later cleared, when a data-plane request selects it, then the request fails as an upstream connection error (native 502-class semantics on the data plane, no fabricated provider body) and the router logs an error containing the account id and the actionable hint ("disable use_proxy for this account or configure network.proxy_url").
2. Given an OAuth account in the same state, when its refresh triggers, then the refresh fails and follows the existing transient-fallback path (old access token, `usedFallback=true`); the account is NOT marked permanently errored by proxy misconfiguration alone.
3. Given any proxy dial failure (proxy down), when a proxied request runs, then the failure surfaces as an upstream connect error — never a silent direct retry or a fake success.

**Edge Cases**:
- On the wire this failure is indistinguishable from a genuine upstream outage (accepted; diagnostics live in the error log).

### US-5: Deterministic Egress (P1)

As a platform operator, I want environment-variable proxy behavior removed so that egress is fully determined by the settings value.

**Acceptance Scenarios**:
1. Given `HTTPS_PROXY=socks5://elsewhere:1080` is set in the router's environment and no `network.proxy_url` is configured, when traffic runs, then all egress is direct (env ignored).
2. Given both env var and `network.proxy_url` are set, when traffic runs, then only the configured `proxy_url` is honored.

**Edge Cases**:
- `ROUTER_OAUTH_*` endpoint overrides are orthogonal and unaffected.
- Dual truth-source window (review M4, accepted): the toggle validation and settings projection read the config file value while egress reads the atomic slot; a hand-edited config.json can diverge until the next settings reload/restart. The dangerous direction (empty slot + use_proxy=true) is covered by the US-4 fail-fast defence; the reverse continues on the old proxy until reload — same hot-reload semantics as the in-flight-connection rule.
- `proxy/set` error precedence (review M1, fixed): account existence/soft-delete is checked BEFORE the 9002 global-proxy requirement — a wrong id always reports 1001/1004 regardless of use_proxy.
- Behavior change is documented in the spec/release notes (breaking for deployments that relied on implicit env behavior).

### US-6: Hot Reload (P1)

As a platform operator, I want proxy changes to take effect without restarting the router.

**Acceptance Scenarios**:
1. Given the router is running, when I update `network.proxy_url` via the settings API, then subsequent outbound dials use the new proxy without a restart.
2. In-flight SSE/WebSocket connections that dialed before the change finish on their original path.

## Non-Functional Requirements

- NFR-1: Secret hygiene — proxy credentials are stored in config.json (MVP plaintext, same tier as upstream api_key), masked in every API projection, log line, and error message; `scripts/log-scrub.sh` gains credential-URL patterns.
- NFR-2: Envelope discipline — new endpoint follows `{code,msg,data}` with 200 for business errors; registered codes 9001 `invalid_proxy_url`, 9002 `proxy_url_required` (9xxx = 009) in docs/error-codes.md + Go/TS registries.
- NFR-3: OpenAPI — new operation + schema changes land in openapi/admin.yaml in the same PR with regenerated Go/TS clients.
- NFR-4: Migrations — additive `000010_account_use_proxy` (up/down) in all three dialects; zero-downtime expand.
- NFR-5: Observability — startup logs one masked line ("outbound proxy: socks5://host:port" or "none"); failure-path logs carry account id + hint. No steady-state per-request proxy field (deliberately cut).
- NFR-6: Stdlib-only — http.Transport.Proxy supports http/https/socks5/socks5h natively (verified against Go 1.25 source docs); no new dependencies.

## Out of Scope

- Multiple proxy entities, groups, batch application, master enable switch, PAC, automatic NO_PROXY exclusions.
- Setup-wizard proxy step (known gap: bind during wizard cannot pre-configure a proxy; documented mitigation: finish wizard → configure proxy → bind).
- Connectivity-test endpoint (cut: format errors are caught at save; reachability errors surface on first real request).
- `proxy_in_use` referential-integrity check (cut: runtime fail-fast defense covers it without a store dependency in the settings path).
- Per-request `proxy_source` field in request_records.

## Review Checklist (constitution anchors)

- [ ] Fail-fast: misconfigured use_proxy fails loudly (US-4); no zero-value fallbacks anywhere in the egress path.
- [ ] Envelope + code registry updated in the same commit as handlers.
- [ ] Spec→contract→code parity: admin.yaml declares the new operation; parity probe added.
- [ ] Migration rollback plan: down migration drops the column.
- [ ] No secrets in logs: scrub script extended and green.

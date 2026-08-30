# Feature 009 — Outbound Proxy: Implementation Tasks

> Status: implemented (this branch). Review anchors in spec.md §Review Checklist.
> Design doc: /tmp/opencode/009-outbound-proxy-plan.md (v2)

## P0 — Egress decision core
- [x] T-001 `internal/proxydial`: atomic slot, `Configure/Current/Configured/ProxyFunc/Decide`, thin `ParseAndValidate`, `MaskedURL`, sentinels `ErrInvalidProxyURL` / `ErrProxyRequired` (D8: no port/length/query checks).
- [x] T-002 `proxydial` unit tests: parse matrix, truth table incl. fail-fast stale opt-in, hot-slot read, masked URL, concurrent swap.
- [x] T-003 `domain.UpstreamAccount.UseProxy` (non-credential, every auth_method; `Validate()` untouched).

## P1 — Storage
- [x] T-004 Migration `000010_account_use_proxy` up/down in sqlite/postgres/mysql (additive column with default).
- [x] T-005 `AccountRepo.UpdateUseProxy` (Cols-forced bool write; missing row → `ErrAccountNotFound`).
- [x] T-006 `AccountListItem.UseProxy` + `ListForAdminAPI` projection (all auth methods) + store round-trip test.

## P2 — Egress wiring (env vars exit; spec §3.4)
- [x] T-007 `openai.Client` dual pre-built clients (direct / proxied via `proxydial.ProxyFunc`); `httpClientFor(account)` + flattened `httpClientForUseProxy(useProxy, accountID)`; warn log with account id + actionable hint (D7).
- [x] T-008 Call sites: bridge forward (HTTP), bridge WS dial, codex WS dial, playground, `FetchUsage` (+useProxy param), account/gateway forwards. `provider.UpstreamRequest`/`BuildInput` carry `UseProxy`/`AccountID`; proxy handlers pass the selected account's flags.
- [x] T-009 OAuth: provider egress pair via package-level lazily-built DefaultTransport clones (injected test client still wins); bind traffic follows global slot; `Refresh(ctx, token, useProxy)`; stale opt-in → transient `request_failed` (existing `usedFallback=true` fallback). Coordinator/usage-refresher thread the account row's flag.
- [x] T-010 `ModelRefresher` dual clients; fail-fast on stale opt-in.
- [x] T-011 Egress integration tests via local forwarding proxy: direct / proxied / bypass / dead-proxy (502-class, no fallback) / stale opt-in (`ErrProxyRequired`); refresh egress + transient classification.

## P3 — Settings + admin API
- [x] T-012 `config.NetworkConfig{ProxyURL}` + loader SourceMap provenance (`network.proxy_url`).
- [x] T-013 `applyOutboundProxy` (boot fail-fast on invalid URL; promote path; settings on-reload hook keeps previous slot on error) + one masked startup log line.
- [x] T-014 Settings patch `network.proxy_url` (empty clears, 9001 on invalid, 2012 on unknown key), masked GET projection `NetworkSettings` (never raw credentials), updater apply.
- [x] T-015 `POST /api/admin/accounts/{id}/proxy/set` (idempotent absolute assignment; 9002 when unconfigured; 1001/1004 semantics; nil reader fails closed) + route registration.
- [x] T-016 Error codes 9001/9002 in Go registry + fixture + docs/error-codes.md (9xxx = 009) + TS `errcode.ts` (parity test green).
- [x] T-017 `openapi/admin.yaml`: `SettingsNetworkPatch`, `NetworkSettings`, `ProxySetRequest`, envelopes, `AccountListItem.use_proxy`, `accountProxySet` operation; both codegens regenerated; freshness gate byte-stable.
- [x] T-018 Envelope-parity probes (success + `proxy_url_required` + OAuth row) and malformed probes registered; parity + malformed-inventory tests green.
- [x] T-019 Settings network round-trip tests (masked projection, 9001, 2012, clear) + handler tests (all auth methods, idempotency, missing/deleted, malformed, store failure → 1901, nil reader fail-closed).

## P4 — Frontend
- [x] T-020 Settings page "Outbound proxy" card (save/clear, masked display, static OAuth-bind hint; write-only raw URL).
- [x] T-021 Account detail `use_proxy` switch → `proxy/set`, `invalidateAdminAccountQueries`; strings in `detail.strings.ts`.
- [x] T-022 `errcode.ts` 9001/9002; settings contract validator covers `network`; router test mock updated.
- [x] T-023 Gates: biome 0 issues, `tsc --noEmit` clean, vitest 183 passed, `pnpm build` ok.

## Explicitly not done (spec §Out of Scope)
- Wizard proxy step, connectivity-test endpoint, `proxy_in_use` referential check, steady-state `proxy_source` field, multi-proxy/grouping.

## Known follow-ups
- PG/MySQL migration files are untested locally (no containers in this env); CI's testcontainers path covers them.
- `make fe-build && make go-build` + process restart required before validating in a browser (AGENTS.md runtime rule).

## Review outcome (2026-08-29, sdd-review)

Verdict: **APPROVE-WITH-NITS** (0 Blocker / 0 Major / 8 Minor / 4 Nit). Gates re-run by reviewer: go build/test/-race OK, lint net-change −1 vs HEAD, envelope-parity PASS (incl. 3 new probes), log-scrub interception verified, codegen byte-stable, redocly valid, FE tsc/biome/vitest green. `scripts/coverage-floor.sh` FAIL is pre-existing at HEAD (store 76.8→76.9, playgroundapi 78.7→78.1 — no regression).

Addressed in this branch:
- M1 → handler reorders account lookup before the 9002 check; precedence固化 in spec §8.9.
- M2 → `ForwardGatewayRequestWithCapture` api-key branch threads `account.UseProxy` (dead-code trap removed).
- M5 → detail page hint reworded (fail-fast semantics explicit).
- M8 → `applyOutboundProxy` logs only on real proxy-state transitions.
- M3 (partial) → added bind-egress test (`TestOpenAIProvider_BindEgressFollowsGlobalProxy`) and `TestModelRefresher_StaleOptInFailsFast`.

Deferred follow-ups (test-only, tracked here per constitution traceability):
- M3 remainder: defence WARN log content assertion (account id + hint), coordinator `acct.UseProxy` passthrough assertion, playground egress failure branch test.
- M4/M7 → documented (spec §8.8); plan §6 Playwright journey is a follow-up e2e patch (not yet written).

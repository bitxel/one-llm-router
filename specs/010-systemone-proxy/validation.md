# Validation — 2026-10-24

- Full `go test ./...`: passed after the final store fix.
- `golangci-lint run`: 0 issues.
- Frontend Vitest: 224 tests passed; the account form's 6 tests were rerun after
  its final label change and passed.
- Frontend TypeScript: passed. Biome on the changed account/UI/test files: passed.
  Full Biome remains blocked by pre-existing formatting in admin/index.tsx,
  admin/requests.tsx and admin/requests.test.tsx; those edits were preserved.
- Frontend and backend production builds: passed.
- Playwright with fresh isolated router processes: all 6 existing data-plane
  compatibility tests and the new System One account/forwarding journey passed.
  The System One journey passed again after the final backend rebuild.
- System One provider coverage from unit and proxy integration tests: 90%.
- OpenAPI regeneration produces identical generated files; envelope parity passed.
- Log/fixture secret scanning passed. The environment lacks the sqlite3 CLI;
  fixture DB fields were exported using Python sqlite3 and scanned with the same
  project log-scrub rules.

Independent subagent review found a MySQL JSON comparison regression in account
updates. It was fixed and reviewed again: no unresolved high/medium findings.
Actual MySQL repository tests covered six capability storage states and repeated
saves. The review used relevant account migrations only: the pre-existing MySQL
000008 migration has a foreign-key type mismatch, so this is not evidence of a
successful full MySQL clean installation.

Review report: `/tmp/systemone-implementation-review.md`.
Plan: `/tmp/systemone-generic-technical-plan.md`.
Test logs: `/tmp/systemone-*.log`.

At the initial implementation checkpoint, no live inference had been performed;
browser verification used newly built, isolated processes and temporary databases.

## Live OpenRouter verification — 2026-10-24

With the user's explicit authorization, reused the existing OpenRouter key and
created a separate System One account, `openrouter-mercury-decide-free` (ID 7).
The running router on port 8000 now matches the newly built binary. Original
account configuration/status fingerprints for all six existing rows remained
unchanged, including the disabled source OpenRouter account.

- One direct upstream call: HTTP 200, 70 input / 1 output tokens, reported cost 0.
- Router text/noul call: HTTP 200, 70 input / 1 output tokens, reported cost 0.
- Router JSON/noul+choice+score call: HTTP 200, 280 input / 3 output tokens,
  reported cost 0.
- Both routed records selected account 7, recorded success and exact token usage,
  and preserved the request and response payloads. Captures contain no API key.
- No provider-specific code or configuration change to existing accounts was needed.

Sanitized live report: `/tmp/systemone-openrouter-live-report.md`.
Detailed responses and checks: `/tmp/systemone-openrouter-live/`.

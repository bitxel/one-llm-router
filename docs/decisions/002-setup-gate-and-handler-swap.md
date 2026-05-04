# ADR — Setup Gate + Atomic Handler Swap

- **Status**: Accepted
- **Date**: 2026-04-18
- **Feature**: `002-setup-wizard-and-admin-portal-skeleton`
- **Deciders**: platform maintainers

## Context

Feature 002 ships the first-install setup wizard. The constraint space
is tight:

1. **FR-006 — No upstream traffic before setup completes.** `/v1/*`
   MUST refuse requests until `config.json` has been written.
2. **FR-007 — Deleting `config.json` MUST NOT re-enable setup.** Once
   setup has run, the file system is authoritative: a missing config
   means boot-fail, not "let the wizard rerun".
3. **Single port, single process.** Operators reach the router on one
   TCP address; there is no separate admin port, no separate setup
   binary.
4. **Wizard commit MUST take effect immediately.** After the operator
   clicks "Commit and finish", the very next request to `/v1/*` MUST
   succeed without a process restart — otherwise the happy-path wall-
   clock budget (SC-1 < 2 min) blows up on operators who assume "the
   web page finished loading" equals "the server is ready".
5. **No racy commit.** Two simultaneous commits MUST not both succeed.

## Options considered

### A. Restart-on-commit (rejected)

The wizard commits, writes `config.json`, then calls `os.Exit(0)` and
relies on the process supervisor (systemd, Docker restart policy) to
bring the router back up. Pros: the boot path is then the only code
that transitions from setup-pending to steady-state, which is simple.
Cons: fails requirement 4 — in dev and air-gapped deployments there is
often no supervisor, and even with one the wizard tab sees a dropped
connection mid-redirect. Also breaks SC-1 wall-clock.

### B. Second HTTP listener for setup (rejected)

Run the wizard on a dedicated port; once it commits, shut down the
setup listener and start the main listener. Simpler state machine, but
fails requirement 3: operators have to know + open two ports.

### C. Atomic `http.Handler` swap (chosen)

Keep a single `*http.Server` bound to `:8080`. Its `Handler` field is
an `atomic.Pointer[http.Handler]` that starts pointing at the
"setup-pending" handler tree (wizard SPA + `/api/setup/*` + a gate that
returns 503 on everything else). When `setup.Commit` succeeds, the app
atomically swaps in the steady-state handler (admin SPA + `/api/admin/*`
+ `/v1/*` proxy). The next request — including any in-flight WebSocket
upgrade from the wizard — picks up the new handler without restart.

## Decision

We adopt **Option C — atomic handler swap**.

Concrete shape:

- `internal/app.App` owns an `atomic.Pointer[http.Handler]`. `Handler()`
  returns an `http.Handler` that reads + dispatches through the
  pointer.
- Two handler trees are built at boot:
  - *Setup-pending tree*: `registerSetupRoutes(mux)` + the setup gate
    middleware that 503s any `/v1/*` path.
  - *Steady-state tree*: `registerSteadyStateRoutes(mux)` — includes
    the `/v1/*` proxy wired with `ProxyHandler.SetBodyLogFunc(…)` for
    hot-reloadable observability.
- On `POST /api/setup/commit` success, `App.promoteToSteadyState()` is
  called. It rebuilds the steady-state tree against the freshly-loaded
  `*Config` and swaps the pointer.
- `setup.Commit` itself re-stats `config.json` **inside** the
  package-level `Serialiser` mutex to close the concurrent-commit
  window (FR-005, R-6). Two callers both see the file after the
  winner's `rename`; the loser returns `code=2001 setup_already_done`.

## Consequences

**Positive**

- Wizard commit → admin portal is a single sub-second hop; no supervisor
  dependency.
- `/v1/*` hot-path is unchanged after steady-state; the atomic pointer
  is read once per request with no contention.
- Boot path and post-commit path share the same `registerSteadyState-
  Routes` function, so there is exactly one definition of what
  "production routing" looks like.

**Negative / mitigations**

- Test surface grows: `TestPromoteToSteadyState` in `internal/app`
  exercises the swap directly; hot-reload integration test in
  `internal/api/adminapi` asserts post-swap behaviour against the
  wizard commit endpoint.
- Developers need to remember to register every production route in
  `registerSteadyStateRoutes`, not in an ad-hoc place. Mitigated by a
  package comment and by the fact that `cmd/one-llm-router/main.go` only calls
  `BuildApp` → `Start`; there is no other place to add routes.

## References

- `specs/002-.../spec.md` §FR-005, FR-006, FR-007
- `specs/002-.../plan.md` §Handler wiring + §Round-3 review findings
  F-001, F-002, F-003
- `internal/app/app.go` (atomic pointer + `promoteToSteadyState`)
- `internal/setup/commit.go` (serialiser + re-stat)

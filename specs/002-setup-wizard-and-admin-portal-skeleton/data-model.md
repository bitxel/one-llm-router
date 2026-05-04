# Data Model: Setup Wizard and Admin Portal Skeleton

**Feature**: 002-setup-wizard-and-admin-portal-skeleton
**Date**: 2026-04-17 (revised 2026-04-18 — collapsed to single-file config model to align with spec `Clarifications § Session 2026-04-16`; revised again 2026-04-18 PM — **removed the `system_config` DB table entirely**; `config.json` presence is now the sole setup-completion marker, with brownfield auto-materialization preserving FR-002)

## Overview

002 introduces **zero** new persistent tables and **one** new on-disk artifact (`config.json`). No changes to existing entities (`upstream_accounts`, `request_records`, `sessions`).

**Storage split** (aligned with spec):

| Lives in | Why |
|---|---|
| `config.json` (on-disk file) | The **single** operator-editable configuration record AND the sole setup-completion marker. Holds DB driver+DSN, runtime toggles, and plugin intents. Written at wizard commit (atomic rename) and re-written atomically by `POST /api/admin/settings/update` for hot-reloadable fields. File existence = setup done. Spec Assumption: "The on-disk configuration file is the single file that the wizard writes; no separate 'bootstrap' file is introduced in 002." |

Rule of thumb: **everything setup-related lives in `config.json`**. The DB carries no meta-state about whether setup has happened; business data (`upstream_accounts`, `request_records`, `sessions`) lives there as before.

**Why no DB marker?** Earlier drafts split "on-disk config" from "DB-side setup marker" specifically to resist file deletion (FR-007). In practice this doubled the write surface — every wizard commit had to coordinate a file rename with a DB insert, creating the R-1 crash-window risk and multiplying the code paths an implementer has to reason about. The simplification: **if you delete `config.json`, you start over — unless the DB you previously wrote to still has `upstream_accounts` rows, in which case the router auto-materializes a fresh `config.json` from env on next boot (see § Brownfield auto-materialization below)**. FR-002 is preserved via this path; FR-007 is preserved because the brownfield guard re-creates the file before the wizard gate is evaluated, so `rm config.json && restart` does NOT re-open the wizard on a populated install.

---

## Entity: `config.json` (new on-disk file)

The single operator-editable configuration record. Written atomically by `internal/config/writer.go` via tmp-file + `fsync` + `os.Rename`. Loaded before DB init (for DB credentials) and re-read after DB init (for runtime toggles + plugin intents — same file, single parse at boot; subsequent hot-reloads publish via an in-memory `atomic.Value`).

**Default path**: `./config.json` relative to the process working directory.
**Override**: `ROUTER_CONFIG_PATH` environment variable.
**Permissions**: `0600` at creation (`os.OpenFile` mode). Startup warns if the file on disk has wider perms.

### Schema

```json
{
  "version": 1,
  "db": {
    "driver": "sqlite3",
    "url": "router.db"
  },
  "runtime": {
    "log_client_request_body":   false,
    "log_upstream_request_body":  false,
    "log_upstream_response_body":  false,
    "log_retention_days": 30,
    "log_level":          "info"
  },
  "plugins": {
    "admin_auth":  { "enabled": false },
    "client_keys": { "enabled": false }
  },
  "created_at": "2026-04-17T10:15:00Z",
  "updated_at": "2026-04-17T10:15:00Z"
}
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `version` | integer | yes | Always `1` in 002. Readers MUST reject unknown/future versions with a clear error. |
| `db.driver` | string | yes | One of `"sqlite3"`, `"postgres"`, `"mysql"`. |
| `db.url` | string | yes | Driver-specific DSN. MAY contain a password; the file's `0600` perms are the protection. Never returned by `GET /api/admin/settings`. |
| `runtime.log_client_request_body` | boolean | yes (defaulted on write) | Hot-reloadable via `POST /api/admin/settings/update`. Defaults to `false` (MVP parity). When `true`, every `/v1/*` request body is written to the request-history row up to the body cap. |
| `runtime.log_upstream_request_body` | boolean | yes (defaulted on write) | Hot-reloadable. Defaults to `false`. When `true`, the exact request body sent to the upstream provider is written to the request-history row. Independently toggleable from the client request and upstream response captures. |
| `runtime.log_upstream_response_body` | boolean | yes (defaulted on write) | Hot-reloadable. Defaults to `false`. When `true`, upstream response bodies are written to the request-history row. Independently toggleable from the request-body captures. |
| `runtime.log_retention_days` | integer | yes (defaulted on write) | `[1..365]`. Hot-reloadable. Defaults to `30`. Governs how long request-history rows are retained before the pruner deletes them. |
| `runtime.log_level` | string | yes (defaulted on write) | One of `"debug" \| "info" \| "warn" \| "error"`. Hot-reloadable — the next log call picks up the new level without a restart. Defaults to `"info"`. |
| `plugins.admin_auth` | object | yes (defaulted on write) | Operator intent for admin-auth. 002 ships schema `{ "enabled": bool }`; the future admin-auth feature extends it with plugin-specific fields (`session_ttl_minutes`, `jwt_secret_ref`, …) without bumping `version` because each new key is additive and defaults to its zero value. |
| `plugins.admin_auth.enabled` | boolean | yes (defaulted on write) | Defaults to `false` (MVP parity). Recorded by the wizard; 002 does not activate any plugin. The future admin-auth plugin reads this flag at boot and gates itself accordingly. |
| `plugins.client_keys` | object | yes (defaulted on write) | Same shape as `plugins.admin_auth`. 004 extends it when it lands. |
| `plugins.client_keys.enabled` | boolean | yes (defaulted on write) | Defaults to `false`. 004's client-keys plugin consumes this at boot. |
| `created_at` | RFC3339 string | yes | Set once at commit; never touched thereafter. |
| `updated_at` | RFC3339 string | yes | Refreshed on every atomic write (wizard commit + every Settings update). |

### Field-level env override precedence (FR-008)

For any scalar key `X` readable via this file, the effective runtime value is:

1. `ROUTER_<UPPER_X>` environment variable, if set and non-empty → **env wins; key is "pinned"**.
2. Otherwise the value from `config.json`.
3. Otherwise the hard-coded default.

Keys supported with env override in 002:

| Config path | Env var | Default |
|---|---|---|
| `db.driver` | `ROUTER_DB_DRIVER` | (no default — wizard must fill) |
| `db.url` | `ROUTER_DB_URL` | (no default — wizard must fill) |

002 runtime keys (`runtime.log_client_request_body`, `runtime.log_upstream_request_body`, `runtime.log_upstream_response_body`, `runtime.log_retention_days`, `runtime.log_level`) are **NOT env-overridable**. They are operator-editable exclusively via `config.json` (initial value written by the wizard; mutations via `POST /api/admin/settings/update`). This keeps the 002 settings UX simple (every Settings-page field is always editable). 003+ may introduce env-overridable runtime keys; at that point the shared `2013 env_override_readonly` machinery activates end-to-end.

Plugin flags (`plugins.*.enabled`) are NOT env-overridable in 002 — they are operator choices recorded at install time. The server-side source tracking labels them `"file"` (once committed) or `"default"` (on brownfield upgrade before the first Settings write materialises the file), but this label is never returned over HTTP in 002 (Round-3 review 2026-04-19 flattened the settings response; `GET /api/admin/settings` no longer exposes per-field `source`).

`POST /api/admin/settings/update` still refuses to update any field whose effective source is `env:*` with HTTP 200 envelope `{code:2013, msg:"env_override_readonly", data:{}}` (see `docs/error-codes.md` and `contracts/admin-api.md`); the machinery is dormant in 002 because no 002 runtime key has that source, and the update endpoint's allow-list is itself scoped to (a) the five `runtime.log_*` keys, and (b) `plugins.admin_auth.enabled` + `plugins.client_keys.enabled` (the two plugin IDs that have a `PluginsConfig` sub-struct in 002; see Round-4 D9). `db.*` is intentionally outside the allow-list in 002.

### Read path

```
LoadConfig():
  1. read file at ROUTER_CONFIG_PATH (default "./config.json")
       if file is missing                     → return ErrNoConfig  (triggers setup mode)
       if file cannot be parsed               → return err (refuse to start — fail-closed)
       if file.version is absent              → return ErrUnsupportedConfigVersion (with migration hint)
       if file.version > SupportedVersion (=1) → return ErrUnsupportedConfigVersion (forward-compat: never downgrade)
       if file.version < SupportedVersion     → auto-upgrade the in-memory representation; log INFO; do NOT rewrite the file here — the next update persists the new shape
       if file.mode on disk > 0o600           → log.Warn, continue
  2. for every env-overridable key: env (if set) wins over the file value; record the effective source per field
  3. publish the resulting Config through `atomic.Value` for request-time readers
```

**`config.Load` does not decide setup mode** — it only produces an effective `*Config` (or `ErrNoConfig`) plus per-field source attribution. Setup-mode gating happens downstream by **checking `config.json` presence on disk** (see `internal/setup/gate.go`). Three boot shapes:

1. **Greenfield** — no `config.json`, no DB env vars set → `config.Load` returns `ErrNoConfig`. `BuildApp` boots with a minimal in-memory `*Config` that carries **no** DB connection; migrations are deferred. Gate engages (config file absent) and only `/setup/*` + `/admin/*` (SPA HTML) + `/api/setup/*` + `/api/admin/health` are served. The wizard's commit (US-1 step 5) opens the DB, runs migrations, inserts the first account, then atomically renames `config.json` into place — the rename is the single observable "setup complete" moment.
2. **Brownfield / MVP upgrade (FR-002)** — no `config.json`, `ROUTER_DB_DRIVER`+`ROUTER_DB_URL` set → `config.Load` returns a `*Config` populated entirely from env (DB fields record `SourceMap["db.driver"] = "env:ROUTER_DB_DRIVER"` etc. in the internal loader map; runtime/plugins record `"default"`). `BuildApp` opens the env-DB and runs migrations up. **Before the gate evaluates,** a boot-time probe runs `SELECT COUNT(*) FROM upstream_accounts` — if the count is > 0, `BuildApp` synthesizes a fresh `config.json` from the current env + defaults (atomic tmp+rename, `0600`) and proceeds as if it had found the file on disk. Gate opens; router serves `/v1/*` immediately. Log line: `"brownfield upgrade detected: materialized config.json from environment (accounts=<N>)"`. If the account count is zero, the materialization step is skipped and the gate closes (operator walks the wizard).
3. **Steady-state** — `config.json` present → its values load; env overlays where applicable (in 002: only `db.driver` and `db.url` — `runtime.*` and `plugins.*` are file/default only per D4); gate opens immediately — no DB round-trip needed for the setup decision.

This matches spec FR-001 ("neither source present" ⇒ wizard) because "source" refers to **effective config**, not to the on-disk file per se — env-set DB credentials plus an already-populated DB is a legitimate source that keeps the router out of the wizard path.

### Write path (atomic)

```
WriteConfigAtomic(path, cfg):
  tmp  = path + ".tmp." + PID        (PID-suffixed so crashed tmps never collide)
  open(tmp, O_WRONLY|O_CREATE|O_TRUNC, 0600)
  write JSON-encoded cfg             (cfg.updated_at = now())
  fsync
  close
  rename(tmp, path)                  (atomic on same filesystem on Linux + macOS)
```

Two callers:

1. **`internal/setup.Commit`** — writes the first `config.json` as the final step of wizard commit. See § Commit atomicity, below.
2. **`internal/api/adminapi/settings.Update` (handler for `POST /api/admin/settings/update`)** — writes a new `config.json` after mutating the runtime block.
3. **`internal/app.BuildApp` brownfield path** — synthesizes `config.json` at boot when the file is absent but the env-configured DB has accounts (FR-002 auto-materialization).

### Commit atomicity (SC-3, R-1)

**Commit is DB-first, file-last.** The DB transaction is the fallible step; the file rename is the single observable "setup complete" moment. This inversion (from the earlier file-first/DB-last design) means a crash between phases leaves the DB fully committed but without a file — which is indistinguishable from brownfield upgrade and auto-recovers on next boot.

```
(a) BEGIN DB transaction   (BEGIN IMMEDIATE on SQLite, SERIALIZABLE on PG/MySQL)
(b) INSERT first upstream account   (OPTIONAL since 2026-04-15 — skipped when the
                                     wizard's first_account block is omitted;
                                     see § "Skipping the first upstream account"
                                     below. Uses ON CONFLICT DO NOTHING to stay
                                     idempotent against re-runs.)
(c) COMMIT DB transaction            ← DB is now populated (or only migrated if
                                       the operator skipped account seeding)
(d) open tmp = config.json.tmp.<pid>; write JSON; fsync; close
(e) os.Rename(tmp, config.json)      ← the single publish moment; file presence = setup done
```

**Concurrent-commit loser**: step (d) uses `os.OpenFile(tmp, O_CREATE|O_WRONLY|O_EXCL, 0600)`; if the target `config.json` is materialized between `probe-dsn` and `commit` by another operator in a parallel tab, step (e)'s rename succeeds atomically (POSIX rename replaces) but the pre-commit check catches it: **before** starting (a), Commit calls `os.Stat(config.json)` — if already present, returns HTTP 200 envelope `{code:2001, msg:"setup_already_done", data:{}}`. This is a best-effort pre-check; the true invariant is "DB `ON CONFLICT DO NOTHING` on the account" which makes a concurrent commit idempotent regardless of who wins the rename race.

Recovery rules:

| Failure at step | On-disk state | DB state | Next boot behaviour |
|---|---|---|---|
| a / b | no tmp, no file | tx rolled back | Gate closed (no `config.json`). If env-DB configured and `upstream_accounts` still empty → wizard re-runs greenfield. |
| c (COMMIT lands then crash) | no tmp, no file | account committed | **Brownfield path triggers**: boot probes `upstream_accounts > 0`, materializes `config.json` from env, gate opens. No manual intervention. |
| d (tmp write fails) | stale `.tmp.<pid>` may exist | account committed | Same as row above; startup sweeps stale `.tmp.*` files next to the target path. |
| e (rename completes) | `config.json` published | account committed | Steady-state; gate opens immediately. |

This is the "safe to re-run" guarantee underpinning SC-3. The crash-window risk of the earlier design (file written, DB not committed → gate open, router has no accounts) is structurally impossible under this ordering.

### Skipping the first upstream account (setup-api.md v2.4)

From 2026-04-15 the wizard MAY omit the `first_account` block entirely. The commit flow then runs steps (a)→(e) with step (b) skipped: migrations complete, `config.json` is published, but `upstream_accounts` stays empty.

**Why this is safe**: the crash-window analysis above treats step (b) as an additive DB write guarded by `ON CONFLICT DO NOTHING`. Omitting it removes the write — the DB transaction becomes a no-op schema migration, the file-last publish semantics are unchanged, and the brownfield boot probe (`accounts > 0`) explicitly tolerates an empty `upstream_accounts` row set by deferring back to the greenfield wizard if `config.json` ever disappears.

**Observable consequences** (US-1 AC-4, FR-003):

- `/api/admin/health` returns `degraded` (RV-011 fix 2026-04-15): the health handler counts active accounts and reports `degraded` when the count is zero.
- `/v1/*` requests return `503` with the proxy native-shape error `code = no_available_account` until the operator adds an active account via `POST /api/admin/accounts` (AC — `internal/api/adminapi/accounts.go`). This reuses the existing zero-capacity response shape (`internal/api/proxy.go` → `ErrCodeNoAvailableAccount`); it is not a new error code.
- The admin dashboard renders the "no healthy accounts" banner (V-001) sourced from the same `/api/admin/health` endpoint — so the skipped-account state is observable without relying on ad-hoc UI state.

**Partial payload rejection**: a `first_account` block that is present but missing required scalars (e.g. `name` filled, `api_key` empty) is NOT treated as "skip". The validator walks the per-field rules and returns the appropriate envelope — `2004 invalid_account_name` / `2015 invalid_account_provider` / `2005 invalid_api_key` / `2016 invalid_base_url` — as registered in `internal/api/errcode/codes.go` and `docs/error-codes.md`. Only a fully-empty block (all scalars zero, `base_url` nil or empty string) counts as intentional skip.

---

## Brownfield auto-materialization (FR-002)

**Trigger condition**: at `BuildApp` time, all three hold:

1. `config.json` is absent from disk (`ROUTER_CONFIG_PATH` or `./config.json`)
2. `ROUTER_DB_DRIVER` and `ROUTER_DB_URL` are both set in the environment
3. After migrations run, `SELECT COUNT(*) FROM upstream_accounts > 0`

**Action**: synthesize a minimal `config.json`:

```go
cfg := &Config{
    Version:   1,
    DB:        DBConfig{Driver: envDriver, URL: envURL},
    Runtime:   defaultRuntimeConfig(),  // log_client_request_body=false, log_upstream_request_body=false, log_upstream_response_body=false, log_retention_days=30, log_level="info"
    Plugins:   defaultPluginsConfig(),  // all plugins disabled
    UpdatedAt: time.Now().UTC(),
    CreatedAt: time.Now().UTC(),
}
if err := writer.WriteAtomic(path, cfg); err != nil {
    return nil, fmt.Errorf("brownfield auto-materialize failed: %w", err)
}
slog.Info("brownfield upgrade: materialized config.json from env",
    "driver", envDriver, "accounts", accountCount, "path", path)
```

**Post-conditions**: gate opens, `/v1/*` serves immediately, operator sees Settings page with the synthesized DB info card (driver/host/database_name) + defaulted runtime controls. The Settings endpoint does NOT surface the `source` field on any row in 002 (D6 + D4), so the UI looks indistinguishable from a greenfield install. The internal `SourceMap` retains `db.driver = "env:*"`, so a future version that makes `db.*` patchable would reject such an edit with `2013 env_override_readonly`; in 002 that code path is dormant (db.* is not patchable at all). Operator's first `POST /api/admin/settings/update` writes a proper `config.json` with mutated runtime fields; env-pinned db fields keep winning the next boot because env is re-evaluated.

**Security**: because the synthesized file contains no secrets (API keys live in `upstream_accounts`, not in the file), and the env DB URL is already process-visible, materialization does not change the attack surface. File permissions `0600` are set at creation.

**Idempotence**: the trigger fires at most once per process per boot. If the synthesis itself fails (e.g. parent directory not writable), boot aborts with a clear error — the operator can either fix permissions or manually create `config.json` and restart.

**Spec traceability**: this mechanism is what delivers FR-002 ("automatically record the install as complete on first 002 startup"). The earlier DB-marker design achieved the same semantic via a `system_config` row backfill at migration time; the new design achieves it via a boot-time file synthesis from env + DB probe. Both satisfy FR-002; the file-based approach removes one storage layer.

---

## Plugin Registry (in-memory only, not persisted)

`internal/plugin` ships a registry of `Plugin` factories discovered at import time via `init()`. The registry itself is not persisted — plugins register themselves every boot. Three pieces of state interact:

1. **Registered set** (compile-time): which plugin IDs have `plugin.Register(...)` been called for? — derived from the Go build.
2. **Enabled set** (boot-time): for each registered plugin ID, is `config.Plugins.Enabled(id)` true? The app — **not the plugin** — performs this check by reading the typed `config.Config.Plugins` struct. Plugins have no `Enabled` method in 002. 002 ships **no** concrete plugins, so this set is always empty; 003/004/005 populate it.
3. **Capabilities held** (boot-time): which enabled plugins type-assert to which capability interfaces? — derived by `BuildApp`.

```go
// Package plugin — root interface (see contracts/plugin-interface.md for the full definition)
type Plugin interface {
    ID() string   // stable lower_snake_case identifier, matches config.json.plugins.<ID()>
}

// Binding is a plugin's compile-time registration handle.
type Binding struct {
    ID      string
    Factory func() Plugin
}

// Register adds a Binding; duplicate IDs panic.
// Called from each plugin package's init().
func Register(b Binding)

// Registry returns a sorted-by-ID snapshot of registered Bindings.
// Called once by BuildApp.
func Registry() []Binding

// Capability interfaces a plugin MAY implement
type AdminAuth interface {
    Plugin
    Authenticate(r *http.Request) (adminID string, err error)
}

type ClientKeyAuth interface {
    Plugin
    Authenticate(r *http.Request) (clientKeyID int64, err error)
}

type ProxyHook interface {
    Plugin
    OnProxyComplete(ctx context.Context, ev ProxyCompleteEvent)
}
```

The config-side typed surface for plugin flags (what the app reads to decide enabled/disabled) lives in `internal/config`:

```go
// Package config
type PluginsConfig struct {
    AdminAuth  AdminAuthPluginConfig  `json:"admin_auth"`
    ClientKeys ClientKeysPluginConfig `json:"client_keys"`
}

// Enabled reports whether the plugin with the given ID is turned on in the
// current effective config. Unknown IDs return false and log a single warning
// per process (tracked via a sync.Map to avoid log-flood): unknown-ID calls
// almost always indicate a new plugin's author forgot to add the case arm
// here (see plugin-interface.md §Adding a new plugin). Returning false is
// safe-by-default; the warning is the tripwire.
func (p PluginsConfig) Enabled(id string) bool {
    switch id {
    case "admin_auth":  return p.AdminAuth.Enabled
    case "client_keys": return p.ClientKeys.Enabled
    default:
        warnUnknownPluginIDOnce(id)
        return false
    }
}

// AdminAuthPluginConfig holds the operator-editable flags the 003 admin-auth
// plugin reads at boot. 002 defines only Enabled; 003 adds more fields
// (omitempty so old config.json files keep parsing).
type AdminAuthPluginConfig struct {
    Enabled bool `json:"enabled"`
    // 003 adds: SessionTTLMinutes int    `json:"session_ttl_minutes,omitempty"`
    // 003 adds: JWTSecretRef      string `json:"jwt_secret_ref,omitempty"`
}

type ClientKeysPluginConfig struct {
    Enabled bool `json:"enabled"`
    // 004 adds fields similarly.
}
```

**Why struct-per-plugin instead of `map[string]PluginEntry`:** compile-time field discovery, IDE navigation, grep-ability, and each plugin's own sub-struct can grow its type-safe fields in 003+ without touching the shared shape. The switch in `Enabled(id)` is boilerplate, but it is boilerplate whose cost scales with the *tiny* number of plugins (3 through phase 005), not with the number of readers.

**Forward compatibility:** decoding uses Go's default permissive mode (no `DisallowUnknownFields`), so a future `config.json` with extra keys still parses cleanly on an older 002 binary. Writes use `encoding/json.Marshal` of the exact struct, so unknown keys would be dropped — which is fine in 002 because all legal keys are represented in the struct; later features add their keys **and** their struct fields in the same PR.

**002 registers no concrete plugins.** Both the "registered set" and the "enabled set" are empty after `BuildApp` returns, which is the validated baseline: the server runs correctly with zero plugins, setup gate protects `/v1/*` (native 503) and `/api/admin/*` (envelope `code=2011`) until `config.json` is on disk, while the SPA HTML shell at `/admin/*` and `/setup/*` renders the wizard when setup is pending. Routes not served by always-on handlers return 404.

The **setup gate is NOT part of this registry.** It lives in `internal/setup/gate.go` as always-on infrastructure that `BuildApp` wraps around the root handler unconditionally. Making it a plugin would imply operators can disable it, which is exactly what the gate protects against.

---

## Page ↔ plugin dependency map (for the SPA nav)

The portal shell's left sidebar hard-codes the nav entries **client-side** — the entry list, the `planned` badge text on future rows, and the enabling plugin capability are all TS constants in `frontend/src/components/shared/Sidebar.tsx`. They are **not** served from the backend. The server-side `data.plugins[]` returned by `GET /api/admin/settings` carries only the plugins that are actually registered in this build (rows of `{id, label, enabled}`; in 002 the array is empty). The Settings page consumes that array to render live on/off state of any shipped plugin; the sidebar itself does not need it. This keeps 002's Go footprint small — no `internal/app/shipped_features.go`, no `internal/api/adminapi/roadmap.go`.

This table is the canonical mapping — the SPA constants MUST match this map row-for-row.

| Nav entry | Path | Enabled condition | Ships in |
|---|---|---|---|
| Dashboard | `/admin` | always enabled once `config.json` exists (gate open) | 002 |
| Settings | `/admin/settings` (SPA route; fetches `GET /api/admin/settings`) | always enabled once `config.json` exists (gate open) | 002 |
| Accounts | `/admin/accounts` | always enabled once `config.json` exists (gate open) | 003 live |
| Playground | `/admin/playground` | always enabled once `config.json` exists (gate open) | 004 live |
| Requests | `/admin/requests` | always enabled once `config.json` exists (gate open) | current live |
| Client Keys | `/admin/client-keys` | `plugins.client_keys.enabled == true` **and** a registered plugin with ID `client_keys` is in the registry | planned |
| Observability | `/admin/observability` | a registered plugin with ID `prometheus` is in the registry (flag shape TBD in 005) | 005 |

The current shell keeps `Dashboard`, `Accounts`, `Playground`, `Requests`, and `Settings` enabled, while `Client Keys` and `Observability` remain visibly disabled with a `planned` label hard-coded in the Sidebar component. Keeping this table in the spec-level data model (instead of hidden inside the SPA) means sdd-tasks can generate the nav without re-inventing the mapping. `data.plugins[]` still controls only the live/on-off state of shipped plugins on the Settings page; it no longer drives sidebar enablement.

---

## Data Volume Estimates

- `config.json`: typically under 1 KB. Never appended — always a full atomic rewrite.
- No new tables. Existing `upstream_accounts` sees +1 row per wizard Commit that registers a seed account; since 2026-04-15 operators may skip that step, so the growth pattern is 0 or 1 row per commit. Growth pattern otherwise unchanged.

No new indexes in 002.

---

## Validation Rules (server-side)

Applied in `internal/setup/validator.go` and `internal/api/adminapi/settings.go`.

| Field | Rule |
|---|---|
| `config.json.db.driver` | one of `"sqlite3"`, `"postgres"`, `"mysql"` |
| `config.json.db.url` | 1–4096 chars; driver-specific format; trailing whitespace stripped |
| `config.json.runtime.log_client_request_body` | strict `true` / `false` |
| `config.json.runtime.log_upstream_request_body` | strict `true` / `false` |
| `config.json.runtime.log_upstream_response_body` | strict `true` / `false` |
| `config.json.runtime.log_retention_days` | integer `[1, 365]` |
| `config.json.runtime.log_level` | one of `"debug"`, `"info"`, `"warn"`, `"error"` |
| `config.json.plugins.admin_auth` | must be a JSON object; `enabled` (bool, required, defaulted to `false`); unknown fields are accepted silently on Load (forward-compat) and dropped on Save in 002 because the 002 struct only declares `Enabled` |
| `config.json.plugins.client_keys` | same shape rules as `plugins.admin_auth` |
| `config.json.version` | strict `1` in 002 |
| Setup-complete marker | file existence of `config.json` at `ROUTER_CONFIG_PATH` (default `./config.json`). One-way transition: absent → present. Reversal requires explicit operator action (`rm config.json` plus clearing `upstream_accounts` to bypass the brownfield auto-materialize guard). |

Violations return HTTP 200 with the project envelope `{code: <integer from docs/error-codes.md §Feature 002>, msg, data: {field?, details?}}` — see `docs/error-codes.md` and `contracts/*.md` for the per-endpoint code mapping.

---

**Done.** Ready for contract design.

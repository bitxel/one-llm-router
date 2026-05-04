# Plugin Interface Contract (internal Go API)

**Feature**: 002-setup-wizard-and-admin-portal-skeleton
**Package**: `internal/plugin`
**Audience**: 003 (admin auth), 004 (client keys), 005 (observability), and any future extension-point author. This is an **internal Go contract**, not an HTTP contract.

## Mental model

A plugin in this codebase is a **feature-flag-gated capability implementation**, not a generic HTTP middleware. The model has three pieces:

1. **`Plugin`** — the root interface every plugin implements. It answers one question: *who am I?* (`ID()`). "Am I turned on?" is **not** a plugin question — the app reads `config.Plugins.Enabled(id)` from the typed config struct. Plugins cannot override the app's decision, which keeps the feature-flag semantics centralized and testable in `internal/config`.
2. **Capability interfaces** — one named Go interface per extension point (`AdminAuth`, `ClientKeyAuth`, `ProxyHook`, …). A plugin declares the capabilities it provides by satisfying these interfaces. The app discovers capabilities via type-assertion at boot.
3. **Registry** — a package-level list of `Binding{ID, Factory}` entries, populated via `init()`-time `Register()` calls. Duplicate IDs panic.

URL routing is **not** a plugin concern. Plugins never say "I mount at `/admin/*`". The app decides where each capability is invoked. Adding a new extension point is "define one new interface in `internal/plugin/` + add one type-assertion arm in `BuildApp`".

Generic infrastructure middleware (access log, recoverer, request-id, body-cap) lives in `internal/api/middleware.go` and is always-on. The setup gate (`internal/setup/gate.go`) is always-on infrastructure and is wired directly into `BuildApp` — it is **not** a plugin.

Rationale: 001 hard-wired middleware composition inside `cmd/one-llm-router/main.go`. 002 introduces the plugin seam so 003 can add admin auth, 004 can add client-key auth, and 005 can add observability hooks without touching `main.go`, `proxy.go`, or each other.

---

## The `Plugin` root interface

```go
// Package plugin hosts the compile-time plugin registry and all capability
// interfaces the router exposes as extension points.
package plugin

// Plugin is the root interface every plugin implements. It does not by
// itself provide any behaviour — behaviour comes from the capability
// interfaces a plugin additionally implements (see below).
type Plugin interface {
	// ID is the stable machine-readable feature flag key, lower_snake_case.
	// It is the object key under `config.json.plugins.<ID>` and is emitted
	// in structured logs. Two plugins sharing the same ID is a boot-time
	// fatal (panic at init()).
	ID() string
}
```

### Optional `Initializer`

Plugins that need one-time setup (prepared statements, background workers, warm-up caches) opt into `Initializer`:

```go
// Initializer is an optional capability the app calls once after Enabled
// returns true. Returning a non-nil error aborts boot.
type Initializer interface {
	Plugin
	Init(deps Deps) error
}
```

### `Deps`

```go
// Deps is the narrow set of dependencies a plugin receives at Init time.
// Anything beyond this set must justify itself during review.
type Deps struct {
	Ctx    context.Context
	DB     *xorm.Engine     // nil while config.json is absent (setup gate engaged)
	Logger *slog.Logger     // pre-scoped with plugin_id = <ID>
	Clock  func() time.Time // mockable
}
```

002 intentionally keeps plugin config minimal: the only per-plugin knob is the `enabled` boolean at `config.json.plugins.<ID>.enabled`, which the app reads directly from the typed `config.Config.Plugins` struct and uses to skip disabled plugins at boot. Plugin-specific extra fields (e.g. `session_ttl_minutes` for `admin_auth`) are added to the matching sub-struct (`config.AdminAuthPluginConfig`, `config.ClientKeysPluginConfig`, …) in the feature that introduces them, and the plugin reads them by calling a narrow accessor provided on `Deps` at that time. Deferring that accessor keeps 002's plugin surface at "just identity" — the smallest possible seam.

---

## Capability interfaces

Each capability interface is a **named extension point**. A plugin participates in an extension point by satisfying its interface. `BuildApp` discovers participation via type-assertion and routes calls into the plugin at the right moment.

002 defines the three capability interfaces the near-term roadmap needs; 002 itself invokes **none** of them (no plugin is concrete in 002). 003/004/005 flip them on.

```go
// AdminAuth authenticates requests hitting admin endpoints.
// Activated in 003. The app invokes Authenticate exactly once per
// inbound /api/admin/* JSON request (the SPA static assets at /admin/*
// and all /api/setup/* endpoints are allow-listed in the middleware chain,
// per the 2026-04-18 PM path-convention split).
// Returning a non-nil error causes the app to reject with 401/403.
type AdminAuth interface {
	Plugin
	Authenticate(r *http.Request) (adminID string, err error)
}

// ClientKeyAuth authenticates clients hitting proxied endpoints.
// Activated in 004. Invoked exactly once per inbound /v1/* request.
type ClientKeyAuth interface {
	Plugin
	Authenticate(r *http.Request) (clientKeyID int64, err error)
}

// ProxyHook observes proxied requests after they complete.
// Activated in 005. Invoked once per completed /v1/* request, regardless
// of outcome. Implementations MUST be non-blocking — the app invokes all
// ProxyHooks inside a goroutine with a bounded queue; slow hooks drop
// events, they never slow down the proxy.
type ProxyHook interface {
	Plugin
	OnProxyComplete(ctx context.Context, ev ProxyCompleteEvent)
}
```

`ProxyCompleteEvent` is defined alongside the interface and is an additive-only struct (new fields are allowed; field removal requires a major bump of the plugin contract).

### Rule for adding a new capability

Adding a new extension point means:

1. Define one new interface in `internal/plugin/` (e.g. `type RateLimiter interface { Plugin; ... }`).
2. Add one type-assertion arm in `BuildApp` that picks up plugins implementing the new interface and wires them into the right place.
3. Document the new capability in this file.

There is no central "list of capabilities a plugin declares"; the Go type system is the list.

---

## Binding + registration

```go
// Binding couples a plugin ID to its compile-time factory. Entries live
// in each plugin's own package, mirroring internal/store/dialect.go.
type Binding struct {
	ID      string
	Factory func() Plugin
}

// Register is called from each plugin package's init() to add itself
// to the global registry. Duplicate IDs panic at init — this is a
// compile-time-adjacent guarantee that boot fails loudly rather than
// silently dropping a plugin.
func Register(b Binding)

// Registry returns a snapshot of registered plugins, ordered by ID.
// Called once by BuildApp.
func Registry() []Binding
```

### Expected registration pattern (used by 003+)

```go
// internal/plugin/adminauth/plugin.go  (ships in 003, not 002)
func init() {
	plugin.Register(plugin.Binding{
		ID:      "admin_auth",
		Factory: func() plugin.Plugin { return &adminAuthPlugin{} },
	})
}
```

**002 ships zero concrete plugins.** The interfaces and registry exist; nothing registers into them yet. This is intentional: 002 validates the seam by wiring it into `BuildApp` and proving `TestPluginMatrix` passes with an empty registry. 003/004/005 add the concrete plugins.

### Sidebar nav stubs — hard-coded client-side, not a Go concern

The portal sidebar needs to keep the shell shape stable even before the remaining plugin-backed sections exist. The Round-3 review 2026-04-19 (D7) moved this list entirely into the SPA bundle as TS constants in `frontend/src/components/shared/Sidebar.tsx`: `Dashboard`, `Accounts`, `Playground`, `Requests`, and `Settings` are live, while `Client Keys` and `Observability` stay visible as disabled `planned` entries. There is **no** Go-side `internal/app/shipped_features.go` and no `/api/admin/roadmap` endpoint; the Go registry tracks only *actually-registered* plugins.

Consequently, `GET /api/admin/settings` returns `data.plugins = []` in 002 (no concrete plugin ships). 003 adds an `admin_auth` row, 004 adds `client_keys`, 005 adds `prometheus`. The Settings page consumes `data.plugins[]` to render live on/off state of *shipped* plugins; the sidebar renders identically regardless of what the server returns. See `contracts/admin-api.md §GET /api/admin/settings` for the per-row schema.

---

## Lifecycle

```
BuildApp(ctx, cfg, deps)
  ├── config.Load()               (file at $ROUTER_CONFIG_PATH → env overlay → defaults;
  │                                if file is absent, brownfield probe may synthesize it
  │                                from env + upstream_accounts count — see plan.md T-2;
  │                                publishes initial *Config through atomic.Value)
  ├── open DB + run migrations    (migrate up on boot; dirty state logs slog.Error but
  │                                does NOT block boot — see plan.md R-9)
  ├── compute setup.State from    (pure os.Stat(configPath); no DB read)
  │   config.json presence
  ├── set up always-on infra: request-id → access-log → recoverer → setup-gate → mux
  │   (this is the chain as implemented in 002 BuildApp — see
  │    internal/app/app.go buildHandler. Body-cap is Phase 3+ wiring and
  │    will slot between recoverer and setup-gate when added.)
  ├── for each Binding in plugin.Registry():
  │     if !cfg.Plugins.Enabled(b.ID): continue    // app checks flag; plugin factory not called
  │     p := b.Factory()                           // only instantiate enabled plugins
  │     if init, ok := p.(plugin.Initializer); ok:
  │         init.Init(deps)                        // may return error → abort boot
  │     // Capability discovery (type-assertion arms)
  │     if c, ok := p.(plugin.AdminAuth);     ok: app.adminAuth  = c   // singleton
  │     if c, ok := p.(plugin.ClientKeyAuth); ok: app.clientAuth = c   // singleton
  │     if c, ok := p.(plugin.ProxyHook);     ok: app.proxyHooks = append(app.proxyHooks, c)
  │     app.plugins[p.ID()] = p
  ├── compose the http.Handler tree, calling into capabilities at the
  │   pre-defined extension points
  └── return *App
```

### Singleton vs multi-instance capabilities

- `AdminAuth` and `ClientKeyAuth` are **singletons**: enabling two plugins that implement the same one is a boot-time fatal (panic in `BuildApp`). There is one admin-auth strategy per process; conflict is a configuration error.
- `ProxyHook` is **multi-instance**: the app invokes every enabled plugin that implements it. The Prometheus exporter and a hypothetical OTel exporter can coexist.

The singleton/multi policy is hard-coded in `BuildApp` — it is not encoded in the interface. When 005 starts, we may revisit and lift this into a marker interface if the policy becomes load-bearing.

### Shutdown

Plugins do not have a `Shutdown` method in 002. If 003 needs it, it adds `io.Closer` as an *optional* interface and app-level shutdown does a type-assertion walk. Minimal first.

---

## Testing contract

Every plugin (when it lands in 003+) must provide in its own package:

1. `TestPlugin_Identity` — asserts `ID()` matches the registration, and the plugin type-asserts to at least one capability interface.
2. `TestPlugin_SkippedWhenDisabled` — `BuildApp` with `config.Plugins.<ID>.Enabled = false` does not call the plugin's Factory. Asserted via a counter on the factory closure.
3. `TestPlugin_InitRejectsBadConfig` — if the plugin implements `Initializer`, malformed `Deps` or missing per-plugin config fields return a non-nil error.

002 ships **registry-level** tests instead (no concrete plugins to test):

- `TestRegistry_EmptyIsValid` — `BuildApp` runs with `plugin.Registry()` empty and passes smoke checks.
- `TestRegistry_DuplicateIDPanics` — registering two bindings with the same ID panics.
- `TestRegistry_StableOrder` — `Registry()` returns bindings in lexical-ID order, stable across calls.
- `TestPluginMatrix_EmptyAndSetupPending` — `BuildApp` assembles correctly with `config.json` absent (setup gate engaged) and zero plugins. During setup-pending: (a) `/v1/*` returns 001's native error shape with HTTP 503; (b) `/api/admin/*` (except `/api/admin/health`) returns HTTP 200 + envelope `code = 2011 setup_required`; (c) `/api/admin/health` is always reachable with envelope `code = 0` and `data.setup_state = "pending"`; (d) `/admin/*` SPA requests redirect `302 → /setup/` for non-asset paths; (e) `/api/setup/status|probe-dsn|commit` are available.

---

## Adding a new plugin (003+)

Every concrete plugin landing in 003 / 004 / 005 must touch **three** places. Skipping any of them produces a silently-disabled plugin (not a compile error), so future reviewers should use this as a PR checklist.

### Step 1 — declare per-plugin config in `internal/config/config.go`

Add a typed sub-struct to `PluginsConfig` and a case arm to `PluginsConfig.Enabled(id)`:

```go
type PluginsConfig struct {
    AdminAuth  AdminAuthPluginConfig  `json:"admin_auth"`
    ClientKeys ClientKeysPluginConfig `json:"client_keys"`
    Prometheus PrometheusPluginConfig `json:"prometheus"` // NEW in 005
}

type PrometheusPluginConfig struct {
    Enabled bool `json:"enabled"`
    // ...005-specific fields added here, additively.
}

func (p PluginsConfig) Enabled(id string) bool {
    switch id {
    case "admin_auth":  return p.AdminAuth.Enabled
    case "client_keys": return p.ClientKeys.Enabled
    case "prometheus":  return p.Prometheus.Enabled // NEW in 005
    default:
        // warnUnknownPluginIDOnce uses a sync.Map so each missing id logs at most once per process.
        warnUnknownPluginIDOnce(id)
        return false
    }
}
```

### Step 2 — implement the capability interface + register the binding

The plugin package (e.g. `internal/plugin/prometheus/`) exposes a `Plugin` implementation that satisfies one or more capability interfaces, and calls `plugin.Register` from its `init()`:

```go
func init() {
    plugin.Register(plugin.Binding{
        ID:      "prometheus",
        Factory: func() plugin.Plugin { return &exporter{} },
    })
}
```

### Step 3 — contract tests in the plugin's package

Each plugin ships three tests (see the Testing contract below): `TestPlugin_Identity`, `TestPlugin_SkippedWhenDisabled`, `TestPlugin_InitRejectsBadConfig`. `TestPlugin_SkippedWhenDisabled` specifically asserts that with `config.Plugins.<Field>.Enabled = false` the factory is **never** invoked — this is also the regression net that catches a forgotten Step 1 case arm.

---

## Invariants (enforced by 002 tests)

1. `plugin.Registry()` is stable across calls within a process lifetime.
2. Two bindings with the same ID cause `init()` to panic.
3. A plugin whose `config.Plugins.Enabled(ID) == false` has its Factory **never** invoked — `BuildApp` skips the entry before calling any plugin code. 002 asserts this by counting factory invocations in a test with all flags false.
4. `Plugin.Init` (if implemented) is called exactly once per process, per enabled plugin.
5. `AdminAuth` and `ClientKeyAuth` are singleton capabilities: if two enabled plugins implement the same one, `BuildApp` returns an error.

## Changelog

| Version | Date | Change |
|---|---|---|
| 1.0 | 2026-04-17 | Initial draft (generic-middleware model). |
| 2.0 | 2026-04-17 | Rewritten to feature-flag + capability model per architectural decision revision. Package `internal/middleware` → `internal/plugin`; removed `Surface/Scope/Order/Wrap/State`; added capability interfaces (`AdminAuth`, `ClientKeyAuth`, `ProxyHook`); `SetupGate` is no longer a plugin. |
| 2.1 | 2026-04-18 | `Enabled(cfg ConfigReader) bool` → `Enabled(flag string) bool`; removed `ConfigReader` interface (deferred to 003+ when a concrete plugin needs rich config). Config path `plugin_intents.<id>` renamed to `plugins.<id>`; value domain `"off" \| "enforcing"` simplified to `"off" \| "on"`. |
| 3.0 | 2026-04-18 | **Removed `Enabled` from the `Plugin` interface entirely.** The app — not the plugin — decides enabled/disabled by reading `config.Plugins.Enabled(id)` from a typed struct. Config shape at `config.json.plugins.<id>` changed from flat string (`"on" \| "off"`) to nested object (`{ "enabled": bool, <future plugin-specific keys> }`) so 003/004 can extend per-plugin config without bumping the `version` field. Lifecycle: factories of disabled plugins are never called; test invariant updated accordingly. |
| 3.1 | 2026-04-19 | Round-3 review cascade: removed the obsolete "Static 'shipped features' list" section — `internal/app/shipped_features.go` no longer exists (D7). The sidebar's "coming in NNN" entries are hard-coded client-side in `Sidebar.tsx`. `AdminAuth.Authenticate` docstring updated to reference the post-D2 paths (`/api/admin/*` JSON + `/api/setup/*` allow-list). `TestPluginMatrix_EmptyAndSetupPending` test matrix clarified for the envelope + path-split world (envelope `2011` for `/api/admin/*`, 503 native for `/v1/*`, 302 redirect for SPA `/admin/*`). |

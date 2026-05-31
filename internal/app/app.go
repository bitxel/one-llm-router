// Package app assembles the running router out of foundation
// components (config, store, plugin, setup). BuildApp is the single
// entry point; main() stays thin and simply Start()s the returned
// App.
//
// Invariants (T-028, data-model.md §Boot shapes):
//   - BuildApp never panics on misconfiguration; every failure surfaces
//     as a wrapped error the caller can log + exit on.
//   - Setup-pending boots (nil config OR config.ErrNoConfig) skip the
//     DB entirely — no Open, no migrate, no plugin init. Only the
//     setup wizard SPA + its APIs are reachable via the gate.
//   - Steady-state boots run `migrate up`, log dirty-state as
//     slog.Error but DO NOT abort, then attempt the brownfield
//     auto-materializer, then iterate plugin.Registry() and skip any
//     plugin disabled in PluginsConfig.
//   - The returned handler is always safe to serve over HTTP; even in
//     bare-bones setup mode the gate short-circuits /v1/* with HTTP
//     503 + 001's native error shape (see internal/setup/gate.go).
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"xorm.io/xorm"

	"github.com/user/one-llm-router/internal/api"
	"github.com/user/one-llm-router/internal/api/admin"
	"github.com/user/one-llm-router/internal/api/adminapi"
	"github.com/user/one-llm-router/internal/api/exportapi"
	"github.com/user/one-llm-router/internal/api/oauthapi"
	"github.com/user/one-llm-router/internal/api/playgroundapi"
	apisetup "github.com/user/one-llm-router/internal/api/setup"
	"github.com/user/one-llm-router/internal/app/buildinfo"
	"github.com/user/one-llm-router/internal/config"
	"github.com/user/one-llm-router/internal/core"
	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/oauth"
	"github.com/user/one-llm-router/internal/plugin"
	"github.com/user/one-llm-router/internal/provider/openai"
	"github.com/user/one-llm-router/internal/setup"
	"github.com/user/one-llm-router/internal/spa"
	"github.com/user/one-llm-router/internal/store"
)

// Deps bundles environmental inputs BuildApp needs but does not own.
// Everything not in Deps (plugins, the DB, the mux) is derived inside
// BuildApp so the caller has a single audit point.
type Deps struct {
	// ConfigPath is the absolute path to config.json (already
	// resolved from ROUTER_CONFIG_PATH by the caller). Used by the
	// setup gate AND the brownfield materializer.
	ConfigPath string

	// Env is the env.LookupEnv closure used by the brownfield
	// materializer. Production callers pass config.OSEnv(); tests
	// pass a MapEnv. Nil falls back to config.OSEnv().
	Env config.Env

	// Logger is the structured logger every component should share.
	// Nil falls back to slog.Default().
	Logger *slog.Logger

	// LogLevel is the live slog.LevelVar backing the root logger.
	// BuildApp, promoteToSteadyState, and the ConfigUpdater reload
	// hook all call LogLevel.Set(...) when cfg.Runtime.LogLevel
	// changes, so operators can flip debug/info/warn via
	// /api/admin/settings/update without a restart (FR-011). Nil is
	// tolerated — in that mode the log level is whatever the caller
	// pre-wired into their slog handler and remains static.
	LogLevel *slog.LevelVar

	// ListenAddr is the TCP address Start() binds. Required when
	// Start is called; the zero value works for tests that only
	// Handler() the returned App.
	ListenAddr string

	// OAuthProvider overrides the default OpenAI OAuth provider used
	// by the 003 coordinator. Production callers leave this nil;
	// tests can inject a deterministic fake to avoid live network
	// dependency when exercising the full app wiring.
	OAuthProvider oauth.Provider

	// OAuthClock overrides the coordinator clock. Production callers
	// leave this nil so real time is used; tests can inject a fake
	// clock to drive browser/device timers deterministically.
	OAuthClock oauth.Clock

	// CodexBackendBaseURL overrides the ChatGPT Codex backend URL for
	// end-to-end app tests. Production callers leave this empty so OAuth
	// account tokens are only sent to the fixed ChatGPT backend.
	CodexBackendBaseURL string
}

// App is the composed router — a handler, optional store, and
// optional plugins. main() calls Start to bind the listener, Stop to
// drain it; tests bypass the listener and drive Handler() directly.
//
// Concurrency model:
//   - handler is an atomic.Pointer so promoteToSteadyState can swap
//     the root handler in-place after a successful setup commit
//     (FR-007 "committed wizard must be observable without a
//     process restart"). All other App fields are guarded by
//     promoteMu — the only mutator is promoteToSteadyState, which
//     runs serialised behind setup.Serialiser AND promoteMu so a
//     second wizard commit cannot race a rebuild.
//   - ServeHTTP (through Handler()) reads handler atomically and
//     therefore never blocks behind promoteMu.
type App struct {
	// handler points at the CURRENT root http.Handler. Swapped
	// atomically by promoteToSteadyState; every request reads it
	// via Handler() without taking a lock.
	handler atomic.Pointer[http.Handler]
	logger  *slog.Logger
	deps    Deps

	// promoteMu serialises promoteToSteadyState against itself. The
	// method is idempotent — subsequent calls after a successful
	// promotion are no-ops — but the mutex keeps the setup-pending
	// → steady-state transition atomic with respect to config +
	// store + recorder + plugins + handler reconstruction.
	promoteMu sync.Mutex

	// promoted latches once steady-state wiring succeeds so the
	// reloader can skip redundant work on hot-reload (vs. wizard-
	// commit) reloads.
	promoted bool

	// store is nil in setup-pending boots.
	store *store.Store
	// recorder is nil in setup-pending boots; owns async request logging.
	recorder *core.RequestRecorder
	// plugins is the ordered, enabled subset of plugin.Registry().
	plugins []plugin.Plugin
	// oauthCoordinator owns the in-memory OAuth flow state for the
	// admin browser/device auth surfaces in 003+ steady-state boots.
	// Nil in setup-pending mode.
	oauthCoordinator *oauth.Coordinator

	srv        *http.Server
	listenAddr string
}

// Handler returns the root HTTP handler (gate + middleware + mux).
// Exposed primarily for integration tests; production code uses
// Start / Stop.
//
// The returned http.Handler is a thin adapter that reads App.handler
// atomically on every request, so post-commit handler swaps are
// immediately observable.
func (a *App) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := a.handler.Load()
		if h == nil {
			http.Error(w, "router not ready", http.StatusServiceUnavailable)
			return
		}
		(*h).ServeHTTP(w, r)
	})
}

// Store returns the underlying *store.Store or nil in setup-pending
// mode. Tests use it to assert migration state.
func (a *App) Store() *store.Store { return a.store }

// Plugins returns the ordered slice of enabled plugins.
func (a *App) Plugins() []plugin.Plugin { return a.plugins }

// Start binds the HTTP listener and serves until ctx is canceled or
// Stop is called. Safe to call from main() inside a goroutine.
// Returns http.ErrServerClosed on graceful shutdown.
func (a *App) Start(ctx context.Context) error {
	if a.listenAddr == "" {
		return errors.New("app: ListenAddr must be set before Start")
	}
	a.srv = &http.Server{
		Addr:              a.listenAddr,
		Handler:           a.Handler(),
		ReadTimeout:       130 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	// Best-effort ctx bridge: if the caller cancels ctx while Start
	// is blocked, trigger a graceful shutdown so the listener exits
	// rather than leaking the goroutine.
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.srv.Shutdown(shutCtx)
	}()
	a.logger.Info("server listening", "addr", a.listenAddr)
	return a.srv.ListenAndServe()
}

// Stop gracefully drains the HTTP server and closes the DB store.
// Respects ctx's deadline. Safe to call if Start never ran — in that
// case Shutdown is skipped but the store is still released.
func (a *App) Stop(ctx context.Context) error {
	if a == nil {
		return nil
	}
	var err error
	if a.srv != nil {
		err = a.srv.Shutdown(ctx)
	}
	if a.recorder != nil {
		if closeErr := a.recorder.Close(ctx); closeErr != nil && err == nil {
			err = closeErr
		}
		a.recorder = nil
	}
	if a.oauthCoordinator != nil {
		a.oauthCoordinator.Shutdown()
		a.oauthCoordinator = nil
	}
	if a.store != nil {
		if closeErr := a.store.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
		a.store = nil
	}
	return err
}

// BuildApp assembles a running-ready router.
//
// Contract (data-model.md §Boot shapes):
//   - cfg==nil  → setup-pending candidate. BuildApp FIRST attempts the
//     FR-002 brownfield auto-materialization (requires
//     ROUTER_DB_DRIVER + ROUTER_DB_URL in env AND a populated
//     upstream_accounts table). If materialization succeeds,
//     the fresh config is re-loaded and the steady-state
//     path runs. Otherwise the app boots in setup-pending
//     mode — no DB, no plugins. The gate allow-list keeps
//     /setup/* and /api/admin/health reachable.
//   - cfg!=nil  → steady-state. Opens DB → migrate Up → loads plugins
//     → publishes config.
//
// On ANY fatal error the function returns (nil, err) with no side
// effects leaked: if the DB was opened and a later step fails, the
// DB is closed before returning.
func BuildApp(ctx context.Context, cfg *config.Config, src config.SourceMap, deps Deps) (*App, error) {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if deps.ConfigPath == "" {
		return nil, errors.New("app.BuildApp: Deps.ConfigPath is required")
	}
	if deps.Env == nil {
		deps.Env = config.OSEnv()
	}

	a := &App{
		logger:     logger,
		listenAddr: deps.ListenAddr,
		deps:       deps,
	}

	// 1. Sweep stale tmp files BEFORE any atomic write (writer.go
	//    requires this so the first WriteAtomic does not hit EEXIST
	//    against a crashed predecessor). Harmless when no tmp files
	//    exist. Errors downgrade to WARN — a missing parent dir at
	//    boot is the gate's problem, not ours. Note: DEBUG-level path
	//    in the logger structured field is handled by the caller's
	//    log level config; the key itself stays generic.
	if err := config.SweepStale(deps.ConfigPath); err != nil {
		logger.Warn("sweep stale tmp files failed", "error", err)
	}

	// 2. Setup-pending candidate → attempt brownfield auto-materialization.
	//    Only runs when cfg is nil (loader returned ErrNoConfig) AND env
	//    carries DB credentials. On success we fall through to the
	//    steady-state path with a freshly loaded cfg; on failure we
	//    continue in setup-pending mode.
	//
	//    migrationsDone tracks whether the brownfield probe has already
	//    run `migrate up` against this DB. If it has, the steady-state
	//    path skips the redundant second invocation — the probe itself
	//    would not have succeeded otherwise, and golang-migrate's
	//    idempotency guard only short-circuits at the version check,
	//    not at the metadata-table lookups upstream of it.
	migrationsDone := false
	if cfg == nil {
		materializedCfg, err := tryBrownfield(ctx, deps, logger)
		if err != nil {
			return nil, fmt.Errorf("app.BuildApp: brownfield probe: %w", err)
		}
		if materializedCfg == nil {
			// True setup-pending — no DB, no plugins.
			logger.Info("booting in setup-pending mode (no config.json, brownfield not applicable)")
			a.storeHandler(buildHandler(a, deps, nil, nil, nil))
			return a, nil
		}
		cfg = materializedCfg
		migrationsDone = true
	}

	// 3. Steady-state — open DB.
	st, err := store.New(cfg.DB.Driver, cfg.DB.URL, defaultMaxConns, defaultMinConns)
	if err != nil {
		return nil, fmt.Errorf("app.BuildApp: open store: %w", err)
	}
	a.store = st

	// 4. Run migrations (skipped if the brownfield probe already did
	//    so against the same DSN). Dirty state is LOGGED as ERROR
	//    but not fatal — the operator can use `one-llm-router migrate force
	//    <v>` to recover while the health endpoint stays up.
	if !migrationsDone {
		if err := runMigrations(ctx, &cfg.DB, logger); err != nil {
			_ = st.Close()
			a.store = nil
			return nil, fmt.Errorf("app.BuildApp: %w", err)
		}
	}

	// 5. Plugin assembly. 002 ships zero concrete plugins, so the
	//    loop is effectively a no-op — but we still execute it so
	//    any 003+ plugin registered via import-time side effect wires
	//    up at boot in the expected order.
	plugins, err := assemblePlugins(ctx, cfg, st.Engine(), logger)
	if err != nil {
		_ = st.Close()
		a.store = nil
		return nil, fmt.Errorf("app.BuildApp: assemble plugins: %w", err)
	}
	a.plugins = plugins

	// 6. Publish effective config so hot-reload readers (e.g. the
	//    settings handler, future proxy body-logging toggle) see the
	//    post-boot value before the first request lands. The
	//    SourceMap is published in the same goroutine so the settings
	//    handler can reject env-sourced patches with 2013
	//    env_override_readonly.
	config.Publisher.Store(cfg)
	config.SourcePublisher.Store(src)

	// Align the root logger's level with cfg.Runtime.LogLevel so the
	// very first boot log line is filtered correctly. The admin
	// updater and promote path re-apply this on every config change.
	applyLogLevel(deps.LogLevel, cfg)

	recordRepo := store.NewRequestRecordRepo(st.Engine())
	recorder := core.NewRequestRecorder(recordRepo, logger)
	recorder.Start()
	a.recorder = recorder

	a.storeHandler(buildHandler(a, deps, st, cfg, recorder))
	a.promoted = true
	logger.Info("boot complete",
		"db_driver", cfg.DB.Driver,
		"plugins_enabled", len(plugins),
	)
	return a, nil
}

// storeHandler atomically swaps the root handler.
func (a *App) storeHandler(h http.Handler) {
	a.handler.Store(&h)
}

// promoteToSteadyState completes the setup-pending → steady-state
// transition after a successful wizard commit. It is called by the
// post-commit reloader AFTER config.json has been atomically written
// and the live config publisher has been updated.
//
// Responsibilities (match BuildApp steady-state path, minus migrations
// which commit already ran):
//
//  1. Load the fresh *config.Config from deps.ConfigPath.
//  2. Open a long-lived *store.Store on the DB that commit just
//     populated.
//  3. Start a RequestRecorder backed by that store.
//  4. Assemble the enabled plugins.
//  5. Rebuild the root handler (proxy + admin + setup + middleware)
//     with the newly-live dependencies and atomically swap.
//
// Idempotency: subsequent calls are no-ops. FR-007 only requires one
// promotion per process lifetime; a hot-reload of runtime settings
// (e.g. changing log_retention_days via /api/admin/settings/update)
// does NOT go through this path.
//
// Concurrency: holds promoteMu for the full duration. Callers MUST NOT
// hold setup.Serialiser while calling — commit releases Serialiser
// before invoking the reloader precisely so that promoteToSteadyState
// can itself acquire Serialiser if it needs to swap WriteAtomic races,
// which today it does not.
//
// On failure, state is left untouched (no partial swap); the reloader
// surfaces the error to the wizard UI which can prompt a restart.
func (a *App) promoteToSteadyState(ctx context.Context) error {
	a.promoteMu.Lock()
	defer a.promoteMu.Unlock()

	if a.promoted {
		// Already in steady-state (either BuildApp promoted on a
		// cfg-present boot, or a previous commit already promoted).
		// Subsequent config reloads go through config.Publisher and
		// do not need a handler swap.
		return nil
	}

	cfg, src, err := config.Load(ctx, a.deps.ConfigPath, a.deps.Env)
	if err != nil {
		return fmt.Errorf("promote: load config: %w", err)
	}

	st, err := store.New(cfg.DB.Driver, cfg.DB.URL, defaultMaxConns, defaultMinConns)
	if err != nil {
		return fmt.Errorf("promote: open store: %w", err)
	}

	// Migrations were run inside setup.Commit; skip.

	plugins, err := assemblePlugins(ctx, cfg, st.Engine(), a.logger)
	if err != nil {
		_ = st.Close()
		return fmt.Errorf("promote: assemble plugins: %w", err)
	}

	recordRepo := store.NewRequestRecordRepo(st.Engine())
	recorder := core.NewRequestRecorder(recordRepo, a.logger)
	recorder.Start()

	a.store = st
	a.plugins = plugins
	a.recorder = recorder
	a.storeHandler(buildHandler(a, a.deps, st, cfg, recorder))
	a.promoted = true

	config.Publisher.Store(cfg)
	config.SourcePublisher.Store(src)
	applyLogLevel(a.deps.LogLevel, cfg)
	a.logger.Info("promoted to steady-state",
		"db_driver", cfg.DB.Driver,
		"plugins_enabled", len(plugins),
	)
	return nil
}

// tryBrownfield runs the FR-002 auto-materialization probe and, on
// success, loads the fresh config.json back from disk so the
// steady-state path can proceed uniformly.
//
// Returns:
//   - (nil, nil) — brownfield not applicable (no env, no accounts,
//     or preconditions fail gracefully). Caller proceeds in
//     setup-pending.
//   - (*Config, nil) — config.json was materialized and re-loaded.
//   - (nil, err) — filesystem or DB error that should abort boot.
func tryBrownfield(ctx context.Context, deps Deps, logger *slog.Logger) (*config.Config, error) {
	// Fast path: if env doesn't carry the DB credentials, there is
	// nothing to probe. Read the env once to keep the BootstrapIfBrownfield
	// contract honest about what it needs.
	driver, driverOK := deps.Env(setup.EnvVarDBDriver)
	url, urlOK := deps.Env(setup.EnvVarDBURL)
	if !driverOK || driver == "" || !urlOK || url == "" {
		return nil, nil
	}

	// Temporary DB to probe upstream_accounts count. We open a
	// minimal store, run migrate up so an empty brownfield DB gets
	// the schema needed to answer the count query, then tear it
	// down before re-loading.
	tmpStore, err := store.New(driver, url, defaultMaxConns, defaultMinConns)
	if err != nil {
		return nil, fmt.Errorf("open brownfield store: %w", err)
	}
	defer func() { _ = tmpStore.Close() }()

	if err := runMigrations(ctx, &config.DBConfig{Driver: driver, URL: url}, logger); err != nil {
		return nil, fmt.Errorf("brownfield migrate: %w", err)
	}

	counter := &engineCounter{eng: tmpStore.Engine()}
	materialized, err := setup.BootstrapIfBrownfield(ctx, counter, deps.ConfigPath, deps.Env, logger)
	if err != nil {
		return nil, err
	}
	if !materialized {
		return nil, nil
	}

	// Re-load from disk now that BootstrapIfBrownfield has written
	// the file atomically. Env overlay applies uniformly.
	cfg, _, err := config.Load(ctx, deps.ConfigPath, deps.Env)
	if err != nil {
		return nil, fmt.Errorf("reload materialized config: %w", err)
	}
	logger.Info("brownfield boot: steady-state after auto-materialization", "db_driver", cfg.DB.Driver)
	return cfg, nil
}

// runMigrations wraps the golang-migrate "up" with the dirty-state
// tolerance spelled out in plan.md T-FAULT-dirty-migrate: a dirty
// schema yields ERROR logs + boot continues. Any other error aborts.
//
// Emits exactly one INFO log line per invocation ("running schema
// migrations on boot"). Tests rely on that line being observable to
// prove the I6/T-006 invariant: brownfield materialization runs
// migrate up exactly once — not once in tryBrownfield and again in
// the steady-state branch.
func runMigrations(ctx context.Context, db *config.DBConfig, logger *slog.Logger) error {
	logger.Info("running schema migrations on boot", "driver", db.Driver)
	mig, err := store.NewMigrator(ctx, db)
	if err != nil {
		return fmt.Errorf("new migrator: %w", err)
	}
	defer func() {
		if closeErr := mig.Close(); closeErr != nil {
			logger.Warn("migrator close returned error", "error", closeErr)
		}
	}()
	if upErr := mig.Up(ctx); upErr != nil {
		_, dirty, vErr := mig.Version(ctx)
		switch {
		case vErr == nil && dirty:
			// Canonical dirty-boot path — R-9 contract says log + continue.
			logger.Error("migration schema is dirty — continuing anyway",
				"error", upErr,
				"status", mig.Status(ctx),
				"hint", "run `one-llm-router migrate force <v>` to recover",
			)
			return nil
		case vErr != nil:
			// Version() itself failed (e.g. schema_migrations table
			// corrupt or driver I/O glitch). We can't tell dirty vs
			// clean, so fail boot — but surface BOTH errors so the
			// operator sees the upstream migrate failure AND the
			// downstream probe failure. Without this split, L-003
			// would conflate "dirty schema (recoverable via force)"
			// with "migrator metadata unreachable (probably a DB
			// outage)" under the same opaque "migrate up" wrap.
			logger.Error("migrate up failed and version probe also failed",
				"up_err", upErr,
				"version_err", vErr,
			)
			// Join both errors so callers can errors.Is/As on either
			// the up failure or the version probe failure without
			// losing context.
			return fmt.Errorf("migrate up: %w", errors.Join(upErr, vErr))
		default:
			// vErr == nil && !dirty → genuine migrate failure with
			// clean schema. No automatic recovery.
			return fmt.Errorf("migrate up: %w", upErr)
		}
	}
	return nil
}

// buildHandler wires the root handler chain shared by both boot
// shapes. Middleware order (outermost → innermost):
//
//	RequestIDMiddleware ← RequestLoggingMiddleware ← RecoverHandler ← SetupGate ← mux
//
// Rationale for ordering (see internal/api/recover.go header):
//   - RequestIDMiddleware outermost so every downstream log/envelope,
//     including panic-recovery lines, carry the same correlation id.
//   - RequestLoggingMiddleware wraps RecoverHandler so panics still
//     emit the per-request "method/path/status/latency" audit line
//     (the recover layer converts the panic into a 500 envelope
//     before the log middleware's deferred write observes status).
//   - SetupGate sits INSIDE recover/logging so a 2011 refusal is
//     still logged AND carries a request id, matching the admin-api
//     contract.
func buildHandler(a *App, deps Deps, st *store.Store, cfg *config.Config, recorder *core.RequestRecorder) http.Handler {
	mux := http.NewServeMux()

	// Gate is constructed BEFORE the health handler so that health
	// can consult its latch-state via Gate.ProbeState() instead of
	// performing an independent stat. This closes the L-001
	// split-brain (health reporting "pending" after an FR-007
	// latched-open gate saw config.json deleted post-setup).
	gate := setup.NewGate(deps.ConfigPath, nil, a.logger)

	// L-001/P-001: a gate paired with a setup-pending mux (no /v1/*
	// or /api/admin/* routes) MUST NOT latch from disk observation
	// alone. Otherwise, the brief window between WriteAtomic and
	// handler swap during wizard commit would let concurrent
	// requests bypass the pending responses and hit a mux that does
	// not have the steady-state handlers yet (404 storm). Only the
	// promote path's MarkOpen may open such a gate. The steady-state
	// mux built below re-constructs its own gate (via this same
	// buildHandler) which, being paired with a mux that DOES have
	// the steady routes, leaves the observer enabled and will latch
	// normally on disk observation or MarkOpen.
	if st == nil || cfg == nil {
		gate.DisableFilesystemObserver()
	}

	// /api/admin/health — always reachable per the gate. When the DB
	// is not yet wired (setup-pending), report "degraded" with the
	// setup_state hint so the SPA shell can render its banner.
	mux.HandleFunc("/api/admin/health", makeHealthHandler(a, st, gate, a.logger))

	// /api/setup/* — wizard API surface. Registered unconditionally
	// because the gate's allow-list forwards /api/setup/* straight
	// through in the pending state, and answers the same handlers
	// with state=done in the post-commit steady state. The handler
	// itself is stateless beyond its dependencies; no per-request
	// work leaks between the two modes.
	setupHandler := apisetup.NewHandler(
		gate,
		deps.ConfigPath,
		migratorFactory{},
		accountCreator{},
		postCommitReloader(a),
		a.logger,
	)
	apisetup.RegisterRoutes(mux, setupHandler)

	// /api/admin/* envelope JSON surface AND /v1/* Codex proxy.
	// Registered only in steady-state (cfg != nil AND st != nil)
	// because the backing core services need the DB-opening
	// *store.Store to exist. In setup-pending mode the gate
	// intercepts /api/admin/* with envelope code 2011 setup_required
	// and /v1/* with 001 native HTTP 503 BEFORE reaching the mux, so
	// skipping registration here is safe.
	if st != nil && cfg != nil {
		registerSteadyStateRoutes(mux, a, deps, st, cfg, recorder, gate)

		// Defense in depth for L-001/P-001: when we're composing a
		// steady-state mux, the caller (boot path or
		// promoteToSteadyState) has already decided the runtime is
		// done. Latch the gate open deterministically so in-flight
		// and subsequent requests skip the disk probe entirely and
		// never observe a transient "pending" state, which would
		// otherwise route /v1/* or /api/admin/* through the
		// gate's setup-pending branches.
		gate.MarkOpen()
	}

	// SPA — the embedded React bundle serves both the setup wizard
	// under /setup/* and the admin portal under /admin/*. The gate
	// decides whether /admin/* is reachable in the first place; when
	// it is, this handler serves the HTML + static assets from the
	// `internal/spa` embed and falls back to index.html for deep
	// links so TanStack Router can own client-side navigation.
	//
	// `/admin/` and `/setup/` are registered as separate mux patterns
	// because http.ServeMux strips exact matches and redirects to the
	// sub-tree pattern when we register only `/admin/`.
	spaHandler := spa.Handler()
	mux.Handle("/setup/", http.StripPrefix("/setup", spaHandler))
	mux.Handle("/setup", http.RedirectHandler("/setup/", http.StatusMovedPermanently))
	mux.Handle("/admin/", http.StripPrefix("/admin", spaHandler))
	mux.Handle("/admin", http.RedirectHandler("/admin/", http.StatusMovedPermanently))
	// Vite builds reference hashed bundles with absolute paths
	// (`/assets/index-<hash>.js`) so the same manifest serves both
	// `/setup/*` and `/admin/*` mounts. Registering the SPA handler at
	// `/assets/` (no strip prefix) resolves those references against
	// the same embedded filesystem without duplicating builds or
	// leaking an assets-only ServeMux entry.
	mux.Handle("/assets/", spaHandler)
	mux.Handle("/favicon.svg", spaHandler)

	return api.RequestIDMiddleware()(
		api.RequestLoggingMiddleware(a.logger)(
			api.RecoverHandler(gate.Wrap(mux)),
		),
	)
}

// registerSteadyStateRoutes wires every handler that needs a live DB
// + config: the /api/admin/* envelope handlers (settings + wrapped 001
// account CRUD / requests query / sessions resolve) AND the 001
// transparent /v1/* Codex proxy. Only reached when cfg + store are
// live (steady-state). Dependencies are instantiated here rather than
// returned from BuildApp so their wiring is visible at a single point.
func registerSteadyStateRoutes(
	mux *http.ServeMux,
	a *App,
	deps Deps,
	st *store.Store,
	cfg *config.Config,
	recorder *core.RequestRecorder,
	gate *setup.Gate,
) {
	accountRepo := store.NewAccountRepo(st.Engine())
	recordRepo := store.NewRequestRecordRepo(st.Engine())

	accountSvc := core.NewAccountService(accountRepo, a.logger)
	requestSvc := core.NewRequestService(recordRepo)
	healthSvc := core.NewHealthService(accountRepo)
	selector := core.NewAccountSelector(accountRepo, core.NewConsistentHashRouter())
	var oauthCoord *oauth.Coordinator
	if deps.OAuthClock != nil || deps.OAuthProvider != nil {
		oauthCoord = oauth.NewCoordinatorWithClock(deps.OAuthClock, deps.OAuthProvider, a.logger)
	} else {
		oauthCoord = oauth.NewCoordinator(nil, a.logger)
	}
	oauthCoord.SetAccountStore(accountRepo)
	selector.PreForward = oauthCoord.RefreshIfStale
	a.oauthCoordinator = oauthCoord

	inner := admin.NewHandler(accountSvc, requestSvc, healthSvc, selector, a.logger)
	inner.SetAccountListProvider(accountRepo)
	wrapped := adminapi.NewWrappedHandler(inner, gate, a.logger)

	settingsUpdater := adminapi.NewConfigUpdater(deps.ConfigPath, config.Reader, config.Publisher)
	// Hot-reload log_level: every successful settings update pushes
	// the new cfg into the shared slog.LevelVar so operators never
	// need to restart the router to flip verbosity.
	if deps.LogLevel != nil {
		lv := deps.LogLevel
		settingsUpdater.SetOnReload(func(c *config.Config) { applyLogLevel(lv, c) })
	}
	// Wire the env closure so the updater can re-publish SourceMap
	// after every WriteAtomic — this keeps the 2013
	// env_override_readonly guard correct after a settings write.
	settingsUpdater.SetEnv(deps.Env)
	settingsHandler := adminapi.NewSettingsHandler(config.Reader, settingsUpdater, a.logger)
	settingsHandler.SetSourceReader(config.SourceReader)
	oauthChain := func(next http.Handler) http.Handler { return next }

	// Register OAuth admin lifecycle routes in one scope so a future
	// admin-auth plugin can wrap them uniformly with one chain.
	oauthapi.RegisterBrowserHandlers(mux, oauthCoord, oauthChain)
	oauthapi.RegisterDeviceHandlers(mux, oauthCoord, oauthChain)
	adminapi.RegisterImportAuthJSONHandler(
		mux,
		adminapi.NewImportAuthJSONHandler(oauth.NewAuthJSONImportService(accountRepo, oauthCoord.Now), a.logger),
		oauthChain,
	)
	adminapi.RegisterRequestLogsHandler(
		mux,
		adminapi.NewRequestLogsHandler(requestSvc, accountRepo, a.logger),
		oauthChain,
	)
	dashboardSvc := core.NewDashboardService(recordRepo, accountRepo)
	adminapi.RegisterDashboardHandler(
		mux,
		adminapi.NewDashboardHandler(dashboardSvc, a.logger),
		oauthChain,
	)
	usageSvc := core.NewGatewayUsageService(recordRepo, accountRepo)
	adminapi.RegisterUsageHandler(
		mux,
		adminapi.NewUsageHandler(usageSvc, a.logger),
		oauthChain,
	)
	exportapi.RegisterExportAuthJSONHandler(
		mux,
		exportapi.NewExportAuthJSONHandler(accountRepo, a.logger),
		oauthChain,
	)

	// /api/admin/* routes, skipping /api/admin/health (owned by
	// makeHealthHandler registered in buildHandler).
	mux.HandleFunc("GET /api/admin/settings", settingsHandler.Get)
	mux.HandleFunc("POST /api/admin/settings/update", settingsHandler.Update)

	mux.HandleFunc("POST /api/admin/accounts", wrapped.CreateAccount)
	mux.HandleFunc("GET /api/admin/accounts", wrapped.ListAccounts)
	mux.HandleFunc("GET /api/admin/accounts/{id}", wrapped.GetAccount)
	mux.HandleFunc("POST /api/admin/accounts/{id}/enable", wrapped.EnableAccount)
	mux.HandleFunc("POST /api/admin/accounts/{id}/disable", wrapped.DisableAccount)
	mux.HandleFunc("POST /api/admin/accounts/{id}/delete", wrapped.DeleteAccount)

	mux.HandleFunc("GET /api/admin/sessions/resolve", wrapped.ResolveSession)

	// /v1/* — account-specific Codex transport. API-key accounts
	// preserve the OpenAI Platform-compatible path, while OAuth ChatGPT
	// accounts map supported Responses paths onto the ChatGPT Codex
	// backend. The proxy uses the same selector/recorder as admin
	// routes so session affinity and request recording stay consistent.
	// The client timeout matches 001's default of 120s — long enough for
	// a streaming completion but bounded so a stuck upstream cannot leak
	// goroutines. max request body follows 001's default cap.
	//
	// Body-logging toggles (client request + upstream request + upstream
	// response) are hot-reloadable per 002 FR-011 / T-400: the proxy consults
	// config.Reader.Load() on every request via the func installed by
	// SetBodyLogFunc. The static `bodyLog` flag we pass to
	// NewProxyHandler only acts as a fallback for tests or setups
	// where the live-reader slot is unpublished (should not happen in
	// steady-state, but defaults are safe).
	bodyLogFunc := func() (clientReqLog, upstreamReqLog, upstreamRespLog bool) {
		live := config.Reader.Load()
		if live == nil {
			return cfg.Runtime.LogClientRequestBody,
				cfg.Runtime.LogUpstreamRequestBody,
				cfg.Runtime.LogUpstreamResponseBody
		}
		return live.Runtime.LogClientRequestBody,
			live.Runtime.LogUpstreamRequestBody,
			live.Runtime.LogUpstreamResponseBody
	}
	initialBody := cfg.Runtime.LogClientRequestBody ||
		cfg.Runtime.LogUpstreamRequestBody ||
		cfg.Runtime.LogUpstreamResponseBody
	modelRenameFunc := func() []config.ModelRenameRule {
		live := config.Reader.Load()
		if live == nil {
			return cloneModelRenameRules(cfg.Runtime.ModelRenames)
		}
		return cloneModelRenameRules(live.Runtime.ModelRenames)
	}
	proxyClient := openai.NewClient(proxyClientTimeout)
	if deps.CodexBackendBaseURL != "" {
		proxyClient.SetCodexBackendBaseURLForTest(deps.CodexBackendBaseURL)
	}
	playgroundSvc := core.NewPlaygroundService(accountRepo, selector, proxyClient)
	playgroundSvc.SetRecorder(recorder)
	playgroundSvc.SetBodyLogFunc(bodyLogFunc)
	playgroundapi.RegisterHandler(mux, playgroundapi.NewHandler(playgroundSvc, a.logger), oauthChain)

	proxy := api.NewProxyHandler(selector, recorder, proxyClient, initialBody, 0, a.logger)
	proxy.SetBodyLogFunc(bodyLogFunc)
	proxy.SetModelRenameFunc(modelRenameFunc)
	// The data-plane proxy owns the provider-compatible `/v1/*`
	// subtree plus the explicitly selected Codex-native compatibility
	// paths. The classifier inside ProxyHandler is the final allowlist;
	// these mux patterns only make sure selected non-/v1 paths reach it
	// instead of falling through to the SPA or 404 handling.
	mux.Handle("/v1/", proxy)
	mux.Handle("/backend-api", proxy)
	mux.Handle("/backend-api/", proxy)
}

func cloneModelRenameRules(in []config.ModelRenameRule) []config.ModelRenameRule {
	if in == nil {
		return []config.ModelRenameRule{}
	}
	out := make([]config.ModelRenameRule, len(in))
	copy(out, in)
	return out
}

// proxyClientTimeout is the upstream per-request timeout for /v1/*.
// Aligns with 001's default (see internal/api/proxy_test.go fixtures).
const proxyClientTimeout = 120 * time.Second

// makeHealthHandler returns a /api/admin/health handler wired to the
// live store + setup gate. The envelope policy applies (data.status
// is the liveness signal; HTTP status is always 200).
//
// Setup state is sourced from `gate.ProbeState()` — NOT from a fresh
// stat — so that health and the SetupGate middleware agree on the
// same effective state. Specifically, after the gate has latched
// open per FR-007, deleting config.json mid-flight must not flip
// health back to "pending" while the gate keeps serving traffic
// (L-001). If the very first probe fails (e.g. EACCES on the config
// dir during boot), ProbeState returns a probe error that we surface
// as `data.setup_error` for operator diagnostics.
func makeHealthHandler(_ *App, st *store.Store, gate *setup.Gate, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reqID := api.RequestIDFromContext(r.Context())

		setupDone, probeErr := gate.ProbeState()
		data := map[string]any{
			"checked_at": time.Now().UTC().Format(time.RFC3339),
			"setup_state": map[bool]string{
				true:  "done",
				false: "pending",
			}[setupDone],
			"system": buildinfo.Map(),
		}
		// Surface a ProbeState error (e.g. config path is a
		// directory or EACCES on the first probe before latching)
		// to operators. The error message is generic on purpose —
		// the full path belongs in DEBUG logs only (plan.md §362);
		// setup.StateReader is responsible for that redaction.
		if probeErr != nil {
			data["setup_error"] = "config_path_unreadable"
			logger.Warn("health: setup-state probe failed", "error", probeErr, "request_id", reqID)
		}

		if st == nil {
			// Setup-pending path — no DB to probe.
			data["status"] = "degraded"
			data["accounts"] = map[string]any{
				"active":   0,
				"disabled": 0,
			}
			data["checks"] = map[string]any{
				"database": map[string]any{"ok": false, "reason": "setup_pending"},
			}
			api.WriteOK(w, reqID, data)
			return
		}

		// Steady-state path — best-effort DB ping plus active/disabled
		// account tallies. Account capacity is exposed as operational
		// data, but it is not a health dependency: an installed router
		// with no active upstream accounts is reachable and correctly
		// serving the admin API, so it remains `healthy`.
		start := time.Now()
		active, err := st.Engine().Context(r.Context()).
			Where("status = ?", domain.AccountStatusActive).
			Count(&domain.UpstreamAccount{})
		if err != nil {
			latency := time.Since(start).Milliseconds()
			logger.Error("health: db check failed", "error", err, "request_id", reqID)
			data["status"] = "unhealthy"
			data["accounts"] = map[string]any{}
			data["checks"] = map[string]any{
				"database": map[string]any{"ok": false, "latency_ms": latency, "error": "db_check_failed"},
			}
			api.WriteOK(w, reqID, data)
			return
		}
		disabled, err := st.Engine().Context(r.Context()).
			Where("status = ?", domain.AccountStatusDisabled).
			Count(&domain.UpstreamAccount{})
		if err != nil {
			latency := time.Since(start).Milliseconds()
			logger.Error("health: db check failed", "error", err, "request_id", reqID)
			data["status"] = "unhealthy"
			data["accounts"] = map[string]any{}
			data["checks"] = map[string]any{
				"database": map[string]any{"ok": false, "latency_ms": latency, "error": "db_check_failed"},
			}
			api.WriteOK(w, reqID, data)
			return
		}
		latency := time.Since(start).Milliseconds()
		data["status"] = "healthy"
		data["active_accounts"] = active
		data["disabled_accounts"] = disabled
		accountsData := map[string]any{
			"active":   active,
			"disabled": disabled,
		}
		if rawID := r.URL.Query().Get("account_id"); rawID != "" {
			if id, parseErr := strconv.ParseInt(rawID, 10, 64); parseErr == nil && id > 0 {
				row := &domain.UpstreamAccount{}
				has, getErr := st.Engine().Context(r.Context()).Cols("id", "status").ID(id).Get(row)
				switch {
				case getErr != nil:
					logger.Error("health: account probe failed", "error", getErr, "request_id", reqID, "account_id", id)
					data["status"] = "unhealthy"
					data["checks"] = map[string]any{
						"database": map[string]any{"ok": false, "latency_ms": latency, "error": "db_check_failed"},
					}
					data["accounts"] = map[string]any{}
					api.WriteOK(w, reqID, data)
					return
				case has:
					accountsData[strconv.FormatInt(id, 10)] = map[string]any{
						"status": row.Status,
					}
				default:
					accountsData[strconv.FormatInt(id, 10)] = map[string]any{
						"status": "missing",
					}
				}
			}
		}
		data["accounts"] = accountsData
		data["checks"] = map[string]any{
			"database": map[string]any{"ok": true, "latency_ms": latency},
		}
		api.WriteOK(w, reqID, data)
	}
}

// assemblePlugins walks plugin.Registry() in order, skipping any
// plugin whose ID is disabled in cfg.Plugins.Enabled. Initializer
// errors abort the boot — a plugin that cannot initialise is a
// configuration bug, not a transient condition.
func assemblePlugins(ctx context.Context, cfg *config.Config, engine *xorm.Engine, logger *slog.Logger) ([]plugin.Plugin, error) {
	bindings := plugin.Registry()
	out := make([]plugin.Plugin, 0, len(bindings))
	for _, b := range bindings {
		if !cfg.Plugins.Enabled(b.ID) {
			logger.Info("app.BuildApp: plugin disabled by config; skipping", "plugin_id", b.ID)
			continue
		}
		p := b.Factory()
		if p == nil || p.ID() != b.ID {
			return nil, fmt.Errorf("plugin %q: factory returned invalid plugin", b.ID)
		}
		if init, ok := p.(plugin.Initializer); ok {
			if err := init.Init(plugin.Deps{
				Ctx:    ctx,
				DB:     engine,
				Logger: logger.With("plugin_id", b.ID),
				Clock:  time.Now,
			}); err != nil {
				return nil, fmt.Errorf("plugin %q init: %w", b.ID, err)
			}
		}
		out = append(out, p)
		logger.Info("app.BuildApp: plugin enabled", "plugin_id", b.ID)
	}
	return out, nil
}

// engineCounter adapts a *xorm.Engine to setup.AccountCounter by
// counting the UpstreamAccount table. Lives here (not in store/)
// because brownfield's dependency surface belongs at the composition
// root.
type engineCounter struct{ eng *xorm.Engine }

func (e *engineCounter) CountUpstreamAccounts(ctx context.Context) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	n, err := e.eng.Context(ctx).Count(&domain.UpstreamAccount{})
	if err != nil {
		return 0, fmt.Errorf("count upstream_accounts: %w", err)
	}
	return n, nil
}

const (
	defaultMaxConns = 10
	defaultMinConns = 2
)

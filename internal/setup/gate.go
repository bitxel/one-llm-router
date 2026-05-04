package setup

import (
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/user/one-llm-router/internal/api"
	"github.com/user/one-llm-router/internal/api/errcode"
)

// Gate is the setup-completion enforcement middleware. It wraps the
// application's root handler and short-circuits requests that require
// a completed wizard before reaching the underlying mux.
//
// Decision table (matching specs/002-.../contracts/admin-api.md
// §Routing decision table and plan.md T-US1-1):
//
//	Path                                | config.json absent (setup-pending)
//	------------------------------------+---------------------------------------------
//	/setup/*  (SPA + /api/setup/*)      | pass through
//	/api/admin/health                   | pass through (SPA shell needs this always)
//	/                                   | 302 → /setup/ (T-US1-1)
//	/admin/ + /admin/* (HTML)           | 302 → /setup/
//	/api/admin/* (other JSON)           | HTTP 200 + envelope code=2011 setup_required
//	/v1/*, /backend-api*                | HTTP 503 + 001 native error shape (NO envelope)
//	everything else                     | pass through
//
// Once setup is done, the gate stays out of the way except for
// browser HTML entrypoints that are no longer meaningful:
//
//	GET/HEAD /                         | 302 → /admin/
//	GET/HEAD /setup + /setup/* (HTML)  | 302 → /admin/
//
// Latching semantics (spec FR-007):
//
//   - Before config.json exists, the gate treats setup as pending.
//   - When the wizard commit's atomic rename plants config.json, the
//     gate must flip to "done" without a process restart (otherwise
//     the happy-path US-1 wizard cannot complete). To keep the hot
//     path cheap, the gate re-stats disk on demand but caches the
//     "done" verdict for the process lifetime once true is observed
//     (deleting config.json intentionally does NOT put a running
//     server back into setup mode — FR-007).
//   - Between boot and the first successful disk observation, repeat
//     calls pay at most one os.Stat per request. A planned refinement
//     for 003 is a filesystem-watcher-backed cache; 002 keeps the
//     logic simple and testable.
//
// Gate is goroutine-safe: the fast path is a lock-free atomic read,
// and the slow path (before the first "done" observation) coalesces
// concurrent stats under `mu` so the underlying reader is invoked at
// most once per boot on the happy path. After `done` latches true, the
// slow path is skipped entirely for the life of the process.
type Gate struct {
	reader     stateReader
	configPath string
	logger     *slog.Logger

	// mu serialises slow-path stats so N concurrent first-requests do
	// not each trigger their own reader.IsDone call. Once `done`
	// latches true the mutex is never acquired again.
	mu sync.Mutex

	// done is set exactly once, atomically, when IsDone first returns
	// true. Once true it never goes back to false, mirroring FR-007.
	done atomic.Bool

	// observerDisabled, when true, suppresses the disk-observation
	// branch inside probe(). The gate will then stay "pending" until
	// MarkOpen is called explicitly.
	//
	// Production uses this to close the L-001/P-001 race on wizard
	// commit: when buildHandler composes a setup-pending mux (no
	// /v1/* or /api/admin/* routes), it DISABLES the observer so the
	// gate cannot latch-on-disk and start forwarding traffic to a
	// mux that does not have the steady-state handlers yet. After
	// promoteToSteadyState builds a new handler+gate paired with the
	// steady mux (observer enabled by default), the new gate is free
	// to latch either via disk probe or via MarkOpen.
	observerDisabled atomic.Bool
}

// stateReader is the dependency the gate asks about setup completion.
// Declared as an interface so gate_test.go can drop in a fake that
// simulates flaky disks; production always passes StateReader{}.
type stateReader interface {
	IsDone(path string) (bool, error)
}

// NewGate constructs a Gate. configPath is the absolute path to
// `config.json` (callers should resolve ROUTER_CONFIG_PATH → abs
// before passing). reader is optional — nil falls back to the default
// StateReader. logger is optional — nil falls back to slog.Default().
//
// The constructor does NOT stat the disk; the first IsDone call in
// ServeHTTP pays that cost, ensuring tests can construct a gate in a
// temp dir without races against the outer test harness.
func NewGate(configPath string, reader stateReader, logger *slog.Logger) *Gate {
	if reader == nil {
		reader = StateReader{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Gate{
		reader:     reader,
		configPath: configPath,
		logger:     logger,
	}
}

// Wrap returns an http.Handler that runs the gate before next.
func (g *Gate) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g.isDone() {
			// Post-commit: browsers hitting the bare root or
			// refreshing a stale /setup/<step> URL should land in
			// the admin portal. Redirect HTML paths only — the
			// JSON setup APIs stay reachable so an already-issued
			// idempotent commit retry (wizard resumed in a second
			// tab after the first succeeded) still converges to
			// the same state rather than bouncing through a 302
			// that browsers would follow as GET, breaking the
			// POST.
			if isRootHTMLPath(r) || isSetupHTMLPath(r) {
				redirectNoStore(w, r, "/admin/")
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		g.serveSetupPending(w, r, next)
	})
}

func redirectNoStore(w http.ResponseWriter, r *http.Request, target string) {
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, target, http.StatusFound)
}

func isRootHTMLPath(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	return r.URL.Path == "/"
}

// isSetupHTMLPath returns true for /setup and /setup/* paths that
// render the wizard SPA shell (i.e. not /api/setup/*). The HTTP
// method must be safe (GET or HEAD) — non-idempotent verbs on these
// paths would be a protocol mistake the SPA handler already rejects
// with 405 (F006), so we let the request fall through untouched and
// let the SPA handler's Allow header answer.
func isSetupHTMLPath(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	p := r.URL.Path
	if p == "/setup" {
		return true
	}
	if !strings.HasPrefix(p, "/setup/") {
		return false
	}
	// Keep /api/setup/* (JSON) out of the redirect. The Wrap handler
	// only ever sees stripped paths in the SPA case; the mux
	// registration uses /api/setup/ as a prefix, so any /api/setup
	// request arrives with its prefix intact and will not match the
	// /setup/ check below (it starts with /api/setup/). This branch
	// exists purely for defensive symmetry in case a future route
	// rewrites paths before the gate sees them.
	return !strings.HasPrefix(p, "/api/setup/")
}

func isSetupPendingDataPlanePath(path string) bool {
	return strings.HasPrefix(path, "/v1/") ||
		path == "/backend-api" ||
		strings.HasPrefix(path, "/backend-api/")
}

// DisableFilesystemObserver is called by the composer that wires a
// gate to a setup-pending mux (i.e. one that lacks /v1/* and
// /api/admin/* routes). While disabled, probe() ignores disk state
// and always reports pending, preventing the gate from forwarding
// traffic to a mux whose routes are not yet installed.
//
// Only MarkOpen can latch a gate in the disabled state, so the
// disabled→open transition is exclusively driven by an in-process
// signal from the code that also installs a steady-state mux.
//
// Safe to call from any goroutine.
func (g *Gate) DisableFilesystemObserver() {
	g.observerDisabled.Store(true)
}

// MarkOpen latches the gate open regardless of disk state and
// regardless of whether the filesystem observer is disabled. This is
// the explicit in-process signal the promote path uses to commit the
// setup-pending → steady-state transition atomically with handler
// installation.
//
// Semantics (FR-007 latch): once called, subsequent calls are no-ops
// and the gate reports open for the rest of the process lifetime.
//
// Safe to call from any goroutine.
func (g *Gate) MarkOpen() {
	if g.done.CompareAndSwap(false, true) {
		g.logger.Info("setup gate: latched open via in-process signal")
	}
}

// IsOpen reports whether the gate is currently latched open (i.e.
// setup has been observed complete at least once in this process).
// It is the authoritative effective-state signal — operators and
// admin endpoints MUST use this rather than restat config.json
// independently; otherwise they drift from the gate's FR-007
// "latch-open on observation" contract.
//
// The method triggers the same disk probe isDone() does when the
// gate has never latched yet, so health-check callers observe the
// wizard-commit transition at exactly the same request on which
// wrapped traffic would be let through.
//
// Concurrency: safe to call from any goroutine; uses the same
// atomic fast path as Wrap.
func (g *Gate) IsOpen() bool {
	return g.isDone()
}

// ProbeState is the richer sibling of IsOpen used by the health
// endpoint. It returns the same boolean IsOpen does, plus any error
// encountered during the underlying stat probe when the gate has not
// yet latched. Once the gate is latched open the returned error is
// always nil because no further disk I/O happens.
//
// Health and other observability endpoints call ProbeState so they
// can surface `setup_error` to operators WITHOUT performing an
// independent stat — which would risk drifting from the gate's
// effective state after FR-007 latching (L-001 decision: the gate
// is the single source of truth for setup state).
func (g *Gate) ProbeState() (open bool, probeErr error) {
	return g.probe()
}

// probe is the shared core used by isDone / IsOpen / ProbeState. It
// returns (open, probeErr) following the latching contract:
//
//   - If the gate is already latched open → (true, nil), no disk I/O.
//   - Otherwise, acquire `mu`, double-check the latch, then stat the
//     reader. A reader error returns (false, err) — the gate does NOT
//     latch on error (transient mount hiccups must not be confused
//     with a wizard completion signal). A successful done=true both
//     latches and logs the transition.
//
// This single code path keeps the "exactly one reader.IsDone call
// per latch event" invariant that I1 / TestGate_Concurrent-
// FirstRequests_NoDoubleInit depend on, regardless of whether the
// Wrap middleware or the health endpoint is the first caller.
func (g *Gate) probe() (open bool, probeErr error) {
	if g.done.Load() {
		return true, nil
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.done.Load() {
		return true, nil
	}

	// L-001/P-001 race fix: when the gate is paired with a
	// setup-pending mux (no /v1/* or /api/admin/* routes), we cannot
	// let disk observation alone flip the latch — doing so would
	// forward concurrent traffic from the still-installed pending
	// mux to handlers that do not exist, producing a 404 storm
	// during the brief window between WriteAtomic and handler swap.
	// Only MarkOpen (called by the promote path, after the steady
	// mux is installed) may open a gate in this state.
	if g.observerDisabled.Load() {
		return false, nil
	}

	done, err := g.reader.IsDone(g.configPath)
	if err != nil {
		// Treat any stat error as "setup pending". The alternative
		// (crashing the server) is worse — a transient mount hiccup
		// would take the service down and bounce operators into a
		// wizard they never intended to revisit. The error surfaces
		// at WARN because it's actionable — if this logs more than
		// once a quarter, operators should look at disk health.
		// Config path stays out of the WARN line per plan.md §362;
		// operators can cross-reference with DEBUG logs.
		g.logger.Warn("setup gate: stat failed, treating as setup-pending", "error", err)
		return false, err
	}
	if done {
		g.done.Store(true)
		g.logger.Info("setup gate: config.json observed, gate latched open")
	}
	return done, nil
}

// isDone reports whether setup has completed. Thin wrapper over
// probe() for the Wrap fast path — drops the error value to keep the
// hot path allocation-free.
func (g *Gate) isDone() bool {
	done, _ := g.probe()
	return done
}

// serveSetupPending applies the decision table when the gate is
// closed. Request routing is path-prefix based because the Go stdlib
// mux is not available at this layer (gate wraps the entire root
// handler and must work before any pattern-match).
func (g *Gate) serveSetupPending(w http.ResponseWriter, r *http.Request, next http.Handler) {
	path := r.URL.Path
	reqID := api.RequestIDFromContext(r.Context())

	switch {
	// 1. SPA setup assets — must be reachable while setup is pending.
	//    This covers the wizard HTML, its JS/CSS chunks, and anything
	//    the SPA loads from /setup/*.
	case path == "/setup" || strings.HasPrefix(path, "/setup/"):
		next.ServeHTTP(w, r)
		return

	// 2. Setup APIs — POST /api/setup/probe-dsn, POST /api/setup/commit,
	//    GET /api/setup/status. Without these the wizard cannot submit.
	case strings.HasPrefix(path, "/api/setup/"):
		next.ServeHTTP(w, r)
		return

	// 3. Health — always reachable so the portal shell can fetch it
	//    on first paint and show a "setup required" banner instead of
	//    a hard 404. Exact match is deliberate — /api/admin/health/*
	//    sub-paths (if 003 ever adds them) are NOT allow-listed.
	case path == "/api/admin/health":
		next.ServeHTTP(w, r)
		return

	// 4. Root + /admin/ + /admin/* HTML deep links — redirect to /setup/.
	//    Per plan.md T-US1-1 ("GET / with no config.json → 302 /setup/"),
	//    the bare-root redirect is part of the gate contract, not a
	//    cosmetic nicety. The 302 carries Cache-Control: no-store so
	//    browsers refetch after setup completes.
	case path == "/" || path == "/admin" || strings.HasPrefix(path, "/admin/"):
		redirectNoStore(w, r, "/setup/")
		return

	// 5. Other /api/admin/* paths — JSON envelope refusal.
	case strings.HasPrefix(path, "/api/admin/"):
		api.WriteBizErr(w, reqID,
			errcode.SetupRequired,
			errcode.Symbol(errcode.SetupRequired),
			map[string]any{},
		)
		return

	// 6. Data-plane proxy paths — 001 native error shape, HTTP 503.
	//    Envelope is explicitly excluded from /v1/*, selected
	//    /backend-api/* data-plane paths
	//    per AGENTS.md / 006 contracts.
	//    A Codex client seeing 503 + {"error":{…}} will treat the
	//    router as an upstream outage, which is the correct posture
	//    during setup (we have no accounts to route to yet).
	case isSetupPendingDataPlanePath(path):
		api.WriteNativeError(
			w,
			http.StatusServiceUnavailable,
			"service_unavailable",
			"setup_required",
			"router setup is not complete; visit /setup/ to finish installation",
			reqID,
		)
		return

	// 7. Anything else — pass through. The bare root `/` is handled
	//    above (302 → /setup/). This branch covers static top-level
	//    assets (e.g. /favicon.ico), robots.txt, and future paths the
	//    gate does not special-case.
	default:
		next.ServeHTTP(w, r)
	}
}

package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/user/one-llm-router/internal/api"
)

// staticReader is a stateReader fake whose IsDone verdict is frozen.
type staticReader struct {
	done bool
	err  error
	// calls counts IsDone invocations so tests can prove the gate
	// memoises after the first call.
	calls atomic.Int32
}

func (s *staticReader) IsDone(_ string) (bool, error) {
	s.calls.Add(1)
	return s.done, s.err
}

// okSentinel is the inner handler every test wraps with the gate. It
// records the path seen and writes a tiny body so tests can assert
// pass-through distinct from gate-written responses.
type okSentinel struct {
	seen []string
	mu   sync.Mutex
}

func (o *okSentinel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	o.mu.Lock()
	o.seen = append(o.seen, r.URL.Path)
	o.mu.Unlock()
	w.Header().Set("X-Sentinel", "hit")
	_, _ = w.Write([]byte("sentinel"))
}

func (o *okSentinel) saw(path string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, p := range o.seen {
		if p == path {
			return true
		}
	}
	return false
}

// silent logger so test output stays clean.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newGateWithDone builds a gate whose IsDone returns done/err and a
// sentinel the caller can poll for pass-through.
func newGateWithDone(t *testing.T, done bool) (*Gate, *staticReader, *okSentinel) {
	t.Helper()
	r := &staticReader{done: done}
	return NewGate("/nonexistent/config.json", r, silentLogger()), r, &okSentinel{}
}

// newGateWithRealFS constructs a gate pointed at a real temp path so
// the StateReader path is exercised at least once.
func newGateWithRealFS(t *testing.T, writeFile bool) (*Gate, string, *okSentinel) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if writeFile {
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	return NewGate(path, nil, silentLogger()), path, &okSentinel{}
}

func TestGate_SetupDone_AllPathsPassThrough(t *testing.T) {
	t.Parallel()
	g, _, sentinel := newGateWithRealFS(t, true)
	h := g.Wrap(sentinel)

	paths := []string{
		"/admin/",
		"/admin/settings",
		"/api/admin/settings",
		"/api/admin/health",
		"/api/setup/status",
		"/v1/chat/completions",
	}
	for _, p := range paths {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("path %s: status = %d, want 200", p, rec.Code)
		}
		if rec.Header().Get("X-Sentinel") != "hit" {
			t.Errorf("path %s: sentinel was not called", p)
		}
	}
}

func TestGate_SetupDone_RootRedirectsToAdmin(t *testing.T) {
	t.Parallel()

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			g, _, sentinel := newGateWithRealFS(t, true)
			h := g.Wrap(sentinel)

			req := httptest.NewRequest(method, "/", nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusFound {
				t.Errorf("%s /: status = %d, want 302", method, rec.Code)
			}
			if loc := rec.Header().Get("Location"); loc != "/admin/" {
				t.Errorf("%s /: Location = %q, want /admin/", method, loc)
			}
			if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
				t.Errorf("%s /: Cache-Control = %q, want no-store", method, cc)
			}
			if sentinel.saw("/") {
				t.Errorf("%s /: sentinel saw request — gate must short-circuit root", method)
			}
		})
	}

	t.Run("non-safe methods fall through", func(t *testing.T) {
		t.Parallel()
		g, _, sentinel := newGateWithRealFS(t, true)
		h := g.Wrap(sentinel)

		req := httptest.NewRequest(http.MethodPost, "/", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Header().Get("X-Sentinel") != "hit" {
			t.Error("POST /: sentinel should be called (not redirected)")
		}
	})
}

// TestGate_SetupDone_SetupHTMLRedirectsToAdmin locks in F009: once
// the gate has latched open, an operator who lands on /setup/<step>
// (likely because they stayed on the wizard URL and hit refresh
// after commit) should bounce to the admin portal. The JSON setup
// APIs under /api/setup/* must remain reachable so idempotent
// commit retries still converge.
func TestGate_SetupDone_SetupHTMLRedirectsToAdmin(t *testing.T) {
	t.Parallel()
	g, _, sentinel := newGateWithRealFS(t, true)
	h := g.Wrap(sentinel)

	htmlPaths := []string{"/setup", "/setup/", "/setup/db", "/setup/complete"}
	for _, p := range htmlPaths {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusFound {
			t.Errorf("path %s: status = %d, want 302", p, rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/admin/" {
			t.Errorf("path %s: Location = %q, want /admin/", p, loc)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("path %s: Cache-Control = %q, want no-store", p, cc)
		}
		if rec.Header().Get("X-Sentinel") == "hit" {
			t.Errorf("path %s: sentinel should not be called on redirect", p)
		}
	}

	// Non-GET/HEAD must fall through so the SPA's own 405 handler
	// answers with an Allow header (F006).
	req := httptest.NewRequest(http.MethodPost, "/setup/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("X-Sentinel") != "hit" {
		t.Errorf("POST /setup/: sentinel should be called (not redirected)")
	}

	// /api/setup/* must NOT redirect — it's JSON.
	req = httptest.NewRequest(http.MethodGet, "/api/setup/status", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("/api/setup/status: status = %d, want 200 (JSON must not redirect)", rec.Code)
	}
}

func TestGate_SetupPending_SetupAssetsPassThrough(t *testing.T) {
	t.Parallel()
	g, _, sentinel := newGateWithDone(t, false)
	h := g.Wrap(sentinel)

	paths := []string{
		"/setup/",
		"/setup/index.html",
		"/setup/assets/main.js",
		"/setup", // exact match allowance
	}
	for _, p := range paths {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("path %s: status = %d, want 200 (pass-through)", p, rec.Code)
		}
		if !sentinel.saw(p) {
			t.Errorf("path %s: sentinel did not see request — gate short-circuited", p)
		}
	}
}

func TestGate_SetupPending_SetupAPIsPassThrough(t *testing.T) {
	t.Parallel()
	g, _, sentinel := newGateWithDone(t, false)
	h := g.Wrap(sentinel)

	paths := []string{
		"/api/setup/status",
		"/api/setup/probe-dsn",
		"/api/setup/commit",
	}
	for _, p := range paths {
		req := httptest.NewRequest(http.MethodPost, p, strings.NewReader("{}"))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("path %s: status = %d, want 200", p, rec.Code)
		}
		if !sentinel.saw(p) {
			t.Errorf("path %s: sentinel did not see request", p)
		}
	}
}

func TestGate_SetupPending_HealthPassThrough(t *testing.T) {
	t.Parallel()
	g, _, sentinel := newGateWithDone(t, false)
	h := g.Wrap(sentinel)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if !sentinel.saw("/api/admin/health") {
		t.Error("sentinel did not see health — gate must pass through")
	}
}

func TestGate_SetupPending_HealthSubpathIsGated(t *testing.T) {
	// /api/admin/health/* sub-paths are NOT allow-listed (only exact
	// match is). If 003 ever adds `/api/admin/health/db` etc. it must
	// land behind the gate.
	t.Parallel()
	g, _, sentinel := newGateWithDone(t, false)
	h := g.Wrap(sentinel)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/health/db", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if sentinel.saw("/api/admin/health/db") {
		t.Error("gate must not pass through sub-paths of /api/admin/health")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (envelope)", rec.Code)
	}
	if !envelopeHasCode(t, rec.Body.Bytes(), 2011) {
		t.Errorf("body = %s, want envelope code=2011", rec.Body.String())
	}
}

func TestGate_SetupPending_AdminHTMLRedirects(t *testing.T) {
	t.Parallel()
	g, _, sentinel := newGateWithDone(t, false)
	h := g.Wrap(sentinel)

	paths := []string{"/admin", "/admin/", "/admin/settings", "/admin/accounts/42"}
	for _, p := range paths {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusFound {
			t.Errorf("path %s: status = %d, want 302", p, rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/setup/" {
			t.Errorf("path %s: Location = %q, want /setup/", p, loc)
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("path %s: missing Cache-Control: no-store", p)
		}
		if sentinel.saw(p) {
			t.Errorf("path %s: sentinel saw request — gate must short-circuit", p)
		}
	}
}

func TestGate_SetupPending_AdminAPIReturnsEnvelope2011(t *testing.T) {
	t.Parallel()
	g, _, sentinel := newGateWithDone(t, false)
	h := g.Wrap(sentinel)

	paths := []string{
		"/api/admin/settings",
		"/api/admin/accounts",
		"/api/admin/sessions/42",
	}
	for _, p := range paths {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		req = req.WithContext(api.WithRequestID(req.Context(), "req-test-42"))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("path %s: status = %d, want 200", p, rec.Code)
		}
		if got := rec.Header().Get("X-Request-Id"); got != "req-test-42" {
			t.Errorf("path %s: X-Request-Id = %q, want req-test-42", p, got)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("path %s: Content-Type = %q, want application/json", p, ct)
		}
		if !envelopeHasCode(t, rec.Body.Bytes(), 2011) {
			t.Errorf("path %s: body = %s, want code=2011", p, rec.Body.String())
		}
		if sentinel.saw(p) {
			t.Errorf("path %s: sentinel saw request — gate must short-circuit", p)
		}
	}
}

func TestGate_SetupPending_V1ReturnsNative503(t *testing.T) {
	t.Parallel()
	g, _, sentinel := newGateWithDone(t, false)
	h := g.Wrap(sentinel)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	req = req.WithContext(api.WithRequestID(req.Context(), "req-v1"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	// Body MUST be 001's native shape, NOT the envelope.
	var body map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := body["error"]; !ok {
		t.Errorf("body = %s, want top-level `error` key (native shape)", rec.Body.String())
	}
	if _, ok := body["code"]; ok {
		t.Errorf("body = %s, must NOT have top-level `code` (that is the envelope)", rec.Body.String())
	}
	if got := rec.Header().Get("X-Request-Id"); got != "req-v1" {
		t.Errorf("X-Request-Id = %q, want req-v1", got)
	}
	if strings.Contains(rec.Body.String(), `"request_id"`) {
		t.Errorf("body = %s, must not contain request_id; request id is header-only", rec.Body.String())
	}

	// Decode and assert native data-plane error semantics.
	var env api.RouterErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal RouterErrorEnvelope: %v", err)
	}
	if env.Error.Code != "setup_required" {
		t.Errorf("error.code = %q, want setup_required", env.Error.Code)
	}
	if env.Error.Type != "service_unavailable" {
		t.Errorf("error.type = %q, want service_unavailable", env.Error.Type)
	}
	if sentinel.saw("/v1/chat/completions") {
		t.Error("sentinel saw /v1/* — gate must short-circuit")
	}
}

func TestGate_SetupPending_RootRedirectsToSetup(t *testing.T) {
	// GET / during setup-pending redirects to /setup/ per plan.md
	// T-US1-1. Other ambient paths (favicon, robots, arbitrary unknown
	// /api/other prefixes) still pass through so the outer mux can
	// serve them (404, static, marketing landing, etc.).
	t.Parallel()
	g, _, sentinel := newGateWithDone(t, false)
	h := g.Wrap(sentinel)

	// Root must redirect, not pass through.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Errorf("GET /: status = %d, want 302", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/setup/" {
		t.Errorf("GET /: Location = %q, want /setup/", got)
	}
	if sentinel.saw("/") {
		t.Error("GET /: sentinel saw request — gate must short-circuit root")
	}

	// Non-root unknown paths still pass through.
	for _, p := range []string{"/favicon.ico", "/robots.txt", "/api/other"} {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if !sentinel.saw(p) {
			t.Errorf("path %s: sentinel did not see request — gate wrongly short-circuited", p)
		}
		if rec.Code != http.StatusOK {
			t.Errorf("path %s: status = %d, want 200", p, rec.Code)
		}
	}
}

func TestGate_Memoizes_IsDoneCalledOnce(t *testing.T) {
	t.Parallel()
	g, reader, _ := newGateWithDone(t, true)
	h := g.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 25; i++ {
		req := httptest.NewRequest(http.MethodGet, "/admin/anything", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
	}
	if got := reader.calls.Load(); got != 1 {
		t.Errorf("IsDone calls = %d, want 1 (memoised)", got)
	}
}

func TestGate_Memoizes_StatErrorTreatedAsPending(t *testing.T) {
	t.Parallel()
	reader := &staticReader{err: errors.New("stat exploded")}
	g := NewGate("/x", reader, silentLogger())
	h := g.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/admin/settings", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// Stat error → setup-pending → 302 on /admin/*.
	if rec.Code != http.StatusFound {
		t.Errorf("status = %d, want 302 (stat error → pending)", rec.Code)
	}
}

func TestGate_Memoizes_PostSetupDeletionIsIgnored(t *testing.T) {
	// FR-007 says "delete config.json MUST NOT re-enable setup mode
	// on an already-running server". Deleting the file mid-flight
	// must not flip the gate back closed.
	t.Parallel()
	g, path, sentinel := newGateWithRealFS(t, true)
	h := g.Wrap(sentinel)

	// Prime the gate (file present → done=true).
	req := httptest.NewRequest(http.MethodGet, "/api/admin/settings", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !sentinel.saw("/api/admin/settings") {
		t.Fatal("precondition failed: gate did not pass through with file present")
	}

	// Delete the file under the running gate.
	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	// Still pass through — the gate has latched.
	req = httptest.NewRequest(http.MethodGet, "/api/admin/settings", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !sentinel.saw("/api/admin/settings") {
		t.Error("gate closed after file deletion — must latch open for process lifetime")
	}
}

func TestGate_LogsOnceAtFirstRequest(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	reader := &staticReader{done: true}
	g := NewGate("/configured/path", reader, logger)
	h := g.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	// Exactly one "latched open" line. Count occurrences.
	count := strings.Count(buf.String(), "latched open")
	if count != 1 {
		t.Errorf("'latched open' count = %d, want 1\nlogs:\n%s", count, buf.String())
	}
}

func TestGate_ConcurrentFirstRequests_NoDoubleInit(t *testing.T) {
	// Under -race, many goroutines calling Wrap concurrently must not
	// cause racy IsDone reads — the gate uses an atomic.Bool fast
	// path plus a sync.Mutex double-check so the underlying reader
	// is invoked at most once per latch event (see gate.probe).
	t.Parallel()
	reader := &staticReader{done: true}
	g := NewGate("/x", reader, silentLogger())
	h := g.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	const N = 200
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/api/admin/health", nil)
			h.ServeHTTP(httptest.NewRecorder(), req)
		}()
	}
	wg.Wait()

	if got := reader.calls.Load(); got != 1 {
		t.Errorf("IsDone calls = %d, want 1 (sync.Once must serialise)", got)
	}
}

func TestGate_NilReaderFallsBackToRealStat(t *testing.T) {
	// Passing reader=nil must not panic; production wires it that way.
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	g := NewGate(path, nil, nil) // nil logger too → defaults
	sentinel := &okSentinel{}
	h := g.Wrap(sentinel)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/settings", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !sentinel.saw("/api/admin/settings") {
		t.Error("gate with real reader did not pass through on a present config file")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestGate_AllMethodsShareDecision(t *testing.T) {
	// The decision table is path-based, not method-based. Every
	// method against /api/admin/settings in setup-pending must emit
	// the same envelope — no special-casing by verb.
	t.Parallel()
	g, _, sentinel := newGateWithDone(t, false)
	h := g.Wrap(sentinel)

	methods := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete}
	for _, m := range methods {
		req := httptest.NewRequest(m, "/api/admin/settings", strings.NewReader(""))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("method %s: status = %d, want 200 (envelope 2011)", m, rec.Code)
		}
		if !envelopeHasCode(t, rec.Body.Bytes(), 2011) {
			t.Errorf("method %s: body = %s, want code=2011", m, rec.Body.String())
		}
	}
}

func TestGate_IsOpen_AndProbeState(t *testing.T) {
	// IsOpen / ProbeState are the observability hooks that L-001
	// forces to share the gate's latching contract. They must:
	//
	//   1. Report false before the first successful IsDone → done.
	//   2. Latch open when a successful done=true arrives, matching
	//      the same path Wrap uses. Only ONE reader.IsDone call per
	//      latch (concurrent-safe with Wrap's fast path).
	//   3. Surface ProbeState's probeErr on transient stat failure
	//      without ever flipping the latch from false to true.
	//   4. Never go back to false after latching (FR-007), even if
	//      a subsequent ProbeState/IsOpen call would observe a
	//      reader that suddenly returns (false, nil) — the fast
	//      path short-circuits before the reader is consulted.
	t.Parallel()

	t.Run("initially pending", func(t *testing.T) {
		g, reader, _ := newGateWithDone(t, false)
		open, err := g.ProbeState()
		if open {
			t.Errorf("ProbeState open = true, want false (pending)")
		}
		if err != nil {
			t.Errorf("ProbeState err = %v, want nil", err)
		}
		if g.IsOpen() {
			t.Errorf("IsOpen = true, want false")
		}
		if reader.calls.Load() != 2 {
			t.Errorf("reader.calls = %d, want 2 (ProbeState + IsOpen, both miss latch)", reader.calls.Load())
		}
	})

	t.Run("latches on first done=true", func(t *testing.T) {
		reader := &staticReader{done: true}
		g := NewGate("/x", reader, silentLogger())
		open, err := g.ProbeState()
		if !open {
			t.Errorf("ProbeState open = false, want true")
		}
		if err != nil {
			t.Errorf("ProbeState err = %v, want nil", err)
		}
		// Latch engaged → subsequent calls bypass reader entirely.
		_ = g.IsOpen()
		_, _ = g.ProbeState()
		if reader.calls.Load() != 1 {
			t.Errorf("reader.calls = %d, want 1 (latched open after first call)", reader.calls.Load())
		}
	})

	t.Run("surfaces probe error without latching", func(t *testing.T) {
		sentinel := errors.New("disk I/O")
		reader := &staticReader{done: false, err: sentinel}
		g := NewGate("/x", reader, silentLogger())

		open, err := g.ProbeState()
		if open {
			t.Errorf("ProbeState open = true, want false on error")
		}
		if !errors.Is(err, sentinel) {
			t.Errorf("ProbeState err = %v, want %v wrapped", err, sentinel)
		}

		// Still pending — no latch set.
		if g.IsOpen() {
			t.Errorf("IsOpen = true after stat error, want false")
		}

		// Now flip the reader to success → latch engages.
		reader.err = nil
		reader.done = true
		if !g.IsOpen() {
			t.Error("IsOpen = false after reader recovered, want true")
		}
	})

	t.Run("health-vs-gate agreement under latch", func(t *testing.T) {
		// Reproduces the L-001 split-brain: Wrap and IsOpen must
		// agree across a post-latch config.json deletion.
		g, path, sentinel := newGateWithRealFS(t, true /* writeFile */)
		h := g.Wrap(sentinel)

		// Prime the latch.
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if !g.IsOpen() {
			t.Fatal("gate did not latch open on first pass")
		}

		// Now delete config.json. Both Wrap and IsOpen must keep
		// reporting "open" — that is the FR-007 contract.
		if err := os.Remove(path); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		if !g.IsOpen() {
			t.Error("IsOpen = false after post-latch deletion, want true (FR-007)")
		}
		open, err := g.ProbeState()
		if !open {
			t.Error("ProbeState open = false after post-latch deletion, want true (FR-007)")
		}
		if err != nil {
			t.Errorf("ProbeState err = %v, want nil (latched path skips reader)", err)
		}
	})
}

// envelopeHasCode parses body as the router JSON envelope and asserts
// the integer code matches want.
func envelopeHasCode(t *testing.T, body []byte, want int) bool {
	t.Helper()
	var env struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Errorf("envelope unmarshal: %v — body=%s", err, string(body))
		return false
	}
	if env.Code != want {
		return false
	}
	if len(env.Data) == 0 || string(env.Data) == "null" {
		t.Errorf("data must be an object, got %q", string(env.Data))
		return false
	}
	return true
}

// TestGate_DisableFilesystemObserver_IgnoresDiskSignal covers the
// L-001/P-001 race fix: a gate paired with the setup-pending mux
// MUST NOT latch open just because config.json appeared on disk.
// Doing so would forward in-flight /v1/* or /api/admin/* traffic
// through a mux that has not yet registered those routes (the
// steady mux is built after WriteAtomic, during promote).
func TestGate_DisableFilesystemObserver_IgnoresDiskSignal(t *testing.T) {
	t.Parallel()

	r := &staticReader{done: true}
	g := NewGate("/nonexistent/config.json", r, silentLogger())
	g.DisableFilesystemObserver()

	if open, err := g.ProbeState(); err != nil || open {
		t.Fatalf("ProbeState() = (%v, %v), want (false, nil) while observer disabled", open, err)
	}
	if r.calls.Load() != 0 {
		t.Fatalf("reader.IsDone invoked %d times; must be 0 when observer is disabled", r.calls.Load())
	}

	sentinel := &okSentinel{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	g.Wrap(sentinel).ServeHTTP(rec, req)

	if sentinel.saw("/v1/responses") {
		t.Fatalf("/v1/* must not pass through while observer disabled; sentinel saw request")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("HTTP status = %d, want 503", rec.Code)
	}

	g.MarkOpen()
	if !g.IsOpen() {
		t.Fatal("IsOpen() = false after MarkOpen(); latch must flip regardless of observer flag")
	}
	rec2 := httptest.NewRecorder()
	g.Wrap(sentinel).ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
	if !sentinel.saw("/v1/responses") {
		t.Fatal("/v1/* must pass through after MarkOpen")
	}
}

// TestGate_MarkOpen_IsIdempotent ensures FR-007 latch semantics are
// preserved across repeat calls and that the first caller wins.
func TestGate_MarkOpen_IsIdempotent(t *testing.T) {
	t.Parallel()

	g, _, _ := newGateWithDone(t, false)

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g.MarkOpen()
		}()
	}
	wg.Wait()

	if !g.IsOpen() {
		t.Fatal("IsOpen() = false after concurrent MarkOpen calls")
	}
}

// compile-time assertion: StateReader satisfies stateReader.
var _ stateReader = StateReader{}

// ensure context.Context type is used (imported indirectly via api).
var _ context.Context = context.Background()

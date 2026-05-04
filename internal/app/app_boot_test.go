package app

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/user/one-llm-router/internal/config"
	"github.com/user/one-llm-router/internal/store"
	_ "github.com/user/one-llm-router/internal/store"
)

// TestBootWithDirtyDB is the T-030 integration test: fresh SQLite →
// BuildApp → migration applied + Dirty=false; then force-dirty via
// the migrator CLI wrapper → restart → slog.Error emitted AND the
// health endpoint still responds.
//
// The invariant captured here is that operators who've painted
// themselves into a dirty-schema corner can still observe the
// running router (and therefore re-run `one-llm-router migrate force <v>`
// without needing to SSH) — dirty-state is loud but not fatal.
func TestBootWithDirtyDB(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "router.db")
	cfgPath := writeConfig(t, dir, dbFile)

	// === First boot: fresh schema → Up applies cleanly ===
	cfg := mustLoad(t, cfgPath)
	a1, err := BuildApp(context.Background(), cfg, nil, Deps{
		ConfigPath: cfgPath,
		Logger:     silentLogger(),
	})
	if err != nil {
		t.Fatalf("first BuildApp: %v", err)
	}
	// Close the store so the subsequent force-dirty step does not
	// contend with it on SQLite's single-writer lock.
	if err := a1.Stop(context.Background()); err != nil {
		t.Fatalf("Stop a1: %v", err)
	}

	// Sanity check: version > 0, clean.
	mig1, err := store.NewMigrator(context.Background(), &cfg.DB)
	if err != nil {
		t.Fatalf("NewMigrator (post-boot): %v", err)
	}
	v, dirty, err := mig1.Version(context.Background())
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if v == 0 {
		t.Errorf("Version = 0 after BuildApp, want > 0 (migrations did not run)")
	}
	if dirty {
		t.Errorf("Dirty = true after clean BuildApp, want false")
	}

	// === Force-dirty the schema ===
	// golang-migrate marks a schema as dirty by Force(v) followed by
	// a deliberate Up that fails. The simpler, portable approach is to
	// Force() a bogus version — that sets dirty implicitly only when
	// used mid-migration, so we instead manually mark via raw SQL,
	// mimicking the operator scenario where a migration half-applied.
	markDirty(t, &cfg.DB, int(v))
	_ = mig1.Close()

	// === Second boot: dirty DB → BuildApp succeeds, health still serves ===
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	cfg2 := mustLoad(t, cfgPath)
	a2, err := BuildApp(context.Background(), cfg2, nil, Deps{
		ConfigPath: cfgPath,
		Logger:     logger,
	})
	if err != nil {
		t.Fatalf("second BuildApp on dirty DB: %v", err)
	}
	t.Cleanup(func() { _ = a2.Stop(context.Background()) })

	logs := logBuf.String()
	// Assert the real slog message, not any log line containing "dirty".
	if !strings.Contains(logs, "migration schema is dirty") {
		t.Errorf("expected slog.Error 'migration schema is dirty', got:\n%s", logs)
	}

	// Health endpoint must still respond.
	req := httptest.NewRequest(http.MethodGet, "/api/admin/health", nil)
	rec := httptest.NewRecorder()
	a2.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("health status = %d, want 200 even on dirty boot", rec.Code)
	}
	// T-030 requires HTTP 200 + envelope success; business state lives in data.status.
	var env struct {
		Code int `json:"code"`
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("health envelope: %v — body=%s", err, rec.Body.String())
	}
	if env.Code != 0 {
		t.Errorf("health envelope code = %d, want 0", env.Code)
	}
	if env.Data.Status == "" {
		t.Errorf("health data.status empty — body=%s", rec.Body.String())
	}
}

// markDirty flips the dirty flag on the schema_migrations table to
// simulate a partial migration. Works across the three supported
// drivers; the call used in this test is sqlite3-only because the
// test harness does not spin up Postgres/MySQL.
func markDirty(t *testing.T, db *config.DBConfig, version int) {
	t.Helper()
	// Use a dedicated migrator to keep the SQL driver imports
	// centralised. The underlying sql.DB is closed on Migrator.Close.
	mig, err := store.NewMigrator(context.Background(), db)
	if err != nil {
		t.Fatalf("NewMigrator (mark dirty): %v", err)
	}
	defer func() { _ = mig.Close() }()
	// Force with the current version keeps the version pointer but
	// does not set dirty — so we use a non-existent version which
	// golang-migrate accepts via Force and which leaves version
	// pointing at an invalid state. On subsequent Up the library
	// sees a version it can't reconcile, returns ErrDirty, and Up
	// leaves the schema marked dirty.
	//
	// Simpler alternative: Force(version) + ExecContext setting
	// dirty=1 directly. golang-migrate's schema_migrations table
	// layout for sqlite3: (version INTEGER, dirty BOOLEAN).
	if err := mig.Force(context.Background(), version); err != nil {
		t.Fatalf("Force: %v", err)
	}
	// Re-open the raw sql.DB to flip the dirty bit. This is the
	// cleanest way to simulate an operator-visible dirty state
	// without inducing an actual migration failure (the migration
	// files in the test fixture always apply cleanly).
	sqlDB := openRawSQLite(t, db.URL)
	defer func() { _ = sqlDB.Close() }()
	if _, err := sqlDB.Exec("UPDATE schema_migrations SET dirty = 1 WHERE version = ?", version); err != nil {
		t.Fatalf("UPDATE schema_migrations: %v", err)
	}
}

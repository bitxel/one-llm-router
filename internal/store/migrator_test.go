package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/user/one-llm-router/internal/config"
)

// sqliteDBConfig returns a DBConfig rooted in a per-test tempdir so the
// file-backed sqlite DB survives across the multiple sql.Open calls
// inside Migrator + real-world boot. :memory: is avoided because each
// connection gets its own isolated DB under modernc/sqlite (not the
// shared semantics needed for a migrator test).
func sqliteDBConfig(t *testing.T) *config.DBConfig {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	return &config.DBConfig{Driver: "sqlite3", URL: path}
}

func TestMigrator_NewMigrator_NilConfig_Error(t *testing.T) {
	t.Parallel()
	_, err := NewMigrator(context.Background(), nil)
	if err == nil {
		t.Fatal("err = nil, want error for nil config")
	}
}

func TestMigrator_NewMigrator_UnknownDriver_Error(t *testing.T) {
	t.Parallel()
	_, err := NewMigrator(context.Background(), &config.DBConfig{Driver: "pineapple", URL: "file:x"})
	if err == nil {
		t.Fatal("err = nil, want error for unknown driver")
	}
	if !strings.Contains(err.Error(), "pineapple") {
		t.Errorf("err = %v, want message mentioning driver name", err)
	}
}

func TestMigrator_NewMigrator_ContextCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewMigrator(ctx, sqliteDBConfig(t))
	if err == nil {
		t.Fatal("err = nil, want ctx cancellation error")
	}
}

func TestMigrator_Up_AppliesAllMigrations(t *testing.T) {
	t.Parallel()
	mig, err := NewMigrator(context.Background(), sqliteDBConfig(t))
	if err != nil {
		t.Fatalf("NewMigrator: %v", err)
	}
	defer func() { _ = mig.Close() }()

	// Before any Up, Version should be (0, false, nil).
	v, dirty, err := mig.Version(context.Background())
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if v != 0 || dirty {
		t.Errorf("Version before Up = (%d, %v), want (0, false)", v, dirty)
	}

	if err := mig.Up(context.Background()); err != nil {
		t.Fatalf("Up: %v", err)
	}

	v, dirty, err = mig.Version(context.Background())
	if err != nil {
		t.Fatalf("Version after Up: %v", err)
	}
	if v == 0 {
		t.Errorf("Version after Up = %d, want > 0 (001 ships one migration)", v)
	}
	if dirty {
		t.Error("dirty = true after clean Up")
	}
}

func TestMigrator_Up_Idempotent_ReturnsNilOnErrNoChange(t *testing.T) {
	t.Parallel()
	mig, err := NewMigrator(context.Background(), sqliteDBConfig(t))
	if err != nil {
		t.Fatalf("NewMigrator: %v", err)
	}
	defer func() { _ = mig.Close() }()

	if err := mig.Up(context.Background()); err != nil {
		t.Fatalf("first Up: %v", err)
	}
	// Second Up: ErrNoChange is suppressed to nil per contract.
	if err := mig.Up(context.Background()); err != nil {
		t.Errorf("second Up: %v, want nil (ErrNoChange must be suppressed)", err)
	}
}

func TestMigrator_Status_Shapes(t *testing.T) {
	t.Parallel()
	db := sqliteDBConfig(t)
	mig, err := NewMigrator(context.Background(), db)
	if err != nil {
		t.Fatalf("NewMigrator: %v", err)
	}
	defer func() { _ = mig.Close() }()

	ctx := context.Background()

	// 1. Clean slate — version=0.
	got := mig.Status(ctx)
	if got != "version=0 (no migrations applied)" {
		t.Errorf("Status before Up = %q, want 'version=0 (no migrations applied)'", got)
	}

	// 2. After Up — version=<n>.
	if err := mig.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	got = mig.Status(ctx)
	if !strings.HasPrefix(got, "version=") || strings.Contains(got, "dirty=") {
		t.Errorf("Status after Up = %q, want 'version=<n>' (no dirty)", got)
	}

	// 3. Force dirty — version=<n> dirty=true (recovery required).
	// We simulate by Forcing to a negative (invalid) version then back.
	// Actually we can't easily produce a dirty state without running a
	// migration that SQL-errors. Skip dirty shape coverage at this
	// level; the T-030 integration test covers dirty logging end-to-end.
}

func TestMigrator_Force_ResetsVersion(t *testing.T) {
	t.Parallel()
	mig, err := NewMigrator(context.Background(), sqliteDBConfig(t))
	if err != nil {
		t.Fatalf("NewMigrator: %v", err)
	}
	defer func() { _ = mig.Close() }()

	ctx := context.Background()
	if err := mig.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}

	// Force back to 0 — schema tables remain but migration state is
	// zeroed. This is the recovery knob the CLI exposes.
	if err := mig.Force(ctx, 0); err != nil {
		t.Fatalf("Force(0): %v", err)
	}
	v, dirty, err := mig.Version(ctx)
	if err != nil {
		t.Fatalf("Version after Force: %v", err)
	}
	// After Force(0), golang-migrate records version=0, not-dirty,
	// which our Version() surfaces as (0, false, nil).
	if v != 0 || dirty {
		t.Errorf("Version after Force(0) = (%d, %v), want (0, false)", v, dirty)
	}
}

// TestMigrator_Force_InvalidVersion covers tasks.md T-019 error_path
// ("Force(ctx, 999) on an unknown version returns an error"). Before
// the T-001 fix, Force silently accepted arbitrary versions because
// golang-migrate does not validate them.
func TestMigrator_Force_InvalidVersion(t *testing.T) {
	t.Parallel()
	mig, err := NewMigrator(context.Background(), sqliteDBConfig(t))
	if err != nil {
		t.Fatalf("NewMigrator: %v", err)
	}
	defer func() { _ = mig.Close() }()
	ctx := context.Background()

	// 999 does not correspond to any migration file — must error.
	err = mig.Force(ctx, 999)
	if err == nil {
		t.Fatal("Force(999) err = nil, want non-nil (T-019 error_path)")
	}
	if !strings.Contains(err.Error(), "version") {
		t.Errorf("Force(999) err = %q, want to mention 'version'", err.Error())
	}

	// Negative version — must also error.
	if err := mig.Force(ctx, -1); err == nil {
		t.Error("Force(-1) err = nil, want non-nil (negative versions are invalid)")
	}

	// Version 0 is the null-state marker; must still succeed even
	// against a source that has no migration at version 0.
	if err := mig.Force(ctx, 0); err != nil {
		t.Errorf("Force(0) err = %v, want nil (null-state marker)", err)
	}
}

func TestMigrator_Down_UndoesMigrations(t *testing.T) {
	t.Parallel()
	mig, err := NewMigrator(context.Background(), sqliteDBConfig(t))
	if err != nil {
		t.Fatalf("NewMigrator: %v", err)
	}
	defer func() { _ = mig.Close() }()

	ctx := context.Background()
	if err := mig.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if err := mig.Down(ctx); err != nil {
		t.Fatalf("Down: %v", err)
	}
	// Down on a fully-unwound DB is ErrNoChange → nil.
	if err := mig.Down(ctx); err != nil {
		t.Errorf("second Down: %v, want nil (ErrNoChange suppressed)", err)
	}
}

func TestMigrator_Close_IsIdempotent(t *testing.T) {
	t.Parallel()
	mig, err := NewMigrator(context.Background(), sqliteDBConfig(t))
	if err != nil {
		t.Fatalf("NewMigrator: %v", err)
	}

	if err := mig.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := mig.Close(); err != nil {
		t.Errorf("second Close: %v, want nil (idempotent)", err)
	}
}

func TestMigrator_MethodsRespectContext(t *testing.T) {
	t.Parallel()
	mig, err := NewMigrator(context.Background(), sqliteDBConfig(t))
	if err != nil {
		t.Fatalf("NewMigrator: %v", err)
	}
	defer func() { _ = mig.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := mig.Up(ctx); err == nil {
		t.Error("Up with cancelled ctx returned nil")
	}
	if err := mig.Down(ctx); err == nil {
		t.Error("Down with cancelled ctx returned nil")
	}
	if err := mig.Force(ctx, 0); err == nil {
		t.Error("Force with cancelled ctx returned nil")
	}
	if _, _, err := mig.Version(ctx); err == nil {
		t.Error("Version with cancelled ctx returned nil")
	}
}

func TestMigrator_PingTimeout_FailsFast(t *testing.T) {
	t.Parallel()
	// Use an unreachable postgres DSN (TCP port that refuses) so
	// PingContext errors quickly rather than waiting minutes. We are
	// exercising the error path of NewMigrator, not the postgres
	// driver itself.
	ctx, cancel := context.WithTimeout(context.Background(), 2*pingTimeout)
	defer cancel()
	_, err := NewMigrator(ctx, &config.DBConfig{
		Driver: "postgres",
		URL:    "postgres://user:pwd@127.0.0.1:1/dbname?sslmode=disable&connect_timeout=1",
	})
	if err == nil {
		t.Fatal("err = nil, want ping failure")
	}
	// Must surface under pingTimeout clock with generous budget.
	if !strings.Contains(err.Error(), "store.NewMigrator") {
		t.Errorf("err = %v, want store.NewMigrator prefix", err)
	}
}

// Deadline safety — the pingTimeout constant MUST NOT regress to a
// value that allows production boot to hang for tens of seconds.
func TestMigrator_PingTimeoutBounds(t *testing.T) {
	t.Parallel()
	if pingTimeout <= 0 || pingTimeout > 10*time.Second {
		t.Errorf("pingTimeout = %v, expected in (0, 10s]", pingTimeout)
	}
}

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratedb "github.com/golang-migrate/migrate/v4/database"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/user/one-llm-router/internal/config"
)

// pingTimeout bounds the initial DB connectivity check inside
// NewMigrator. Kept short so boot-time failure surfaces quickly — a
// misconfigured DSN SHOULD NOT silently stall BuildApp for the
// default sql/driver deadline (which can be minutes).
const pingTimeout = 5 * time.Second

// Migrator is a thin, ctx-aware wrapper over golang-migrate/v4. It
// centralises all golang-migrate imports inside the store package
// (per tasks.md T-019 "must_not: import golang-migrate from any
// package other than internal/store") and exposes the five primitives
// that BuildApp and the `one-llm-router migrate` CLI need: Up / Down / Force /
// Version / Status.
//
// Lifecycle:
//
//	mig, err := store.NewMigrator(ctx, &cfg.DB)
//	if err != nil { ... }
//	defer mig.Close()
//	if err := mig.Up(ctx); err != nil { ... }
//
// Thread-safety: Migrator is NOT safe for concurrent use. The
// underlying migrate.Migrate holds a single DB connection; callers
// MUST serialise access. Production callers (BuildApp boot,
// `one-llm-router migrate` CLI) each hold their own instance.
type Migrator struct {
	m      *migrate.Migrate
	driver string
	// validVersions is the set of versions the embedded iofs source
	// recognises. Populated at NewMigrator by walking the source
	// driver so Force can pre-validate the requested version without
	// stat'ing the embed.FS on every call (tasks.md T-019 error_path:
	// `Force(ctx, 999)` on an unknown version must error). An empty
	// set means "no migrations present" and Force(0) is the only
	// version accepted in that case (golang-migrate uses version=0
	// as the null-state marker).
	validVersions map[int]struct{}
}

// NewMigrator opens a dedicated sql.DB against db.URL, constructs an
// iofs source over the dialect's embedded migrations, and wires the
// two into a migrate.Migrate instance. The returned Migrator owns the
// sql.DB — Close releases it.
//
// The sql.DB is pinned to MaxOpenConns=1 because migration is a
// strictly single-connection workflow under golang-migrate; any other
// value invites driver-specific lock contention.
func NewMigrator(ctx context.Context, db *config.DBConfig) (*Migrator, error) {
	if db == nil {
		return nil, errors.New("store.NewMigrator: db config is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	dialect, err := LookupDialect(db.Driver)
	if err != nil {
		return nil, err
	}
	dsn := dialect.NormalizeDSN(db.URL)

	sqlDB, err := sql.Open(dialect.XormDriverName(), dsn)
	if err != nil {
		return nil, fmt.Errorf("store.NewMigrator: open db: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)

	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("store.NewMigrator: ping db: %w", err)
	}

	dbDrv, err := dialect.NewMigrateDriver(sqlDB)
	if err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("store.NewMigrator: create migrate driver: %w", err)
	}

	// From here on, ownership of sqlDB transfers to dbDrv — closing
	// dbDrv closes the underlying sqlDB.  Any error BEFORE
	// NewWithInstance succeeds MUST close dbDrv, not sqlDB directly.

	fsys, err := dialect.MigrationsFS()
	if err != nil {
		_ = closeMigrateDriver(dbDrv)
		return nil, fmt.Errorf("store.NewMigrator: migrations FS: %w", err)
	}
	src, err := iofs.New(fsys, ".")
	if err != nil {
		_ = closeMigrateDriver(dbDrv)
		return nil, fmt.Errorf("store.NewMigrator: iofs source: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, dialect.MigrateDriverName(), dbDrv)
	if err != nil {
		_ = closeMigrateDriver(dbDrv)
		_ = src.Close()
		return nil, fmt.Errorf("store.NewMigrator: new migrate: %w", err)
	}
	// Note: m.Close() takes over cleanup of both src and dbDrv (and
	// therefore sqlDB) from this point onward.

	// Enumerate the source versions once so Force can pre-validate.
	// Use a FRESH iofs instance so we don't disturb the iterator
	// state held by the Migrate wrapper — iofs.Driver's First/Next
	// are stateful and golang-migrate consumes them during Up/Down.
	valid := map[int]struct{}{}
	if enumFS, err := iofs.New(fsys, "."); err == nil {
		valid = enumerateSourceVersions(enumFS)
		_ = enumFS.Close()
	}

	return &Migrator{m: m, driver: db.Driver, validVersions: valid}, nil
}

// enumerateSourceVersions walks a source.Driver to build the set of
// recognised versions. Returns nil if enumeration fails; callers
// treat nil as "skip validation" so a source iterator bug never
// causes a Force call that would otherwise succeed to fail closed.
func enumerateSourceVersions(src interface {
	First() (uint, error)
	Next(uint) (uint, error)
}) map[int]struct{} {
	out := make(map[int]struct{})
	v, err := src.First()
	if err != nil {
		// No migrations at all, or enumeration not supported —
		// either way, return an empty set (Force(0) is the only
		// legal version against a migration-less source).
		return out
	}
	out[int(v)] = struct{}{}
	for {
		next, err := src.Next(v)
		if err != nil {
			return out
		}
		out[int(next)] = struct{}{}
		v = next
	}
}

// closeMigrateDriver wraps the Close signature of migratedb.Driver so
// error handling stays uniform with source.Driver (which also returns
// error). Kept tiny on purpose.
func closeMigrateDriver(d migratedb.Driver) error {
	return d.Close()
}

// Up applies all pending migrations. Returns nil when the schema is
// already at head (ErrNoChange is swallowed per T-019 "Up returns nil
// on ErrNoChange"). Any other error from golang-migrate is wrapped
// with a store-prefixed message so callers can grep the logs.
//
// Context cancellation caveat (L-002): golang-migrate's Migrate.Up
// does NOT accept a context, so a long-running migration cannot be
// interrupted mid-statement by cancelling ctx. We honour ctx.Err()
// at entry, giving "don't start if already cancelled" semantics; a
// cancel that arrives AFTER mig.m.Up() begins will be observed only
// once the underlying statement finishes. For hung migrations the
// operator's recourse is a second SIGINT (which takes the signal
// handler's hard-exit path in cmd/one-llm-router/main.go) or a direct
// `one-llm-router migrate force <v>` after the process is killed.
func (mig *Migrator) Up(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := mig.m.Up()
	if err == nil || errors.Is(err, migrate.ErrNoChange) {
		return nil
	}
	return fmt.Errorf("store.Migrator.Up (%s): %w", mig.driver, err)
}

// Down rolls back all applied migrations. Intended for the CLI path
// only; BuildApp never calls Down. ErrNoChange is swallowed for
// idempotency, matching Up.
func (mig *Migrator) Down(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := mig.m.Down()
	if err == nil || errors.Is(err, migrate.ErrNoChange) {
		return nil
	}
	return fmt.Errorf("store.Migrator.Down (%s): %w", mig.driver, err)
}

// Steps runs n migrations in either direction. n > 0 applies forward
// migrations; n < 0 rolls back that many. Returns nil on ErrNoChange
// for idempotent step calls. Mostly useful for the migration round-
// trip tests and future CLI `one-llm-router migrate steps <n>` surface;
// production boot still goes through Up.
func (mig *Migrator) Steps(ctx context.Context, n int) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("store.Migrator.Steps (%s, n=%d): context: %w", mig.driver, n, err)
	}
	err := mig.m.Steps(n)
	if err == nil || errors.Is(err, migrate.ErrNoChange) {
		return nil
	}
	return fmt.Errorf("store.Migrator.Steps (%s, n=%d): %w", mig.driver, n, err)
}

// Force marks the schema at version v and clears any "dirty" flag.
// Used to recover from a partially-applied migration after manual
// database surgery.
//
// Validation: per tasks.md T-019 error_path, Force MUST reject a
// version that does not correspond to any migration file. We
// pre-compute the set of valid versions at NewMigrator time (see
// enumerateSourceVersions) so this check adds no runtime I/O.
// Version 0 is accepted unconditionally as golang-migrate's
// null-state marker (used to reset a dirty schema back to pristine).
// Negative versions are rejected explicitly.
//
// If the validVersions set is unavailable (enumeration failed at
// construction), Force skips the pre-check and defers to
// golang-migrate's own behaviour.
func (mig *Migrator) Force(ctx context.Context, v int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if v < 0 {
		return fmt.Errorf("store.Migrator.Force (%s, v=%d): negative version is invalid", mig.driver, v)
	}
	if v != 0 && len(mig.validVersions) > 0 {
		if _, ok := mig.validVersions[v]; !ok {
			return fmt.Errorf("store.Migrator.Force (%s, v=%d): version does not correspond to any migration file", mig.driver, v)
		}
	}
	if err := mig.m.Force(v); err != nil {
		return fmt.Errorf("store.Migrator.Force (%s, v=%d): %w", mig.driver, v, err)
	}
	return nil
}

// Version returns the migration version currently recorded in the
// schema_migrations table plus the dirty flag (true means a
// migration was partially applied and rolled back, requiring Force).
// A fresh database with no migrations yet applied returns (0, false,
// nil) — ErrNilVersion is translated into the zero version.
func (mig *Migrator) Version(ctx context.Context) (uint, bool, error) {
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	v, dirty, err := mig.m.Version()
	if err != nil {
		if errors.Is(err, migrate.ErrNilVersion) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("store.Migrator.Version (%s): %w", mig.driver, err)
	}
	return v, dirty, nil
}

// Status returns a human-readable, log-friendly one-liner describing
// the current schema state. Intended for boot-time logging and the
// `one-llm-router migrate status` CLI subcommand. Never returns an empty
// string.
//
// Shapes (stable for grep):
//
//	"version=0 (no migrations applied)"
//	"version=3"
//	"version=3 dirty=true (recovery required)"
//	"error: <wrapped err>"
func (mig *Migrator) Status(ctx context.Context) string {
	v, dirty, err := mig.Version(ctx)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	if dirty {
		return fmt.Sprintf("version=%d dirty=true (recovery required)", v)
	}
	if v == 0 {
		return "version=0 (no migrations applied)"
	}
	return fmt.Sprintf("version=%d", v)
}

// Close releases the migrate instance and its underlying sql.DB.
// Safe to call multiple times; the second call is a no-op. Returns
// the first non-nil error from the two golang-migrate close
// callbacks (source and database driver).
func (mig *Migrator) Close() error {
	if mig == nil || mig.m == nil {
		return nil
	}
	srcErr, dbErr := mig.m.Close()
	mig.m = nil
	if srcErr != nil {
		return fmt.Errorf("store.Migrator.Close source: %w", srcErr)
	}
	if dbErr != nil {
		return fmt.Errorf("store.Migrator.Close db: %w", dbErr)
	}
	return nil
}

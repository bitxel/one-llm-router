package store

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"strings"

	migratedb "github.com/golang-migrate/migrate/v4/database"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	// Side-effect import: registers the "sqlite" driver with database/sql.
	_ "modernc.org/sqlite"
	"xorm.io/xorm"
)

//go:embed migrations/sqlite/*.sql
var sqliteMigrations embed.FS

func init() {
	RegisterDialect(sqliteDialect{})
}

type sqliteDialect struct{}

func (sqliteDialect) Name() string              { return "sqlite3" }
func (sqliteDialect) XormDriverName() string    { return "sqlite" }
func (sqliteDialect) MigrateDriverName() string { return "sqlite" }

// sqlitePragmaDefaults are appended to the DSN so that modernc.org/sqlite
// applies them on EVERY new connection (not just the first one, which is the
// failure mode of `engine.Exec("PRAGMA ...")`). Mirrors codex-lb's
// @event.listens_for("connect") pattern. synchronous=NORMAL matches
// codex-lb's defaults and is the recommended durability/perf tradeoff under
// WAL per https://sqlite.org/pragma.html#pragma_synchronous.
var sqlitePragmaDefaults = []string{
	"_pragma=busy_timeout(5000)",
	"_pragma=foreign_keys(1)",
	"_pragma=journal_mode(WAL)",
	"_pragma=synchronous(NORMAL)",
}

// NormalizeDSN returns the default path when dsn is empty and appends our
// PRAGMA defaults unless the operator has already supplied the same keys.
// The special ":memory:" DSN is passed through unchanged: PRAGMAs like WAL
// are meaningless for in-memory databases and tests rely on private,
// isolated per-open instances.
//
// modernc.org/sqlite only honours query parameters when the DSN is a URI
// (i.e. starts with "file:"), so plain paths like "router.db" are rewritten
// to "file:router.db?..." before pragma appending. Existing "file:" URIs
// and query strings are preserved.
func (sqliteDialect) NormalizeDSN(dsn string) string {
	if dsn == "" {
		dsn = "router.db"
	}
	if dsn == ":memory:" {
		return dsn
	}
	if !strings.HasPrefix(dsn, "file:") {
		dsn = "file:" + dsn
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	for _, p := range sqlitePragmaDefaults {
		if hasPragmaAlready(dsn, p) {
			continue
		}
		dsn += sep + p
		sep = "&"
	}
	return dsn
}

// hasPragmaAlready reports whether the DSN query string already contains the
// pragma name from spec like "_pragma=busy_timeout(5000)". We only match on
// the pragma name so operators can override the value.
func hasPragmaAlready(dsn, spec string) bool {
	// spec == "_pragma=busy_timeout(5000)" -> name = "_pragma=busy_timeout("
	open := strings.Index(spec, "(")
	if open < 0 {
		return strings.Contains(dsn, spec)
	}
	prefix := spec[:open+1]
	return strings.Contains(dsn, prefix)
}

// ConfigureEngine applies PRAGMAs for the in-memory DSN where we cannot use
// _pragma= query parameters. For file-backed databases all PRAGMAs are
// already delivered via the DSN so this is a no-op.
func (sqliteDialect) ConfigureEngine(engine *xorm.Engine) error {
	if engine.DataSourceName() != ":memory:" {
		return nil
	}
	pragmas := []string{
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
		// journal_mode=WAL is intentionally omitted for :memory:.
	}
	for _, p := range pragmas {
		if _, err := engine.Exec(p); err != nil {
			return fmt.Errorf("apply pragma %q: %w", p, err)
		}
	}
	return nil
}

// ConfigurePool pins SQLite to a single open connection to avoid
// "database is locked" with in-memory and WAL-mode databases.
func (sqliteDialect) ConfigurePool(engine *xorm.Engine, _ int32, _ int32) {
	engine.SetMaxOpenConns(1)
}

func (sqliteDialect) MigrationsFS() (fs.FS, error) {
	sub, err := fs.Sub(sqliteMigrations, "migrations/sqlite")
	if err != nil {
		return nil, fmt.Errorf("sub sqlite migrations: %w", err)
	}
	return sub, nil
}

func (sqliteDialect) NewMigrateDriver(db *sql.DB) (migratedb.Driver, error) {
	return migratesqlite.WithInstance(db, &migratesqlite.Config{})
}

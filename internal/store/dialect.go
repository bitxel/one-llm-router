package store

import (
	"database/sql"
	"fmt"
	"io/fs"
	"sort"
	"sync"

	migratedb "github.com/golang-migrate/migrate/v4/database"
	"xorm.io/xorm"
)

// Dialect encapsulates everything that differs between relational databases:
// connection setup, engine/pool tuning, embedded migration assets, and the
// migration driver wiring for golang-migrate. Adding a new RDBMS is a matter
// of implementing this interface and calling RegisterDialect in an init().
type Dialect interface {
	// Name is the canonical identifier used in config (e.g. "sqlite3").
	Name() string

	// XormDriverName is the sql.Register driver name passed to xorm.NewEngine.
	XormDriverName() string

	// MigrateDriverName is the migration library driver name
	// (e.g. "sqlite", "postgres", "mysql").
	MigrateDriverName() string

	// NormalizeDSN lets a dialect apply defaults or append required query
	// parameters (e.g. MySQL parseTime=true).
	NormalizeDSN(dsn string) string

	// ConfigureEngine runs dialect-specific statements after the engine
	// is created (e.g. SQLite PRAGMAs). Must be idempotent.
	ConfigureEngine(engine *xorm.Engine) error

	// ConfigurePool applies connection-pool settings. A dialect may choose
	// to override the provided values (SQLite pins to 1 writer).
	ConfigurePool(engine *xorm.Engine, maxConns, minConns int32)

	// MigrationsFS returns the embedded migrations rooted at the subdirectory
	// returned by MigrationsSubpath.
	MigrationsFS() (fs.FS, error)

	// NewMigrateDriver wraps a *sql.DB with the dialect-specific migration
	// driver used by golang-migrate.
	NewMigrateDriver(db *sql.DB) (migratedb.Driver, error)
}

var (
	dialectMu sync.RWMutex
	dialects  = map[string]Dialect{}
)

// RegisterDialect adds a dialect to the registry. Intended to be called from
// init() in dialect-specific files so that importing the package enables the
// driver. Panics on duplicate registration to catch bugs early.
func RegisterDialect(d Dialect) {
	dialectMu.Lock()
	defer dialectMu.Unlock()
	if _, exists := dialects[d.Name()]; exists {
		panic(fmt.Sprintf("store: dialect %q already registered", d.Name()))
	}
	dialects[d.Name()] = d
}

// LookupDialect returns the registered dialect by name.
func LookupDialect(name string) (Dialect, error) {
	dialectMu.RLock()
	defer dialectMu.RUnlock()
	d, ok := dialects[name]
	if !ok {
		return nil, fmt.Errorf("unsupported database driver: %q (supported: %v)", name, supportedDriversLocked())
	}
	return d, nil
}

// SupportedDrivers returns the sorted list of registered dialect names. Safe
// for concurrent use.
func SupportedDrivers() []string {
	dialectMu.RLock()
	defer dialectMu.RUnlock()
	return supportedDriversLocked()
}

func supportedDriversLocked() []string {
	names := make([]string, 0, len(dialects))
	for k := range dialects {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

package store

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/user/one-llm-router/internal/domain"
	"xorm.io/xorm"
)

type Store struct {
	engine  *xorm.Engine
	dialect Dialect
}

// New creates a Store for the requested driver. Supported drivers are
// registered via the dialect registry (dialect_*.go files). Adding a new
// RDBMS requires only a new dialect implementation plus migration assets.
func New(driver, dsn string, maxConns, minConns int32) (*Store, error) {
	dialect, err := LookupDialect(driver)
	if err != nil {
		return nil, err
	}

	dsn = dialect.NormalizeDSN(dsn)

	engine, err := xorm.NewEngine(dialect.XormDriverName(), dsn)
	if err != nil {
		return nil, fmt.Errorf("create xorm engine (%s): %w", dialect.Name(), err)
	}
	// xorm defaults both application and database time zones to Local.
	// Feature 003's timestamp contract is UTC-stable across dialects, so
	// we pin both sides explicitly instead of inheriting the router host's
	// local zone.
	engine.SetTZLocation(time.UTC)
	engine.SetTZDatabase(time.UTC)

	if err := dialect.ConfigureEngine(engine); err != nil {
		return nil, fmt.Errorf("configure %s engine: %w", dialect.Name(), err)
	}

	dialect.ConfigurePool(engine, maxConns, minConns)

	return &Store{engine: engine, dialect: dialect}, nil
}

// Migrate runs the up migrations for the Store's dialect using golang-migrate.
// The driver argument is accepted for backward compatibility but must match
// the driver this Store was created with.
func (s *Store) Migrate(driver string) error {
	if driver != "" && driver != s.dialect.Name() {
		return fmt.Errorf("migrate driver mismatch: store=%s requested=%s", s.dialect.Name(), driver)
	}

	migrateFS, err := s.dialect.MigrationsFS()
	if err != nil {
		return err
	}

	dbDriver, err := s.dialect.NewMigrateDriver(s.engine.DB().DB)
	if err != nil {
		return fmt.Errorf("create migration driver: %w", err)
	}

	source, err := iofs.New(migrateFS, ".")
	if err != nil {
		return fmt.Errorf("create migration source: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", source, s.dialect.MigrateDriverName(), dbDriver)
	if err != nil {
		return fmt.Errorf("create migrate instance: %w", err)
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}

// Sync is a test-only helper that uses XORM's struct sync. Production paths
// must use Migrate for parity with real deployments.
func (s *Store) Sync() error {
	return s.engine.Sync2(
		new(domain.UpstreamAccount),
		new(domain.RequestRecord),
	)
}

func (s *Store) Engine() *xorm.Engine {
	return s.engine
}

func (s *Store) Dialect() Dialect {
	return s.dialect
}

func (s *Store) Close() error {
	return s.engine.Close()
}

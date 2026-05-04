package store

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	migratedb "github.com/golang-migrate/migrate/v4/database"
	migratepg "github.com/golang-migrate/migrate/v4/database/postgres"
	// Side-effect import: registers the "postgres" driver with database/sql.
	_ "github.com/lib/pq"
	"xorm.io/xorm"
)

//go:embed migrations/postgres/*.sql
var postgresMigrations embed.FS

func init() {
	RegisterDialect(postgresDialect{})
}

type postgresDialect struct{}

func (postgresDialect) Name() string              { return "postgres" }
func (postgresDialect) XormDriverName() string    { return "postgres" }
func (postgresDialect) MigrateDriverName() string { return "postgres" }

func (postgresDialect) NormalizeDSN(dsn string) string { return dsn }

func (postgresDialect) ConfigureEngine(_ *xorm.Engine) error { return nil }

func (postgresDialect) ConfigurePool(engine *xorm.Engine, maxConns, minConns int32) {
	engine.SetMaxOpenConns(int(maxConns))
	engine.SetMaxIdleConns(int(minConns))
}

func (postgresDialect) MigrationsFS() (fs.FS, error) {
	sub, err := fs.Sub(postgresMigrations, "migrations/postgres")
	if err != nil {
		return nil, fmt.Errorf("sub postgres migrations: %w", err)
	}
	return sub, nil
}

func (postgresDialect) NewMigrateDriver(db *sql.DB) (migratedb.Driver, error) {
	return migratepg.WithInstance(db, &migratepg.Config{})
}

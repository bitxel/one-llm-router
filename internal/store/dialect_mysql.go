package store

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"strings"

	// Side-effect import: registers the "mysql" driver with database/sql.
	_ "github.com/go-sql-driver/mysql"
	migratedb "github.com/golang-migrate/migrate/v4/database"
	migratemysql "github.com/golang-migrate/migrate/v4/database/mysql"
	"xorm.io/xorm"
)

//go:embed migrations/mysql/*.sql
var mysqlMigrations embed.FS

func init() {
	RegisterDialect(mysqlDialect{})
}

type mysqlDialect struct{}

func (mysqlDialect) Name() string              { return "mysql" }
func (mysqlDialect) XormDriverName() string    { return "mysql" }
func (mysqlDialect) MigrateDriverName() string { return "mysql" }

// NormalizeDSN appends the DSN parameters required by both the MySQL driver
// (parseTime=true for time.Time) and golang-migrate (multiStatements=true).
// Existing values in the DSN are preserved.
func (mysqlDialect) NormalizeDSN(dsn string) string {
	required := map[string]string{
		"parseTime":       "true",
		"multiStatements": "true",
		"charset":         "utf8mb4",
	}
	sep := "?"
	if idx := strings.Index(dsn, "?"); idx >= 0 {
		sep = "&"
	}
	for key, val := range required {
		if !containsQueryParam(dsn, key) {
			dsn += sep + key + "=" + val
			sep = "&"
		}
	}
	return dsn
}

func containsQueryParam(dsn, key string) bool {
	idx := strings.Index(dsn, "?")
	if idx < 0 {
		return false
	}
	query := dsn[idx+1:]
	for _, part := range strings.Split(query, "&") {
		if eq := strings.Index(part, "="); eq >= 0 {
			if part[:eq] == key {
				return true
			}
		} else if part == key {
			return true
		}
	}
	return false
}

func (mysqlDialect) ConfigureEngine(_ *xorm.Engine) error { return nil }

func (mysqlDialect) ConfigurePool(engine *xorm.Engine, maxConns, minConns int32) {
	engine.SetMaxOpenConns(int(maxConns))
	engine.SetMaxIdleConns(int(minConns))
}

func (mysqlDialect) MigrationsFS() (fs.FS, error) {
	sub, err := fs.Sub(mysqlMigrations, "migrations/mysql")
	if err != nil {
		return nil, fmt.Errorf("sub mysql migrations: %w", err)
	}
	return sub, nil
}

func (mysqlDialect) NewMigrateDriver(db *sql.DB) (migratedb.Driver, error) {
	return migratemysql.WithInstance(db, &migratemysql.Config{})
}

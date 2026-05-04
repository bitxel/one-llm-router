package app

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// openRawSQLite opens a vanilla sql.DB against the given file path
// using modernc's pure-Go driver. Used exclusively by the integration
// tests that need to mutate schema_migrations outside the migrator's
// lifecycle.
func openRawSQLite(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open sqlite: %v", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		t.Fatalf("db.Ping: %v", err)
	}
	return db
}

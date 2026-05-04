package store

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSupportedDrivers_IncludesRegistered(t *testing.T) {
	drivers := SupportedDrivers()
	for _, want := range []string{"sqlite3", "postgres", "mysql"} {
		assert.Contains(t, drivers, want, "expected registry to include %q", want)
	}
}

func TestLookupDialect_Unknown(t *testing.T) {
	_, err := LookupDialect("unknown_driver")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported database driver")
}

func TestLookupDialect_Known(t *testing.T) {
	for _, name := range []string{"sqlite3", "postgres", "mysql"} {
		d, err := LookupDialect(name)
		require.NoError(t, err, name)
		assert.Equal(t, name, d.Name())
		assert.NotEmpty(t, d.XormDriverName(), name)
		assert.NotEmpty(t, d.MigrateDriverName(), name)
		fsys, err := d.MigrationsFS()
		require.NoError(t, err, name)
		assert.NotNil(t, fsys, name)
	}
}

func TestRegisterDialect_Duplicate_Panics(t *testing.T) {
	d, err := LookupDialect("sqlite3")
	require.NoError(t, err)
	assert.Panics(t, func() {
		RegisterDialect(d)
	})
}

func TestMySQLDialect_NormalizeDSN_AddsRequiredParams(t *testing.T) {
	d, err := LookupDialect("mysql")
	require.NoError(t, err)

	dsn := d.NormalizeDSN("user:pass@tcp(localhost:3306)/mydb")
	assert.Contains(t, dsn, "parseTime=true")
	assert.Contains(t, dsn, "multiStatements=true")
	assert.Contains(t, dsn, "charset=utf8mb4")
	assert.Equal(t, 1, strings.Count(dsn, "?"), "DSN should have exactly one '?'")
}

func TestMySQLDialect_NormalizeDSN_PreservesExisting(t *testing.T) {
	d, err := LookupDialect("mysql")
	require.NoError(t, err)

	dsn := d.NormalizeDSN("user:pass@tcp(host)/db?parseTime=false&charset=latin1")
	assert.Contains(t, dsn, "parseTime=false", "must not override user-supplied value")
	assert.Contains(t, dsn, "charset=latin1", "must not override user-supplied value")
	assert.Contains(t, dsn, "multiStatements=true", "must still add missing param")
}

func TestSQLiteDialect_NormalizeDSN_DefaultsPath(t *testing.T) {
	d, err := LookupDialect("sqlite3")
	require.NoError(t, err)
	// Empty DSN falls back to the router.db file path with PRAGMA defaults.
	// Driver requires the file: URI scheme for query-string parsing.
	got := d.NormalizeDSN("")
	assert.True(t, strings.HasPrefix(got, "file:router.db?"), "empty DSN must default to file:router.db URI (got %q)", got)
	assert.Contains(t, got, "_pragma=busy_timeout(5000)")
	assert.Contains(t, got, "_pragma=foreign_keys(1)")
	assert.Contains(t, got, "_pragma=journal_mode(WAL)")
	assert.Contains(t, got, "_pragma=synchronous(NORMAL)")

	got = d.NormalizeDSN("custom.db")
	assert.True(t, strings.HasPrefix(got, "file:custom.db?"), "got %q", got)
	assert.Contains(t, got, "_pragma=journal_mode(WAL)")
}

// :memory: is passed through unchanged (WAL/URI semantics are meaningless).
func TestSQLiteDialect_NormalizeDSN_MemoryUnchanged(t *testing.T) {
	d, err := LookupDialect("sqlite3")
	require.NoError(t, err)
	assert.Equal(t, ":memory:", d.NormalizeDSN(":memory:"))
}

// Operator-supplied PRAGMA must win over our defaults.
func TestSQLiteDialect_NormalizeDSN_PreservesExistingPragma(t *testing.T) {
	d, err := LookupDialect("sqlite3")
	require.NoError(t, err)

	got := d.NormalizeDSN("custom.db?_pragma=journal_mode(DELETE)&_pragma=foreign_keys(0)")
	assert.Contains(t, got, "_pragma=journal_mode(DELETE)", "operator value must not be overridden")
	assert.Contains(t, got, "_pragma=foreign_keys(0)")
	// Remaining pragmas still appended.
	assert.Contains(t, got, "_pragma=busy_timeout(5000)")
	assert.Contains(t, got, "_pragma=synchronous(NORMAL)")
	assert.NotContains(t, got, "_pragma=journal_mode(WAL)", "our WAL default must not be appended when operator set a different value")
}

// Operator that already set file: URI must not get double-prefixed.
func TestSQLiteDialect_NormalizeDSN_PreservesFileURI(t *testing.T) {
	d, err := LookupDialect("sqlite3")
	require.NoError(t, err)
	got := d.NormalizeDSN("file:/var/lib/router/app.db")
	assert.True(t, strings.HasPrefix(got, "file:/var/lib/router/app.db?"), "got %q", got)
	assert.False(t, strings.HasPrefix(got, "file:file:"))
}

func TestPostgresDialect_NormalizeDSN_Unchanged(t *testing.T) {
	d, err := LookupDialect("postgres")
	require.NoError(t, err)
	in := "postgres://user:pass@host/db"
	assert.Equal(t, in, d.NormalizeDSN(in))
}

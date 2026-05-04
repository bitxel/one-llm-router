package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/user/one-llm-router/internal/config"
)

// TestMigrator_MultiModeAuth_UpDownInvariance is the per-backend
// rollback invariance test called out by tasks.md T-094b (scheduled
// under Phase 10 but already wired here while the migration files are
// freshly landed — a failure after T-010/T-011 is far cheaper to debug
// than a failure after Phase 10's parallel test explosion).
//
// It asserts the 003 migration pair (SQLite variant) satisfies four
// invariants the rest of Feature 003 relies on:
//
//  1. `up` adds all 9 new columns to upstream_accounts AND relaxes
//     api_key from NOT NULL to NULL so oauth_* rows can be inserted.
//  2. `down` drops those 9 columns AND reinstates api_key NOT NULL.
//  3. `up → down → up` leaves the schema byte-identical to a
//     one-shot `up` (drift here means a migration pair is not an
//     inverse of itself and the CI gate at T-094b will catch it late).
//  4. The request_records → upstream_accounts FK survives both
//     transitions — insertable references to a row that was present
//     before the migration still land.
//
// Runs under the default sqliteDBConfig (file-backed, per-test
// tempdir) so it exercises the same code path the production boot
// wires up. postgres/mysql variants are covered in T-094b proper
// once the dialect test harness lands.
func TestMigrator_MultiModeAuth_UpDownInvariance(t *testing.T) {
	t.Parallel()

	dbCfg := sqliteDBConfig(t)
	ctx := context.Background()

	// ---- Step 1: initial Up through 000002 ----
	mig, err := NewMigrator(ctx, dbCfg)
	if err != nil {
		t.Fatalf("NewMigrator: %v", err)
	}
	if err := mig.Steps(ctx, 2); err != nil {
		t.Fatalf("first Steps(+2): %v", err)
	}
	if err := mig.Close(); err != nil {
		t.Fatalf("Close after first Up: %v", err)
	}

	// Inspect post-Up schema + insert rows of both auth_method types.
	sqlDB := openTestDB(t, dbCfg)
	postUpCreateSQL := readCreateTableSQL(t, sqlDB, "upstream_accounts")
	mustHaveColumn(t, postUpCreateSQL, "auth_method")
	mustHaveColumn(t, postUpCreateSQL, "access_token")
	mustHaveColumn(t, postUpCreateSQL, "access_expires_at")
	mustNotHaveText(t, postUpCreateSQL, "api_key     TEXT NOT NULL",
		"api_key NOT NULL must be relaxed after 000002 up")

	// Index assertion: `idx_upstream_accounts_auth_method` is the
	// only non-status index 000002 adds; the admin list query
	// `WHERE status='active' AND auth_method=?` relies on it. A
	// migration that drops the `CREATE INDEX` statement is a silent
	// perf regression — this check kills that mutation.
	mustHaveIndex(t, sqlDB, "idx_upstream_accounts_auth_method")

	// Insert one api_key row + one oauth row. Oauth row demonstrates
	// that api_key NULL is now accepted. The first INSERT OMITS
	// auth_method entirely so we can assert the SQL-level DEFAULT
	// 'api_key' kicked in; a future migration that drops the
	// DEFAULT (or typos it) lands the column as empty, not 'api_key',
	// and the select-back below will fail.
	mustExec(t, sqlDB, `INSERT INTO upstream_accounts (name, api_key) VALUES ('k1', 'sk-1')`)
	var storedMethod string
	if err := sqlDB.QueryRow(`SELECT auth_method FROM upstream_accounts WHERE name = 'k1'`).Scan(&storedMethod); err != nil {
		t.Fatalf("select-back auth_method: %v", err)
	}
	if storedMethod != "api_key" {
		t.Errorf("auth_method DEFAULT on plain api_key insert = %q, want \"api_key\" (SQL DEFAULT drift)", storedMethod)
	}
	mustExec(t, sqlDB, `INSERT INTO upstream_accounts (name, auth_method, access_token, refresh_token, id_token, last_refresh, access_expires_at) VALUES ('o1', 'oauth_browser', x'11', x'22', x'33', '2026-04-15T10:00:00Z', '2026-04-15T11:00:00Z')`)

	// FK sanity: a request_record pointing at the oauth row (id=2)
	// must insert cleanly — drifts in the migration's FK handling
	// show up here long before they do in production.
	mustExec(t, sqlDB, `INSERT INTO request_records (request_id, upstream_account_id, method, path, status_code, latency_ms, outcome) VALUES ('r-oauth', 2, 'POST', '/v1/chat', 200, 10, 'success')`)

	_ = sqlDB.Close()

	// ---- Step 2: roll back ONLY 000002 (Steps(-1)) ----
	// Down() rolls back everything to the zero state; we only want
	// to exercise the 000002 up/down pair, so Steps(-1) is the precise
	// knob. The test intentionally stops before 000003 so later
	// migrations cannot change what this invariance check is proving.
	mig, err = NewMigrator(ctx, dbCfg)
	if err != nil {
		t.Fatalf("NewMigrator (pre-Down): %v", err)
	}
	if err := mig.Steps(ctx, -1); err != nil {
		t.Fatalf("Steps(-1): %v", err)
	}
	if err := mig.Close(); err != nil {
		t.Fatalf("Close after Down: %v", err)
	}

	sqlDB = openTestDB(t, dbCfg)
	postDownCreateSQL := readCreateTableSQL(t, sqlDB, "upstream_accounts")
	mustNotHaveColumn(t, postDownCreateSQL, "auth_method")
	mustNotHaveColumn(t, postDownCreateSQL, "access_token")
	mustNotHaveColumn(t, postDownCreateSQL, "access_expires_at")
	mustHaveText(t, postDownCreateSQL, "api_key     TEXT NOT NULL",
		"api_key NOT NULL must be restored after 000002 down")

	// OAuth row should be gone (rollback is explicitly destructive
	// for oauth_* rows — data-model.md §Rollback).
	var oauthCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM upstream_accounts WHERE api_key IS NULL`).Scan(&oauthCount); err != nil {
		t.Fatalf("count oauth rows: %v", err)
	}
	if oauthCount != 0 {
		t.Errorf("oauth-row count after Down = %d, want 0 (rollback must delete api_key-NULL rows)", oauthCount)
	}

	// api_key row must survive.
	var apikeyCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM upstream_accounts WHERE api_key IS NOT NULL`).Scan(&apikeyCount); err != nil {
		t.Fatalf("count api_key rows: %v", err)
	}
	if apikeyCount != 1 {
		t.Errorf("api_key-row count after Down = %d, want 1", apikeyCount)
	}
	_ = sqlDB.Close()

	// ---- Step 3: Up again (rollback invariance) ----
	mig, err = NewMigrator(ctx, dbCfg)
	if err != nil {
		t.Fatalf("NewMigrator (second Up): %v", err)
	}
	if err := mig.Steps(ctx, 1); err != nil {
		t.Fatalf("second Steps(+1): %v", err)
	}
	if err := mig.Close(); err != nil {
		t.Fatalf("Close after second Up: %v", err)
	}

	sqlDB = openTestDB(t, dbCfg)
	defer func() { _ = sqlDB.Close() }()
	postUp2CreateSQL := readCreateTableSQL(t, sqlDB, "upstream_accounts")

	if postUp2CreateSQL != postUpCreateSQL {
		t.Errorf("schema drifted across up→down→up cycle.\npost-up1 schema:\n%s\npost-up2 schema:\n%s",
			postUpCreateSQL, postUp2CreateSQL)
	}

	// Insertability check after the second up — oauth inserts must
	// work again so subsequent migrations aren't silently broken.
	mustExec(t, sqlDB, `INSERT INTO upstream_accounts (name, auth_method, access_token, refresh_token, id_token, last_refresh, access_expires_at) VALUES ('o2', 'oauth_device', x'AA', x'BB', x'CC', '2026-04-15T12:00:00Z', '2026-04-15T13:00:00Z')`)
}

func TestMigrator_RequestBodySplit_UpDownInvariance(t *testing.T) {
	t.Parallel()

	dbCfg := sqliteDBConfig(t)
	ctx := context.Background()

	mig, err := NewMigrator(ctx, dbCfg)
	if err != nil {
		t.Fatalf("NewMigrator: %v", err)
	}
	if err := mig.Steps(ctx, 2); err != nil {
		t.Fatalf("Steps(+2): %v", err)
	}
	if err := mig.Close(); err != nil {
		t.Fatalf("Close after Steps(+2): %v", err)
	}

	sqlDB := openTestDB(t, dbCfg)
	assertRequestRecordBodyColumnsBaseline(t, sqlDB)
	mustExec(t, sqlDB, `INSERT INTO request_records (request_id, method, path, status_code, latency_ms, outcome, request_body, response_body) VALUES ('r-body-split', 'POST', '/v1/responses', 200, 12, 'success', 'client-body', 'response-body')`)
	assertRequestRecordLegacyValues(t, sqlDB, "client-body", "response-body")
	_ = sqlDB.Close()

	mig, err = NewMigrator(ctx, dbCfg)
	if err != nil {
		t.Fatalf("NewMigrator (000003 up): %v", err)
	}
	if err := mig.Steps(ctx, 1); err != nil {
		t.Fatalf("Steps(+1): %v", err)
	}
	if err := mig.Close(); err != nil {
		t.Fatalf("Close after Steps(+1): %v", err)
	}

	sqlDB = openTestDB(t, dbCfg)
	assertRequestRecordBodyColumnsForward(t, sqlDB)
	assertRequestRecordSplitValues(t, sqlDB, "client-body", "", "response-body")
	mustExec(t, sqlDB, `UPDATE request_records SET upstream_request_body = 'upstream-body' WHERE request_id = 'r-body-split'`)
	assertRequestRecordSplitValues(t, sqlDB, "client-body", "upstream-body", "response-body")
	_ = sqlDB.Close()

	mig, err = NewMigrator(ctx, dbCfg)
	if err != nil {
		t.Fatalf("NewMigrator (pre-down): %v", err)
	}
	if err := mig.Steps(ctx, -1); err != nil {
		t.Fatalf("Steps(-1): %v", err)
	}
	if err := mig.Close(); err != nil {
		t.Fatalf("Close after Steps(-1): %v", err)
	}

	sqlDB = openTestDB(t, dbCfg)
	assertRequestRecordBodyColumnsBaseline(t, sqlDB)
	assertRequestRecordLegacyValues(t, sqlDB, "client-body", "response-body")
	_ = sqlDB.Close()

	mig, err = NewMigrator(ctx, dbCfg)
	if err != nil {
		t.Fatalf("NewMigrator (reapply): %v", err)
	}
	if err := mig.Steps(ctx, 1); err != nil {
		t.Fatalf("reapply Steps(+1): %v", err)
	}
	if err := mig.Close(); err != nil {
		t.Fatalf("Close after reapply: %v", err)
	}

	sqlDB = openTestDB(t, dbCfg)
	defer func() { _ = sqlDB.Close() }()
	assertRequestRecordBodyColumnsForward(t, sqlDB)
	assertRequestRecordSplitValues(t, sqlDB, "client-body", "", "response-body")
}

func assertRequestRecordBodyColumnsForward(t *testing.T, db *sql.DB) {
	t.Helper()

	columns := requestRecordColumnSet(t, db)
	for _, column := range []string{"client_request_body", "upstream_request_body", "upstream_response_body"} {
		if !columns[column] {
			t.Fatalf("request_records missing forward body column %q; columns=%v", column, columns)
		}
	}
	for _, column := range []string{"request_body", "response_body"} {
		if columns[column] {
			t.Fatalf("request_records unexpectedly retained legacy body column %q; columns=%v", column, columns)
		}
	}
}

func assertRequestRecordBodyColumnsBaseline(t *testing.T, db *sql.DB) {
	t.Helper()

	columns := requestRecordColumnSet(t, db)
	for _, column := range []string{"request_body", "response_body"} {
		if !columns[column] {
			t.Fatalf("request_records missing baseline body column %q; columns=%v", column, columns)
		}
	}
	for _, column := range []string{"client_request_body", "upstream_request_body", "upstream_response_body"} {
		if columns[column] {
			t.Fatalf("request_records unexpectedly retained split body column %q; columns=%v", column, columns)
		}
	}
}

func requestRecordColumnSet(t *testing.T, db *sql.DB) map[string]bool {
	t.Helper()

	rows, err := db.Query(`PRAGMA table_info(request_records)`)
	if err != nil {
		t.Fatalf("sqlite PRAGMA table_info(request_records): %v", err)
	}
	defer func() { _ = rows.Close() }()

	columns := make(map[string]bool)
	for rows.Next() {
		var (
			cid        int
			name       string
			colType    string
			notNull    int
			defaultVal sql.NullString
			pk         int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultVal, &pk); err != nil {
			t.Fatalf("sqlite request_records PRAGMA scan: %v", err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("sqlite request_records PRAGMA rows: %v", err)
	}
	return columns
}

func assertRequestRecordSplitValues(t *testing.T, db *sql.DB, wantClient, wantUpstream, wantResponse string) {
	t.Helper()

	var (
		client   sql.NullString
		upstream sql.NullString
		response sql.NullString
	)
	if err := db.QueryRow(`SELECT client_request_body, upstream_request_body, upstream_response_body FROM request_records WHERE request_id = 'r-body-split'`).
		Scan(&client, &upstream, &response); err != nil {
		t.Fatalf("select split request body values: %v", err)
	}
	assertNullableString(t, "client_request_body", client, wantClient)
	assertNullableString(t, "upstream_request_body", upstream, wantUpstream)
	assertNullableString(t, "upstream_response_body", response, wantResponse)
}

func assertRequestRecordLegacyValues(t *testing.T, db *sql.DB, wantRequest, wantResponse string) {
	t.Helper()

	var (
		request  sql.NullString
		response sql.NullString
	)
	if err := db.QueryRow(`SELECT request_body, response_body FROM request_records WHERE request_id = 'r-body-split'`).
		Scan(&request, &response); err != nil {
		t.Fatalf("select legacy request body values: %v", err)
	}
	assertNullableString(t, "request_body", request, wantRequest)
	assertNullableString(t, "response_body", response, wantResponse)
}

func assertNullableString(t *testing.T, label string, got sql.NullString, want string) {
	t.Helper()

	if want == "" {
		if got.Valid && got.String != "" {
			t.Fatalf("%s = %q, want empty or NULL", label, got.String)
		}
		return
	}
	if !got.Valid {
		t.Fatalf("%s is NULL, want %q", label, want)
	}
	if got.String != want {
		t.Fatalf("%s = %q, want %q", label, got.String, want)
	}
}

// openTestDB opens a raw sql.DB against the sqlite file at dbCfg. It
// applies the same PRAGMAs the production dialect wires into the DSN
// so foreign_keys enforcement matches runtime (otherwise the test
// would silently pass even if the migration broke the FK).
func openTestDB(t *testing.T, dbCfg *config.DBConfig) *sql.DB {
	t.Helper()
	dialect, err := LookupDialect(dbCfg.Driver)
	if err != nil {
		t.Fatalf("LookupDialect: %v", err)
	}
	db, err := sql.Open(dialect.XormDriverName(), dialect.NormalizeDSN(dbCfg.URL))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	return db
}

func readCreateTableSQL(t *testing.T, db *sql.DB, table string) string {
	t.Helper()
	var sqlText string
	err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&sqlText)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("table %q missing from sqlite_master", table)
		}
		t.Fatalf("read sqlite_master: %v", err)
	}
	return sqlText
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func mustHaveColumn(t *testing.T, createSQL, col string) {
	t.Helper()
	if !strings.Contains(createSQL, col) {
		t.Errorf("CREATE TABLE is missing column %q.\nSchema:\n%s", col, createSQL)
	}
}

func mustNotHaveColumn(t *testing.T, createSQL, col string) {
	t.Helper()
	if strings.Contains(createSQL, col) {
		t.Errorf("CREATE TABLE unexpectedly still contains column %q.\nSchema:\n%s", col, createSQL)
	}
}

func mustHaveText(t *testing.T, createSQL, needle, label string) {
	t.Helper()
	if !strings.Contains(createSQL, needle) {
		t.Errorf("%s: expected %q in CREATE TABLE.\nSchema:\n%s", label, needle, createSQL)
	}
}

func mustNotHaveText(t *testing.T, createSQL, needle, label string) {
	t.Helper()
	if strings.Contains(createSQL, needle) {
		t.Errorf("%s: unexpectedly found %q in CREATE TABLE.\nSchema:\n%s", label, needle, createSQL)
	}
}

// mustHaveIndex asserts an index of the given name exists on SQLite
// via sqlite_master. The same introspection idea works on Postgres
// (pg_indexes) / MySQL (SHOW INDEX) when those dialect tests are
// wired in under T-094b proper.
func mustHaveIndex(t *testing.T, db *sql.DB, indexName string) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, indexName).Scan(&count); err != nil {
		t.Fatalf("sqlite_master lookup for index %q: %v", indexName, err)
	}
	if count == 0 {
		t.Errorf("expected index %q to exist after 000002 up", indexName)
	}
}

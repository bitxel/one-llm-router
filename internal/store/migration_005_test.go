package store

import (
	"context"
	"database/sql"
	"testing"
)

func TestMigrator_RequestClientIP_UpDownInvariance(t *testing.T) {
	t.Parallel()

	dbCfg := sqliteDBConfig(t)
	ctx := context.Background()

	mig, err := NewMigrator(ctx, dbCfg)
	if err != nil {
		t.Fatalf("NewMigrator: %v", err)
	}
	if err := mig.Steps(ctx, 4); err != nil {
		t.Fatalf("Steps(+4): %v", err)
	}
	if err := mig.Close(); err != nil {
		t.Fatalf("Close after Steps(+4): %v", err)
	}

	sqlDB := openTestDB(t, dbCfg)
	assertRequestRecordClientIPColumn(t, sqlDB, false)
	mustExec(t, sqlDB, `INSERT INTO request_records (request_id, method, path, status_code, latency_ms, outcome) VALUES ('r-client-ip', 'POST', '/v1/responses', 200, 12, 'success')`)
	_ = sqlDB.Close()

	mig, err = NewMigrator(ctx, dbCfg)
	if err != nil {
		t.Fatalf("NewMigrator (000005 up): %v", err)
	}
	if err := mig.Steps(ctx, 1); err != nil {
		t.Fatalf("Steps(+1): %v", err)
	}
	if err := mig.Close(); err != nil {
		t.Fatalf("Close after Steps(+1): %v", err)
	}

	sqlDB = openTestDB(t, dbCfg)
	assertRequestRecordClientIPColumn(t, sqlDB, true)
	assertRequestRecordClientIPValue(t, sqlDB, "r-client-ip", "")
	mustExec(t, sqlDB, `UPDATE request_records SET client_ip = '203.0.113.77' WHERE request_id = 'r-client-ip'`)
	assertRequestRecordClientIPValue(t, sqlDB, "r-client-ip", "203.0.113.77")
	_ = sqlDB.Close()

	mig, err = NewMigrator(ctx, dbCfg)
	if err != nil {
		t.Fatalf("NewMigrator (000005 down): %v", err)
	}
	if err := mig.Steps(ctx, -1); err != nil {
		t.Fatalf("Steps(-1): %v", err)
	}
	if err := mig.Close(); err != nil {
		t.Fatalf("Close after Steps(-1): %v", err)
	}

	sqlDB = openTestDB(t, dbCfg)
	assertRequestRecordClientIPColumn(t, sqlDB, false)
	_ = sqlDB.Close()

	mig, err = NewMigrator(ctx, dbCfg)
	if err != nil {
		t.Fatalf("NewMigrator (000005 reapply): %v", err)
	}
	if err := mig.Steps(ctx, 1); err != nil {
		t.Fatalf("reapply Steps(+1): %v", err)
	}
	if err := mig.Close(); err != nil {
		t.Fatalf("Close after reapply: %v", err)
	}

	sqlDB = openTestDB(t, dbCfg)
	defer func() { _ = sqlDB.Close() }()
	assertRequestRecordClientIPColumn(t, sqlDB, true)
	assertRequestRecordClientIPValue(t, sqlDB, "r-client-ip", "")
}

func assertRequestRecordClientIPColumn(t *testing.T, db *sql.DB, want bool) {
	t.Helper()

	columns := requestRecordColumnSet(t, db)
	if columns["client_ip"] != want {
		t.Fatalf("request_records client_ip column exists=%v, want %v; columns=%v", columns["client_ip"], want, columns)
	}
}

func assertRequestRecordClientIPValue(t *testing.T, db *sql.DB, requestID string, want string) {
	t.Helper()

	var got sql.NullString
	if err := db.QueryRow(`SELECT client_ip FROM request_records WHERE request_id = ?`, requestID).Scan(&got); err != nil {
		t.Fatalf("select client_ip for %q: %v", requestID, err)
	}
	assertNullableString(t, "client_ip", got, want)
}

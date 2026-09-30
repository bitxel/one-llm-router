package store

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func TestMigrator_AccountModelsMetadata_UpDownInvariance(t *testing.T) {
	t.Parallel()

	dbCfg := sqliteDBConfig(t)
	ctx := context.Background()

	mig, err := NewMigrator(ctx, dbCfg)
	if err != nil {
		t.Fatalf("NewMigrator: %v", err)
	}
	if err := mig.Steps(ctx, 10); err != nil {
		t.Fatalf("Steps(+10): %v", err)
	}
	if err := mig.Close(); err != nil {
		t.Fatalf("Close after Steps(+10): %v", err)
	}

	sqlDB := openTestDB(t, dbCfg)
	assertAccountModelsMetadataColumn(t, sqlDB, false)
	_ = sqlDB.Close()

	mig, err = NewMigrator(ctx, dbCfg)
	if err != nil {
		t.Fatalf("NewMigrator (000011 up): %v", err)
	}
	if err := mig.Steps(ctx, 1); err != nil {
		t.Fatalf("Steps(+1): %v", err)
	}
	if err := mig.Close(); err != nil {
		t.Fatalf("Close after Steps(+1): %v", err)
	}

	sqlDB = openTestDB(t, dbCfg)
	assertAccountModelsMetadataColumn(t, sqlDB, true)
	mustExec(t, sqlDB, `INSERT INTO upstream_accounts (name, provider, api_key, status) VALUES ('meta', 'openai', 'sk', 'active')`)
	mustExec(t, sqlDB, `INSERT INTO account_models (account_id, model_id, source, metadata) VALUES (1, 'gpt-4o', 'upstream', '{"id":"gpt-4o","object":"model"}')`)
	var got string
	if err := sqlDB.QueryRow(`SELECT metadata FROM account_models WHERE model_id = 'gpt-4o'`).Scan(&got); err != nil {
		t.Fatalf("select metadata: %v", err)
	}
	if got != `{"id":"gpt-4o","object":"model"}` {
		t.Fatalf("metadata = %q, want stored JSON", got)
	}
	_ = sqlDB.Close()

	mig, err = NewMigrator(ctx, dbCfg)
	if err != nil {
		t.Fatalf("NewMigrator (000011 down): %v", err)
	}
	if err := mig.Steps(ctx, -1); err != nil {
		t.Fatalf("Steps(-1): %v", err)
	}
	if err := mig.Close(); err != nil {
		t.Fatalf("Close after Steps(-1): %v", err)
	}

	sqlDB = openTestDB(t, dbCfg)
	assertAccountModelsMetadataColumn(t, sqlDB, false)
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
	assertAccountModelsMetadataColumn(t, sqlDB, true)
}

func assertAccountModelsMetadataColumn(t *testing.T, db *sql.DB, want bool) {
	t.Helper()

	rows, err := db.Query(`PRAGMA table_info(account_models)`)
	if err != nil {
		t.Fatalf("pragma table_info(account_models): %v", err)
	}
	defer func() { _ = rows.Close() }()

	columns := map[string]bool{}
	for rows.Next() {
		var (
			cid       int
			name      string
			typ       string
			notNull   int
			dfltValue sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
			t.Fatalf("scan pragma: %v", err)
		}
		columns[strings.ToLower(name)] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("pragma rows: %v", err)
	}

	if columns["metadata"] != want {
		t.Fatalf("account_models metadata column exists=%v, want %v; columns=%v", columns["metadata"], want, columns)
	}
}

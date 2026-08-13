package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	mysqldrv "github.com/go-sql-driver/mysql"
	"github.com/lib/pq"
	"github.com/user/one-llm-router/internal/config"
	"github.com/user/one-llm-router/internal/core"
	"github.com/user/one-llm-router/internal/domain"
)

const testPostgresDSNEnv = "TEST_POSTGRES_DSN"
const testMySQLDSNEnv = "TEST_MYSQL_DSN"

type rollbackBackend struct {
	name string
	cfg  *config.DBConfig
}

func TestMigrationRollback(t *testing.T) {
	t.Helper()
	verifyRollbackMigrationFiles(t)

	backends := []rollbackBackend{
		{name: "sqlite", cfg: sqliteDBConfig(t)},
	}
	if pgCfg := postgresRollbackDBConfig(t); pgCfg != nil {
		backends = append(backends, rollbackBackend{name: "postgres", cfg: pgCfg})
	}
	if myCfg := mysqlRollbackDBConfig(t); myCfg != nil {
		backends = append(backends, rollbackBackend{name: "mysql", cfg: myCfg})
	}

	for _, backend := range backends {
		backend := backend
		t.Run(backend.name, func(t *testing.T) {
			runMigrationRollbackCase(t, backend)
		})
	}
}

func verifyRollbackMigrationFiles(t *testing.T) {
	t.Helper()

	cases := []struct {
		name string
		path string
	}{
		{name: "sqlite", path: rollbackFixturePath(t, "migrations", "sqlite", "000002_multi_mode_auth.down.sql")},
		{name: "postgres", path: rollbackFixturePath(t, "migrations", "postgres", "000002_multi_mode_auth.down.sql")},
		{name: "mysql", path: rollbackFixturePath(t, "migrations", "mysql", "000002_multi_mode_auth.down.sql")},
	}
	disallowed := []string{
		"DROP COLUMN api_key",
		"DROP COLUMN base_url",
		"DROP COLUMN provider",
		"DROP COLUMN status",
		"DROP COLUMN created_at",
		"DROP COLUMN updated_at",
		"DROP TABLE request_records",
		"DROP TABLE upstream_accounts",
	}

	for _, tc := range cases {
		body, err := os.ReadFile(tc.path)
		if err != nil {
			t.Fatalf("%s rollback migration missing: %v", tc.name, err)
		}
		sqlText := string(body)
		for _, needle := range disallowed {
			if strings.Contains(sqlText, needle) {
				t.Fatalf("%s rollback migration contains destructive 002 statement %q", tc.name, needle)
			}
		}
	}
}

func rollbackFixturePath(t *testing.T, elems ...string) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	parts := append([]string{filepath.Dir(file)}, elems...)
	return filepath.Join(parts...)
}

func runMigrationRollbackCase(t *testing.T, backend rollbackBackend) {
	t.Helper()

	ctx := context.Background()
	mig, err := NewMigrator(ctx, backend.cfg)
	if err != nil {
		t.Fatalf("NewMigrator(%s): %v", backend.name, err)
	}
	defer func() { _ = mig.Close() }()

	if err := mig.Steps(ctx, 1); err != nil {
		t.Fatalf("%s first Steps(+1): %v", backend.name, err)
	}

	baselineDB := openTestDB(t, backend.cfg)
	baseline := insertRollbackAPIKeyRow(t, baselineDB, backend.cfg.Driver, "baseline-api-key")
	assertRollbackBaselineRow(t, baselineDB, backend.cfg.Driver, baseline)
	_ = baselineDB.Close()

	if err := mig.Steps(ctx, 1); err != nil {
		t.Fatalf("%s second Steps(+1): %v", backend.name, err)
	}

	forwardDB := openTestDB(t, backend.cfg)
	assertRollbackForwardSchema(t, forwardDB, backend.cfg.Driver)
	secondAPIKey := insertRollbackAPIKeyRow(t, forwardDB, backend.cfg.Driver, "forward-api-key")
	insertRollbackOAuthRow(t, forwardDB, backend.cfg.Driver, "forward-oauth")
	assertRollbackBaselineRow(t, forwardDB, backend.cfg.Driver, baseline)
	assertRollbackBaselineRow(t, forwardDB, backend.cfg.Driver, secondAPIKey)
	_ = forwardDB.Close()

	if err := mig.Steps(ctx, -1); err != nil {
		t.Fatalf("%s Steps(-1): %v", backend.name, err)
	}

	rolledBackDB := openTestDB(t, backend.cfg)
	assertRollbackBaselineSchema(t, rolledBackDB, backend.cfg.Driver)
	assertRollbackBaselineRow(t, rolledBackDB, backend.cfg.Driver, baseline)
	assertRollbackBaselineRow(t, rolledBackDB, backend.cfg.Driver, secondAPIKey)
	assertRollbackOAuthRowDropped(t, rolledBackDB, backend.cfg.Driver, "forward-oauth")
	if got := countNullAPIKeyRows(t, rolledBackDB, backend.cfg.Driver); got != 0 {
		t.Fatalf("%s oauth rows after rollback = %d, want 0", backend.name, got)
	}
	assertRollbackLegacySelectorRoundTrip(t, rolledBackDB, backend.cfg.Driver, baseline, secondAPIKey)
	_ = rolledBackDB.Close()

	if err := mig.Steps(ctx, 1); err != nil {
		t.Fatalf("%s reapply Steps(+1): %v", backend.name, err)
	}

	reappliedDB := openTestDB(t, backend.cfg)
	defer func() { _ = reappliedDB.Close() }()
	assertRollbackForwardSchema(t, reappliedDB, backend.cfg.Driver)
	assertRollbackBaselineRow(t, reappliedDB, backend.cfg.Driver, baseline)
	assertRollbackBaselineRow(t, reappliedDB, backend.cfg.Driver, secondAPIKey)
	assertRollbackForwardTypes(t, reappliedDB, backend.cfg.Driver)
}

type rollbackAPIKeyRow struct {
	ID       int64
	Name     string
	Provider string
	APIKey   string
	Status   string
}

func insertRollbackAPIKeyRow(t *testing.T, db *sql.DB, driver, name string) rollbackAPIKeyRow {
	t.Helper()

	row := rollbackAPIKeyRow{
		Name:     name,
		Provider: "openai",
		APIKey:   "sk_" + strings.ReplaceAll(name, "-", "_") + "_fixture_0123456789abcdef",
		Status:   "active",
	}
	switch driver {
	case "sqlite3":
		mustExec(t, db, `INSERT INTO upstream_accounts (name, provider, api_key, status) VALUES (?, ?, ?, ?)`,
			row.Name, row.Provider, row.APIKey, row.Status)
	case "postgres":
		mustExec(t, db, `INSERT INTO upstream_accounts (name, provider, api_key, status) VALUES ($1, $2, $3, $4)`,
			row.Name, row.Provider, row.APIKey, row.Status)
	case "mysql":
		mustExec(t, db, `INSERT INTO upstream_accounts (name, provider, api_key, status) VALUES (?, ?, ?, ?)`,
			row.Name, row.Provider, row.APIKey, row.Status)
	default:
		t.Fatalf("unsupported driver %q", driver)
	}
	row.ID = lookupRollbackAPIKeyID(t, db, driver, row.Name)
	return row
}

func lookupRollbackAPIKeyID(t *testing.T, db *sql.DB, driver, name string) int64 {
	t.Helper()

	var id int64
	switch driver {
	case "sqlite3", "mysql":
		if err := db.QueryRow(`SELECT id FROM upstream_accounts WHERE name = ?`, name).Scan(&id); err != nil {
			t.Fatalf("%s lookup api-key row id %q: %v", driver, name, err)
		}
	case "postgres":
		if err := db.QueryRow(`SELECT id FROM upstream_accounts WHERE name = $1`, name).Scan(&id); err != nil {
			t.Fatalf("%s lookup api-key row id %q: %v", driver, name, err)
		}
	default:
		t.Fatalf("unsupported driver %q", driver)
	}
	return id
}

func insertRollbackOAuthRow(t *testing.T, db *sql.DB, driver, name string) {
	t.Helper()

	lastRefresh := time.Date(2026, 4, 22, 10, 0, 0, 0, time.UTC)
	expiresAt := lastRefresh.Add(2 * time.Hour)
	switch driver {
	case "sqlite3":
		mustExec(t, db, `INSERT INTO upstream_accounts (name, provider, auth_method, status, access_token, refresh_token, id_token, last_refresh, access_expires_at, email, plan_type, chatgpt_account_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			name,
			"openai",
			"oauth_browser",
			"active",
			[]byte("oauth-access"),
			[]byte("oauth-refresh"),
			[]byte("oauth-id"),
			lastRefresh.Format(time.RFC3339),
			expiresAt.Format(time.RFC3339),
			"oauth@example.com",
			"chatgpt-plus",
			"acct_rollback_123",
		)
	case "postgres":
		mustExec(t, db, `INSERT INTO upstream_accounts (name, provider, auth_method, status, access_token, refresh_token, id_token, last_refresh, access_expires_at, email, plan_type, chatgpt_account_id) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
			name,
			"openai",
			"oauth_browser",
			"active",
			[]byte("oauth-access"),
			[]byte("oauth-refresh"),
			[]byte("oauth-id"),
			lastRefresh,
			expiresAt,
			"oauth@example.com",
			"chatgpt-plus",
			"acct_rollback_123",
		)
	case "mysql":
		mustExec(t, db, `INSERT INTO upstream_accounts (name, provider, auth_method, status, access_token, refresh_token, id_token, last_refresh, access_expires_at, email, plan_type, chatgpt_account_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			name,
			"openai",
			"oauth_browser",
			"active",
			[]byte("oauth-access"),
			[]byte("oauth-refresh"),
			[]byte("oauth-id"),
			lastRefresh,
			expiresAt,
			"oauth@example.com",
			"chatgpt-plus",
			"acct_rollback_123",
		)
	default:
		t.Fatalf("unsupported driver %q", driver)
	}
}

func assertRollbackBaselineRow(t *testing.T, db *sql.DB, driver string, want rollbackAPIKeyRow) {
	t.Helper()

	var got rollbackAPIKeyRow
	switch driver {
	case "sqlite3":
		if err := db.QueryRow(`SELECT id, name, provider, api_key, status FROM upstream_accounts WHERE name = ?`, want.Name).
			Scan(&got.ID, &got.Name, &got.Provider, &got.APIKey, &got.Status); err != nil {
			t.Fatalf("select sqlite baseline row %q: %v", want.Name, err)
		}
	case "postgres":
		if err := db.QueryRow(`SELECT id, name, provider, api_key, status FROM upstream_accounts WHERE name = $1`, want.Name).
			Scan(&got.ID, &got.Name, &got.Provider, &got.APIKey, &got.Status); err != nil {
			t.Fatalf("select postgres baseline row %q: %v", want.Name, err)
		}
	case "mysql":
		if err := db.QueryRow(`SELECT id, name, provider, api_key, status FROM upstream_accounts WHERE name = ?`, want.Name).
			Scan(&got.ID, &got.Name, &got.Provider, &got.APIKey, &got.Status); err != nil {
			t.Fatalf("select mysql baseline row %q: %v", want.Name, err)
		}
	default:
		t.Fatalf("unsupported driver %q", driver)
	}

	if got != want {
		t.Fatalf("%s row drifted across rollback cycle: got %+v want %+v", driver, got, want)
	}
}

type rollbackLegacyRepo struct {
	db     *sql.DB
	driver string
}

func (r rollbackLegacyRepo) Create(context.Context, *domain.UpstreamAccount) error {
	return errors.New("rollbackLegacyRepo.Create not implemented")
}

func (r rollbackLegacyRepo) UpdateStatus(context.Context, int64, string) error {
	return errors.New("rollbackLegacyRepo.UpdateStatus not implemented")
}

func (r rollbackLegacyRepo) UpdateDetails(context.Context, int64, core.AccountDetailsPatch) (*domain.UpstreamAccount, error) {
	return nil, errors.New("rollbackLegacyRepo.UpdateDetails not implemented")
}

func (r rollbackLegacyRepo) List(_ context.Context, statusFilter []string) ([]domain.UpstreamAccount, error) {
	if len(statusFilter) == 0 {
		return r.queryActiveRows()
	}
	if len(statusFilter) == 1 && statusFilter[0] == domain.AccountStatusActive {
		return r.queryActiveRows()
	}
	return nil, fmt.Errorf("rollbackLegacyRepo.List unsupported status filter %v", statusFilter)
}

func (r rollbackLegacyRepo) ListActive(context.Context) ([]domain.UpstreamAccount, error) {
	return r.queryActiveRows()
}

func (r rollbackLegacyRepo) GetByID(_ context.Context, id int64) (*domain.UpstreamAccount, error) {
	var row domain.UpstreamAccount
	switch r.driver {
	case "sqlite3", "mysql":
		err := r.db.QueryRow(`SELECT id, name, provider, api_key, status FROM upstream_accounts WHERE id = ?`, id).
			Scan(&row.ID, &row.Name, &row.Provider, &row.APIKey, &row.Status)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrAccountNotFound
		}
		if err != nil {
			return nil, fmt.Errorf("legacy get account %d: %w", id, err)
		}
	case "postgres":
		err := r.db.QueryRow(`SELECT id, name, provider, api_key, status FROM upstream_accounts WHERE id = $1`, id).
			Scan(&row.ID, &row.Name, &row.Provider, &row.APIKey, &row.Status)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrAccountNotFound
		}
		if err != nil {
			return nil, fmt.Errorf("legacy get account %d: %w", id, err)
		}
	default:
		return nil, fmt.Errorf("unsupported driver %q", r.driver)
	}
	return &row, nil
}

func (r rollbackLegacyRepo) queryActiveRows() ([]domain.UpstreamAccount, error) {
	var (
		rows *sql.Rows
		err  error
	)
	switch r.driver {
	case "sqlite3", "mysql":
		rows, err = r.db.Query(`SELECT id, name, provider, api_key, status FROM upstream_accounts WHERE status = ? ORDER BY id ASC`, domain.AccountStatusActive)
	case "postgres":
		rows, err = r.db.Query(`SELECT id, name, provider, api_key, status FROM upstream_accounts WHERE status = $1 ORDER BY id ASC`, domain.AccountStatusActive)
	default:
		err = fmt.Errorf("unsupported driver %q", r.driver)
	}
	if err != nil {
		return nil, fmt.Errorf("legacy list active rows: %w", err)
	}
	defer func() { _ = rows.Close() }()

	active := make([]domain.UpstreamAccount, 0, 2)
	for rows.Next() {
		var row domain.UpstreamAccount
		if scanErr := rows.Scan(&row.ID, &row.Name, &row.Provider, &row.APIKey, &row.Status); scanErr != nil {
			return nil, fmt.Errorf("legacy scan active row: %w", scanErr)
		}
		active = append(active, row)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("legacy iterate active rows: %w", rowsErr)
	}
	return active, nil
}

func assertRollbackLegacySelectorRoundTrip(
	t *testing.T,
	db *sql.DB,
	driver string,
	wantRows ...rollbackAPIKeyRow,
) {
	t.Helper()

	repo := rollbackLegacyRepo{db: db, driver: driver}
	active, err := repo.ListActive(context.Background())
	if err != nil {
		t.Fatalf("%s legacy ListActive after rollback: %v", driver, err)
	}
	if len(active) != len(wantRows) {
		t.Fatalf("%s legacy ListActive count=%d want=%d", driver, len(active), len(wantRows))
	}
	for _, want := range wantRows {
		got, err := repo.GetByID(context.Background(), want.ID)
		if err != nil {
			t.Fatalf("%s legacy GetByID(%d): %v", driver, want.ID, err)
		}
		if got.Name != want.Name || got.Provider != want.Provider || got.APIKey != want.APIKey || got.Status != want.Status {
			t.Fatalf("%s legacy GetByID(%d) = %+v want %+v", driver, want.ID, got, want)
		}
	}

	selector := core.NewAccountSelector(repo, nil)
	selected, err := selector.SelectAccount(context.Background(), "")
	if err != nil {
		t.Fatalf("%s legacy selector after rollback: %v", driver, err)
	}
	if selected.APIKey == "" {
		t.Fatalf("%s legacy selector returned row without api_key: %+v", driver, selected)
	}
}

func assertRollbackOAuthRowDropped(t *testing.T, db *sql.DB, driver, name string) {
	t.Helper()

	var count int
	switch driver {
	case "sqlite3", "mysql":
		if err := db.QueryRow(`SELECT COUNT(*) FROM upstream_accounts WHERE name = ?`, name).Scan(&count); err != nil {
			t.Fatalf("%s count oauth row %q: %v", driver, name, err)
		}
	case "postgres":
		if err := db.QueryRow(`SELECT COUNT(*) FROM upstream_accounts WHERE name = $1`, name).Scan(&count); err != nil {
			t.Fatalf("%s count oauth row %q: %v", driver, name, err)
		}
	default:
		t.Fatalf("unsupported driver %q", driver)
	}
	if count != 0 {
		t.Fatalf("%s rollback retained oauth row %q (count=%d)", driver, name, count)
	}
}

func countNullAPIKeyRows(t *testing.T, db *sql.DB, driver string) int {
	t.Helper()

	var count int
	switch driver {
	case "sqlite3":
		if err := db.QueryRow(`SELECT COUNT(*) FROM upstream_accounts WHERE api_key IS NULL`).Scan(&count); err != nil {
			t.Fatalf("count sqlite oauth rows: %v", err)
		}
	case "postgres":
		if err := db.QueryRow(`SELECT COUNT(*) FROM upstream_accounts WHERE api_key IS NULL`).Scan(&count); err != nil {
			t.Fatalf("count postgres oauth rows: %v", err)
		}
	case "mysql":
		if err := db.QueryRow(`SELECT COUNT(*) FROM upstream_accounts WHERE api_key IS NULL`).Scan(&count); err != nil {
			t.Fatalf("count mysql oauth rows: %v", err)
		}
	default:
		t.Fatalf("unsupported driver %q", driver)
	}
	return count
}

func assertRollbackForwardSchema(t *testing.T, db *sql.DB, driver string) {
	t.Helper()

	for _, column := range []string{
		"auth_method",
		"access_token",
		"refresh_token",
		"id_token",
		"last_refresh",
		"access_expires_at",
		"email",
		"plan_type",
		"chatgpt_account_id",
	} {
		if !hasRollbackColumn(t, db, driver, column) {
			t.Fatalf("%s missing forward column %q", driver, column)
		}
	}
	if rollbackColumnRequired(t, db, driver, "api_key") {
		t.Fatalf("%s api_key unexpectedly still required after forward migration", driver)
	}
	if !hasRollbackIndex(t, db, driver, "idx_upstream_accounts_auth_method") {
		t.Fatalf("%s missing idx_upstream_accounts_auth_method after forward migration", driver)
	}
	switch driver {
	case "postgres":
		if !hasPostgresConstraint(t, db, "chk_auth_method") {
			t.Fatal("postgres missing chk_auth_method after forward migration")
		}
	case "mysql":
		if !hasMySQLConstraint(t, db, "chk_auth_method") {
			t.Fatal("mysql missing chk_auth_method after forward migration")
		}
	}
}

func assertRollbackBaselineSchema(t *testing.T, db *sql.DB, driver string) {
	t.Helper()

	for _, column := range []string{
		"auth_method",
		"access_token",
		"refresh_token",
		"id_token",
		"last_refresh",
		"access_expires_at",
		"email",
		"plan_type",
		"chatgpt_account_id",
	} {
		if hasRollbackColumn(t, db, driver, column) {
			t.Fatalf("%s rollback unexpectedly retained column %q", driver, column)
		}
	}
	if !rollbackColumnRequired(t, db, driver, "api_key") {
		t.Fatalf("%s api_key is not required after rollback", driver)
	}
	if hasRollbackIndex(t, db, driver, "idx_upstream_accounts_auth_method") {
		t.Fatalf("%s rollback unexpectedly retained idx_upstream_accounts_auth_method", driver)
	}
	switch driver {
	case "postgres":
		if hasPostgresConstraint(t, db, "chk_auth_method") {
			t.Fatal("postgres rollback unexpectedly retained chk_auth_method")
		}
	case "mysql":
		if hasMySQLConstraint(t, db, "chk_auth_method") {
			t.Fatal("mysql rollback unexpectedly retained chk_auth_method")
		}
	}
}

func assertRollbackForwardTypes(t *testing.T, db *sql.DB, driver string) {
	t.Helper()

	switch driver {
	case "sqlite3":
		if got := rollbackColumnType(t, db, driver, "access_token"); got != "BLOB" {
			t.Fatalf("sqlite access_token type = %q, want BLOB", got)
		}
		if got := rollbackColumnType(t, db, driver, "id_token"); got != "BLOB" {
			t.Fatalf("sqlite id_token type = %q, want BLOB", got)
		}
		if got := rollbackColumnType(t, db, driver, "access_expires_at"); got != "DATETIME" {
			t.Fatalf("sqlite access_expires_at type = %q, want DATETIME", got)
		}
	case "postgres":
		if got := rollbackColumnType(t, db, driver, "access_token"); got != "bytea" {
			t.Fatalf("postgres access_token type = %q, want bytea", got)
		}
		if got := rollbackColumnType(t, db, driver, "id_token"); got != "bytea" {
			t.Fatalf("postgres id_token type = %q, want bytea", got)
		}
		if got := rollbackColumnType(t, db, driver, "access_expires_at"); got != "timestamp with time zone" {
			t.Fatalf("postgres access_expires_at type = %q, want timestamp with time zone", got)
		}
	case "mysql":
		if got := rollbackColumnType(t, db, driver, "access_token"); got != "varbinary" {
			t.Fatalf("mysql access_token type = %q, want varbinary", got)
		}
		if got := rollbackColumnType(t, db, driver, "id_token"); got != "varbinary" {
			t.Fatalf("mysql id_token type = %q, want varbinary", got)
		}
		if got := rollbackColumnType(t, db, driver, "access_expires_at"); got != "timestamp" {
			t.Fatalf("mysql access_expires_at type = %q, want timestamp", got)
		}
		if got := rollbackColumnMaxLength(t, db, driver, "access_token"); got != 8192 {
			t.Fatalf("mysql access_token max length = %d, want 8192", got)
		}
		if got := rollbackColumnMaxLength(t, db, driver, "refresh_token"); got != 8192 {
			t.Fatalf("mysql refresh_token max length = %d, want 8192", got)
		}
		if got := rollbackColumnMaxLength(t, db, driver, "id_token"); got != 8192 {
			t.Fatalf("mysql id_token max length = %d, want 8192", got)
		}
		if got := rollbackColumnDatetimePrecision(t, db, driver, "last_refresh"); got != 6 {
			t.Fatalf("mysql last_refresh datetime precision = %d, want 6", got)
		}
		if got := rollbackColumnDatetimePrecision(t, db, driver, "access_expires_at"); got != 6 {
			t.Fatalf("mysql access_expires_at datetime precision = %d, want 6", got)
		}
	default:
		t.Fatalf("unsupported driver %q", driver)
	}
}

func hasRollbackColumn(t *testing.T, db *sql.DB, driver, column string) bool {
	t.Helper()

	switch driver {
	case "sqlite3":
		rows, err := db.Query(`PRAGMA table_info(upstream_accounts)`)
		if err != nil {
			t.Fatalf("sqlite PRAGMA table_info: %v", err)
		}
		defer func() { _ = rows.Close() }()
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
				t.Fatalf("sqlite PRAGMA scan: %v", err)
			}
			if name == column {
				return true
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("sqlite PRAGMA rows: %v", err)
		}
		return false
	case "postgres":
		var exists bool
		if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='upstream_accounts' AND column_name=$1)`, column).Scan(&exists); err != nil {
			t.Fatalf("postgres information_schema.columns(%s): %v", column, err)
		}
		return exists
	case "mysql":
		var exists bool
		if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name='upstream_accounts' AND column_name=?)`, column).Scan(&exists); err != nil {
			t.Fatalf("mysql information_schema.columns(%s): %v", column, err)
		}
		return exists
	default:
		t.Fatalf("unsupported driver %q", driver)
	}
	return false
}

func rollbackColumnRequired(t *testing.T, db *sql.DB, driver, column string) bool {
	t.Helper()

	switch driver {
	case "sqlite3":
		rows, err := db.Query(`PRAGMA table_info(upstream_accounts)`)
		if err != nil {
			t.Fatalf("sqlite PRAGMA table_info: %v", err)
		}
		defer func() { _ = rows.Close() }()
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
				t.Fatalf("sqlite PRAGMA scan: %v", err)
			}
			if name == column {
				return notNull == 1
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("sqlite PRAGMA rows: %v", err)
		}
		return false
	case "postgres":
		var nullable string
		if err := db.QueryRow(`SELECT is_nullable FROM information_schema.columns WHERE table_schema='public' AND table_name='upstream_accounts' AND column_name=$1`, column).Scan(&nullable); err != nil {
			t.Fatalf("postgres information_schema nullable(%s): %v", column, err)
		}
		return nullable == "NO"
	case "mysql":
		var nullable string
		if err := db.QueryRow(`SELECT is_nullable FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name='upstream_accounts' AND column_name=?`, column).Scan(&nullable); err != nil {
			t.Fatalf("mysql information_schema nullable(%s): %v", column, err)
		}
		return nullable == "NO"
	default:
		t.Fatalf("unsupported driver %q", driver)
	}
	return false
}

func rollbackColumnType(t *testing.T, db *sql.DB, driver, column string) string {
	t.Helper()

	switch driver {
	case "sqlite3":
		rows, err := db.Query(`PRAGMA table_info(upstream_accounts)`)
		if err != nil {
			t.Fatalf("sqlite PRAGMA table_info: %v", err)
		}
		defer func() { _ = rows.Close() }()
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
				t.Fatalf("sqlite PRAGMA scan: %v", err)
			}
			if name == column {
				return strings.ToUpper(colType)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("sqlite PRAGMA rows: %v", err)
		}
	case "postgres":
		var typ string
		if err := db.QueryRow(`SELECT data_type FROM information_schema.columns WHERE table_schema='public' AND table_name='upstream_accounts' AND column_name=$1`, column).Scan(&typ); err != nil {
			t.Fatalf("postgres information_schema type(%s): %v", column, err)
		}
		return typ
	case "mysql":
		var typ string
		if err := db.QueryRow(`SELECT data_type FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name='upstream_accounts' AND column_name=?`, column).Scan(&typ); err != nil {
			t.Fatalf("mysql information_schema type(%s): %v", column, err)
		}
		return typ
	default:
		t.Fatalf("unsupported driver %q", driver)
	}
	return ""
}

func rollbackColumnMaxLength(t *testing.T, db *sql.DB, driver, column string) int64 {
	t.Helper()

	switch driver {
	case "mysql":
		var length sql.NullInt64
		if err := db.QueryRow(`SELECT character_maximum_length FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name='upstream_accounts' AND column_name=?`, column).Scan(&length); err != nil {
			t.Fatalf("mysql information_schema length(%s): %v", column, err)
		}
		if !length.Valid {
			t.Fatalf("mysql information_schema length(%s) is NULL", column)
		}
		return length.Int64
	default:
		t.Fatalf("unsupported driver %q", driver)
	}
	return 0
}

func rollbackColumnDatetimePrecision(t *testing.T, db *sql.DB, driver, column string) int64 {
	t.Helper()

	switch driver {
	case "mysql":
		var precision sql.NullInt64
		if err := db.QueryRow(`SELECT datetime_precision FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name='upstream_accounts' AND column_name=?`, column).Scan(&precision); err != nil {
			t.Fatalf("mysql information_schema datetime_precision(%s): %v", column, err)
		}
		if !precision.Valid {
			t.Fatalf("mysql information_schema datetime_precision(%s) is NULL", column)
		}
		return precision.Int64
	default:
		t.Fatalf("unsupported driver %q", driver)
	}
	return 0
}

func hasRollbackIndex(t *testing.T, db *sql.DB, driver, index string) bool {
	t.Helper()

	switch driver {
	case "sqlite3":
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, index).Scan(&count); err != nil {
			t.Fatalf("sqlite index lookup(%s): %v", index, err)
		}
		return count > 0
	case "postgres":
		var exists bool
		if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE schemaname='public' AND tablename='upstream_accounts' AND indexname=$1)`, index).Scan(&exists); err != nil {
			t.Fatalf("postgres pg_indexes(%s): %v", index, err)
		}
		return exists
	case "mysql":
		var exists bool
		if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name='upstream_accounts' AND index_name=?)`, index).Scan(&exists); err != nil {
			t.Fatalf("mysql information_schema.statistics(%s): %v", index, err)
		}
		return exists
	default:
		t.Fatalf("unsupported driver %q", driver)
	}
	return false
}

func hasPostgresConstraint(t *testing.T, db *sql.DB, constraint string) bool {
	t.Helper()

	var exists bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE table_schema='public' AND table_name='upstream_accounts' AND constraint_name=$1)`, constraint).Scan(&exists); err != nil {
		t.Fatalf("postgres constraint lookup(%s): %v", constraint, err)
	}
	return exists
}

func hasMySQLConstraint(t *testing.T, db *sql.DB, constraint string) bool {
	t.Helper()

	var exists bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema = DATABASE() AND table_name='upstream_accounts' AND constraint_name=?)`, constraint).Scan(&exists); err != nil {
		t.Fatalf("mysql constraint lookup(%s): %v", constraint, err)
	}
	return exists
}

func postgresRollbackDBConfig(t *testing.T) *config.DBConfig {
	t.Helper()

	baseDSN := strings.TrimSpace(os.Getenv(testPostgresDSNEnv))
	if baseDSN == "" {
		return nil
	}

	adminDB, dbName, childDSN := newTestPostgresDatabase(t, baseDSN)
	t.Cleanup(func() {
		if err := dropTestPostgresDatabase(adminDB, dbName); err != nil {
			t.Fatalf("drop test postgres db %q: %v", dbName, err)
		}
		_ = adminDB.Close()
	})

	return &config.DBConfig{Driver: "postgres", URL: childDSN}
}

func mysqlRollbackDBConfig(t *testing.T) *config.DBConfig {
	t.Helper()

	baseDSN := strings.TrimSpace(os.Getenv(testMySQLDSNEnv))
	if baseDSN == "" {
		return nil
	}

	adminDB, dbName, childDSN := newTestMySQLDatabase(t, baseDSN)
	t.Cleanup(func() {
		if err := dropTestMySQLDatabase(adminDB, dbName); err != nil {
			t.Fatalf("drop test mysql db %q: %v", dbName, err)
		}
		_ = adminDB.Close()
	})

	return &config.DBConfig{Driver: "mysql", URL: childDSN}
}

func newTestPostgresDatabase(t *testing.T, baseDSN string) (*sql.DB, string, string) {
	t.Helper()

	adminDB, err := sql.Open("postgres", baseDSN)
	if err != nil {
		t.Fatalf("sql.Open(postgres): %v", err)
	}
	if err := adminDB.Ping(); err != nil {
		_ = adminDB.Close()
		t.Fatalf("ping postgres admin db: %v", err)
	}

	dbName := fmt.Sprintf("router_rollback_%d", time.Now().UnixNano())
	if _, err := adminDB.Exec(`CREATE DATABASE ` + pq.QuoteIdentifier(dbName)); err != nil {
		_ = adminDB.Close()
		t.Fatalf("create postgres test db %q: %v", dbName, err)
	}

	childDSN, err := withPostgresDatabase(baseDSN, dbName)
	if err != nil {
		_ = dropTestPostgresDatabase(adminDB, dbName)
		_ = adminDB.Close()
		t.Fatalf("rewrite postgres dsn: %v", err)
	}
	return adminDB, dbName, childDSN
}

func dropTestPostgresDatabase(adminDB *sql.DB, dbName string) error {
	if adminDB == nil {
		return nil
	}
	if _, err := adminDB.Exec(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, dbName); err != nil {
		return err
	}
	_, err := adminDB.Exec(`DROP DATABASE IF EXISTS ` + pq.QuoteIdentifier(dbName))
	return err
}

func newTestMySQLDatabase(t *testing.T, baseDSN string) (*sql.DB, string, string) {
	t.Helper()

	adminDB, err := sql.Open("mysql", baseDSN)
	if err != nil {
		t.Fatalf("sql.Open(mysql): %v", err)
	}
	if err := adminDB.Ping(); err != nil {
		_ = adminDB.Close()
		t.Fatalf("ping mysql admin db: %v", err)
	}

	dbName := fmt.Sprintf("router_rollback_%d", time.Now().UnixNano())
	if _, err := adminDB.Exec("CREATE DATABASE `" + dbName + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); err != nil {
		_ = adminDB.Close()
		t.Fatalf("create mysql test db %q: %v", dbName, err)
	}

	childDSN, err := withMySQLDatabase(baseDSN, dbName)
	if err != nil {
		_ = dropTestMySQLDatabase(adminDB, dbName)
		_ = adminDB.Close()
		t.Fatalf("rewrite mysql dsn: %v", err)
	}
	return adminDB, dbName, childDSN
}

func dropTestMySQLDatabase(adminDB *sql.DB, dbName string) error {
	if adminDB == nil {
		return nil
	}
	_, err := adminDB.Exec("DROP DATABASE IF EXISTS `" + dbName + "`")
	return err
}

func withPostgresDatabase(baseDSN, dbName string) (string, error) {
	parsed, err := url.Parse(baseDSN)
	if err != nil {
		return "", err
	}
	parsed.Path = "/" + dbName
	return parsed.String(), nil
}

func withMySQLDatabase(baseDSN, dbName string) (string, error) {
	cfg, err := mysqldrv.ParseDSN(baseDSN)
	if err != nil {
		return "", err
	}
	cfg.DBName = dbName
	return cfg.FormatDSN(), nil
}

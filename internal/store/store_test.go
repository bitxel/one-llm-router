package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/user/one-llm-router/internal/core"
	"github.com/user/one-llm-router/internal/domain"
)

func setupTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	s, err := New("sqlite3", ":memory:", 1, 1)
	require.NoError(t, err)
	require.NoError(t, s.Migrate("sqlite3"))
	return s, func() { _ = s.Close() }
}

func TestNew_InvalidDriver(t *testing.T) {
	_, err := New("oracle", "dsn", 10, 2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unsupported database driver: "oracle"`)
}

func TestNew_EmptyDSN_SQLite(t *testing.T) {
	dir := t.TempDir()
	prev, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(prev) })

	s, err := New("sqlite3", "", 1, 1)
	require.NoError(t, err)
	defer func() { _ = s.Close() }()

	// DSN now carries a file: URI prefix plus PRAGMA defaults; path portion
	// must still resolve to the default "router.db".
	dsn := s.Engine().DataSourceName()
	assert.True(t, strings.HasPrefix(dsn, "file:router.db"), "expected DSN to start with file:router.db, got %q", dsn)

	// Force the driver to open a real connection so the SQLite file is
	// materialised on disk. xorm is lazy so just calling New does not
	// create the file.
	require.NoError(t, s.Engine().Ping())

	_, err = os.Stat(filepath.Join(dir, "router.db"))
	require.NoError(t, err, "default DSN should create router.db in working directory")
}

func TestNew_UsesUTCTimezones(t *testing.T) {
	s, err := New("sqlite3", ":memory:", 1, 1)
	require.NoError(t, err)
	defer func() { _ = s.Close() }()

	require.NotNil(t, s.Engine().TZLocation)
	require.NotNil(t, s.Engine().DatabaseTZ)
	assert.Equal(t, time.UTC, s.Engine().TZLocation)
	assert.Equal(t, time.UTC, s.Engine().DatabaseTZ)
}

func TestMigrate_SQLite(t *testing.T) {
	s, err := New("sqlite3", ":memory:", 1, 1)
	require.NoError(t, err)
	defer func() { _ = s.Close() }()

	require.NoError(t, s.Migrate("sqlite3"))

	ok, err := s.Engine().IsTableExist(&domain.UpstreamAccount{})
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = s.Engine().IsTableExist(&domain.RequestRecord{})
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestBaselineDDLIncludesRouterMetadata(t *testing.T) {
	t.Parallel()

	for _, dialect := range []string{"sqlite", "postgres", "mysql"} {
		dialect := dialect
		t.Run(dialect, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join("migrations", dialect, "000001_init.up.sql")
			body, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Contains(t, string(body), "router_metadata")
		})
	}
}

func TestMigrator_RequestRecordsHasRouterMetadataAfterFullUp(t *testing.T) {
	t.Parallel()

	dbCfg := sqliteDBConfig(t)
	mig, err := NewMigrator(context.Background(), dbCfg)
	require.NoError(t, err)
	require.NoError(t, mig.Up(context.Background()))
	require.NoError(t, mig.Close())

	sqlDB := openTestDB(t, dbCfg)
	defer func() { _ = sqlDB.Close() }()
	columns := requestRecordColumnSet(t, sqlDB)
	assert.True(t, columns["router_metadata"], "request_records columns=%v", columns)
	assert.True(t, columns["model_params"], "request_records columns=%v", columns)
	assert.True(t, columns["client_ip"], "request_records columns=%v", columns)
}

func TestMigrate_DriverMismatch(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	err := s.Migrate("postgres")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "migrate driver mismatch")
}

// TestNew_Postgres_ValidDSN exercises the postgres branch of New (including pool settings)
// without requiring a running server. xorm accepts a well-formed DSN without connecting yet.
func TestNew_Postgres_ValidDSN(t *testing.T) {
	dsn := "postgres://u:p@127.0.0.1:65432/nodb?sslmode=disable"
	s, err := New("postgres", dsn, 15, 3)
	require.NoError(t, err)
	require.NotNil(t, s)
	defer func() { _ = s.Close() }()

	assert.Equal(t, "postgres", s.Engine().DriverName())
	assert.Equal(t, dsn, s.Engine().DataSourceName())
}

func TestAccountRepo_Create_ClosedStore(t *testing.T) {
	s, cleanup := setupTestStore(t)
	repo := NewAccountRepo(s.Engine())
	ctx := context.Background()
	cleanup()

	err := repo.Create(ctx, &domain.UpstreamAccount{
		Name:     "x",
		Provider: domain.ProviderOpenAI,
		APIKey:   "k",
		Status:   domain.AccountStatusActive,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insert account:")
}

func TestAccountRepo_UpdateStatus_NotFound(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	repo := NewAccountRepo(s.Engine())

	err := repo.UpdateStatus(context.Background(), 99999, domain.AccountStatusDisabled)
	assert.ErrorIs(t, err, domain.ErrAccountNotFound)
}

func TestAccountRepo_UpdateStatus_ClosedStore(t *testing.T) {
	s, cleanup := setupTestStore(t)
	repo := NewAccountRepo(s.Engine())
	ctx := context.Background()

	acct := &domain.UpstreamAccount{
		Name:     "a",
		Provider: domain.ProviderOpenAI,
		APIKey:   "k",
		Status:   domain.AccountStatusActive,
	}
	require.NoError(t, repo.Create(ctx, acct))
	cleanup()

	err := repo.UpdateStatus(ctx, acct.ID, domain.AccountStatusDisabled)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update account")
}

func TestAccountRepo_CRUD(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	repo := NewAccountRepo(s.Engine())
	ctx := context.Background()

	acct := &domain.UpstreamAccount{
		Name:     "test-account",
		Provider: domain.ProviderOpenAI,
		APIKey:   "sk-test-key",
		Status:   domain.AccountStatusActive,
	}
	require.NoError(t, repo.Create(ctx, acct))
	assert.NotZero(t, acct.ID)

	got, err := repo.GetByID(ctx, acct.ID)
	require.NoError(t, err)
	assert.Equal(t, "test-account", got.Name)
	assert.Equal(t, domain.AccountStatusActive, got.Status)

	accounts, err := repo.ListActive(ctx)
	require.NoError(t, err)
	assert.Len(t, accounts, 1)

	require.NoError(t, repo.UpdateStatus(ctx, acct.ID, domain.AccountStatusDisabled))
	got2, err := repo.GetByID(ctx, acct.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.AccountStatusDisabled, got2.Status)

	active, err := repo.ListActive(ctx)
	require.NoError(t, err)
	assert.Len(t, active, 0)
}

func TestAccountRepo_GetByID_NotFound(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	repo := NewAccountRepo(s.Engine())

	_, err := repo.GetByID(context.Background(), 9999)
	assert.ErrorIs(t, err, domain.ErrAccountNotFound)
}

func TestAccountRepo_List_StatusFilter(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	repo := NewAccountRepo(s.Engine())
	ctx := context.Background()

	for _, name := range []string{"a1", "a2", "a3"} {
		acct := &domain.UpstreamAccount{Name: name, Provider: "openai", APIKey: "sk-" + name, Status: domain.AccountStatusActive}
		require.NoError(t, repo.Create(ctx, acct))
	}
	require.NoError(t, repo.UpdateStatus(ctx, 2, domain.AccountStatusDisabled))

	all, err := repo.List(ctx, nil)
	require.NoError(t, err)
	assert.Len(t, all, 3)

	active, err := repo.List(ctx, []string{domain.AccountStatusActive})
	require.NoError(t, err)
	assert.Len(t, active, 2)
}

func TestRequestRecordRepo_InsertAndQuery(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	acctRepo := NewAccountRepo(s.Engine())
	repo := NewRequestRecordRepo(s.Engine())
	ctx := context.Background()

	parent := &domain.UpstreamAccount{Name: "p1", Provider: "openai", APIKey: "sk-p1", Status: domain.AccountStatusActive}
	require.NoError(t, acctRepo.Create(ctx, parent))
	acctID := parent.ID
	clientReqBody := `{"prompt":"client"}`
	upstreamReqBody := `{"input":"upstream"}`
	upstreamRespBody := `{"id":"resp_store"}`
	rec := &domain.RequestRecord{
		RequestID:         domain.NewRequestID(),
		UpstreamAccountID: &acctID,
		Method:            "POST",
		Path:              "/v1/responses",
		StatusCode:        200,
		LatencyMs:         150,
		Outcome:           domain.OutcomeSuccess,
		ResponseMode:      domain.ResponseModeSSE,
		ModelParams: domain.JSONMap{
			"temperature": 0.2,
		},
		RouterMetadata: domain.JSONMap{
			"bridge": domain.JSONMap{
				"op_id":             "op.openai.responses.create",
				"bridge_id":         "bridge.openai.responses.direct",
				"client_contract":   "contract.openai.v1.responses",
				"upstream_contract": "contract.openai.v1.responses",
				"credential_class":  "api_key",
			},
		},
		TokenUsage:           domain.JSONMap{"input": 100, "output": 50},
		ClientRequestBody:    &clientReqBody,
		UpstreamRequestBody:  &upstreamReqBody,
		UpstreamResponseBody: &upstreamRespBody,
	}
	require.NoError(t, repo.Insert(ctx, rec))
	assert.NotZero(t, rec.ID)

	results, err := repo.Query(ctx, core.QueryParams{Limit: 10})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, rec.RequestID, results[0].RequestID)
	assert.Equal(t, 200, results[0].StatusCode)
	assert.Equal(t, 0.2, results[0].ModelParams["temperature"])
	assert.Equal(t, map[string]interface{}{
		"op_id":             "op.openai.responses.create",
		"bridge_id":         "bridge.openai.responses.direct",
		"client_contract":   "contract.openai.v1.responses",
		"upstream_contract": "contract.openai.v1.responses",
		"credential_class":  "api_key",
	}, results[0].RouterMetadata["bridge"])
	require.NotNil(t, results[0].ClientRequestBody)
	assert.Equal(t, clientReqBody, *results[0].ClientRequestBody)
	require.NotNil(t, results[0].UpstreamRequestBody)
	assert.Equal(t, upstreamReqBody, *results[0].UpstreamRequestBody)
	require.NotNil(t, results[0].UpstreamResponseBody)
	assert.Equal(t, upstreamRespBody, *results[0].UpstreamResponseBody)

	got, err := repo.GetByID(ctx, rec.ID)
	require.NoError(t, err)
	assert.Equal(t, 0.2, got.ModelParams["temperature"])
	assert.Equal(t, map[string]interface{}{
		"op_id":             "op.openai.responses.create",
		"bridge_id":         "bridge.openai.responses.direct",
		"client_contract":   "contract.openai.v1.responses",
		"upstream_contract": "contract.openai.v1.responses",
		"credential_class":  "api_key",
	}, got.RouterMetadata["bridge"])
	require.NotNil(t, got.ClientRequestBody)
	assert.Equal(t, clientReqBody, *got.ClientRequestBody)
	require.NotNil(t, got.UpstreamRequestBody)
	assert.Equal(t, upstreamReqBody, *got.UpstreamRequestBody)
	require.NotNil(t, got.UpstreamResponseBody)
	assert.Equal(t, upstreamRespBody, *got.UpstreamResponseBody)
}

func TestRequestRecordRepo_QueryFilters(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	acctRepo := NewAccountRepo(s.Engine())
	repo := NewRequestRecordRepo(s.Engine())
	ctx := context.Background()

	parent := &domain.UpstreamAccount{Name: "p2", Provider: "openai", APIKey: "sk-p2", Status: domain.AccountStatusActive}
	require.NoError(t, acctRepo.Create(ctx, parent))
	acctID := parent.ID
	for i := range 5 {
		outcome := domain.OutcomeSuccess
		if i%2 == 0 {
			outcome = domain.OutcomeUpstreamError
		}
		rec := &domain.RequestRecord{
			RequestID:         domain.NewRequestID(),
			UpstreamAccountID: &acctID,
			Method:            "POST",
			Path:              "/v1/chat/completions",
			StatusCode:        200,
			LatencyMs:         100,
			Outcome:           outcome,
			ResponseMode:      domain.ResponseModeJSON,
		}
		require.NoError(t, repo.Insert(ctx, rec))
	}

	outcome := domain.OutcomeSuccess
	results, err := repo.Query(ctx, core.QueryParams{Outcome: &outcome, Limit: 10})
	require.NoError(t, err)
	assert.Len(t, results, 2)

	results2, err := repo.Query(ctx, core.QueryParams{AccountID: &acctID, Limit: 10})
	require.NoError(t, err)
	assert.Len(t, results2, 5)
}

func TestRequestRecordRepo_QueryAdvancedFiltersAndOptions(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	acctRepo := NewAccountRepo(s.Engine())
	repo := NewRequestRecordRepo(s.Engine())
	ctx := context.Background()

	accountA := &domain.UpstreamAccount{Name: "filter-a", Provider: "openai", APIKey: "sk-filter-a", Status: domain.AccountStatusActive}
	accountB := &domain.UpstreamAccount{Name: "filter-b", Provider: "openai", APIKey: "sk-filter-b", Status: domain.AccountStatusActive}
	require.NoError(t, acctRepo.Create(ctx, accountA))
	require.NoError(t, acctRepo.Create(ctx, accountB))
	errCode := "rate_limit_exceeded"

	records := []*domain.RequestRecord{
		{
			RequestID:         "req_filter_alpha",
			UpstreamAccountID: &accountA.ID,
			Method:            "POST",
			Path:              "/v1/responses",
			StatusCode:        200,
			LatencyMs:         70,
			Outcome:           domain.OutcomeSuccess,
			Model:             ptrString("gpt-5.4-mini"),
			ResponseMode:      domain.ResponseModeJSON,
		},
		{
			RequestID:         "req_filter_beta",
			ClientIP:          "203.0.113.77",
			UpstreamAccountID: &accountB.ID,
			Method:            "POST",
			Path:              "/v1/chat/completions",
			StatusCode:        429,
			LatencyMs:         90,
			Outcome:           domain.OutcomeUpstreamError,
			ErrorCode:         &errCode,
			Model:             ptrString("gpt-5.4"),
			ResponseMode:      domain.ResponseModeSSE,
		},
		{
			RequestID:         "req_filter_beta_wrong_account",
			UpstreamAccountID: &accountA.ID,
			Method:            "POST",
			Path:              "/v1/chat/completions",
			StatusCode:        429,
			LatencyMs:         91,
			Outcome:           domain.OutcomeUpstreamError,
			ErrorCode:         &errCode,
			Model:             ptrString("gpt-5.4"),
			ResponseMode:      domain.ResponseModeSSE,
		},
		{
			RequestID:         "req_filter_beta_wrong_outcome",
			UpstreamAccountID: &accountB.ID,
			Method:            "POST",
			Path:              "/v1/chat/completions",
			StatusCode:        200,
			LatencyMs:         92,
			Outcome:           domain.OutcomeSuccess,
			Model:             ptrString("gpt-5.4"),
			ResponseMode:      domain.ResponseModeSSE,
		},
		{
			RequestID:         "req_filter_beta_wrong_model",
			UpstreamAccountID: &accountB.ID,
			Method:            "POST",
			Path:              "/v1/chat/completions",
			StatusCode:        429,
			LatencyMs:         93,
			Outcome:           domain.OutcomeUpstreamError,
			ErrorCode:         &errCode,
			Model:             ptrString("gpt-4.1"),
			ResponseMode:      domain.ResponseModeSSE,
		},
		{
			RequestID:         "req_filter_beta_wrong_mode",
			UpstreamAccountID: &accountB.ID,
			Method:            "POST",
			Path:              "/v1/chat/completions",
			StatusCode:        429,
			LatencyMs:         94,
			Outcome:           domain.OutcomeUpstreamError,
			ErrorCode:         &errCode,
			Model:             ptrString("gpt-5.4"),
			ResponseMode:      domain.ResponseModeJSON,
		},
		{
			RequestID:         "req_filter_gamma",
			UpstreamAccountID: &accountB.ID,
			Method:            "POST",
			Path:              "/v1/chat/completions",
			StatusCode:        429,
			LatencyMs:         95,
			Outcome:           domain.OutcomeUpstreamError,
			ErrorCode:         &errCode,
			Model:             ptrString("gpt-5.4"),
			ResponseMode:      domain.ResponseModeSSE,
		},
	}
	for _, record := range records {
		require.NoError(t, repo.Insert(ctx, record))
	}

	results, err := repo.Query(ctx, core.QueryParams{
		AccountIDs:    []int64{accountB.ID},
		Outcomes:      []string{domain.OutcomeUpstreamError},
		Models:        []string{"gpt-5.4"},
		ResponseModes: []string{domain.ResponseModeSSE},
		Search:        ptrString("BETA"),
		Limit:         10,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "req_filter_beta", results[0].RequestID)

	got, err := repo.GetByID(ctx, records[1].ID)
	require.NoError(t, err)
	assert.Equal(t, "req_filter_beta", got.RequestID)
	assert.Equal(t, "203.0.113.77", got.ClientIP)

	ipResults, err := repo.Query(ctx, core.QueryParams{
		Search: ptrString("203.0.113.77"),
		Limit:  10,
	})
	require.NoError(t, err)
	require.Len(t, ipResults, 1)
	assert.Equal(t, "req_filter_beta", ipResults[0].RequestID)

	options, err := repo.FilterOptions(ctx, core.QueryParams{Search: ptrString("filter")})
	require.NoError(t, err)
	assert.ElementsMatch(t, []int64{accountA.ID, accountB.ID}, options.AccountIDs)
	assert.ElementsMatch(t, []string{domain.OutcomeSuccess, domain.OutcomeUpstreamError}, options.Outcomes)
	assert.ElementsMatch(t, []string{"gpt-5.4-mini", "gpt-5.4", "gpt-4.1"}, options.Models)
	assert.ElementsMatch(t, []string{domain.ResponseModeJSON, domain.ResponseModeSSE}, options.ResponseModes)
}

func TestRequestRecordRepo_DashboardAggregatesRangeAccountTokensErrorsAndTTFT(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	acctRepo := NewAccountRepo(s.Engine())
	repo := NewRequestRecordRepo(s.Engine())
	ctx := context.Background()

	accountA := &domain.UpstreamAccount{Name: "dash-a", Provider: "openai", APIKey: "sk-dash-a", Status: domain.AccountStatusActive}
	accountB := &domain.UpstreamAccount{Name: "dash-b", Provider: "openai", APIKey: "sk-dash-b", Status: domain.AccountStatusActive}
	require.NoError(t, acctRepo.Create(ctx, accountA))
	require.NoError(t, acctRepo.Create(ctx, accountB))

	ttftFast := 100
	ttftSlow := 300
	records := []*domain.RequestRecord{
		{
			RequestID:         domain.NewRequestID(),
			UpstreamAccountID: &accountA.ID,
			Method:            "POST",
			Path:              "/v1/responses",
			StatusCode:        200,
			LatencyMs:         900,
			TTFTMs:            &ttftFast,
			Outcome:           domain.OutcomeSuccess,
			ResponseMode:      domain.ResponseModeSSE,
			TokenUsage:        domain.JSONMap{"input": 100, "cached_input": 40, "output": 20},
		},
		{
			RequestID:         domain.NewRequestID(),
			UpstreamAccountID: &accountA.ID,
			Method:            "POST",
			Path:              "/v1/responses",
			StatusCode:        429,
			LatencyMs:         1200,
			TTFTMs:            &ttftSlow,
			Outcome:           domain.OutcomeUpstreamError,
			ResponseMode:      domain.ResponseModeSSE,
			TokenUsage:        domain.JSONMap{"input_tokens": 20, "cached_input": 5, "output_tokens": 10},
		},
		{
			RequestID:         domain.NewRequestID(),
			UpstreamAccountID: &accountB.ID,
			Method:            "POST",
			Path:              "/v1/responses",
			StatusCode:        200,
			LatencyMs:         100,
			Outcome:           domain.OutcomeSuccess,
			ResponseMode:      domain.ResponseModeJSON,
			TokenUsage:        domain.JSONMap{"input": 1000, "cached_input": 1000, "output": 1000},
		},
	}
	for _, record := range records {
		require.NoError(t, repo.Insert(ctx, record))
	}

	aggregation, err := repo.Dashboard(ctx, core.DashboardQuery{
		Range:     core.DashboardRange7D,
		AccountID: &accountA.ID,
		Now:       time.Now().UTC().Add(time.Second),
	})
	require.NoError(t, err)

	assert.Equal(t, core.DashboardRange7D, aggregation.Range)
	assert.Equal(t, 6*60*60, aggregation.BucketSeconds)
	assert.Equal(t, 2, aggregation.Requests.Total)
	assert.Equal(t, 45, aggregation.Tokens.Totals.InputCached)
	assert.Equal(t, 75, aggregation.Tokens.Totals.InputNonCached)
	assert.Equal(t, 30, aggregation.Tokens.Totals.Output)
	assert.Equal(t, 1, aggregation.ErrorRate.Errors)
	assert.Equal(t, 2, aggregation.ErrorRate.Total)
	assert.InDelta(t, 0.5, aggregation.ErrorRate.Value, 0.000001)
	require.NotNil(t, aggregation.TTFT.P95MS)
	assert.Equal(t, 300, *aggregation.TTFT.P95MS)
	assert.Equal(t, 2, aggregation.TTFT.SampleCount)
	assert.Equal(t, 2, sumDashboardCounts(aggregation.Requests.Series))
	assert.Equal(t, 1, sumDashboardErrors(aggregation.ErrorRate.Series))
	assert.Equal(t, 30, sumDashboardTokenOutput(aggregation.Tokens.Series))
}

func TestRequestRecordRepo_UsageSummaryStreamsTokenUsage(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	repo := NewRequestRecordRepo(s.Engine())
	ctx := context.Background()

	records := []*domain.RequestRecord{
		{
			RequestID:    domain.NewRequestID(),
			Method:       "POST",
			Path:         "/v1/responses",
			StatusCode:   200,
			Outcome:      domain.OutcomeSuccess,
			ResponseMode: domain.ResponseModeJSON,
			TokenUsage:   domain.JSONMap{"input": 10, "cached_input": 3, "output": 5},
		},
		{
			RequestID:    domain.NewRequestID(),
			Method:       "POST",
			Path:         "/v1/chat/completions",
			StatusCode:   200,
			Outcome:      domain.OutcomeSuccess,
			ResponseMode: domain.ResponseModeSSE,
			TokenUsage: domain.JSONMap{
				"input_tokens":         4,
				"output_tokens":        6,
				"input_tokens_details": domain.JSONMap{"cached_tokens": 2},
			},
		},
		{
			RequestID:    domain.NewRequestID(),
			Method:       "GET",
			Path:         "/v1/models",
			StatusCode:   200,
			Outcome:      domain.OutcomeSuccess,
			ResponseMode: domain.ResponseModeJSON,
		},
		{
			RequestID:    domain.NewRequestID(),
			Method:       "GET",
			Path:         "/v1/responses/resp_123",
			StatusCode:   200,
			Outcome:      domain.OutcomeSuccess,
			ResponseMode: domain.ResponseModeJSON,
			TokenUsage:   domain.JSONMap{"input": 999, "output": 999},
		},
	}
	for _, record := range records {
		require.NoError(t, repo.Insert(ctx, record))
	}

	summary, err := repo.UsageSummary(ctx)

	require.NoError(t, err)
	assert.Equal(t, 4, summary.RequestCount)
	assert.Equal(t, 2023, summary.TotalTokens)
	assert.Equal(t, 5, summary.CachedInputTokens)
}

func TestRequestRecordRepo_RespectsCanceledContext(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	repo := NewRequestRecordRepo(s.Engine())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := repo.Insert(ctx, &domain.RequestRecord{
		RequestID:    domain.NewRequestID(),
		Method:       "POST",
		Path:         "/v1/responses",
		StatusCode:   200,
		LatencyMs:    1,
		Outcome:      domain.OutcomeSuccess,
		ResponseMode: domain.ResponseModeJSON,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)

	_, err = repo.Query(ctx, core.QueryParams{Limit: 1})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)

	_, err = repo.GetByID(ctx, 1)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)

	_, err = repo.FilterOptions(ctx, core.QueryParams{})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)

	_, err = repo.UsageSummary(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)

	_, err = repo.DeleteBefore(ctx, time.Now())
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestRequestRecordRepo_DeleteBefore(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	repo := NewRequestRecordRepo(s.Engine())
	ctx := context.Background()

	rec := &domain.RequestRecord{
		RequestID:    domain.NewRequestID(),
		Method:       "GET",
		Path:         "/v1/models",
		StatusCode:   200,
		LatencyMs:    10,
		Outcome:      domain.OutcomeSuccess,
		ResponseMode: domain.ResponseModeJSON,
	}
	require.NoError(t, repo.Insert(ctx, rec))

	deleted, err := repo.DeleteBefore(ctx, time.Now().Add(1*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)

	results, err := repo.Query(ctx, core.QueryParams{Limit: 10})
	require.NoError(t, err)
	assert.Len(t, results, 0)
}

func TestRequestRecordRepo_Pagination(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	repo := NewRequestRecordRepo(s.Engine())
	ctx := context.Background()

	for range 10 {
		rec := &domain.RequestRecord{
			RequestID:    domain.NewRequestID(),
			Method:       "POST",
			Path:         "/v1/responses",
			StatusCode:   200,
			LatencyMs:    50,
			Outcome:      domain.OutcomeSuccess,
			ResponseMode: domain.ResponseModeJSON,
		}
		require.NoError(t, repo.Insert(ctx, rec))
	}

	page1, err := repo.Query(ctx, core.QueryParams{Limit: 3})
	require.NoError(t, err)
	require.Len(t, page1, 3)

	lastID := page1[len(page1)-1].ID
	page2, err := repo.Query(ctx, core.QueryParams{Limit: 3, BeforeID: &lastID})
	require.NoError(t, err)
	require.Len(t, page2, 3)

	assert.True(t, page2[0].ID < lastID, "page 2 IDs should be less than cursor")
}

func ptrString(value string) *string {
	return &value
}

func sumDashboardCounts(points []core.DashboardCountPoint) int {
	total := 0
	for _, point := range points {
		total += point.Count
	}
	return total
}

func sumDashboardErrors(points []core.DashboardRatePoint) int {
	total := 0
	for _, point := range points {
		total += point.Errors
	}
	return total
}

func sumDashboardTokenOutput(points []core.DashboardTokenPoint) int {
	total := 0
	for _, point := range points {
		total += point.Output
	}
	return total
}

func TestAccountRepo_GetByID_ClosedStore(t *testing.T) {
	s, cleanup := setupTestStore(t)
	repo := NewAccountRepo(s.Engine())
	cleanup()
	_, err := repo.GetByID(context.Background(), 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get account 1:")
}

func TestAccountRepo_List_ClosedStore(t *testing.T) {
	s, cleanup := setupTestStore(t)
	repo := NewAccountRepo(s.Engine())
	cleanup()
	_, err := repo.List(context.Background(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list accounts:")
}

func TestAccountRepo_ListActive_ClosedStore(t *testing.T) {
	s, cleanup := setupTestStore(t)
	repo := NewAccountRepo(s.Engine())
	cleanup()
	_, err := repo.ListActive(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list active accounts:")
}

func TestRequestRecordRepo_Insert_ClosedStore(t *testing.T) {
	s, cleanup := setupTestStore(t)
	repo := NewRequestRecordRepo(s.Engine())
	cleanup()
	err := repo.Insert(context.Background(), &domain.RequestRecord{
		RequestID: domain.NewRequestID(), Method: "POST", Path: "/v1/x",
		StatusCode: 200, LatencyMs: 10, Outcome: domain.OutcomeSuccess, ResponseMode: domain.ResponseModeJSON,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insert request record:")
}

func TestRequestRecordRepo_Query_ClosedStore(t *testing.T) {
	s, cleanup := setupTestStore(t)
	repo := NewRequestRecordRepo(s.Engine())
	cleanup()
	_, err := repo.Query(context.Background(), core.QueryParams{Limit: 10})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query request records:")
}

func TestRequestRecordRepo_DeleteBefore_ClosedStore(t *testing.T) {
	s, cleanup := setupTestStore(t)
	repo := NewRequestRecordRepo(s.Engine())
	cleanup()
	_, err := repo.DeleteBefore(context.Background(), time.Now())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request records:")
}

func TestRequestRecordRepo_DeleteBefore_NoneToDelete(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	repo := NewRequestRecordRepo(s.Engine())

	deleted, err := repo.DeleteBefore(context.Background(), time.Now().Add(-24*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, int64(0), deleted)
}

func TestMigrate_SQLite_Idempotent(t *testing.T) {
	s, err := New("sqlite3", ":memory:", 1, 1)
	require.NoError(t, err)
	defer func() { _ = s.Close() }()

	require.NoError(t, s.Migrate("sqlite3"))
	require.NoError(t, s.Migrate("sqlite3"))
}

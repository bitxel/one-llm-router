package adminapi

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/one-llm-router/internal/api"
	"github.com/user/one-llm-router/internal/api/admin"
	"github.com/user/one-llm-router/internal/api/errcode"
	"github.com/user/one-llm-router/internal/api/testutil"
	"github.com/user/one-llm-router/internal/core"
	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/store"
)

type alwaysOpenGate struct{}

func (alwaysOpenGate) ProbeState() (bool, error) { return true, nil }

type accountsHarness struct {
	mux  *http.ServeMux
	repo *store.AccountRepo
	db   *store.Store
}

func setupAccountsHarness(t *testing.T) *accountsHarness {
	t.Helper()

	s, err := store.New("sqlite3", ":memory:", 1, 1)
	require.NoError(t, err)
	require.NoError(t, s.Migrate("sqlite3"))
	t.Cleanup(func() { _ = s.Close() })

	engine := s.Engine()
	accountRepo := store.NewAccountRepo(engine)
	recordRepo := store.NewRequestRecordRepo(engine)
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	selector := core.NewAccountSelector(accountRepo, core.NewConsistentHashRouter())
	accountSvc := core.NewAccountService(accountRepo, logger)
	requestSvc := core.NewRequestService(recordRepo)
	healthSvc := core.NewHealthService(accountRepo)
	inner := admin.NewHandler(accountSvc, requestSvc, healthSvc, selector, logger)
	wrapped := NewWrappedHandler(inner, alwaysOpenGate{}, logger)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/admin/accounts", wrapped.CreateAccount)
	mux.HandleFunc("GET /api/admin/accounts", wrapped.ListAccounts)
	mux.HandleFunc("POST /api/admin/accounts/{id}/update", wrapped.UpdateAccount)

	return &accountsHarness{
		mux:  mux,
		repo: accountRepo,
		db:   s,
	}
}

func requestWithID(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req.WithContext(api.WithRequestID(req.Context(), "req-test"))
}

func doAccountsRequest(t *testing.T, mux *http.ServeMux, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, requestWithID(method, target, body))
	return rec
}

func accountByName(t *testing.T, raw []any, name string) map[string]any {
	t.Helper()
	for _, item := range raw {
		m, ok := item.(map[string]any)
		require.True(t, ok, "account item is not an object: %#v", item)
		if m["name"] == name {
			return m
		}
	}
	t.Fatalf("account %q not found in response", name)
	return nil
}

func oauthRow(name string, method domain.AuthMethod, plan *string, email *string, accountID *string) *domain.UpstreamAccount {
	lastRefresh := time.Date(2026, 4, 15, 10, 2, 17, 0, time.UTC)
	accessExpiresAt := lastRefresh.Add(59 * time.Minute)
	return &domain.UpstreamAccount{
		Name:             name,
		Provider:         domain.ProviderOpenAI,
		Status:           domain.AccountStatusActive,
		AuthMethod:       method,
		AccessToken:      []byte("tok-access-" + name),
		RefreshToken:     []byte("tok-refresh-" + name),
		IDToken:          []byte("tok-id-" + name),
		LastRefresh:      &lastRefresh,
		AccessExpiresAt:  &accessExpiresAt,
		Email:            email,
		PlanType:         plan,
		ChatGPTAccountID: accountID,
	}
}

func TestAccountsCreate(t *testing.T) {
	t.Run("omitted auth_method and explicit api_key stay on the legacy success shape", func(t *testing.T) {
		h := setupAccountsHarness(t)

		omitted := doAccountsRequest(t, h.mux, http.MethodPost, "/api/admin/accounts", `{"name":"legacy-omitted","provider":"openai","api_key":"sk-omitted"}`)
		omittedData := testutil.AssertEnvelopeDataShape(t, omitted, 0)
		explicit := doAccountsRequest(t, h.mux, http.MethodPost, "/api/admin/accounts", `{"name":"legacy-explicit","provider":"openai","api_key":"sk-explicit","auth_method":"api_key"}`)
		explicitData := testutil.AssertEnvelopeDataShape(t, explicit, 0)

		for _, data := range []map[string]any{omittedData, explicitData} {
			assert.NotContains(t, data, "auth_method")
			assert.NotContains(t, data, "email")
			assert.NotContains(t, data, "plan_type")
			assert.NotContains(t, data, "plan_type_label")
			assert.NotContains(t, data, "chatgpt_account_id")
			assert.NotContains(t, data, "last_refresh")
			assert.NotContains(t, data, "access_expires_at")
			assert.Equal(t, "active", data["status"])
		}

		first, err := h.repo.GetByID(context.Background(), 1)
		require.NoError(t, err)
		assert.Equal(t, domain.AuthMethodAPIKey, first.AuthMethod)

		second, err := h.repo.GetByID(context.Background(), 2)
		require.NoError(t, err)
		assert.Equal(t, domain.AuthMethodAPIKey, second.AuthMethod)
	})

	t.Run("oauth auth_method values are rejected on this endpoint", func(t *testing.T) {
		h := setupAccountsHarness(t)

		cases := []domain.AuthMethod{
			domain.AuthMethodOAuthBrowser,
			domain.AuthMethodOAuthDevice,
			domain.AuthMethodOAuthImport,
		}
		for _, method := range cases {
			method := method
			t.Run(string(method), func(t *testing.T) {
				rec := doAccountsRequest(t, h.mux, http.MethodPost, "/api/admin/accounts", `{"name":"blocked","provider":"openai","api_key":"sk-test","auth_method":"`+string(method)+`"}`)
				data := testutil.AssertEnvelopeDataShape(t, rec, errcode.OAuthModeRequiresFlowEndpoint)
				assert.Equal(t, string(method), data["got"])
				assert.Equal(t, []any{"api_key"}, data["allowed_here"])
			})
		}
	})

	t.Run("empty and whitespace-only api_key reuse 2005 invalid_api_key", func(t *testing.T) {
		h := setupAccountsHarness(t)

		cases := []string{
			`{"name":"empty","provider":"openai","api_key":"","auth_method":"api_key"}`,
			`{"name":"spaces","provider":"openai","api_key":"   ","auth_method":"api_key"}`,
			`{"name":"missing","provider":"openai","auth_method":"api_key"}`,
			`{"name":"too-long","provider":"openai","api_key":"sk-` + strings.Repeat("x", 300) + `","auth_method":"api_key"}`,
		}
		for _, body := range cases {
			rec := doAccountsRequest(t, h.mux, http.MethodPost, "/api/admin/accounts", body)
			data := testutil.AssertEnvelopeDataShape(t, rec, errcode.InvalidAPIKey)
			assert.Equal(t, "api_key", data["field"])
		}
	})

	t.Run("field-specific validation remains mapped to the 002 codes", func(t *testing.T) {
		h := setupAccountsHarness(t)

		invalidProvider := doAccountsRequest(t, h.mux, http.MethodPost, "/api/admin/accounts", `{"name":"bad-provider","provider":"anthropic","api_key":"sk-test"}`)
		providerData := testutil.AssertEnvelopeDataShape(t, invalidProvider, errcode.InvalidAccountPayload)
		assert.Equal(t, "provider", providerData["field"])

		invalidBaseURL := doAccountsRequest(t, h.mux, http.MethodPost, "/api/admin/accounts", `{"name":"bad-base","provider":"openai","api_key":"sk-test","base_url":"ftp://router.internal"}`)
		baseURLData := testutil.AssertEnvelopeDataShape(t, invalidBaseURL, errcode.InvalidAccountPayload)
		assert.Equal(t, "base_url", baseURLData["field"])

		malformed := doAccountsRequest(t, h.mux, http.MethodPost, "/api/admin/accounts", `{`)
		testutil.AssertEnvelope(t, malformed, errcode.InvalidAccountPayload)
	})
}

func TestAccountsList(t *testing.T) {
	h := setupAccountsHarness(t)

	apiKeyRow := &domain.UpstreamAccount{
		Name:       "apikey-row",
		Provider:   domain.ProviderOpenAI,
		APIKey:     "sk-api-row",
		Status:     domain.AccountStatusActive,
		AuthMethod: domain.AuthMethodAPIKey,
	}
	require.NoError(t, h.repo.Create(context.Background(), apiKeyRow))

	mappedPlan := "chatgpt-plus"
	mappedEmail := "alice@example.com"
	mappedAccountID := "org-mapped"
	_, err := h.repo.InsertUpstreamAccount(context.Background(), oauthRow(
		"oauth-mapped",
		domain.AuthMethodOAuthBrowser,
		&mappedPlan,
		&mappedEmail,
		&mappedAccountID,
	))
	require.NoError(t, err)

	unknownPlan := "chatgpt-edu"
	unknownEmail := "edu@example.com"
	unknownAccountID := "org-unknown"
	_, err = h.repo.InsertUpstreamAccount(context.Background(), oauthRow(
		"oauth-unknown-plan",
		domain.AuthMethodOAuthBrowser,
		&unknownPlan,
		&unknownEmail,
		&unknownAccountID,
	))
	require.NoError(t, err)

	missingEmailPlan := "chatgpt-team"
	missingEmailAccountID := "org-no-email"
	_, err = h.repo.InsertUpstreamAccount(context.Background(), oauthRow(
		"oauth-missing-email",
		domain.AuthMethodOAuthDevice,
		&missingEmailPlan,
		nil,
		&missingEmailAccountID,
	))
	require.NoError(t, err)

	rec := doAccountsRequest(t, h.mux, http.MethodGet, "/api/admin/accounts", "")
	data := testutil.AssertEnvelopeDataShape(t, rec, 0)
	assert.Equal(t, float64(4), data["total"])

	rawAccounts, ok := data["accounts"].([]any)
	require.True(t, ok, "data.accounts should be an array, got %#v", data["accounts"])
	require.Len(t, rawAccounts, 4)

	body := rec.Body.Bytes()
	for _, forbiddenKey := range []string{`"api_key":`, `"access_token":`, `"refresh_token":`, `"id_token":`} {
		assert.NotContainsf(t, string(body), forbiddenKey, "response leaked forbidden key %s", forbiddenKey)
	}
	for _, secret := range []string{
		"sk-api-row",
		"tok-access-oauth-mapped",
		"tok-refresh-oauth-mapped",
		"tok-id-oauth-mapped",
		"tok-access-oauth-unknown-plan",
		"tok-access-oauth-missing-email",
	} {
		assert.NotContainsf(t, string(body), secret, "response leaked secret value %q", secret)
	}

	apiKeyItem := accountByName(t, rawAccounts, "apikey-row")
	assert.Equal(t, string(domain.AuthMethodAPIKey), apiKeyItem["auth_method"])
	assert.NotContains(t, apiKeyItem, "email")
	assert.NotContains(t, apiKeyItem, "plan_type")
	assert.NotContains(t, apiKeyItem, "plan_type_label")
	assert.NotContains(t, apiKeyItem, "chatgpt_account_id")
	assert.NotContains(t, apiKeyItem, "last_refresh")
	assert.NotContains(t, apiKeyItem, "access_expires_at")

	mappedItem := accountByName(t, rawAccounts, "oauth-mapped")
	assert.Equal(t, string(domain.AuthMethodOAuthBrowser), mappedItem["auth_method"])
	assert.Equal(t, mappedEmail, mappedItem["email"])
	assert.Equal(t, mappedPlan, mappedItem["plan_type"])
	assert.Equal(t, "ChatGPT Plus", mappedItem["plan_type_label"])
	assert.Equal(t, mappedAccountID, mappedItem["chatgpt_account_id"])
	assert.Contains(t, mappedItem, "last_refresh")
	assert.Contains(t, mappedItem, "access_expires_at")

	unknownItem := accountByName(t, rawAccounts, "oauth-unknown-plan")
	assert.Equal(t, unknownPlan, unknownItem["plan_type"])
	assert.Equal(t, unknownPlan, unknownItem["plan_type_label"])

	missingEmailItem := accountByName(t, rawAccounts, "oauth-missing-email")
	assert.Equal(t, string(domain.AuthMethodOAuthDevice), missingEmailItem["auth_method"])
	assert.NotContains(t, missingEmailItem, "email")
	assert.Equal(t, missingEmailPlan, missingEmailItem["plan_type"])
	assert.Equal(t, "ChatGPT Team", missingEmailItem["plan_type_label"])
	assert.Equal(t, missingEmailAccountID, missingEmailItem["chatgpt_account_id"])
}

func TestAccountsList_PerfSanity(t *testing.T) {
	h := setupAccountsHarness(t)

	plan := "chatgpt-plus"
	email := "bulk@example.com"
	accountID := "org-bulk"
	for i := 0; i < 50; i++ {
		require.NoError(t, h.repo.Create(context.Background(), &domain.UpstreamAccount{
			Name:       "api-" + strconv.Itoa(i),
			Provider:   domain.ProviderOpenAI,
			APIKey:     "sk-bulk-" + strconv.Itoa(i),
			Status:     domain.AccountStatusActive,
			AuthMethod: domain.AuthMethodAPIKey,
		}))
		_, err := h.repo.InsertUpstreamAccount(context.Background(), oauthRow(
			"oauth-"+strconv.Itoa(i),
			domain.AuthMethodOAuthBrowser,
			&plan,
			&email,
			&accountID,
		))
		require.NoError(t, err)
	}

	start := time.Now()
	rec := doAccountsRequest(t, h.mux, http.MethodGet, "/api/admin/accounts", "")
	elapsed := time.Since(start)

	data := testutil.AssertEnvelopeDataShape(t, rec, 0)
	rawAccounts, ok := data["accounts"].([]any)
	require.True(t, ok)
	require.Len(t, rawAccounts, 100)
	if elapsed > 100*time.Millisecond {
		t.Fatalf("GET /api/admin/accounts took %s, want <= 100ms for 100 rows", elapsed)
	}
}

func TestAccountsList_SystemError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	inner := admin.NewHandler(
		core.NewAccountService(&fakeAccountRepo{
			listFn: func(context.Context, []string) ([]domain.UpstreamAccount, error) {
				return nil, assert.AnError
			},
		}, logger),
		core.NewRequestService(&fakeRequestRepo{}),
		core.NewHealthService(&fakeAccountRepo{}),
		core.NewAccountSelector(&fakeAccountRepo{}, core.NewConsistentHashRouter()),
		logger,
	)
	wrapped := NewWrappedHandler(inner, alwaysOpenGate{}, logger)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/admin/accounts", wrapped.ListAccounts)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/accounts", bytes.NewReader(nil))
	req = req.WithContext(api.WithRequestID(req.Context(), "req-error"))
	mux.ServeHTTP(rec, req)

	testutil.AssertEnvelope(t, rec, errcode.InternalError)
}

type fakeAccountRepo struct {
	createErr    error
	getByIDFn    func(ctx context.Context, id int64) (*domain.UpstreamAccount, error)
	listFn       func(ctx context.Context, statusFilter []string) ([]domain.UpstreamAccount, error)
	listActiveFn func(ctx context.Context) ([]domain.UpstreamAccount, error)
}

func (f *fakeAccountRepo) Create(context.Context, *domain.UpstreamAccount) error { return f.createErr }

func (f *fakeAccountRepo) GetByID(ctx context.Context, id int64) (*domain.UpstreamAccount, error) {
	if f.getByIDFn != nil {
		return f.getByIDFn(ctx, id)
	}
	return nil, domain.ErrAccountNotFound
}

func (f *fakeAccountRepo) List(ctx context.Context, statusFilter []string) ([]domain.UpstreamAccount, error) {
	if f.listFn != nil {
		return f.listFn(ctx, statusFilter)
	}
	return nil, nil
}

func (f *fakeAccountRepo) ListActive(ctx context.Context) ([]domain.UpstreamAccount, error) {
	if f.listActiveFn != nil {
		return f.listActiveFn(ctx)
	}
	return nil, nil
}

func (f *fakeAccountRepo) UpdateStatus(context.Context, int64, string) error { return nil }

func (f *fakeAccountRepo) UpdateDetails(context.Context, int64, core.AccountDetailsPatch) (*domain.UpstreamAccount, error) {
	return nil, domain.ErrAccountNotFound
}

type fakeRequestRepo struct{}

func (*fakeRequestRepo) Insert(context.Context, *domain.RequestRecord) error { return nil }
func (*fakeRequestRepo) Query(context.Context, core.QueryParams) ([]domain.RequestRecord, error) {
	return nil, nil
}
func (*fakeRequestRepo) UsageSummary(context.Context) (core.RequestUsageSummary, error) {
	return core.RequestUsageSummary{}, nil
}
func (*fakeRequestRepo) GetByID(context.Context, int64) (*domain.RequestRecord, error) {
	return nil, domain.ErrRequestRecordNotFound
}
func (*fakeRequestRepo) FilterOptions(context.Context, core.QueryParams) (core.RequestFilterOptions, error) {
	return core.RequestFilterOptions{}, nil
}

func TestAccountsUpdate(t *testing.T) {
	t.Run("updates an api_key row and returns the fresh row in the envelope", func(t *testing.T) {
		h := setupAccountsHarness(t)

		create := doAccountsRequest(t, h.mux, http.MethodPost, "/api/admin/accounts", `{"name":"before","provider":"openai","api_key":"sk-old","base_url":"https://api.openai.com","capabilities":["op.openai.responses"]}`)
		testutil.AssertEnvelopeDataShape(t, create, 0)

		rec := doAccountsRequest(t, h.mux, http.MethodPost, "/api/admin/accounts/1/update", `{"name":"renamed 账户","base_url":"","api_key":"sk-new","capabilities":["op.openai.chat_completions"]}`)
		data := testutil.AssertEnvelopeDataShape(t, rec, 0)
		assert.Equal(t, "renamed 账户", data["name"])
		assert.Equal(t, []any{"op.openai.chat_completions"}, data["capabilities"])

		row, err := h.repo.GetByID(context.Background(), 1)
		require.NoError(t, err)
		assert.Equal(t, "renamed 账户", row.Name)
		assert.Equal(t, "sk-new", row.APIKey)
		assert.Nil(t, row.BaseURL, "empty base_url in the patch must clear the stored value")
	})

	t.Run("rejects edits to oauth rows with a business envelope", func(t *testing.T) {
		h := setupAccountsHarness(t)

		lastRefresh := time.Date(2026, 4, 15, 10, 2, 17, 0, time.UTC)
		accessExpiresAt := lastRefresh.Add(59 * time.Minute)
		oauth := &domain.UpstreamAccount{
			Name:            "oauth-row",
			Provider:        domain.ProviderOpenAI,
			Status:          domain.AccountStatusActive,
			AuthMethod:      domain.AuthMethodOAuthBrowser,
			AccessToken:     []byte("tok"),
			RefreshToken:    []byte("tok-r"),
			IDToken:         []byte("tok-i"),
			LastRefresh:     &lastRefresh,
			AccessExpiresAt: &accessExpiresAt,
		}
		id, err := h.repo.InsertUpstreamAccount(context.Background(), oauth)
		require.NoError(t, err)

		rec := doAccountsRequest(t, h.mux, http.MethodPost, fmt.Sprintf("/api/admin/accounts/%d/update", id), `{"name":"hijack"}`)
		data := testutil.AssertEnvelopeDataShape(t, rec, errcode.InvalidAccountPayload)
		assert.Contains(t, data["detail"].(string), "only api_key rows are editable")
	})

	t.Run("field validation errors surface the offending field in the envelope", func(t *testing.T) {
		h := setupAccountsHarness(t)

		create := doAccountsRequest(t, h.mux, http.MethodPost, "/api/admin/accounts", `{"name":"before","provider":"openai","api_key":"sk-old"}`)
		testutil.AssertEnvelopeDataShape(t, create, 0)

		rec := doAccountsRequest(t, h.mux, http.MethodPost, "/api/admin/accounts/1/update", `{"name":"ctrl\u0001name"}`)
		data := testutil.AssertEnvelopeDataShape(t, rec, errcode.InvalidAccountPayload)
		assert.Equal(t, "name", data["field"])
	})
}
func (*fakeRequestRepo) DeleteBefore(context.Context, time.Time) (int64, error) { return 0, nil }

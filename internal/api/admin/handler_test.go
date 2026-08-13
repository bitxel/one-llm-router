package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/user/one-llm-router/internal/core"
	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/store"
)

func setupTestHandler(t *testing.T) (*Handler, *http.ServeMux, func()) {
	t.Helper()

	s, err := store.New("sqlite3", ":memory:", 1, 1)
	require.NoError(t, err)
	require.NoError(t, s.Migrate("sqlite3"))

	engine := s.Engine()
	accountRepo := store.NewAccountRepo(engine)
	recordRepo := store.NewRequestRecordRepo(engine)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	sessionRouter := core.NewConsistentHashRouter()
	selector := core.NewAccountSelector(accountRepo, sessionRouter)
	accountSvc := core.NewAccountService(accountRepo, logger)
	requestSvc := core.NewRequestService(recordRepo)
	healthSvc := core.NewHealthService(accountRepo)

	handler := NewHandler(accountSvc, requestSvc, healthSvc, selector, logger)
	handler.SetAccountListProvider(accountRepo)
	mux := http.NewServeMux()
	RegisterRoutes(mux, handler)

	return handler, mux, func() { _ = s.Close() }
}

func TestAdminCRUDLifecycle(t *testing.T) {
	_, mux, cleanup := setupTestHandler(t)
	defer cleanup()

	body := `{"name":"team-alpha","provider":"openai","api_key":"sk-test"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/admin/accounts", bytes.NewReader([]byte(body)))
	mux.ServeHTTP(w, r)
	assert.Equal(t, http.StatusCreated, w.Code)

	var created domain.UpstreamAccount
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	assert.Equal(t, "team-alpha", created.Name)
	assert.Equal(t, domain.AccountStatusActive, created.Status)
	assert.NotZero(t, created.ID)

	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/admin/accounts", nil)
	mux.ServeHTTP(w, r)
	assert.Equal(t, http.StatusOK, w.Code)

	var listResp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &listResp))
	accounts := listResp["accounts"].([]any)
	assert.Len(t, accounts, 1)

	w = httptest.NewRecorder()
	r = httptest.NewRequest("POST", "/admin/accounts/1/disable", nil)
	mux.ServeHTTP(w, r)
	assert.Equal(t, http.StatusOK, w.Code)

	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/admin/accounts/1", nil)
	mux.ServeHTTP(w, r)
	assert.Equal(t, http.StatusOK, w.Code)

	var getResp domain.UpstreamAccount
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &getResp))
	assert.Equal(t, domain.AccountStatusDisabled, getResp.Status)

	w = httptest.NewRecorder()
	r = httptest.NewRequest("POST", "/admin/accounts/1/enable", nil)
	mux.ServeHTTP(w, r)
	assert.Equal(t, http.StatusOK, w.Code)

	w = httptest.NewRecorder()
	r = httptest.NewRequest("POST", "/admin/accounts/1/delete", nil)
	mux.ServeHTTP(w, r)
	assert.Equal(t, http.StatusOK, w.Code)

	w = httptest.NewRecorder()
	r = httptest.NewRequest("POST", "/admin/accounts/1/enable", nil)
	mux.ServeHTTP(w, r)
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestAdminAccountNotFound(t *testing.T) {
	_, mux, cleanup := setupTestHandler(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/admin/accounts/999", nil)
	mux.ServeHTTP(w, r)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAdminHealthEndpoint(t *testing.T) {
	_, mux, cleanup := setupTestHandler(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/admin/health", nil)
	mux.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)

	var health core.HealthStatus
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &health))
	assert.Equal(t, "healthy", health.Status)
	assert.Equal(t, 0, health.ActiveAccounts)
}

func TestAdminResolveSession(t *testing.T) {
	_, mux, cleanup := setupTestHandler(t)
	defer cleanup()

	body := `{"name":"acct-1","provider":"openai","api_key":"sk-1"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/admin/accounts", bytes.NewReader([]byte(body)))
	mux.ServeHTTP(w, r)
	require.Equal(t, http.StatusCreated, w.Code)

	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/admin/sessions/resolve?session_key=test-session", nil)
	mux.ServeHTTP(w, r)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "test-session", resp["session_key"])
	assert.NotNil(t, resp["account_id"])
}

func TestAdminResolveSession_MissingParam(t *testing.T) {
	_, mux, cleanup := setupTestHandler(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/admin/sessions/resolve", nil)
	mux.ServeHTTP(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAdminQueryRequests(t *testing.T) {
	_, mux, cleanup := setupTestHandler(t)
	defer cleanup()

	w := httptest.NewRecorder()
	v := url.Values{"start": {time.Now().Add(-24 * time.Hour).Format(time.RFC3339)}}
	r := httptest.NewRequest("GET", "/admin/requests?"+v.Encode(), nil)
	mux.ServeHTTP(w, r)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotNil(t, resp["records"])
}

func TestAdminQueryRequests_MissingStart(t *testing.T) {
	_, mux, cleanup := setupTestHandler(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/admin/requests", nil)
	mux.ServeHTTP(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAdminCreateAccount_Validation(t *testing.T) {
	_, mux, cleanup := setupTestHandler(t)
	defer cleanup()

	w := httptest.NewRecorder()
	body := `{"name":"","api_key":"sk-test"}`
	r := httptest.NewRequest("POST", "/admin/accounts", bytes.NewReader([]byte(body)))
	mux.ServeHTTP(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	w = httptest.NewRecorder()
	body = `{"name":"test","api_key":""}`
	r = httptest.NewRequest("POST", "/admin/accounts", bytes.NewReader([]byte(body)))
	mux.ServeHTTP(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAdminListWithFilter(t *testing.T) {
	_, mux, cleanup := setupTestHandler(t)
	defer cleanup()

	for _, name := range []string{"a1", "a2"} {
		body := `{"name":"` + name + `","api_key":"sk-` + name + `"}`
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/admin/accounts", bytes.NewReader([]byte(body)))
		mux.ServeHTTP(w, r)
		require.Equal(t, http.StatusCreated, w.Code)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/admin/accounts/1/disable", nil)
	mux.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/admin/accounts?status=active", nil)
	mux.ServeHTTP(w, r)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	accounts := resp["accounts"].([]any)
	assert.Len(t, accounts, 1)
}

func TestAdminAccountBaseURL(t *testing.T) {
	_, mux, cleanup := setupTestHandler(t)
	defer cleanup()

	body := `{"name":"custom","api_key":"sk-custom","provider":"openai","base_url":"https://my-proxy.example.com"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/admin/accounts", bytes.NewReader([]byte(body)))
	mux.ServeHTTP(w, r)
	require.Equal(t, http.StatusCreated, w.Code)

	var created domain.UpstreamAccount
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	require.NotNil(t, created.BaseURL)
	assert.Equal(t, "https://my-proxy.example.com", *created.BaseURL)
}

// --- Fakes for handler error-path coverage ---

type fakeAccountRepo struct {
	createErr error

	getByIDFn func(ctx context.Context, id int64) (*domain.UpstreamAccount, error)

	listFn func(ctx context.Context, statusFilter []string) ([]domain.UpstreamAccount, error)

	listActiveFn func(ctx context.Context) ([]domain.UpstreamAccount, error)

	updateStatusErr error

	updateDetailsFn func(ctx context.Context, id int64, patch core.AccountDetailsPatch) (*domain.UpstreamAccount, error)
}

func (f *fakeAccountRepo) Create(ctx context.Context, account *domain.UpstreamAccount) error {
	if f.createErr != nil {
		return f.createErr
	}
	account.ID = 1
	return nil
}

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

func (f *fakeAccountRepo) UpdateStatus(ctx context.Context, id int64, status string) error {
	return f.updateStatusErr
}

func (f *fakeAccountRepo) UpdateDetails(ctx context.Context, id int64, patch core.AccountDetailsPatch) (*domain.UpstreamAccount, error) {
	if f.updateDetailsFn != nil {
		return f.updateDetailsFn(ctx, id, patch)
	}
	return nil, domain.ErrAccountNotFound
}

type fakeRequestRepo struct {
	queryFn func(ctx context.Context, params core.QueryParams) ([]domain.RequestRecord, error)
}

func (f *fakeRequestRepo) Insert(ctx context.Context, record *domain.RequestRecord) error {
	return nil
}

func (f *fakeRequestRepo) GetByID(ctx context.Context, id int64) (*domain.RequestRecord, error) {
	return nil, domain.ErrRequestRecordNotFound
}

func (f *fakeRequestRepo) Query(ctx context.Context, params core.QueryParams) ([]domain.RequestRecord, error) {
	if f.queryFn != nil {
		return f.queryFn(ctx, params)
	}
	return nil, nil
}

func (f *fakeRequestRepo) UsageSummary(context.Context) (core.RequestUsageSummary, error) {
	return core.RequestUsageSummary{}, nil
}

func (f *fakeRequestRepo) FilterOptions(context.Context, core.QueryParams) (core.RequestFilterOptions, error) {
	return core.RequestFilterOptions{}, nil
}

func (f *fakeRequestRepo) DeleteBefore(ctx context.Context, before time.Time) (int64, error) {
	return 0, nil
}

func setupHandlerWithRepos(t *testing.T, ar core.AccountRepository, rr core.RequestRecordRepository) *http.ServeMux {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	sessionRouter := core.NewConsistentHashRouter()
	selector := core.NewAccountSelector(ar, sessionRouter)
	accountSvc := core.NewAccountService(ar, logger)
	requestSvc := core.NewRequestService(rr)
	healthSvc := core.NewHealthService(ar)
	h := NewHandler(accountSvc, requestSvc, healthSvc, selector, logger)
	if provider, ok := ar.(interface {
		ListForAdminAPI(ctx context.Context) ([]store.AccountListItem, error)
	}); ok {
		h.SetAccountListProvider(provider)
	}
	mux := http.NewServeMux()
	RegisterRoutes(mux, h)
	return mux
}

func TestQueryRequests_ValidationAndErrors(t *testing.T) {
	startOK := time.Date(2025, 1, 2, 15, 4, 5, 0, time.UTC).Format(time.RFC3339)
	endOK := time.Date(2025, 1, 3, 15, 4, 5, 0, time.UTC).Format(time.RFC3339)

	tests := []struct {
		name       string
		query      string
		wantStatus int
		repoErr    bool
	}{
		{
			name:       "invalid start time",
			query:      "start=not-a-date",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid end time",
			query:      "start=" + url.QueryEscape(startOK) + "&end=bad-end",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid account_id",
			query:      "start=" + url.QueryEscape(startOK) + "&account_id=notint",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid limit non-numeric",
			query:      "start=" + url.QueryEscape(startOK) + "&limit=xx",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid limit zero",
			query:      "start=" + url.QueryEscape(startOK) + "&limit=0",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid before cursor",
			query:      "start=" + url.QueryEscape(startOK) + "&before=notint",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "repo error",
			query:      "start=" + url.QueryEscape(startOK),
			wantStatus: http.StatusInternalServerError,
			repoErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ar := &fakeAccountRepo{}
			var rr core.RequestRecordRepository = &fakeRequestRepo{}
			if tt.repoErr {
				rr = &fakeRequestRepo{
					queryFn: func(context.Context, core.QueryParams) ([]domain.RequestRecord, error) {
						return nil, errors.New("db unavailable")
					},
				}
			}
			mux := setupHandlerWithRepos(t, ar, rr)

			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/admin/requests?"+tt.query, nil)
			mux.ServeHTTP(w, r)
			assert.Equal(t, tt.wantStatus, w.Code)
		})
	}

	t.Run("all filters valid", func(t *testing.T) {
		ar := &fakeAccountRepo{}
		mux := setupHandlerWithRepos(t, ar, &fakeRequestRepo{
			queryFn: func(_ context.Context, p core.QueryParams) ([]domain.RequestRecord, error) {
				assert.NotNil(t, p.Start)
				assert.NotNil(t, p.End)
				assert.NotNil(t, p.AccountID)
				assert.NotNil(t, p.Outcome)
				assert.Equal(t, 25, p.Limit)
				assert.NotNil(t, p.BeforeID)
				return []domain.RequestRecord{}, nil
			},
		})

		q := url.Values{}
		q.Set("start", startOK)
		q.Set("end", endOK)
		q.Set("account_id", "7")
		q.Set("outcome", domain.OutcomeSuccess)
		q.Set("limit", "25")
		q.Set("before", "100")

		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/admin/requests?"+q.Encode(), nil)
		mux.ServeHTTP(w, r)
		assert.Equal(t, http.StatusOK, w.Code)
	})
}

func TestCreateAccount_MoreCases(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		ar         *fakeAccountRepo
		wantStatus int
	}{
		{
			name:       "invalid JSON",
			body:       `{`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "missing name",
			body:       `{"api_key":"sk"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "missing api_key",
			body:       `{"name":"n"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid provider",
			body:       `{"name":"n","api_key":"sk","provider":"unknown-vendor"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "repo error",
			body:       `{"name":"n","api_key":"sk"}`,
			ar:         &fakeAccountRepo{createErr: errors.New("insert failed")},
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ar := tt.ar
			if ar == nil {
				ar = &fakeAccountRepo{}
			}
			mux := setupHandlerWithRepos(t, ar, &fakeRequestRepo{})

			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/admin/accounts", bytes.NewReader([]byte(tt.body)))
			mux.ServeHTTP(w, r)
			assert.Equal(t, tt.wantStatus, w.Code)
		})
	}
}

func TestListAccounts_RepoError(t *testing.T) {
	ar := &fakeAccountRepo{
		listFn: func(context.Context, []string) ([]domain.UpstreamAccount, error) {
			return nil, errors.New("list failed")
		},
	}
	mux := setupHandlerWithRepos(t, ar, &fakeRequestRepo{})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/admin/accounts", nil)
	mux.ServeHTTP(w, r)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGetAccount_Cases(t *testing.T) {
	t.Run("bad id", func(t *testing.T) {
		mux := setupHandlerWithRepos(t, &fakeAccountRepo{}, &fakeRequestRepo{})
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/admin/accounts/not-a-number", nil)
		mux.ServeHTTP(w, r)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("not found", func(t *testing.T) {
		mux := setupHandlerWithRepos(t, &fakeAccountRepo{}, &fakeRequestRepo{})
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/admin/accounts/404", nil)
		mux.ServeHTTP(w, r)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("repo error", func(t *testing.T) {
		ar := &fakeAccountRepo{
			getByIDFn: func(context.Context, int64) (*domain.UpstreamAccount, error) {
				return nil, fmt.Errorf("db: %w", errors.New("boom"))
			},
		}
		mux := setupHandlerWithRepos(t, ar, &fakeRequestRepo{})
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/admin/accounts/1", nil)
		mux.ServeHTTP(w, r)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("success", func(t *testing.T) {
		resetAt := time.Unix(1755123456, 0).UTC()
		windowSeconds := int64(7200)
		ar := &fakeAccountRepo{
			getByIDFn: func(context.Context, int64) (*domain.UpstreamAccount, error) {
				return &domain.UpstreamAccount{
					ID:                     1,
					Name:                   "a",
					Provider:               domain.ProviderOpenAI,
					Status:                 domain.AccountStatusActive,
					AuthMethod:             domain.AuthMethodOAuthBrowser,
					AccessToken:            []byte("at"),
					RefreshToken:           []byte("rt"),
					IDToken:                []byte("it"),
					LastRefresh:            &resetAt,
					AccessExpiresAt:        &resetAt,
					PrimaryResetAt:         &resetAt,
					PrimaryWindowSeconds:   &windowSeconds,
					PrimaryUsedPercent:     f64ptr(28),
					SecondaryUsedPercent:   f64ptr(10),
					SecondaryResetAt:       &resetAt,
					SecondaryWindowSeconds: &windowSeconds,
				}, nil
			},
		}
		mux := setupHandlerWithRepos(t, ar, &fakeRequestRepo{})
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/admin/accounts/1", nil)
		mux.ServeHTTP(w, r)
		assert.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, `"primary_reset_at"`)
		assert.Contains(t, body, `"secondary_reset_at"`)
		assert.Contains(t, body, `"primary_window_seconds":7200`)
		assert.Contains(t, body, `"secondary_window_seconds":7200`)
		assert.Contains(t, body, `"primary_used_percent":28`)
		assert.Contains(t, body, `"secondary_used_percent":10`)
	})
}

func f64ptr(v float64) *float64 {
	return &v
}

func TestUpdateAccount_Cases(t *testing.T) {
	t.Run("bad id", func(t *testing.T) {
		mux := setupHandlerWithRepos(t, &fakeAccountRepo{}, &fakeRequestRepo{})
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/admin/accounts/not-a-number/update", strings.NewReader(`{}`))
		mux.ServeHTTP(w, r)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid body", func(t *testing.T) {
		mux := setupHandlerWithRepos(t, &fakeAccountRepo{}, &fakeRequestRepo{})
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/admin/accounts/1/update", strings.NewReader(`{`))
		mux.ServeHTTP(w, r)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("not found", func(t *testing.T) {
		mux := setupHandlerWithRepos(t, &fakeAccountRepo{}, &fakeRequestRepo{})
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/admin/accounts/404/update", strings.NewReader(`{"name":"x"}`))
		mux.ServeHTTP(w, r)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("success", func(t *testing.T) {
		ar := &fakeAccountRepo{
			updateDetailsFn: func(_ context.Context, id int64, _ core.AccountDetailsPatch) (*domain.UpstreamAccount, error) {
				return &domain.UpstreamAccount{
					ID:           id,
					Name:         "renamed",
					Provider:     domain.ProviderOpenAI,
					Status:       domain.AccountStatusActive,
					AuthMethod:   domain.AuthMethodAPIKey,
					Capabilities: []string{"op.openai.responses"},
				}, nil
			},
		}
		mux := setupHandlerWithRepos(t, ar, &fakeRequestRepo{})
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/admin/accounts/1/update",
			strings.NewReader(`{"name":"renamed","base_url":"https://api.openai.com"}`))
		mux.ServeHTTP(w, r)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), `"renamed"`)
		assert.Contains(t, w.Body.String(), `"op.openai.responses"`)
	})

	t.Run("validation error surfaces field", func(t *testing.T) {
		ar := &fakeAccountRepo{
			updateDetailsFn: func(context.Context, int64, core.AccountDetailsPatch) (*domain.UpstreamAccount, error) {
				return nil, &domain.ValidationError{Field: "name", Message: "account name must be 1-64 characters (after trimming) and free of control characters"}
			},
		}
		mux := setupHandlerWithRepos(t, ar, &fakeRequestRepo{})
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/admin/accounts/1/update", strings.NewReader(`{"name":"\u0001bad"}`))
		mux.ServeHTTP(w, r)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), `"field":"name"`)
	})

	t.Run("oauth row rejected", func(t *testing.T) {
		ar := &fakeAccountRepo{
			updateDetailsFn: func(context.Context, int64, core.AccountDetailsPatch) (*domain.UpstreamAccount, error) {
				return nil, fmt.Errorf("update details 1: %w: only api_key rows are editable (got %q)", domain.ErrInvalidAccountShape, domain.AuthMethodOAuthBrowser)
			},
		}
		mux := setupHandlerWithRepos(t, ar, &fakeRequestRepo{})
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/admin/accounts/1/update", strings.NewReader(`{"name":"x"}`))
		mux.ServeHTTP(w, r)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestDisableAccount_Cases(t *testing.T) {
	t.Run("bad id", func(t *testing.T) {
		mux := setupHandlerWithRepos(t, &fakeAccountRepo{}, &fakeRequestRepo{})
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/admin/accounts/bad/disable", nil)
		mux.ServeHTTP(w, r)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("not found", func(t *testing.T) {
		mux := setupHandlerWithRepos(t, &fakeAccountRepo{}, &fakeRequestRepo{})
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/admin/accounts/999/disable", nil)
		mux.ServeHTTP(w, r)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("already deleted", func(t *testing.T) {
		_, mux, cleanup := setupTestHandler(t)
		defer cleanup()

		body := `{"name":"gone","api_key":"sk"}`
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/admin/accounts", bytes.NewReader([]byte(body)))
		mux.ServeHTTP(w, r)
		require.Equal(t, http.StatusCreated, w.Code)

		w = httptest.NewRecorder()
		r = httptest.NewRequest(http.MethodPost, "/admin/accounts/1/delete", nil)
		mux.ServeHTTP(w, r)
		require.Equal(t, http.StatusOK, w.Code)

		w = httptest.NewRecorder()
		r = httptest.NewRequest(http.MethodPost, "/admin/accounts/1/disable", nil)
		mux.ServeHTTP(w, r)
		assert.Equal(t, http.StatusConflict, w.Code)
	})

	t.Run("success", func(t *testing.T) {
		_, mux, cleanup := setupTestHandler(t)
		defer cleanup()

		body := `{"name":"live","api_key":"sk"}`
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/admin/accounts", bytes.NewReader([]byte(body)))
		mux.ServeHTTP(w, r)
		require.Equal(t, http.StatusCreated, w.Code)

		w = httptest.NewRecorder()
		r = httptest.NewRequest(http.MethodPost, "/admin/accounts/1/disable", nil)
		mux.ServeHTTP(w, r)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("unexpected repo error", func(t *testing.T) {
		ar := &fakeAccountRepo{
			getByIDFn: func(context.Context, int64) (*domain.UpstreamAccount, error) {
				return nil, errors.New("unexpected")
			},
		}
		mux := setupHandlerWithRepos(t, ar, &fakeRequestRepo{})
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/admin/accounts/1/disable", nil)
		mux.ServeHTTP(w, r)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestDeleteAccount_Cases(t *testing.T) {
	t.Run("bad id", func(t *testing.T) {
		mux := setupHandlerWithRepos(t, &fakeAccountRepo{}, &fakeRequestRepo{})
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/admin/accounts/xyz/delete", nil)
		mux.ServeHTTP(w, r)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("not found", func(t *testing.T) {
		mux := setupHandlerWithRepos(t, &fakeAccountRepo{}, &fakeRequestRepo{})
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/admin/accounts/999/delete", nil)
		mux.ServeHTTP(w, r)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("already deleted idempotent", func(t *testing.T) {
		_, mux, cleanup := setupTestHandler(t)
		defer cleanup()

		body := `{"name":"twice","api_key":"sk"}`
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/admin/accounts", bytes.NewReader([]byte(body)))
		mux.ServeHTTP(w, r)
		require.Equal(t, http.StatusCreated, w.Code)

		w = httptest.NewRecorder()
		r = httptest.NewRequest(http.MethodPost, "/admin/accounts/1/delete", nil)
		mux.ServeHTTP(w, r)
		require.Equal(t, http.StatusOK, w.Code)

		w = httptest.NewRecorder()
		r = httptest.NewRequest(http.MethodPost, "/admin/accounts/1/delete", nil)
		mux.ServeHTTP(w, r)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("success", func(t *testing.T) {
		_, mux, cleanup := setupTestHandler(t)
		defer cleanup()

		body := `{"name":"delme","api_key":"sk"}`
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/admin/accounts", bytes.NewReader([]byte(body)))
		mux.ServeHTTP(w, r)
		require.Equal(t, http.StatusCreated, w.Code)

		w = httptest.NewRecorder()
		r = httptest.NewRequest(http.MethodPost, "/admin/accounts/1/delete", nil)
		mux.ServeHTTP(w, r)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("unexpected repo error", func(t *testing.T) {
		ar := &fakeAccountRepo{
			getByIDFn: func(context.Context, int64) (*domain.UpstreamAccount, error) {
				return nil, errors.New("unexpected")
			},
		}
		mux := setupHandlerWithRepos(t, ar, &fakeRequestRepo{})
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/admin/accounts/1/delete", nil)
		mux.ServeHTTP(w, r)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestResolveSession_Cases(t *testing.T) {
	t.Run("no capacity", func(t *testing.T) {
		ar := &fakeAccountRepo{
			listActiveFn: func(context.Context) ([]domain.UpstreamAccount, error) {
				return []domain.UpstreamAccount{}, nil
			},
		}
		mux := setupHandlerWithRepos(t, ar, &fakeRequestRepo{})
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/admin/sessions/resolve?session_key=k", nil)
		mux.ServeHTTP(w, r)
		assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	})

	t.Run("success", func(t *testing.T) {
		ar := &fakeAccountRepo{
			listActiveFn: func(context.Context) ([]domain.UpstreamAccount, error) {
				return []domain.UpstreamAccount{
					{ID: 1, Name: "only", Provider: domain.ProviderOpenAI, Status: domain.AccountStatusActive},
				}, nil
			},
		}
		mux := setupHandlerWithRepos(t, ar, &fakeRequestRepo{})
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/admin/sessions/resolve?session_key=my-session", nil)
		mux.ServeHTTP(w, r)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, "my-session", resp["session_key"])
		assert.EqualValues(t, 1, resp["account_id"])
	})

	t.Run("list active error", func(t *testing.T) {
		ar := &fakeAccountRepo{
			listActiveFn: func(context.Context) ([]domain.UpstreamAccount, error) {
				return nil, errors.New("db down")
			},
		}
		mux := setupHandlerWithRepos(t, ar, &fakeRequestRepo{})
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/admin/sessions/resolve?session_key=k", nil)
		mux.ServeHTTP(w, r)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestEnableAccount_HandleAccountErrorBranches(t *testing.T) {
	t.Run("invalid transition", func(t *testing.T) {
		s, err := store.New("sqlite3", ":memory:", 1, 1)
		require.NoError(t, err)
		require.NoError(t, s.Migrate("sqlite3"))
		defer func() { _ = s.Close() }()

		engine := s.Engine()
		accountRepo := store.NewAccountRepo(engine)
		logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
		sessionRouter := core.NewConsistentHashRouter()
		selector := core.NewAccountSelector(accountRepo, sessionRouter)
		accountSvc := core.NewAccountService(accountRepo, logger)
		requestSvc := core.NewRequestService(store.NewRequestRecordRepo(engine))
		healthSvc := core.NewHealthService(accountRepo)
		h := NewHandler(accountSvc, requestSvc, healthSvc, selector, logger)
		mux := http.NewServeMux()
		RegisterRoutes(mux, h)

		body := `{"name":"odd","api_key":"sk"}`
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/admin/accounts", bytes.NewReader([]byte(body)))
		mux.ServeHTTP(w, r)
		require.Equal(t, http.StatusCreated, w.Code)

		_, err = engine.Exec("UPDATE upstream_accounts SET status = ? WHERE id = ?", "bogus", 1)
		require.NoError(t, err)

		w = httptest.NewRecorder()
		r = httptest.NewRequest(http.MethodPost, "/admin/accounts/1/enable", nil)
		mux.ServeHTTP(w, r)
		assert.Equal(t, http.StatusConflict, w.Code)
	})

	t.Run("account deleted", func(t *testing.T) {
		_, mux, cleanup := setupTestHandler(t)
		defer cleanup()

		body := `{"name":"del","api_key":"sk"}`
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/admin/accounts", bytes.NewReader([]byte(body)))
		mux.ServeHTTP(w, r)
		require.Equal(t, http.StatusCreated, w.Code)

		w = httptest.NewRecorder()
		r = httptest.NewRequest(http.MethodPost, "/admin/accounts/1/delete", nil)
		mux.ServeHTTP(w, r)
		require.Equal(t, http.StatusOK, w.Code)

		w = httptest.NewRecorder()
		r = httptest.NewRequest(http.MethodPost, "/admin/accounts/1/enable", nil)
		mux.ServeHTTP(w, r)
		assert.Equal(t, http.StatusConflict, w.Code)
	})

	t.Run("not found", func(t *testing.T) {
		mux := setupHandlerWithRepos(t, &fakeAccountRepo{}, &fakeRequestRepo{})
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/admin/accounts/999/enable", nil)
		mux.ServeHTTP(w, r)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("default internal error", func(t *testing.T) {
		ar := &fakeAccountRepo{
			getByIDFn: func(context.Context, int64) (*domain.UpstreamAccount, error) {
				return nil, errors.New("unexpected")
			},
		}
		mux := setupHandlerWithRepos(t, ar, &fakeRequestRepo{})
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/admin/accounts/1/enable", nil)
		mux.ServeHTTP(w, r)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestEnableAccount_BadID(t *testing.T) {
	mux := setupHandlerWithRepos(t, &fakeAccountRepo{}, &fakeRequestRepo{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/admin/accounts/not-numeric/enable", nil)
	mux.ServeHTTP(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestListAccounts_NilSliceNormalized(t *testing.T) {
	ar := &fakeAccountRepo{
		listFn: func(context.Context, []string) ([]domain.UpstreamAccount, error) {
			return nil, nil
		},
	}
	mux := setupHandlerWithRepos(t, ar, &fakeRequestRepo{})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/admin/accounts", nil)
	mux.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	accounts, ok := resp["accounts"].([]any)
	require.True(t, ok)
	assert.Len(t, accounts, 0)
	assert.Equal(t, float64(0), resp["total"])
}

func TestGetHealth_ListAccountsError(t *testing.T) {
	ar := &fakeAccountRepo{
		listFn: func(context.Context, []string) ([]domain.UpstreamAccount, error) {
			return nil, errors.New("list accounts failed")
		},
	}
	mux := setupHandlerWithRepos(t, ar, &fakeRequestRepo{})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/admin/health", nil)
	mux.ServeHTTP(w, r)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	var health core.HealthStatus
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &health))
	assert.Equal(t, "degraded", health.Status)
}

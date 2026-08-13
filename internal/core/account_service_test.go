package core

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/user/one-llm-router/internal/domain"
)

type accountRepoDeleteStatusFails struct {
	*inMemoryAccountRepo
}

func (r *accountRepoDeleteStatusFails) UpdateStatus(ctx context.Context, id int64, status string) error {
	if status == domain.AccountStatusDeleted {
		return errors.New("forced update failure")
	}
	return r.inMemoryAccountRepo.UpdateStatus(ctx, id, status)
}

type inMemoryAccountRepo struct {
	accounts map[int64]*domain.UpstreamAccount
	nextID   int64
}

func newInMemoryAccountRepo() *inMemoryAccountRepo {
	return &inMemoryAccountRepo{accounts: make(map[int64]*domain.UpstreamAccount), nextID: 1}
}

func (r *inMemoryAccountRepo) Create(_ context.Context, acct *domain.UpstreamAccount) error {
	acct.ID = r.nextID
	r.nextID++
	clone := *acct
	r.accounts[acct.ID] = &clone
	return nil
}

func (r *inMemoryAccountRepo) GetByID(_ context.Context, id int64) (*domain.UpstreamAccount, error) {
	if a, ok := r.accounts[id]; ok {
		clone := *a
		return &clone, nil
	}
	return nil, domain.ErrAccountNotFound
}

func (r *inMemoryAccountRepo) List(_ context.Context, filter []string) ([]domain.UpstreamAccount, error) {
	var result []domain.UpstreamAccount
	filterSet := make(map[string]bool)
	for _, s := range filter {
		filterSet[s] = true
	}
	for _, a := range r.accounts {
		if len(filterSet) == 0 || filterSet[a.Status] {
			result = append(result, *a)
		}
	}
	return result, nil
}

func (r *inMemoryAccountRepo) ListActive(_ context.Context) ([]domain.UpstreamAccount, error) {
	var result []domain.UpstreamAccount
	for _, a := range r.accounts {
		if a.Status == domain.AccountStatusActive {
			result = append(result, *a)
		}
	}
	return result, nil
}

func (r *inMemoryAccountRepo) UpdateStatus(_ context.Context, id int64, status string) error {
	if a, ok := r.accounts[id]; ok {
		a.Status = status
		return nil
	}
	return domain.ErrAccountNotFound
}

func (r *inMemoryAccountRepo) UpdateDetails(_ context.Context, id int64, patch AccountDetailsPatch) (*domain.UpstreamAccount, error) {
	a, ok := r.accounts[id]
	if !ok {
		return nil, domain.ErrAccountNotFound
	}
	if patch.Name != nil {
		a.Name = *patch.Name
	}
	if patch.APIKey != nil {
		a.APIKey = *patch.APIKey
	}
	if patch.BaseURL != nil {
		a.BaseURL = patch.BaseURL
	}
	if patch.Capabilities != nil {
		a.Capabilities = patch.Capabilities
	}
	clone := *a
	return &clone, nil
}

func newTestService() (*AccountService, *inMemoryAccountRepo) {
	repo := newInMemoryAccountRepo()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	return NewAccountService(repo, logger), repo
}

func TestAccountService_Create_Success(t *testing.T) {
	svc, _ := newTestService()
	acct, err := svc.Create(context.Background(), "test", "openai", "sk-key", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "test", acct.Name)
	assert.Equal(t, domain.AccountStatusActive, acct.Status)
}

func TestAccountService_Create_EmptyName(t *testing.T) {
	svc, _ := newTestService()
	_, err := svc.Create(context.Background(), "", "openai", "sk-key", nil, nil)
	require.Error(t, err)
	assert.True(t, domain.IsValidationError(err))
}

func TestAccountService_Create_InvalidNameRejected(t *testing.T) {
	svc, _ := newTestService()
	for _, bad := range []string{"   ", "ctrl\x01name", "tab\there"} {
		_, err := svc.Create(context.Background(), bad, "openai", "sk-key", nil, nil)
		require.Error(t, err, bad)
		assert.True(t, domain.IsValidationError(err), bad)
	}
}

func TestAccountService_Create_TrimsAndAllowsMultiByteName(t *testing.T) {
	svc, _ := newTestService()
	acct, err := svc.Create(context.Background(), "  中文账户  ", "openai", "sk-key", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "中文账户", acct.Name)
}

func TestAccountService_Create_EmptyAPIKey(t *testing.T) {
	svc, _ := newTestService()
	_, err := svc.Create(context.Background(), "test", "openai", "", nil, nil)
	require.Error(t, err)
	assert.True(t, domain.IsValidationError(err))
}

func TestAccountService_Create_DefaultProvider(t *testing.T) {
	svc, _ := newTestService()
	acct, err := svc.Create(context.Background(), "test", "", "sk-key", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, domain.ProviderOpenAI, acct.Provider)
}

func TestAccountService_Enable_FromDisabled(t *testing.T) {
	svc, repo := newTestService()
	acct, _ := svc.Create(context.Background(), "test", "openai", "sk-key", nil, nil)
	repo.accounts[acct.ID].Status = domain.AccountStatusDisabled

	err := svc.Enable(context.Background(), acct.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.AccountStatusActive, repo.accounts[acct.ID].Status)
}

func TestAccountService_Enable_AlreadyActive(t *testing.T) {
	svc, _ := newTestService()
	acct, _ := svc.Create(context.Background(), "test", "openai", "sk-key", nil, nil)

	err := svc.Enable(context.Background(), acct.ID)
	require.NoError(t, err)
}

func TestAccountService_Enable_Deleted(t *testing.T) {
	svc, repo := newTestService()
	acct, _ := svc.Create(context.Background(), "test", "openai", "sk-key", nil, nil)
	repo.accounts[acct.ID].Status = domain.AccountStatusDeleted

	err := svc.Enable(context.Background(), acct.ID)
	assert.ErrorIs(t, err, domain.ErrAccountDeleted)
}

func TestAccountService_Disable_FromActive(t *testing.T) {
	svc, repo := newTestService()
	acct, _ := svc.Create(context.Background(), "test", "openai", "sk-key", nil, nil)

	err := svc.Disable(context.Background(), acct.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.AccountStatusDisabled, repo.accounts[acct.ID].Status)
}

func TestAccountService_Disable_AlreadyDisabled(t *testing.T) {
	svc, repo := newTestService()
	acct, _ := svc.Create(context.Background(), "test", "openai", "sk-key", nil, nil)
	repo.accounts[acct.ID].Status = domain.AccountStatusDisabled

	err := svc.Disable(context.Background(), acct.ID)
	require.NoError(t, err)
}

func TestAccountService_Disable_Deleted(t *testing.T) {
	svc, repo := newTestService()
	acct, _ := svc.Create(context.Background(), "test", "openai", "sk-key", nil, nil)
	repo.accounts[acct.ID].Status = domain.AccountStatusDeleted

	err := svc.Disable(context.Background(), acct.ID)
	assert.ErrorIs(t, err, domain.ErrAccountDeleted)
}

func TestAccountService_Delete_FromActive(t *testing.T) {
	svc, repo := newTestService()
	acct, _ := svc.Create(context.Background(), "test", "openai", "sk-key", nil, nil)

	err := svc.Delete(context.Background(), acct.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.AccountStatusDeleted, repo.accounts[acct.ID].Status)
}

func TestAccountService_Delete_AlreadyDeleted(t *testing.T) {
	svc, repo := newTestService()
	acct, _ := svc.Create(context.Background(), "test", "openai", "sk-key", nil, nil)
	repo.accounts[acct.ID].Status = domain.AccountStatusDeleted

	err := svc.Delete(context.Background(), acct.ID)
	require.NoError(t, err)
}

func TestAccountService_Delete_UpdateStatusError(t *testing.T) {
	inner := newInMemoryAccountRepo()
	repo := &accountRepoDeleteStatusFails{inMemoryAccountRepo: inner}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	svc := NewAccountService(repo, logger)

	acct, err := svc.Create(context.Background(), "test", "openai", "sk-key", nil, nil)
	require.NoError(t, err)

	err = svc.Delete(context.Background(), acct.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete account")
}

func TestAccountService_NotFound(t *testing.T) {
	svc, _ := newTestService()
	err := svc.Enable(context.Background(), 999)
	assert.ErrorIs(t, err, domain.ErrAccountNotFound)
}

func TestAccountService_GetByID(t *testing.T) {
	svc, _ := newTestService()
	created, err := svc.Create(context.Background(), "test", "openai", "sk-key", nil, nil)
	require.NoError(t, err)

	got, err := svc.GetByID(context.Background(), created.ID)
	require.NoError(t, err)
	assert.Equal(t, "test", got.Name)

	_, err = svc.GetByID(context.Background(), 999)
	assert.ErrorIs(t, err, domain.ErrAccountNotFound)
}

func TestAccountService_List(t *testing.T) {
	svc, repo := newTestService()
	_, _ = svc.Create(context.Background(), "a1", "openai", "sk-1", nil, nil)
	_, _ = svc.Create(context.Background(), "a2", "openai", "sk-2", nil, nil)
	repo.accounts[2].Status = domain.AccountStatusDisabled

	all, err := svc.List(context.Background(), nil)
	require.NoError(t, err)
	assert.Len(t, all, 2)

	active, err := svc.List(context.Background(), []string{domain.AccountStatusActive})
	require.NoError(t, err)
	assert.Len(t, active, 1)
}

func TestAccountService_Create_UnsupportedProvider(t *testing.T) {
	svc, _ := newTestService()
	_, err := svc.Create(context.Background(), "test", "unknown", "sk-key", nil, nil)
	require.Error(t, err)
	assert.True(t, domain.IsValidationError(err))
}

func TestAccountService_Create_BaseURLValidation(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
		wantErr string
	}{
		{"relative path", "api.openai.com/v1", "must be an absolute URL"},
		{"empty scheme", "//api.openai.com", "must be an absolute URL"},
		{"ftp scheme", "ftp://api.openai.com", "scheme must be http or https"},
		{"no host", "https://", "host is required"},
		{"garbage", "not a url at all %%%%", "invalid URL"},
		{"with query", "https://api.openai.com/?debug=1", "must not contain query string or fragment"},
		{"with fragment", "https://api.openai.com#x", "must not contain query string or fragment"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newTestService()
			bu := tc.baseURL
			_, err := svc.Create(context.Background(), "t", "openai", "sk-key", &bu, nil)
			require.Error(t, err)
			assert.True(t, domain.IsValidationError(err))
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestAccountService_Create_BaseURL_Accepted(t *testing.T) {
	svc, _ := newTestService()
	// Trailing slash is tolerated (we normalise in the proxy with TrimRight).
	for _, good := range []string{"https://api.openai.com", "http://10.0.0.1:8080", "https://host:443", "https://api.example.com/", "https://api.openai.com/v1", "https://api.example.com/foo/bar"} {
		bu := good
		_, err := svc.Create(context.Background(), "t-"+good, "openai", "sk-key", &bu, nil)
		require.NoError(t, err, good)
	}
}

func TestAccountService_Create_AnthropicRejectedInMVP(t *testing.T) {
	svc, _ := newTestService()
	_, err := svc.Create(context.Background(), "test", domain.ProviderAnthropic, "sk-key", nil, nil)
	require.Error(t, err)
	assert.True(t, domain.IsValidationError(err))
	assert.Contains(t, err.Error(), "MVP supports openai only")
}

type createFailsAccountRepo struct {
	*inMemoryAccountRepo
}

func (createFailsAccountRepo) Create(_ context.Context, _ *domain.UpstreamAccount) error {
	return errors.New("create failed")
}

func TestAccountService_Create_RepoError(t *testing.T) {
	repo := &createFailsAccountRepo{inMemoryAccountRepo: newInMemoryAccountRepo()}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	svc := NewAccountService(repo, logger)

	_, err := svc.Create(context.Background(), "test", "openai", "sk-key", nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create account")
}

func TestAccountService_Enable_InvalidFromStatus(t *testing.T) {
	svc, repo := newTestService()
	acct, _ := svc.Create(context.Background(), "test", "openai", "sk-key", nil, nil)
	repo.accounts[acct.ID].Status = "pending"

	err := svc.Enable(context.Background(), acct.ID)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrInvalidTransition)
}

type enableUpdateFailsAccountRepo struct {
	*inMemoryAccountRepo
}

func (r *enableUpdateFailsAccountRepo) UpdateStatus(ctx context.Context, id int64, status string) error {
	if status == domain.AccountStatusActive {
		return errors.New("update failed")
	}
	return r.inMemoryAccountRepo.UpdateStatus(ctx, id, status)
}

func TestAccountService_Enable_UpdateStatusError(t *testing.T) {
	inner := newInMemoryAccountRepo()
	repo := &enableUpdateFailsAccountRepo{inMemoryAccountRepo: inner}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	svc := NewAccountService(repo, logger)

	acct, err := svc.Create(context.Background(), "test", "openai", "sk-key", nil, nil)
	require.NoError(t, err)
	repo.accounts[acct.ID].Status = domain.AccountStatusDisabled

	err = svc.Enable(context.Background(), acct.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "enable account")
}

func TestAccountService_Disable_NotFound(t *testing.T) {
	svc, _ := newTestService()
	err := svc.Disable(context.Background(), 999)
	assert.ErrorIs(t, err, domain.ErrAccountNotFound)
}

type disableUpdateFailsAccountRepo struct {
	*inMemoryAccountRepo
}

func (r *disableUpdateFailsAccountRepo) UpdateStatus(ctx context.Context, id int64, status string) error {
	if status == domain.AccountStatusDisabled {
		return errors.New("update failed")
	}
	return r.inMemoryAccountRepo.UpdateStatus(ctx, id, status)
}

func TestAccountService_Disable_UpdateStatusError(t *testing.T) {
	inner := newInMemoryAccountRepo()
	repo := &disableUpdateFailsAccountRepo{inMemoryAccountRepo: inner}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	svc := NewAccountService(repo, logger)

	acct, err := svc.Create(context.Background(), "test", "openai", "sk-key", nil, nil)
	require.NoError(t, err)

	err = svc.Disable(context.Background(), acct.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disable account")
}

func TestAccountService_Delete_NotFound(t *testing.T) {
	svc, _ := newTestService()
	err := svc.Delete(context.Background(), 999)
	assert.ErrorIs(t, err, domain.ErrAccountNotFound)
}

func TestAccountService_UpdateDetails_Success(t *testing.T) {
	svc, _ := newTestService()
	acct, err := svc.Create(context.Background(), "before", "openai", "sk-old", nil, []string{"op.openai.responses"})
	require.NoError(t, err)

	name := "中文名称"
	key := "sk-new"
	base := "https://api.openai.com"
	updated, err := svc.UpdateDetails(context.Background(), acct.ID, AccountDetailsPatch{
		Name:         &name,
		APIKey:       &key,
		BaseURL:      &base,
		Capabilities: []string{"op.openai.chat_completions"},
	})
	require.NoError(t, err)
	assert.Equal(t, "中文名称", updated.Name)
	assert.Equal(t, "sk-new", updated.APIKey)
	require.NotNil(t, updated.BaseURL)
	assert.Equal(t, "https://api.openai.com", *updated.BaseURL)
	assert.Equal(t, []string{"op.openai.chat_completions"}, updated.Capabilities)
}

func TestAccountService_UpdateDetails_TrimsNameAndKey(t *testing.T) {
	svc, _ := newTestService()
	acct, err := svc.Create(context.Background(), "before", "openai", "sk-old", nil, nil)
	require.NoError(t, err)

	name := "  padded name  "
	key := " sk-key-2 "
	updated, err := svc.UpdateDetails(context.Background(), acct.ID, AccountDetailsPatch{
		Name:   &name,
		APIKey: &key,
	})
	require.NoError(t, err)
	assert.Equal(t, "padded name", updated.Name)
	assert.Equal(t, "sk-key-2", updated.APIKey)
}

func TestAccountService_UpdateDetails_BlankKeyKeepsExisting(t *testing.T) {
	svc, _ := newTestService()
	acct, err := svc.Create(context.Background(), "before", "openai", "sk-keep", nil, nil)
	require.NoError(t, err)

	blank := "   "
	updated, err := svc.UpdateDetails(context.Background(), acct.ID, AccountDetailsPatch{APIKey: &blank})
	require.NoError(t, err)
	assert.Equal(t, "sk-keep", updated.APIKey, "blank api_key must keep the existing key")
}

func TestAccountService_UpdateDetails_ValidationErrors(t *testing.T) {
	svc, _ := newTestService()
	acct, err := svc.Create(context.Background(), "before", "openai", "sk-old", nil, nil)
	require.NoError(t, err)

	t.Run("control-character name", func(t *testing.T) {
		bad := "ctrl\x01name"
		_, err := svc.UpdateDetails(context.Background(), acct.ID, AccountDetailsPatch{Name: &bad})
		require.Error(t, err)
		assert.True(t, domain.IsValidationError(err))
		assert.Contains(t, err.Error(), "name")
	})

	t.Run("blank name", func(t *testing.T) {
		bad := "   "
		_, err := svc.UpdateDetails(context.Background(), acct.ID, AccountDetailsPatch{Name: &bad})
		require.Error(t, err)
		assert.True(t, domain.IsValidationError(err))
	})

	t.Run("over-length key", func(t *testing.T) {
		long := "sk-" + strings.Repeat("x", 300)
		_, err := svc.UpdateDetails(context.Background(), acct.ID, AccountDetailsPatch{APIKey: &long})
		require.Error(t, err)
		assert.True(t, domain.IsValidationError(err))
	})

	t.Run("invalid base_url", func(t *testing.T) {
		bad := "not-a-url"
		_, err := svc.UpdateDetails(context.Background(), acct.ID, AccountDetailsPatch{BaseURL: &bad})
		require.Error(t, err)
		assert.True(t, domain.IsValidationError(err))
	})

	t.Run("unknown capability", func(t *testing.T) {
		_, err := svc.UpdateDetails(context.Background(), acct.ID, AccountDetailsPatch{
			Capabilities: []string{"op.openai.foo"},
		})
		require.Error(t, err)
		assert.True(t, domain.IsValidationError(err))
	})
}

func TestAccountService_UpdateDetails_NotFound(t *testing.T) {
	svc, _ := newTestService()
	name := "x"
	_, err := svc.UpdateDetails(context.Background(), 999, AccountDetailsPatch{Name: &name})
	assert.ErrorIs(t, err, domain.ErrAccountNotFound)
}

type recordingModelRepo struct {
	mu  sync.Mutex
	got map[int64][]string
	err error
}

func (r *recordingModelRepo) ReplaceUpstreamModels(_ context.Context, accountID int64, modelIDs []string) (int, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got[accountID] = append([]string(nil), modelIDs...)
	return len(modelIDs), 0, r.err
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestAccountService_Create_AutoRefreshesModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/models", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"gpt-4o"},{"id":"gpt-5.6-luna"}]}`))
	}))
	t.Cleanup(srv.Close)

	baseURL := srv.URL + "/v1"
	refresher := NewModelRefresher(&http.Client{Timeout: 2 * time.Second}, "", "", discardLogger())
	modelRepo := &recordingModelRepo{got: map[int64][]string{}}
	svc := NewAccountService(newInMemoryAccountRepo(), discardLogger())
	svc.SetModelRefresher(refresher, modelRepo)

	acct, err := svc.Create(context.Background(), "auto-refresh", "openai", "sk-test", &baseURL, nil)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		modelRepo.mu.Lock()
		defer modelRepo.mu.Unlock()
		return len(modelRepo.got[acct.ID]) == 2
	}, 3*time.Second, 50*time.Millisecond)
	assert.Equal(t, []string{"gpt-4o", "gpt-5.6-luna"}, modelRepo.got[acct.ID])
}

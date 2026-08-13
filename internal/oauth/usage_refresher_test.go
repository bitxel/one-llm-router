package oauth

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/provider/openai"
	"github.com/user/one-llm-router/internal/store"
)

type usageRefresherRecorder struct {
	snapshot store.UsageSnapshot
}

func (s *usageRefresherRecorder) InsertUpstreamAccount(context.Context, *domain.UpstreamAccount) (int64, error) {
	return 0, nil
}

func (s *usageRefresherRecorder) GetByID(context.Context, int64) (*domain.UpstreamAccount, error) {
	return nil, domain.ErrAccountNotFound
}

func (s *usageRefresherRecorder) UpdateCredentials(context.Context, int64, store.CredentialPatch) error {
	return nil
}

func (s *usageRefresherRecorder) UpdateStatusIfCurrent(context.Context, int64, string, time.Time) error {
	return nil
}

func (s *usageRefresherRecorder) UpdateUsage(_ context.Context, _ int64, snapshot store.UsageSnapshot) error {
	s.snapshot = snapshot
	return nil
}

func (s *usageRefresherRecorder) ListActive(context.Context) ([]domain.UpstreamAccount, error) {
	return nil, nil
}

func TestUsageRefresherRefreshOnePersistsWindowDeadline(t *testing.T) {
	// Real ChatGPT backend /wham/usage payload shape (limit_window_seconds
	// and reset_at are the authoritative window deadline fields).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/wham/usage", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"user_id":"user-1",
			"rate_limit":{
				"primary_window":{"used_percent":28,"limit_window_seconds":7200,"reset_at":1755123456},
				"secondary_window":{"used_percent":10,"limit_window_seconds":86400,"reset_at":1755163056}
			}
		}`))
	}))
	t.Cleanup(srv.Close)

	now := time.Date(2026, 4, 21, 16, 0, 0, 0, time.UTC)
	acc := &domain.UpstreamAccount{
		ID:              7,
		Name:            "deadline-oauth",
		Provider:        domain.ProviderOpenAI,
		Status:          domain.AccountStatusActive,
		AuthMethod:      domain.AuthMethodOAuthBrowser,
		AccessToken:     []byte("access-token"),
		RefreshToken:    []byte("refresh-token"),
		IDToken:         []byte("id-token"),
		LastRefresh:     &now,
		AccessExpiresAt: ptrTime(now.Add(2 * time.Hour)),
	}
	require.NoError(t, acc.Validate())

	coord := NewCoordinatorWithClock(NewFakeClock(now), &openAIProvider{}, newTestLogger(&bytes.Buffer{}, slog.LevelError))
	storeRec := &usageRefresherRecorder{}

	client := openai.NewClient(30 * time.Second)
	client.SetCodexBackendBaseURLForTest(srv.URL)

	refresher := NewUsageRefresher(storeRec, client, coord, newTestLogger(&bytes.Buffer{}, slog.LevelError), time.Hour)
	refresher.refreshOne(context.Background(), *acc)

	require.NotNil(t, storeRec.snapshot.PrimaryUsedPercent)
	assert.Equal(t, 28.0, *storeRec.snapshot.PrimaryUsedPercent)
	require.NotNil(t, storeRec.snapshot.PrimaryResetAt)
	assert.Equal(t, time.Unix(1755123456, 0).UTC(), *storeRec.snapshot.PrimaryResetAt)
	require.NotNil(t, storeRec.snapshot.PrimaryWindowSeconds)
	assert.Equal(t, int64(7200), *storeRec.snapshot.PrimaryWindowSeconds)

	require.NotNil(t, storeRec.snapshot.SecondaryUsedPercent)
	assert.Equal(t, 10.0, *storeRec.snapshot.SecondaryUsedPercent)
	require.NotNil(t, storeRec.snapshot.SecondaryResetAt)
	assert.Equal(t, time.Unix(1755163056, 0).UTC(), *storeRec.snapshot.SecondaryResetAt)
	require.NotNil(t, storeRec.snapshot.SecondaryWindowSeconds)
	assert.Equal(t, int64(86400), *storeRec.snapshot.SecondaryWindowSeconds)
}

func TestUsageRefresherSparsePayloadKeepsWindowsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rate_limit":{"primary_window":{"used_percent":28},"secondary_window":null}}`))
	}))
	t.Cleanup(srv.Close)

	now := time.Date(2026, 4, 21, 16, 0, 0, 0, time.UTC)
	acc := &domain.UpstreamAccount{
		ID:              8,
		Name:            "sparse-oauth",
		Provider:        domain.ProviderOpenAI,
		Status:          domain.AccountStatusActive,
		AuthMethod:      domain.AuthMethodOAuthDevice,
		AccessToken:     []byte("access-token"),
		RefreshToken:    []byte("refresh-token"),
		IDToken:         []byte("id-token"),
		LastRefresh:     &now,
		AccessExpiresAt: ptrTime(now.Add(2 * time.Hour)),
	}
	require.NoError(t, acc.Validate())

	coord := NewCoordinatorWithClock(NewFakeClock(now), &openAIProvider{}, newTestLogger(&bytes.Buffer{}, slog.LevelError))
	storeRec := &usageRefresherRecorder{}

	client := openai.NewClient(30 * time.Second)
	client.SetCodexBackendBaseURLForTest(srv.URL)

	refresher := NewUsageRefresher(storeRec, client, coord, newTestLogger(&bytes.Buffer{}, slog.LevelError), time.Hour)
	refresher.refreshOne(context.Background(), *acc)

	require.NotNil(t, storeRec.snapshot.PrimaryUsedPercent)
	assert.Equal(t, 28.0, *storeRec.snapshot.PrimaryUsedPercent)
	assert.Nil(t, storeRec.snapshot.PrimaryResetAt)
	assert.Nil(t, storeRec.snapshot.PrimaryWindowSeconds)
	assert.Nil(t, storeRec.snapshot.SecondaryUsedPercent)
	assert.Nil(t, storeRec.snapshot.SecondaryResetAt)
	assert.Nil(t, storeRec.snapshot.SecondaryWindowSeconds)
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

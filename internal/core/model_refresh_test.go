package core

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/one-llm-router/internal/domain"
)

// TestModelRefresher_TriggerAsync covers the shared fire-and-forget
// hook every account-creation path delegates to.
func TestModelRefresher_TriggerAsync(t *testing.T) {
	t.Run("nil receiver, repo, or account are no-ops", func(t *testing.T) {
		var upstreamHits atomic.Int64
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			upstreamHits.Add(1)
		}))
		t.Cleanup(upstream.Close)
		baseURL := upstream.URL

		var nilRefresher *ModelRefresher
		repo := &recordingModelRepo{got: map[int64][]string{}}

		require.NotPanics(t, func() {
			nilRefresher.TriggerAsync(&domain.UpstreamAccount{ID: 1, Status: domain.AccountStatusActive, BaseURL: &baseURL}, repo)
		})

		refresher := NewModelRefresher(&http.Client{Timeout: 2 * time.Second}, nil, "", "", discardLogger())
		require.NotPanics(t, func() {
			refresher.TriggerAsync(&domain.UpstreamAccount{ID: 2, Status: domain.AccountStatusActive, BaseURL: &baseURL}, nil)
			refresher.TriggerAsync(nil, repo)
			refresher.TriggerAsync(&domain.UpstreamAccount{ID: 3, Status: domain.AccountStatusDisabled, BaseURL: &baseURL}, repo)
		})
		// Allow a wrongly-fired goroutine to land before asserting.
		time.Sleep(50 * time.Millisecond)
		assert.Zero(t, upstreamHits.Load(), "a no-op path must never dial upstream")
		assert.Empty(t, repo.got)
	})

	t.Run("active account fetches and persists upstream models", func(t *testing.T) {
		var hits atomic.Int64
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			assert.Equal(t, "/v1/models", r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"gpt-4o"},{"id":"gpt-5.6-luna"}]}`))
		}))
		t.Cleanup(srv.Close)

		baseURL := srv.URL + "/v1"
		repo := &recordingModelRepo{got: map[int64][]string{}}
		refresher := NewModelRefresher(&http.Client{Timeout: 2 * time.Second}, nil, "", "", discardLogger())
		acct := &domain.UpstreamAccount{
			ID:         9,
			Name:       "triggered",
			Provider:   domain.ProviderOpenAI,
			APIKey:     "sk-test",
			BaseURL:    &baseURL,
			Status:     domain.AccountStatusActive,
			AuthMethod: domain.AuthMethodAPIKey,
		}

		refresher.TriggerAsync(acct, repo)

		require.Eventually(t, func() bool {
			repo.mu.Lock()
			defer repo.mu.Unlock()
			return len(repo.got[acct.ID]) == 2
		}, 3*time.Second, 20*time.Millisecond)
		assert.Equal(t, int64(1), hits.Load())
		assert.Equal(t, []string{"gpt-4o", "gpt-5.6-luna"}, repo.got[acct.ID])
	})

	t.Run("upstream failure only logs, never panics", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		t.Cleanup(srv.Close)

		baseURL := srv.URL
		repo := &recordingModelRepo{got: map[int64][]string{}}
		refresher := NewModelRefresher(&http.Client{Timeout: 2 * time.Second}, nil, "", "", discardLogger())
		acct := &domain.UpstreamAccount{
			ID:         3,
			Provider:   domain.ProviderOpenAI,
			APIKey:     "sk-test",
			BaseURL:    &baseURL,
			Status:     domain.AccountStatusActive,
			AuthMethod: domain.AuthMethodAPIKey,
		}

		require.NotPanics(t, func() { refresher.TriggerAsync(acct, repo) })
		time.Sleep(100 * time.Millisecond)
		assert.Empty(t, repo.got)
	})
}

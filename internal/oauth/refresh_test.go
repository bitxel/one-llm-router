package oauth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/requestid"
	"github.com/user/one-llm-router/internal/store"
)

func TestRefresh(t *testing.T) {
	t.Run("api key fast path", func(t *testing.T) {
		provider := &refreshProvider{}
		coord := NewCoordinatorWithClock(
			NewFakeClock(time.Date(2026, 4, 22, 9, 0, 0, 0, time.UTC)),
			provider,
			newTestLogger(io.Discard, slog.LevelInfo),
		)

		acct := &domain.UpstreamAccount{
			ID:         11,
			Name:       "api",
			Provider:   domain.ProviderOpenAI,
			APIKey:     "sk-live-key",
			Status:     domain.AccountStatusActive,
			AuthMethod: domain.AuthMethodAPIKey,
		}

		token, usedFallback, err := coord.RefreshIfStale(context.Background(), acct)
		require.NoError(t, err)
		assert.False(t, usedFallback)
		assert.Equal(t, []byte("sk-live-key"), token)
		assert.Equal(t, int32(0), provider.refreshCalls.Load())
	})

	t.Run("empty auth method fails fast", func(t *testing.T) {
		provider := &refreshProvider{}
		coord := NewCoordinatorWithClock(
			NewFakeClock(time.Date(2026, 4, 22, 9, 0, 0, 0, time.UTC)),
			provider,
			newTestLogger(io.Discard, slog.LevelInfo),
		)

		acct := &domain.UpstreamAccount{
			ID:         110,
			Name:       "legacy",
			Provider:   domain.ProviderOpenAI,
			APIKey:     "sk-legacy",
			Status:     domain.AccountStatusActive,
			AuthMethod: "",
		}

		token, usedFallback, err := coord.RefreshIfStale(context.Background(), acct)
		require.ErrorIs(t, err, domain.ErrUnknownAuthMethod)
		assert.Nil(t, token)
		assert.False(t, usedFallback)
		assert.Equal(t, int32(0), provider.refreshCalls.Load())
	})

	t.Run("fresh oauth fast path returns cloned access token", func(t *testing.T) {
		now := time.Date(2026, 4, 22, 9, 30, 0, 0, time.UTC)
		lastRefresh := now.Add(-20 * time.Minute)
		accessExpiresAt := lastRefresh.Add(time.Hour)
		provider := &refreshProvider{}
		coord := NewCoordinatorWithClock(NewFakeClock(now), provider, newTestLogger(io.Discard, slog.LevelInfo))

		acct := &domain.UpstreamAccount{
			ID:              12,
			Name:            "fresh@example.com",
			Provider:        domain.ProviderOpenAI,
			Status:          domain.AccountStatusActive,
			AuthMethod:      domain.AuthMethodOAuthImport,
			AccessToken:     []byte("fresh-access"),
			RefreshToken:    []byte("fresh-refresh"),
			IDToken:         mustJWT(t, map[string]any{"email": "fresh@example.com"}),
			LastRefresh:     &lastRefresh,
			AccessExpiresAt: &accessExpiresAt,
		}

		token, usedFallback, err := coord.RefreshIfStale(context.Background(), acct)
		require.NoError(t, err)
		assert.False(t, usedFallback)
		assert.Equal(t, []byte("fresh-access"), token)
		assert.Equal(t, int32(0), provider.refreshCalls.Load())

		token[0] = 'X'
		assert.Equal(t, []byte("fresh-access"), acct.AccessToken)
	})

	t.Run("half-life boundary refreshes only after threshold", func(t *testing.T) {
		lastRefresh := time.Date(2026, 4, 22, 9, 0, 0, 0, time.UTC)
		accessExpiresAt := lastRefresh.Add(time.Hour)
		provider := &refreshProvider{
			responses: []refreshProviderResponse{{
				tokens: Tokens{
					AccessToken:  []byte("boundary-new-access"),
					RefreshToken: []byte("boundary-new-refresh"),
					IDToken:      mustJWT(t, map[string]any{"email": "boundary@example.com"}),
					ExpiresIn:    45 * time.Minute,
				},
			}},
		}
		acct := &domain.UpstreamAccount{
			ID:              13,
			Name:            "boundary@example.com",
			Provider:        domain.ProviderOpenAI,
			Status:          domain.AccountStatusActive,
			AuthMethod:      domain.AuthMethodOAuthImport,
			AccessToken:     []byte("boundary-access"),
			RefreshToken:    []byte("boundary-refresh"),
			IDToken:         mustJWT(t, map[string]any{"email": "boundary@example.com"}),
			LastRefresh:     &lastRefresh,
			AccessExpiresAt: &accessExpiresAt,
		}

		threshold := lastRefresh.Add(accessExpiresAt.Sub(lastRefresh) / 2)
		coordAtThreshold := NewCoordinatorWithClock(NewFakeClock(threshold), provider, newTestLogger(io.Discard, slog.LevelInfo))
		token, usedFallback, err := coordAtThreshold.RefreshIfStale(context.Background(), acct)
		require.NoError(t, err)
		assert.False(t, usedFallback)
		assert.Equal(t, []byte("boundary-access"), token)
		assert.Equal(t, int32(0), provider.refreshCalls.Load())

		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()
		storedAcct := insertRefreshAccount(t, repo, refreshAccountFixture{
			name:            "boundary@example.com",
			authMethod:      domain.AuthMethodOAuthImport,
			accessToken:     "boundary-access",
			refreshToken:    "boundary-refresh",
			idToken:         mustJWT(t, map[string]any{"email": "boundary@example.com"}),
			lastRefresh:     lastRefresh,
			accessExpiresAt: accessExpiresAt,
		})
		coordAfterThreshold := NewCoordinatorWithClock(NewFakeClock(threshold.Add(time.Nanosecond)), provider, newTestLogger(io.Discard, slog.LevelInfo))
		coordAfterThreshold.SetAccountStore(repo)

		token, usedFallback, err = coordAfterThreshold.RefreshIfStale(context.Background(), storedAcct)
		require.NoError(t, err)
		assert.False(t, usedFallback)
		assert.Equal(t, []byte("boundary-new-access"), token)
		assert.Equal(t, int32(1), provider.refreshCalls.Load())
	})

	t.Run("singleflight dedupes distinct account snapshots by shared id", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		now := time.Date(2026, 4, 22, 9, 45, 0, 0, time.UTC)
		acct := insertRefreshAccount(t, repo, refreshAccountFixture{
			name:            "singleflight-id@example.com",
			authMethod:      domain.AuthMethodOAuthBrowser,
			accessToken:     "singleflight-stale-access",
			refreshToken:    "singleflight-stale-refresh",
			idToken:         mustJWT(t, map[string]any{"email": "singleflight-id@example.com"}),
			lastRefresh:     now.Add(-80 * time.Minute),
			accessExpiresAt: now.Add(-20 * time.Minute),
		})

		provider := &refreshProvider{
			responses: []refreshProviderResponse{{
				tokens: Tokens{
					AccessToken:  []byte("singleflight-new-access"),
					RefreshToken: []byte("singleflight-new-refresh"),
					IDToken:      mustJWT(t, map[string]any{"email": "singleflight-id@example.com"}),
					ExpiresIn:    time.Hour,
				},
			}},
			refreshEntered:  make(chan struct{}, 1),
			blockRefresh:    make(chan struct{}),
			responseReady:   make(chan struct{}, 1),
			releaseResponse: make(chan struct{}),
		}
		coord := NewCoordinatorWithClock(NewFakeClock(now), provider, newTestLogger(io.Discard, slog.LevelInfo))
		coord.SetAccountStore(repo)

		results := make([]refreshCallResult, 2)
		var wg sync.WaitGroup
		snapshots := []*domain.UpstreamAccount{cloneRefreshAccount(acct), cloneRefreshAccount(acct)}
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, usedFallback, err := coord.RefreshIfStale(context.Background(), snapshots[0])
			results[0] = refreshCallResult{token: token, usedFallback: usedFallback, err: err}
		}()

		<-provider.refreshEntered
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, usedFallback, err := coord.RefreshIfStale(context.Background(), snapshots[1])
			results[1] = refreshCallResult{token: token, usedFallback: usedFallback, err: err}
		}()

		close(provider.blockRefresh)
		<-provider.responseReady
		time.Sleep(20 * time.Millisecond)
		close(provider.releaseResponse)
		wg.Wait()

		assert.Equal(t, int32(1), provider.refreshCalls.Load())
		for _, result := range results {
			require.NoError(t, result.err)
			assert.False(t, result.usedFallback)
			assert.Equal(t, []byte("singleflight-new-access"), result.token)
		}
	})

	t.Run("stale oauth refresh persists new tokens and metadata", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		now := time.Date(2026, 4, 22, 10, 0, 0, 0, time.UTC)
		acct := insertRefreshAccount(t, repo, refreshAccountFixture{
			name:            "refresh-me@example.com",
			authMethod:      domain.AuthMethodOAuthImport,
			accessToken:     "old-access",
			refreshToken:    "old-refresh",
			idToken:         mustJWT(t, map[string]any{"email": "refresh-me@example.com"}),
			lastRefresh:     now.Add(-40 * time.Minute),
			accessExpiresAt: now.Add(20 * time.Minute),
		})

		provider := &refreshProvider{
			responses: []refreshProviderResponse{{
				tokens: Tokens{
					AccessToken:  []byte("new-access"),
					RefreshToken: []byte("new-refresh"),
					IDToken: mustJWT(t, map[string]any{
						"email": "new@example.com",
						"https://api.openai.com/auth": map[string]any{
							"plan_type":          "chatgpt-team",
							"chatgpt_account_id": "acct_new",
						},
					}),
					ExpiresIn: 2 * time.Hour,
				},
			}},
		}
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(NewFakeClock(now), provider, newTestLogger(&logs, slog.LevelInfo))
		coord.SetAccountStore(repo)

		token, usedFallback, err := coord.RefreshIfStale(requestid.WithContext(context.Background(), "req-refresh-ok"), acct)
		require.NoError(t, err)
		assert.False(t, usedFallback)
		assert.Equal(t, []byte("new-access"), token)
		assert.Equal(t, int32(1), provider.refreshCalls.Load())

		exported, err := repo.GetForExport(context.Background(), acct.ID)
		require.NoError(t, err)
		assert.Equal(t, []byte("new-access"), exported.AccessToken)
		assert.Equal(t, []byte("new-refresh"), exported.RefreshToken)
		require.NotNil(t, exported.LastRefresh)
		assert.Equal(t, now, exported.LastRefresh.UTC())

		projected, err := repo.GetProjectionByID(context.Background(), acct.ID)
		require.NoError(t, err)
		require.NotNil(t, projected.AccessExpiresAt)
		assert.Equal(t, now.Add(2*time.Hour), projected.AccessExpiresAt.UTC())
		require.NotNil(t, projected.Email)
		assert.Equal(t, "new@example.com", *projected.Email)
		require.NotNil(t, projected.PlanType)
		assert.Equal(t, "chatgpt-team", *projected.PlanType)
		require.NotNil(t, projected.ChatGPTAccountID)
		assert.Equal(t, "acct_new", *projected.ChatGPTAccountID)
		assert.Equal(t, domain.AuthMethodOAuthImport, projected.AuthMethod)

		assert.Equal(t, 1, countLogLinesWithAll(logs.String(), "oauth_refresh_started", "request_id=req-refresh-ok", fmt.Sprintf("account_id=%d", acct.ID)))
		assert.Equal(t, 1, countLogLinesWithAll(logs.String(), "oauth_refresh_ok", "request_id=req-refresh-ok", fmt.Sprintf("account_id=%d", acct.ID)))
		assert.Equal(t, 0, countLogLinesWithAll(logs.String(), "oauth_refresh_transient_fallback", fmt.Sprintf("account_id=%d", acct.ID)))
	})

	t.Run("permanent refresh failure disables account", func(t *testing.T) {
		testCases := []struct {
			name      string
			errorCode string
		}{
			{name: "invalid grant", errorCode: "invalid_grant"},
			{name: "account deactivated", errorCode: "account_deactivated"},
			{name: "invalid client", errorCode: "invalid_client"},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				repo, cleanup := setupOAuthConsumeStore(t)
				defer cleanup()

				now := time.Date(2026, 4, 22, 10, 30, 0, 0, time.UTC)
				acct := insertRefreshAccount(t, repo, refreshAccountFixture{
					name:            "disabled@example.com",
					authMethod:      domain.AuthMethodOAuthBrowser,
					accessToken:     "old-access",
					refreshToken:    "old-refresh",
					idToken:         mustJWT(t, map[string]any{"email": "disabled@example.com"}),
					lastRefresh:     now.Add(-50 * time.Minute),
					accessExpiresAt: now.Add(10 * time.Minute),
				})

				provider := &refreshProvider{
					responses: []refreshProviderResponse{{
						err: &TokenExchangeError{
							code:       tc.errorCode,
							message:    "refresh permanently rejected",
							httpStatus: 400,
						},
					}},
				}
				var logs bytes.Buffer
				coord := NewCoordinatorWithClock(NewFakeClock(now), provider, newTestLogger(&logs, slog.LevelInfo))
				coord.SetAccountStore(repo)

				token, usedFallback, err := coord.RefreshIfStale(requestid.WithContext(context.Background(), "req-refresh-perm"), acct)
				require.Error(t, err)
				assert.Nil(t, token)
				assert.False(t, usedFallback)
				assert.Equal(t, int32(1), provider.refreshCalls.Load())

				stored, err := repo.GetByID(context.Background(), acct.ID)
				require.NoError(t, err)
				assert.Equal(t, domain.AccountStatusDisabled, stored.Status)
				assert.Equal(t, 1, countLogLinesWithAll(logs.String(), "oauth_refresh_failed", "request_id=req-refresh-perm", "error_code="+tc.errorCode))
				assert.Equal(t, 0, countLogLinesWithAll(logs.String(), "oauth_refresh_transient_fallback", fmt.Sprintf("account_id=%d", acct.ID)))
				assert.Equal(t, 0, countLogLinesWithAll(logs.String(), "oauth_refresh_ok", fmt.Sprintf("account_id=%d", acct.ID)))
			})
		}
	})

	t.Run("transient refresh error falls back without disabling account", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		now := time.Date(2026, 4, 22, 11, 0, 0, 0, time.UTC)
		lastRefresh := now.Add(-90 * time.Minute)
		accessExpiresAt := lastRefresh.Add(time.Hour)
		acct := insertRefreshAccount(t, repo, refreshAccountFixture{
			name:            "fallback@example.com",
			authMethod:      domain.AuthMethodOAuthImport,
			accessToken:     "stale-access",
			refreshToken:    "stale-refresh",
			idToken:         mustJWT(t, map[string]any{"email": "fallback@example.com"}),
			lastRefresh:     lastRefresh,
			accessExpiresAt: accessExpiresAt,
		})

		provider := &refreshProvider{
			responses: []refreshProviderResponse{{
				err: &TokenExchangeError{
					code:       "temporarily_unavailable",
					message:    "try later",
					httpStatus: 503,
				},
			}},
		}
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(NewFakeClock(now), provider, newTestLogger(&logs, slog.LevelInfo))
		coord.SetAccountStore(repo)

		token, usedFallback, err := coord.RefreshIfStale(requestid.WithContext(context.Background(), "req-refresh-transient"), acct)
		require.NoError(t, err)
		assert.True(t, usedFallback)
		assert.Equal(t, []byte("stale-access"), token)
		assert.Equal(t, int32(1), provider.refreshCalls.Load())

		stored, err := repo.GetByID(context.Background(), acct.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.AccountStatusActive, stored.Status)

		exported, err := repo.GetForExport(context.Background(), acct.ID)
		require.NoError(t, err)
		assert.Equal(t, []byte("stale-access"), exported.AccessToken)
		require.NotNil(t, exported.LastRefresh)
		assert.Equal(t, lastRefresh.UTC(), exported.LastRefresh.UTC())

		projected, err := repo.GetProjectionByID(context.Background(), acct.ID)
		require.NoError(t, err)
		require.NotNil(t, projected.AccessExpiresAt)
		assert.Equal(t, accessExpiresAt.UTC(), projected.AccessExpiresAt.UTC())

		assert.Equal(t, 1, countLogLinesWithAll(logs.String(), "oauth_refresh_transient_fallback", "request_id=req-refresh-transient", "error_code=temporarily_unavailable"))
		assert.Contains(t, logs.String(), "last_refresh_age_seconds=5400")
		assert.Equal(t, 0, countLogLinesWithAll(logs.String(), "oauth_refresh_ok", fmt.Sprintf("account_id=%d", acct.ID)))
		assert.Equal(t, 0, countLogLinesWithAll(logs.String(), "oauth_refresh_failed", fmt.Sprintf("account_id=%d", acct.ID)))
	})

	t.Run("provider timeout uses transient fallback with refresh_timeout code", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		now := time.Date(2026, 4, 22, 11, 15, 0, 0, time.UTC)
		acct := insertRefreshAccount(t, repo, refreshAccountFixture{
			name:            "timeout@example.com",
			authMethod:      domain.AuthMethodOAuthBrowser,
			accessToken:     "stale-access",
			refreshToken:    "stale-refresh",
			idToken:         mustJWT(t, map[string]any{"email": "timeout@example.com"}),
			lastRefresh:     now.Add(-95 * time.Minute),
			accessExpiresAt: now.Add(25 * time.Minute),
		})

		provider := &refreshProvider{
			responses: []refreshProviderResponse{{err: context.DeadlineExceeded}},
		}
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(NewFakeClock(now), provider, newTestLogger(&logs, slog.LevelInfo))
		coord.SetAccountStore(repo)

		token, usedFallback, err := coord.RefreshIfStale(requestid.WithContext(context.Background(), "req-refresh-timeout"), acct)
		require.NoError(t, err)
		assert.True(t, usedFallback)
		assert.Equal(t, []byte("stale-access"), token)
		assert.Equal(t, int32(1), provider.refreshCalls.Load())

		stored, err := repo.GetByID(context.Background(), acct.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.AccountStatusActive, stored.Status)

		exported, err := repo.GetForExport(context.Background(), acct.ID)
		require.NoError(t, err)
		assert.Equal(t, []byte("stale-access"), exported.AccessToken)
		require.NotNil(t, exported.LastRefresh)
		assert.Equal(t, acct.LastRefresh.UTC(), exported.LastRefresh.UTC())

		projected, err := repo.GetProjectionByID(context.Background(), acct.ID)
		require.NoError(t, err)
		require.NotNil(t, projected.AccessExpiresAt)
		assert.Equal(t, acct.AccessExpiresAt.UTC(), projected.AccessExpiresAt.UTC())

		assert.Equal(t, 1, countLogLinesWithAll(logs.String(), "oauth_refresh_transient_fallback", "error_code=refresh_timeout"))
		assert.Equal(t, 0, countLogLinesWithAll(logs.String(), "oauth_refresh_ok", fmt.Sprintf("account_id=%d", acct.ID)))
	})

	t.Run("malformed id token refresh response falls back without persisting broken tokens", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		now := time.Date(2026, 4, 22, 11, 20, 0, 0, time.UTC)
		acct := insertRefreshAccount(t, repo, refreshAccountFixture{
			name:            "malformed-id-token@example.com",
			authMethod:      domain.AuthMethodOAuthImport,
			accessToken:     "stale-access",
			refreshToken:    "stale-refresh",
			idToken:         mustJWT(t, map[string]any{"email": "malformed-id-token@example.com"}),
			lastRefresh:     now.Add(-90 * time.Minute),
			accessExpiresAt: now.Add(-30 * time.Minute),
		})

		provider := &refreshProvider{
			responses: []refreshProviderResponse{{
				tokens: Tokens{
					AccessToken:  []byte("broken-access"),
					RefreshToken: []byte("broken-refresh"),
					IDToken:      []byte("not-a-jwt"),
					ExpiresIn:    45 * time.Minute,
				},
			}},
		}
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(NewFakeClock(now), provider, newTestLogger(&logs, slog.LevelInfo))
		coord.SetAccountStore(repo)

		token, usedFallback, err := coord.RefreshIfStale(requestid.WithContext(context.Background(), "req-refresh-malformed-id-token"), acct)
		require.NoError(t, err)
		assert.True(t, usedFallback)
		assert.Equal(t, []byte("stale-access"), token)
		assert.Equal(t, int32(1), provider.refreshCalls.Load())

		stored, err := repo.GetByID(context.Background(), acct.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.AccountStatusActive, stored.Status)
		exported, err := repo.GetForExport(context.Background(), acct.ID)
		require.NoError(t, err)
		assert.Equal(t, []byte("stale-access"), exported.AccessToken)
		assert.Equal(t, []byte("stale-refresh"), exported.RefreshToken)
		assert.Equal(t, 1, countLogLinesWithAll(logs.String(), "oauth_refresh_transient_fallback", "error_code=invalid_response"))
		assert.Equal(t, 0, countLogLinesWithAll(logs.String(), "oauth_refresh_failed", fmt.Sprintf("account_id=%d", acct.ID)))
	})

	t.Run("transient fallback retries on next call", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		now := time.Date(2026, 4, 22, 11, 30, 0, 0, time.UTC)
		acct := insertRefreshAccount(t, repo, refreshAccountFixture{
			name:            "retry@example.com",
			authMethod:      domain.AuthMethodOAuthImport,
			accessToken:     "stale-access",
			refreshToken:    "stale-refresh",
			idToken:         mustJWT(t, map[string]any{"email": "retry@example.com"}),
			lastRefresh:     now.Add(-2 * time.Hour),
			accessExpiresAt: now.Add(-time.Hour),
		})

		provider := &refreshProvider{
			responses: []refreshProviderResponse{
				{
					err: &TokenExchangeError{
						code:       "temporarily_unavailable",
						message:    "burst overloaded",
						httpStatus: 503,
					},
				},
				{
					tokens: Tokens{
						AccessToken:  []byte("refreshed-access"),
						RefreshToken: []byte("refreshed-refresh"),
						IDToken:      mustJWT(t, map[string]any{"email": "retry@example.com"}),
						ExpiresIn:    30 * time.Minute,
					},
				},
			},
		}
		coord := NewCoordinatorWithClock(NewFakeClock(now), provider, newTestLogger(io.Discard, slog.LevelInfo))
		coord.SetAccountStore(repo)

		token, usedFallback, err := coord.RefreshIfStale(context.Background(), acct)
		require.NoError(t, err)
		assert.True(t, usedFallback)
		assert.Equal(t, []byte("stale-access"), token)

		token, usedFallback, err = coord.RefreshIfStale(context.Background(), acct)
		require.NoError(t, err)
		assert.False(t, usedFallback)
		assert.Equal(t, []byte("refreshed-access"), token)
		assert.Equal(t, int32(2), provider.refreshCalls.Load())

		exported, err := repo.GetForExport(context.Background(), acct.ID)
		require.NoError(t, err)
		assert.Equal(t, []byte("refreshed-access"), exported.AccessToken)
	})

	t.Run("panic is surfaced as error and next request retries", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		now := time.Date(2026, 4, 22, 11, 45, 0, 0, time.UTC)
		acct := insertRefreshAccount(t, repo, refreshAccountFixture{
			name:            "panic@example.com",
			authMethod:      domain.AuthMethodOAuthBrowser,
			accessToken:     "panic-stale-access",
			refreshToken:    "panic-stale-refresh",
			idToken:         mustJWT(t, map[string]any{"email": "panic@example.com"}),
			lastRefresh:     now.Add(-2 * time.Hour),
			accessExpiresAt: now.Add(-time.Hour),
		})

		provider := &refreshProvider{
			responses: []refreshProviderResponse{
				{panicValue: "boom"},
				{
					tokens: Tokens{
						AccessToken:  []byte("panic-new-access"),
						RefreshToken: []byte("panic-new-refresh"),
						IDToken:      mustJWT(t, map[string]any{"email": "panic@example.com"}),
						ExpiresIn:    time.Hour,
					},
				},
			},
		}
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(NewFakeClock(now), provider, newTestLogger(&logs, slog.LevelInfo))
		coord.SetAccountStore(repo)

		token, usedFallback, err := coord.RefreshIfStale(requestid.WithContext(context.Background(), "req-refresh-panic"), acct)
		require.Error(t, err)
		assert.Nil(t, token)
		assert.False(t, usedFallback)
		assert.Contains(t, err.Error(), "panic")
		assert.Equal(t, 1, countLogLinesWithAll(logs.String(), "oauth_refresh_started", "request_id=req-refresh-panic"))
		assert.Equal(t, 0, countLogLinesWithAll(logs.String(), "oauth_refresh_transient_fallback", fmt.Sprintf("account_id=%d", acct.ID)))
		assert.Equal(t, 0, countLogLinesWithAll(logs.String(), "oauth_refresh_ok", fmt.Sprintf("account_id=%d", acct.ID)))

		token, usedFallback, err = coord.RefreshIfStale(requestid.WithContext(context.Background(), "req-refresh-panic-retry"), acct)
		require.NoError(t, err)
		assert.Equal(t, []byte("panic-new-access"), token)
		assert.False(t, usedFallback)
		assert.Equal(t, int32(2), provider.refreshCalls.Load())
	})

	t.Run("concurrent transient fallback logs once and returns stale token to all waiters", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		now := time.Date(2026, 4, 22, 11, 47, 0, 0, time.UTC)
		acct := insertRefreshAccount(t, repo, refreshAccountFixture{
			name:            "burst-fallback@example.com",
			authMethod:      domain.AuthMethodOAuthBrowser,
			accessToken:     "burst-stale-access",
			refreshToken:    "burst-stale-refresh",
			idToken:         mustJWT(t, map[string]any{"email": "burst-fallback@example.com"}),
			lastRefresh:     now.Add(-2 * time.Hour),
			accessExpiresAt: now.Add(-time.Hour),
		})

		provider := &refreshProvider{
			responses: []refreshProviderResponse{{
				err: &TokenExchangeError{
					code:       "temporarily_unavailable",
					message:    "retry later",
					httpStatus: 503,
				},
			}},
			refreshEntered:  make(chan struct{}, 1),
			blockRefresh:    make(chan struct{}),
			responseReady:   make(chan struct{}, 1),
			releaseResponse: make(chan struct{}),
		}
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(NewFakeClock(now), provider, newTestLogger(&logs, slog.LevelInfo))
		coord.SetAccountStore(repo)

		results, wg := runConcurrentRefreshCalls(coord, acct, 50, "req-burst-fallback")
		<-provider.refreshEntered
		close(provider.blockRefresh)
		<-provider.responseReady
		time.Sleep(20 * time.Millisecond)
		close(provider.releaseResponse)
		wg.Wait()

		assert.Equal(t, int32(1), provider.refreshCalls.Load())
		assert.Equal(t, 1, countLogLinesWithAll(logs.String(), "oauth_refresh_started", fmt.Sprintf("account_id=%d", acct.ID)))
		assert.Equal(t, 1, countLogLinesWithAll(logs.String(), "oauth_refresh_transient_fallback", fmt.Sprintf("account_id=%d", acct.ID), "error_code=temporarily_unavailable"))
		assert.Equal(t, 0, countLogLinesWithAll(logs.String(), "oauth_refresh_ok", fmt.Sprintf("account_id=%d", acct.ID)))

		for _, result := range results {
			require.NoError(t, result.err)
			assert.True(t, result.usedFallback)
			assert.Equal(t, []byte("burst-stale-access"), result.token)
		}
	})

	t.Run("concurrent refresh conflict retries once and returns credential-update winner", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		now := time.Date(2026, 4, 22, 11, 50, 0, 0, time.UTC)
		acct := insertRefreshAccount(t, repo, refreshAccountFixture{
			name:            "conflict@example.com",
			authMethod:      domain.AuthMethodOAuthImport,
			accessToken:     "conflict-stale-access",
			refreshToken:    "conflict-stale-refresh",
			idToken:         mustJWT(t, map[string]any{"email": "conflict@example.com"}),
			lastRefresh:     now.Add(-2 * time.Hour),
			accessExpiresAt: now.Add(-time.Hour),
		})

		raceStore := &refreshRaceStore{
			repo:                          repo,
			beforeConditionalCredential:   make(chan struct{}, 1),
			continueConditionalCredential: make(chan struct{}),
		}
		winnerRefreshAt := now
		winnerExpiresAt := now.Add(90 * time.Minute)
		winnerPatch := store.CredentialPatch{
			AuthMethod:       acct.AuthMethod,
			AccessToken:      []byte("credential-update-access"),
			RefreshToken:     []byte("credential-update-refresh"),
			IDToken:          mustJWT(t, map[string]any{"email": "conflict@example.com"}),
			LastRefresh:      &winnerRefreshAt,
			AccessExpiresAt:  &winnerExpiresAt,
			Email:            stringPointer("conflict@example.com"),
			PlanType:         stringPointer("chatgpt-plus"),
			ChatGPTAccountID: stringPointer("acct_conflict"),
		}
		provider := &refreshProvider{
			responses: []refreshProviderResponse{{
				tokens: Tokens{
					AccessToken:  []byte("refresh-access"),
					RefreshToken: []byte("refresh-refresh"),
					IDToken:      mustJWT(t, map[string]any{"email": "conflict@example.com"}),
					ExpiresIn:    2 * time.Hour,
				},
			}},
		}
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(NewFakeClock(now), provider, newTestLogger(&logs, slog.LevelInfo))
		coord.SetAccountStore(raceStore)

		type refreshOutcome struct {
			token        []byte
			usedFallback bool
			err          error
		}
		outcomeCh := make(chan refreshOutcome, 1)
		go func() {
			token, usedFallback, err := coord.RefreshIfStale(context.Background(), acct)
			outcomeCh <- refreshOutcome{token: token, usedFallback: usedFallback, err: err}
		}()

		<-raceStore.beforeConditionalCredential
		require.NoError(t, repo.UpdateCredentials(context.Background(), acct.ID, winnerPatch))
		close(raceStore.continueConditionalCredential)

		outcome := <-outcomeCh
		require.NoError(t, outcome.err)
		assert.Equal(t, []byte("credential-update-access"), outcome.token)
		assert.False(t, outcome.usedFallback)
		assert.Equal(t, int32(1), provider.refreshCalls.Load())
		assert.Equal(t, 1, countLogLinesWithAll(logs.String(), "oauth_refresh_conflict_retry", fmt.Sprintf("account_id=%d", acct.ID)))
		assert.Equal(t, 0, countLogLinesWithAll(logs.String(), "oauth_refresh_ok", fmt.Sprintf("account_id=%d", acct.ID)))
		assert.Equal(t, 0, countLogLinesWithAll(logs.String(), "oauth_refresh_failed", fmt.Sprintf("account_id=%d", acct.ID)))

		exported, readErr := repo.GetForExport(context.Background(), acct.ID)
		require.NoError(t, readErr)
		assert.Equal(t, []byte("credential-update-access"), exported.AccessToken)
		assert.Equal(t, []byte("credential-update-refresh"), exported.RefreshToken)
	})

	t.Run("permanent refresh conflict retries once and preserves credential-update winner", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		now := time.Date(2026, 4, 22, 11, 52, 0, 0, time.UTC)
		acct := insertRefreshAccount(t, repo, refreshAccountFixture{
			name:            "conflict-permanent@example.com",
			authMethod:      domain.AuthMethodOAuthImport,
			accessToken:     "conflict-permanent-access",
			refreshToken:    "conflict-permanent-refresh",
			idToken:         mustJWT(t, map[string]any{"email": "conflict-permanent@example.com"}),
			lastRefresh:     now.Add(-2 * time.Hour),
			accessExpiresAt: now.Add(-time.Hour),
		})

		raceStore := &refreshRaceStore{
			repo:                      repo,
			beforeConditionalStatus:   make(chan struct{}, 1),
			continueConditionalStatus: make(chan struct{}),
		}
		winnerRefreshAt := now
		winnerExpiresAt := now.Add(90 * time.Minute)
		winnerPatch := store.CredentialPatch{
			AuthMethod:       acct.AuthMethod,
			AccessToken:      []byte("credential-update-wins-access"),
			RefreshToken:     []byte("credential-update-wins-refresh"),
			IDToken:          mustJWT(t, map[string]any{"email": "conflict-permanent@example.com"}),
			LastRefresh:      &winnerRefreshAt,
			AccessExpiresAt:  &winnerExpiresAt,
			Email:            stringPointer("conflict-permanent@example.com"),
			PlanType:         stringPointer("chatgpt-team"),
			ChatGPTAccountID: stringPointer("acct_conflict_permanent"),
		}
		provider := &refreshProvider{
			responses: []refreshProviderResponse{{
				err: &TokenExchangeError{
					code:       "invalid_grant",
					message:    "refresh revoked",
					httpStatus: 400,
				},
			}},
		}
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(NewFakeClock(now), provider, newTestLogger(&logs, slog.LevelInfo))
		coord.SetAccountStore(raceStore)

		type refreshOutcome struct {
			token        []byte
			usedFallback bool
			err          error
		}
		outcomeCh := make(chan refreshOutcome, 1)
		go func() {
			token, usedFallback, err := coord.RefreshIfStale(context.Background(), acct)
			outcomeCh <- refreshOutcome{token: token, usedFallback: usedFallback, err: err}
		}()

		<-raceStore.beforeConditionalStatus
		require.NoError(t, repo.UpdateCredentials(context.Background(), acct.ID, winnerPatch))
		close(raceStore.continueConditionalStatus)

		outcome := <-outcomeCh
		require.NoError(t, outcome.err)
		assert.Equal(t, []byte("credential-update-wins-access"), outcome.token)
		assert.False(t, outcome.usedFallback)
		assert.Equal(t, int32(1), provider.refreshCalls.Load())
		assert.Equal(t, 1, countLogLinesWithAll(logs.String(), "oauth_refresh_conflict_retry", fmt.Sprintf("account_id=%d", acct.ID)))
		assert.Equal(t, 0, countLogLinesWithAll(logs.String(), "oauth_refresh_ok", fmt.Sprintf("account_id=%d", acct.ID)))
		assert.Equal(t, 0, countLogLinesWithAll(logs.String(), "oauth_refresh_failed", fmt.Sprintf("account_id=%d", acct.ID)))

		stored, err := repo.GetByID(context.Background(), acct.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.AccountStatusActive, stored.Status)
		exported, err := repo.GetForExport(context.Background(), acct.ID)
		require.NoError(t, err)
		assert.Equal(t, []byte("credential-update-wins-access"), exported.AccessToken)
	})

	t.Run("repeated refresh conflicts return the internal sentinel after one retry", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		now := time.Date(2026, 4, 22, 11, 53, 0, 0, time.UTC)
		acct := insertRefreshAccount(t, repo, refreshAccountFixture{
			name:            "double-conflict@example.com",
			authMethod:      domain.AuthMethodOAuthImport,
			accessToken:     "double-conflict-access",
			refreshToken:    "double-conflict-refresh",
			idToken:         mustJWT(t, map[string]any{"email": "double-conflict@example.com"}),
			lastRefresh:     now.Add(-2 * time.Hour),
			accessExpiresAt: now.Add(-time.Hour),
		})

		storeWithRepeatedConflicts := &refreshRepeatedConflictStore{
			repo: repo,
			patches: []store.CredentialPatch{
				{
					AuthMethod:       acct.AuthMethod,
					AccessToken:      []byte("conflict-one-access"),
					RefreshToken:     []byte("conflict-one-refresh"),
					IDToken:          mustJWT(t, map[string]any{"email": "double-conflict@example.com"}),
					LastRefresh:      timePointer(now.Add(-70 * time.Minute)),
					AccessExpiresAt:  timePointer(now.Add(-10 * time.Minute)),
					Email:            stringPointer("double-conflict@example.com"),
					PlanType:         stringPointer("chatgpt-plus"),
					ChatGPTAccountID: stringPointer("acct_conflict_one"),
				},
				{
					AuthMethod:       acct.AuthMethod,
					AccessToken:      []byte("conflict-two-access"),
					RefreshToken:     []byte("conflict-two-refresh"),
					IDToken:          mustJWT(t, map[string]any{"email": "double-conflict@example.com"}),
					LastRefresh:      timePointer(now.Add(-50 * time.Minute)),
					AccessExpiresAt:  timePointer(now.Add(-5 * time.Minute)),
					Email:            stringPointer("double-conflict@example.com"),
					PlanType:         stringPointer("chatgpt-team"),
					ChatGPTAccountID: stringPointer("acct_conflict_two"),
				},
			},
		}
		provider := &refreshProvider{
			responses: []refreshProviderResponse{
				{
					tokens: Tokens{
						AccessToken:  []byte("refresh-access-one"),
						RefreshToken: []byte("refresh-refresh-one"),
						IDToken:      mustJWT(t, map[string]any{"email": "double-conflict@example.com"}),
						ExpiresIn:    30 * time.Minute,
					},
				},
				{
					tokens: Tokens{
						AccessToken:  []byte("refresh-access-two"),
						RefreshToken: []byte("refresh-refresh-two"),
						IDToken:      mustJWT(t, map[string]any{"email": "double-conflict@example.com"}),
						ExpiresIn:    30 * time.Minute,
					},
				},
			},
		}
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(NewFakeClock(now), provider, newTestLogger(&logs, slog.LevelInfo))
		coord.SetAccountStore(storeWithRepeatedConflicts)

		token, usedFallback, err := coord.RefreshIfStale(context.Background(), acct)
		require.ErrorIs(t, err, errConcurrentRefreshConflict)
		assert.Nil(t, token)
		assert.False(t, usedFallback)
		assert.Equal(t, int32(2), provider.refreshCalls.Load())
		assert.Equal(t, 1, countLogLinesWithAll(logs.String(), "oauth_refresh_conflict_retry", fmt.Sprintf("account_id=%d", acct.ID)))
	})

	t.Run("refresh conflict reload of inactive row fails fast", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		now := time.Date(2026, 4, 22, 11, 54, 0, 0, time.UTC)
		acct := insertRefreshAccount(t, repo, refreshAccountFixture{
			name:            "inactive-reload@example.com",
			authMethod:      domain.AuthMethodOAuthBrowser,
			accessToken:     "inactive-reload-access",
			refreshToken:    "inactive-reload-refresh",
			idToken:         mustJWT(t, map[string]any{"email": "inactive-reload@example.com"}),
			lastRefresh:     now.Add(-2 * time.Hour),
			accessExpiresAt: now.Add(-time.Hour),
		})

		storeWithDeletedReload := &refreshDeletedReloadStore{
			repo: repo,
			patch: store.CredentialPatch{
				AuthMethod:       acct.AuthMethod,
				AccessToken:      []byte("inactive-reload-updated-access"),
				RefreshToken:     []byte("inactive-reload-updated-refresh"),
				IDToken:          mustJWT(t, map[string]any{"email": "inactive-reload@example.com"}),
				LastRefresh:      timePointer(now),
				AccessExpiresAt:  timePointer(now.Add(90 * time.Minute)),
				Email:            stringPointer("inactive-reload@example.com"),
				PlanType:         stringPointer("chatgpt-team"),
				ChatGPTAccountID: stringPointer("acct_inactive_reload"),
			},
		}
		provider := &refreshProvider{
			responses: []refreshProviderResponse{{
				tokens: Tokens{
					AccessToken:  []byte("refresh-access"),
					RefreshToken: []byte("refresh-refresh"),
					IDToken:      mustJWT(t, map[string]any{"email": "inactive-reload@example.com"}),
					ExpiresIn:    45 * time.Minute,
				},
			}},
		}
		coord := NewCoordinatorWithClock(NewFakeClock(now), provider, newTestLogger(io.Discard, slog.LevelInfo))
		coord.SetAccountStore(storeWithDeletedReload)

		token, usedFallback, err := coord.RefreshIfStale(context.Background(), acct)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `status "deleted" is not active`)
		assert.Nil(t, token)
		assert.False(t, usedFallback)
		assert.Equal(t, int32(1), provider.refreshCalls.Load())

		stored, err := repo.GetByID(context.Background(), acct.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.AccountStatusDeleted, stored.Status)
	})

	t.Run("panic during refresh burst fails all waiters and a later call retries", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		now := time.Date(2026, 4, 22, 11, 55, 0, 0, time.UTC)
		acct := insertRefreshAccount(t, repo, refreshAccountFixture{
			name:            "panic-burst@example.com",
			authMethod:      domain.AuthMethodOAuthBrowser,
			accessToken:     "panic-burst-stale-access",
			refreshToken:    "panic-burst-stale-refresh",
			idToken:         mustJWT(t, map[string]any{"email": "panic-burst@example.com"}),
			lastRefresh:     now.Add(-2 * time.Hour),
			accessExpiresAt: now.Add(-time.Hour),
		})

		provider := &refreshProvider{
			responses: []refreshProviderResponse{
				{panicValue: "boom"},
				{
					tokens: Tokens{
						AccessToken:  []byte("panic-burst-new-access"),
						RefreshToken: []byte("panic-burst-new-refresh"),
						IDToken:      mustJWT(t, map[string]any{"email": "panic-burst@example.com"}),
						ExpiresIn:    time.Hour,
					},
				},
			},
			refreshEntered:  make(chan struct{}, 1),
			blockRefresh:    make(chan struct{}),
			responseReady:   make(chan struct{}, 1),
			releaseResponse: make(chan struct{}),
		}
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(NewFakeClock(now), provider, newTestLogger(&logs, slog.LevelInfo))
		coord.SetAccountStore(repo)

		results, wg := runConcurrentRefreshCalls(coord, acct, 50, "req-panic-burst")
		<-provider.refreshEntered
		close(provider.blockRefresh)
		<-provider.responseReady
		time.Sleep(20 * time.Millisecond)
		close(provider.releaseResponse)
		wg.Wait()

		assert.Equal(t, int32(1), provider.refreshCalls.Load())
		assert.Equal(t, 1, countLogLinesWithAll(logs.String(), "oauth_refresh_started", fmt.Sprintf("account_id=%d", acct.ID)))
		assert.Equal(t, 0, countLogLinesWithAll(logs.String(), "oauth_refresh_transient_fallback", fmt.Sprintf("account_id=%d", acct.ID)))
		assert.Equal(t, 0, countLogLinesWithAll(logs.String(), "oauth_refresh_ok", fmt.Sprintf("account_id=%d", acct.ID)))
		for _, result := range results {
			require.Error(t, result.err)
			assert.Contains(t, result.err.Error(), "panic")
			assert.Nil(t, result.token)
			assert.False(t, result.usedFallback)
		}

		token, usedFallback, err := coord.RefreshIfStale(context.Background(), acct)
		require.NoError(t, err)
		assert.Equal(t, []byte("panic-burst-new-access"), token)
		assert.False(t, usedFallback)
		assert.Equal(t, int32(2), provider.refreshCalls.Load())
	})

	t.Run("fifty concurrent stale requests share one refresh burst", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
		acct := insertRefreshAccount(t, repo, refreshAccountFixture{
			name:            "burst@example.com",
			authMethod:      domain.AuthMethodOAuthBrowser,
			accessToken:     "burst-stale-access",
			refreshToken:    "burst-stale-refresh",
			idToken:         mustJWT(t, map[string]any{"email": "burst@example.com"}),
			lastRefresh:     now.Add(-80 * time.Minute),
			accessExpiresAt: now.Add(-20 * time.Minute),
		})

		provider := &refreshProvider{
			responses: []refreshProviderResponse{{
				tokens: Tokens{
					AccessToken:  []byte("burst-new-access"),
					RefreshToken: []byte("burst-new-refresh"),
					IDToken:      mustJWT(t, map[string]any{"email": "burst@example.com"}),
					ExpiresIn:    45 * time.Minute,
				},
			}},
			refreshEntered: make(chan struct{}, 1),
			blockRefresh:   make(chan struct{}),
		}
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(NewFakeClock(now), provider, newTestLogger(&logs, slog.LevelInfo))
		coord.SetAccountStore(repo)

		results, wg := runConcurrentRefreshCalls(coord, acct, 50, "req-burst")

		<-provider.refreshEntered
		close(provider.blockRefresh)
		wg.Wait()

		assert.Equal(t, int32(1), provider.refreshCalls.Load())
		assert.Equal(t, 1, countLogLinesWithAll(logs.String(), "oauth_refresh_started", fmt.Sprintf("account_id=%d", acct.ID)))
		assert.Equal(t, 1, countLogLinesWithAll(logs.String(), "oauth_refresh_ok", fmt.Sprintf("account_id=%d", acct.ID)))

		for _, result := range results {
			require.NoError(t, result.err)
			assert.False(t, result.usedFallback)
			assert.Equal(t, []byte("burst-new-access"), result.token)
		}
	})
}

type refreshCallResult struct {
	token        []byte
	usedFallback bool
	err          error
}

func runConcurrentRefreshCalls(coord *Coordinator, acct *domain.UpstreamAccount, count int, requestPrefix string) ([]refreshCallResult, *sync.WaitGroup) {
	results := make([]refreshCallResult, count)
	start := make(chan struct{})
	entered := make(chan struct{}, count)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			entered <- struct{}{}
			token, usedFallback, err := coord.RefreshIfStale(
				requestid.WithContext(context.Background(), fmt.Sprintf("%s-%d", requestPrefix, i)),
				acct,
			)
			results[i] = refreshCallResult{token: token, usedFallback: usedFallback, err: err}
		}(i)
	}
	close(start)
	for i := 0; i < count; i++ {
		<-entered
	}
	return results, &wg
}

func cloneRefreshAccount(acct *domain.UpstreamAccount) *domain.UpstreamAccount {
	if acct == nil {
		return nil
	}
	cloned := *acct
	cloned.AccessToken = cloneBytes(acct.AccessToken)
	cloned.RefreshToken = cloneBytes(acct.RefreshToken)
	cloned.IDToken = cloneBytes(acct.IDToken)
	cloned.BaseURL = cloneStringPointer(acct.BaseURL)
	cloned.Email = cloneStringPointer(acct.Email)
	cloned.PlanType = cloneStringPointer(acct.PlanType)
	cloned.ChatGPTAccountID = cloneStringPointer(acct.ChatGPTAccountID)
	cloned.LastRefresh = cloneTimePointer(acct.LastRefresh)
	cloned.AccessExpiresAt = cloneTimePointer(acct.AccessExpiresAt)
	return &cloned
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := value.UTC()
	return &cloned
}

type refreshProviderResponse struct {
	tokens     Tokens
	err        error
	panicValue any
}

type refreshProvider struct {
	responses       []refreshProviderResponse
	refreshEntered  chan struct{}
	blockRefresh    chan struct{}
	responseReady   chan struct{}
	releaseResponse chan struct{}
	refreshCalls    atomic.Int32

	mu  sync.Mutex
	idx int
}

func (*refreshProvider) BuildAuthorizeURL(string, string) (string, error) {
	return "", errors.New("not used in refresh tests")
}

func (*refreshProvider) ExchangeCode(context.Context, string, string) (Tokens, error) {
	return Tokens{}, errors.New("not used in refresh tests")
}

func (p *refreshProvider) Refresh(_ context.Context, _ []byte) (Tokens, error) {
	p.refreshCalls.Add(1)
	if p.refreshEntered != nil {
		select {
		case p.refreshEntered <- struct{}{}:
		default:
		}
	}
	if p.blockRefresh != nil {
		<-p.blockRefresh
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.idx >= len(p.responses) {
		return Tokens{}, errors.New("unexpected refresh call")
	}
	response := p.responses[p.idx]
	p.idx++
	if p.responseReady != nil {
		select {
		case p.responseReady <- struct{}{}:
		default:
		}
	}
	if p.releaseResponse != nil {
		<-p.releaseResponse
	}
	if response.panicValue != nil {
		panic(response.panicValue)
	}
	if response.err != nil {
		return Tokens{}, response.err
	}
	return response.tokens, nil
}

func (*refreshProvider) RequestDeviceCode(context.Context) (DeviceCode, error) {
	return DeviceCode{}, errors.New("not used in refresh tests")
}

func (*refreshProvider) PollDeviceCode(context.Context, string, string) (Tokens, error) {
	return Tokens{}, errors.New("not used in refresh tests")
}

type refreshAccountFixture struct {
	name            string
	authMethod      domain.AuthMethod
	accessToken     string
	refreshToken    string
	idToken         []byte
	lastRefresh     time.Time
	accessExpiresAt time.Time
}

func insertRefreshAccount(t *testing.T, repo *store.AccountRepo, fixture refreshAccountFixture) *domain.UpstreamAccount {
	t.Helper()

	baseURL := domain.ProviderDefaultURLs[domain.ProviderOpenAI]
	email := fixture.name
	acct := &domain.UpstreamAccount{
		Name:             fixture.name,
		Provider:         domain.ProviderOpenAI,
		BaseURL:          &baseURL,
		Status:           domain.AccountStatusActive,
		AuthMethod:       fixture.authMethod,
		AccessToken:      []byte(fixture.accessToken),
		RefreshToken:     []byte(fixture.refreshToken),
		IDToken:          fixture.idToken,
		LastRefresh:      &fixture.lastRefresh,
		AccessExpiresAt:  &fixture.accessExpiresAt,
		Email:            &email,
		PlanType:         stringPointer("chatgpt-plus"),
		ChatGPTAccountID: stringPointer("acct_initial"),
	}

	id, err := repo.InsertUpstreamAccount(context.Background(), acct)
	require.NoError(t, err)
	stored, err := repo.GetByID(context.Background(), id)
	require.NoError(t, err)
	return stored
}

type refreshRaceStore struct {
	repo *store.AccountRepo

	beforeConditionalCredential   chan struct{}
	continueConditionalCredential chan struct{}
	beforeConditionalStatus       chan struct{}
	continueConditionalStatus     chan struct{}
}

func (s *refreshRaceStore) InsertUpstreamAccount(ctx context.Context, account *domain.UpstreamAccount) (int64, error) {
	return s.repo.InsertUpstreamAccount(ctx, account)
}

func (s *refreshRaceStore) GetByID(ctx context.Context, id int64) (*domain.UpstreamAccount, error) {
	return s.repo.GetByID(ctx, id)
}

func (s *refreshRaceStore) UpdateCredentials(ctx context.Context, id int64, patch store.CredentialPatch) error {
	if patch.ExpectedLastRefresh != nil && s.beforeConditionalCredential != nil {
		select {
		case s.beforeConditionalCredential <- struct{}{}:
		default:
		}
		if s.continueConditionalCredential != nil {
			<-s.continueConditionalCredential
		}
	}
	return s.repo.UpdateCredentials(ctx, id, patch)
}

func (s *refreshRaceStore) UpdateStatus(ctx context.Context, id int64, status string) error {
	return s.repo.UpdateStatus(ctx, id, status)
}

func (s *refreshRaceStore) UpdateStatusIfCurrent(ctx context.Context, id int64, status string, expectedLastRefresh time.Time) error {
	if s.beforeConditionalStatus != nil {
		select {
		case s.beforeConditionalStatus <- struct{}{}:
		default:
		}
		if s.continueConditionalStatus != nil {
			<-s.continueConditionalStatus
		}
	}
	return s.repo.UpdateStatusIfCurrent(ctx, id, status, expectedLastRefresh)
}

func (s *refreshRaceStore) UpdateUsage(ctx context.Context, id int64, snapshot store.UsageSnapshot) error {
	return s.repo.UpdateUsage(ctx, id, snapshot)
}

func (s *refreshRaceStore) ListActive(ctx context.Context) ([]domain.UpstreamAccount, error) {
	return s.repo.ListActive(ctx)
}

func (s *refreshRaceStore) GetProjectionByID(ctx context.Context, id int64) (*domain.UpstreamAccount, error) {
	return s.repo.GetProjectionByID(ctx, id)
}

type refreshRepeatedConflictStore struct {
	repo    *store.AccountRepo
	patches []store.CredentialPatch

	mu  sync.Mutex
	idx int
}

func (s *refreshRepeatedConflictStore) InsertUpstreamAccount(ctx context.Context, account *domain.UpstreamAccount) (int64, error) {
	return s.repo.InsertUpstreamAccount(ctx, account)
}

func (s *refreshRepeatedConflictStore) GetByID(ctx context.Context, id int64) (*domain.UpstreamAccount, error) {
	return s.repo.GetByID(ctx, id)
}

func (s *refreshRepeatedConflictStore) UpdateCredentials(ctx context.Context, id int64, patch store.CredentialPatch) error {
	if patch.ExpectedLastRefresh != nil {
		s.mu.Lock()
		if s.idx < len(s.patches) {
			current := s.patches[s.idx]
			s.idx++
			s.mu.Unlock()
			if err := s.repo.UpdateCredentials(ctx, id, current); err != nil {
				return err
			}
		} else {
			s.mu.Unlock()
		}
	}
	return s.repo.UpdateCredentials(ctx, id, patch)
}

func (s *refreshRepeatedConflictStore) UpdateStatusIfCurrent(ctx context.Context, id int64, status string, expectedLastRefresh time.Time) error {
	return s.repo.UpdateStatusIfCurrent(ctx, id, status, expectedLastRefresh)
}

func (s *refreshRepeatedConflictStore) UpdateUsage(ctx context.Context, id int64, snapshot store.UsageSnapshot) error {
	return s.repo.UpdateUsage(ctx, id, snapshot)
}

func (s *refreshRepeatedConflictStore) ListActive(ctx context.Context) ([]domain.UpstreamAccount, error) {
	return s.repo.ListActive(ctx)
}

func (s *refreshRepeatedConflictStore) GetProjectionByID(ctx context.Context, id int64) (*domain.UpstreamAccount, error) {
	return s.repo.GetProjectionByID(ctx, id)
}

type refreshDeletedReloadStore struct {
	repo  *store.AccountRepo
	patch store.CredentialPatch
	once  sync.Once
}

func (s *refreshDeletedReloadStore) InsertUpstreamAccount(ctx context.Context, account *domain.UpstreamAccount) (int64, error) {
	return s.repo.InsertUpstreamAccount(ctx, account)
}

func (s *refreshDeletedReloadStore) GetByID(ctx context.Context, id int64) (*domain.UpstreamAccount, error) {
	return s.repo.GetByID(ctx, id)
}

func (s *refreshDeletedReloadStore) UpdateCredentials(ctx context.Context, id int64, patch store.CredentialPatch) error {
	s.once.Do(func() {
		_ = s.repo.UpdateCredentials(ctx, id, s.patch)
		_ = s.repo.UpdateStatus(ctx, id, domain.AccountStatusDeleted)
	})
	return store.ErrConditionalUpdateConflict
}

func (s *refreshDeletedReloadStore) UpdateStatusIfCurrent(ctx context.Context, id int64, status string, expectedLastRefresh time.Time) error {
	return s.repo.UpdateStatusIfCurrent(ctx, id, status, expectedLastRefresh)
}

func (s *refreshDeletedReloadStore) UpdateUsage(ctx context.Context, id int64, snapshot store.UsageSnapshot) error {
	return s.repo.UpdateUsage(ctx, id, snapshot)
}

func (s *refreshDeletedReloadStore) ListActive(ctx context.Context) ([]domain.UpstreamAccount, error) {
	return s.repo.ListActive(ctx)
}

func (s *refreshDeletedReloadStore) GetProjectionByID(ctx context.Context, id int64) (*domain.UpstreamAccount, error) {
	return s.repo.GetProjectionByID(ctx, id)
}

func timePointer(t time.Time) *time.Time {
	return &t
}

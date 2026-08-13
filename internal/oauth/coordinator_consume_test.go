package oauth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/requestid"
	"github.com/user/one-llm-router/internal/store"
)

func TestConsumeCode(t *testing.T) {
	t.Run("happy path persists new oauth browser account", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		now := time.Date(2026, 4, 21, 16, 0, 0, 0, time.UTC)
		claimsToken := mustJWT(t, map[string]any{
			"email": "alice@example.com",
			"https://api.openai.com/auth": map[string]any{
				"plan_type":          "chatgpt-plus",
				"chatgpt_account_id": "org_alice",
			},
		})

		provider := &consumeProvider{
			tokens: Tokens{
				AccessToken:  []byte("access-token-v1"),
				RefreshToken: []byte("refresh-token-v1"),
				IDToken:      claimsToken,
				ExpiresIn:    45 * time.Minute,
				LastRefresh:  now,
			},
		}

		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(NewFakeClock(now), provider, newTestLogger(&logs, slog.LevelInfo))
		coord.SetAccountStore(repo)

		flow := newPendingBrowserFlow(now)
		require.NoError(t, coord.TryStartFlow(flow))

		ctx := requestid.WithContext(context.Background(), "req-consume-success")
		account, err := coord.ConsumeCode(ctx, flow.ID, "code-123", flow.State, RailLoopback)
		require.NoError(t, err)
		require.NotNil(t, account)

		assert.Nil(t, coord.CurrentFlow())
		assert.Equal(t, int32(1), provider.exchangeCalls.Load())
		assert.Equal(t, FlowStatusSuccess, flow.Status)
		assert.Equal(t, RailLoopback, flow.ConsumedBy)
		assert.Equal(t, domain.AuthMethodOAuthBrowser, account.AuthMethod)
		assert.Equal(t, "alice@example.com", account.Name)
		assert.Equal(t, domain.ProviderOpenAI, account.Provider)
		assert.Equal(t, domain.AccountStatusActive, account.Status)
		require.NotNil(t, account.BaseURL)
		assert.Equal(t, domain.ProviderDefaultURLs[domain.ProviderOpenAI], *account.BaseURL)
		require.NotNil(t, account.Email)
		assert.Equal(t, "alice@example.com", *account.Email)
		require.NotNil(t, account.PlanType)
		assert.Equal(t, "chatgpt-plus", *account.PlanType)
		require.NotNil(t, account.ChatGPTAccountID)
		assert.Equal(t, "org_alice", *account.ChatGPTAccountID)
		require.NotNil(t, account.LastRefresh)
		assert.Equal(t, now, account.LastRefresh.UTC())
		require.NotNil(t, account.AccessExpiresAt)
		assert.Equal(t, now.Add(45*time.Minute), account.AccessExpiresAt.UTC())

		items, err := repo.ListForAdminAPI(context.Background())
		require.NoError(t, err)
		require.Len(t, items, 1)
		assert.Equal(t, account.ID, items[0].ID)
		projected, err := repo.GetProjectionByID(context.Background(), account.ID)
		require.NoError(t, err)
		assert.Nil(t, projected.AccessToken)
		assert.Nil(t, projected.RefreshToken)
		assert.Nil(t, projected.IDToken)
		assert.Empty(t, projected.APIKey)
		require.NotNil(t, projected.AccessExpiresAt)
		assert.Equal(t, domain.AuthMethodOAuthBrowser, projected.AuthMethod)
		assert.Equal(t, now.Add(45*time.Minute), projected.AccessExpiresAt.UTC())
		exported, err := repo.GetForExport(context.Background(), account.ID)
		require.NoError(t, err)
		assert.Equal(t, []byte("access-token-v1"), exported.AccessToken)
		assert.Equal(t, []byte("refresh-token-v1"), exported.RefreshToken)
		assert.Equal(t, claimsToken, exported.IDToken)
		require.NotNil(t, exported.LastRefresh)
		assert.Equal(t, now, exported.LastRefresh.UTC())
		assert.Contains(t, logs.String(), "oauth_flow_completed")
		assert.Contains(t, logs.String(), "request_id=req-consume-success")
		assert.Contains(t, logs.String(), "rail=loopback")
		assert.Contains(t, logs.String(), fmt.Sprintf("account_id=%d", account.ID))
		assert.Contains(t, logs.String(), "email=alice@example.com")
		assert.Contains(t, logs.String(), "plan_type=chatgpt-plus")
	})

	t.Run("state mismatch leaves flow pending and second call succeeds", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		now := time.Date(2026, 4, 21, 16, 5, 0, 0, time.UTC)
		provider := &consumeProvider{
			tokens: Tokens{
				AccessToken:  []byte("access-token-v1"),
				RefreshToken: []byte("refresh-token-v1"),
				IDToken: mustJWT(t, map[string]any{
					"email": "bob@example.com",
					"auth": map[string]any{
						"plan_type":          "chatgpt-team",
						"chatgpt_account_id": "org_bob",
					},
				}),
				ExpiresIn:   30 * time.Minute,
				LastRefresh: now,
			},
		}

		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(NewFakeClock(now), provider, newTestLogger(&logs, slog.LevelInfo))
		coord.SetAccountStore(repo)

		flow := newPendingBrowserFlow(now)
		require.NoError(t, coord.TryStartFlow(flow))

		_, err := coord.ConsumeCode(requestid.WithContext(context.Background(), "req-state-bad"), flow.ID, "code-1", "state-bad", RailManualPaste)
		require.ErrorIs(t, err, ErrStateMismatch)
		assert.False(t, flow.Consumed.Load())
		assert.Equal(t, FlowStatusPending, flow.Status)
		assert.Equal(t, int32(0), provider.exchangeCalls.Load())
		assert.Contains(t, logs.String(), "oauth_rail_rejected")
		assert.Contains(t, logs.String(), "error_code=oauth_state_mismatch")
		assert.Contains(t, logs.String(), "rail=manual_paste")

		account, err := coord.ConsumeCode(requestid.WithContext(context.Background(), "req-state-good"), flow.ID, "code-1", flow.State, RailManualPaste)
		require.NoError(t, err)
		require.NotNil(t, account)
		assert.Equal(t, int32(1), provider.exchangeCalls.Load())
		assert.Equal(t, FlowStatusSuccess, flow.Status)
		assert.Equal(t, RailManualPaste, flow.ConsumedBy)
	})

	t.Run("state mismatch still wins over already consumed flow", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		now := time.Date(2026, 4, 21, 16, 7, 0, 0, time.UTC)
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(NewFakeClock(now), &consumeProvider{}, newTestLogger(&logs, slog.LevelDebug))
		coord.SetAccountStore(repo)

		flow := newPendingBrowserFlow(now)
		flow.Consumed.Store(true)
		flow.ConsumedBy = RailManualPaste
		flow.Status = FlowStatusSuccess
		require.NoError(t, coord.TryStartFlow(flow))

		_, err := coord.ConsumeCode(requestid.WithContext(context.Background(), "req-state-bad-consumed"), flow.ID, "code-1", "state-bad", RailLoopback)
		require.ErrorIs(t, err, ErrStateMismatch)
		assert.Contains(t, logs.String(), "oauth_rail_rejected")
		assert.Contains(t, logs.String(), "error_code=oauth_state_mismatch")
		assert.NotContains(t, logs.String(), "error_code=already_consumed")
	})

	t.Run("expired flow uses injected clock", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		base := time.Date(2026, 4, 21, 16, 10, 0, 0, time.UTC)
		fake := NewFakeClock(base)
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(fake, &consumeProvider{}, newTestLogger(&logs, slog.LevelInfo))
		coord.SetAccountStore(repo)

		flow := newPendingBrowserFlow(base)
		require.NoError(t, coord.TryStartFlow(flow))

		fake.Step(5*time.Minute + time.Second)

		_, err := coord.ConsumeCode(context.Background(), flow.ID, "code-1", flow.State, RailLoopback)
		require.ErrorIs(t, err, ErrFlowExpired)
		assert.False(t, flow.Consumed.Load())
		assert.Equal(t, FlowStatusPending, flow.Status)
		assert.Contains(t, logs.String(), "oauth_flow_expired")
	})

	t.Run("upstream invalid grant marks flow error and releases immediately", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		now := time.Date(2026, 4, 21, 16, 15, 0, 0, time.UTC)
		provider := &consumeProvider{
			err: &TokenExchangeError{
				code:       "invalid_grant",
				message:    "authorization code already used",
				httpStatus: 400,
			},
		}
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(NewFakeClock(now), provider, newTestLogger(&logs, slog.LevelInfo))
		coord.SetAccountStore(repo)

		flow := newPendingBrowserFlow(now)
		require.NoError(t, coord.TryStartFlow(flow))

		_, err := coord.ConsumeCode(requestid.WithContext(context.Background(), "req-invalid-grant"), flow.ID, "code-1", flow.State, RailManualPaste)
		require.Error(t, err)
		var exchangeErr *TokenExchangeError
		require.ErrorAs(t, err, &exchangeErr)
		assert.Equal(t, "invalid_grant", exchangeErr.Code())
		assert.Nil(t, coord.CurrentFlow())
		assert.Equal(t, FlowStatusError, flow.Status)
		assert.Contains(t, logs.String(), "oauth_flow_failed")
		assert.Contains(t, logs.String(), "error_code=invalid_grant")
		assert.Contains(t, logs.String(), "rail=manual_paste")
	})

	t.Run("upstream server error marks flow error and releases immediately", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		now := time.Date(2026, 4, 21, 16, 20, 0, 0, time.UTC)
		provider := &consumeProvider{
			err: &TokenExchangeError{
				code:       "server_error",
				message:    "upstream unavailable",
				httpStatus: 502,
			},
		}
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(NewFakeClock(now), provider, newTestLogger(&logs, slog.LevelInfo))
		coord.SetAccountStore(repo)

		flow := newPendingBrowserFlow(now)
		require.NoError(t, coord.TryStartFlow(flow))

		_, err := coord.ConsumeCode(requestid.WithContext(context.Background(), "req-upstream-5xx"), flow.ID, "code-1", flow.State, RailLoopback)
		require.Error(t, err)
		var exchangeErr *TokenExchangeError
		require.ErrorAs(t, err, &exchangeErr)
		assert.Equal(t, "server_error", exchangeErr.Code())
		assert.Equal(t, 502, exchangeErr.HTTPStatus())
		assert.Nil(t, coord.CurrentFlow())
		assert.Equal(t, FlowStatusError, flow.Status)
		assert.Contains(t, logs.String(), "oauth_flow_failed")
		assert.Contains(t, logs.String(), "error_code=server_error")
		assert.Contains(t, logs.String(), "rail=loopback")
	})

	t.Run("missing identity claims fails fast instead of inserting blank account name", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		now := time.Date(2026, 4, 21, 16, 22, 0, 0, time.UTC)
		provider := &consumeProvider{
			tokens: Tokens{
				AccessToken:  []byte("access-token-v1"),
				RefreshToken: []byte("refresh-token-v1"),
				IDToken:      mustJWT(t, map[string]any{}),
				ExpiresIn:    10 * time.Minute,
				LastRefresh:  now,
			},
		}
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(NewFakeClock(now), provider, newTestLogger(&logs, slog.LevelInfo))
		coord.SetAccountStore(repo)

		flow := newPendingBrowserFlow(now)
		require.NoError(t, coord.TryStartFlow(flow))

		_, err := coord.ConsumeCode(requestid.WithContext(context.Background(), "req-missing-identity"), flow.ID, "code-1", flow.State, RailManualPaste)
		require.Error(t, err)
		var exchangeErr *TokenExchangeError
		require.ErrorAs(t, err, &exchangeErr)
		assert.Equal(t, "invalid_response", exchangeErr.Code())
		assert.Contains(t, exchangeErr.Message(), "missing email and chatgpt_account_id")
		assert.Nil(t, coord.CurrentFlow())
		assert.Equal(t, FlowStatusError, flow.Status)

		items, err := repo.ListForAdminAPI(context.Background())
		require.NoError(t, err)
		assert.Empty(t, items)
		assert.Contains(t, logs.String(), "oauth_flow_failed")
		assert.Contains(t, logs.String(), "error_code=invalid_response")
	})

	t.Run("store failure returns typed error and releases slot", func(t *testing.T) {
		now := time.Date(2026, 4, 21, 16, 24, 0, 0, time.UTC)
		provider := &consumeProvider{
			tokens: Tokens{
				AccessToken:  []byte("access-token-v1"),
				RefreshToken: []byte("refresh-token-v1"),
				IDToken: mustJWT(t, map[string]any{
					"email": "store-fail@example.com",
					"auth": map[string]any{
						"chatgpt_account_id": "org_store_fail",
					},
				}),
				ExpiresIn:   15 * time.Minute,
				LastRefresh: now,
			},
		}
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(NewFakeClock(now), provider, newTestLogger(&logs, slog.LevelInfo))
		coord.SetAccountStore(failingAccountStore{insertErr: errors.New("disk full")})

		flow := newPendingBrowserFlow(now)
		require.NoError(t, coord.TryStartFlow(flow))

		_, err := coord.ConsumeCode(requestid.WithContext(context.Background(), "req-store-fail"), flow.ID, "code-1", flow.State, RailManualPaste)
		require.Error(t, err)
		var storeErr *StoreError
		require.ErrorAs(t, err, &storeErr)
		assert.Equal(t, "insert account", storeErr.Op)
		assert.Nil(t, coord.CurrentFlow())
		assert.Equal(t, FlowStatusError, flow.Status)
		assert.Contains(t, logs.String(), "oauth_flow_failed")
		assert.Contains(t, logs.String(), "error_code=oauth_store_failed")
	})

	t.Run("cas loss is debug only for loopback", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		now := time.Date(2026, 4, 21, 16, 25, 0, 0, time.UTC)
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(NewFakeClock(now), &consumeProvider{}, newTestLogger(&logs, slog.LevelDebug))
		coord.SetAccountStore(repo)

		flow := newPendingBrowserFlow(now)
		flow.Consumed.Store(true)
		flow.mu.Lock()
		flow.ConsumedBy = RailManualPaste
		flow.mu.Unlock()
		require.NoError(t, coord.TryStartFlow(flow))

		_, err := coord.ConsumeCode(requestid.WithContext(context.Background(), "req-lost-loopback"), flow.ID, "code-1", flow.State, RailLoopback)
		require.ErrorIs(t, err, ErrAlreadyConsumed)
		assert.Equal(t, 0, strings.Count(logs.String(), "level=INFO"))
		assert.Equal(t, 1, strings.Count(logs.String(), "level=DEBUG"))
		assert.Contains(t, logs.String(), "oauth_rail_rejected")
		assert.Contains(t, logs.String(), "rail=loopback")
		assert.Contains(t, logs.String(), "error_code=already_consumed")
		assert.Contains(t, logs.String(), "winner_rail=manual_paste")
	})

	t.Run("cas loss is info for manual paste", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		now := time.Date(2026, 4, 21, 16, 30, 0, 0, time.UTC)
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(NewFakeClock(now), &consumeProvider{}, newTestLogger(&logs, slog.LevelDebug))
		coord.SetAccountStore(repo)

		flow := newPendingBrowserFlow(now)
		flow.Consumed.Store(true)
		flow.mu.Lock()
		flow.ConsumedBy = RailLoopback
		flow.mu.Unlock()
		require.NoError(t, coord.TryStartFlow(flow))

		_, err := coord.ConsumeCode(requestid.WithContext(context.Background(), "req-lost-manual"), flow.ID, "code-1", flow.State, RailManualPaste)
		require.ErrorIs(t, err, ErrAlreadyConsumed)
		assert.Equal(t, 1, strings.Count(logs.String(), "level=INFO"))
		assert.Equal(t, 0, strings.Count(logs.String(), "level=DEBUG"))
		assert.Contains(t, logs.String(), "oauth_rail_rejected")
		assert.Contains(t, logs.String(), "rail=manual_paste")
		assert.Contains(t, logs.String(), "error_code=already_consumed")
		assert.Contains(t, logs.String(), "winner_rail=loopback")
	})

	t.Run("race uses cas first wins and exchanges once", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		now := time.Date(2026, 4, 21, 16, 35, 0, 0, time.UTC)
		provider := &consumeProvider{
			tokens: Tokens{
				AccessToken:  []byte("race-access"),
				RefreshToken: []byte("race-refresh"),
				IDToken: mustJWT(t, map[string]any{
					"email": "race@example.com",
					"auth": map[string]any{
						"plan_type":          "chatgpt-team",
						"chatgpt_account_id": "org_race",
					},
				}),
				ExpiresIn:   time.Hour,
				LastRefresh: now,
			},
			blockExchange:   make(chan struct{}),
			exchangeEntered: make(chan struct{}, 1),
		}

		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(NewFakeClock(now), provider, newTestLogger(&logs, slog.LevelDebug))
		coord.SetAccountStore(repo)

		flow := newPendingBrowserFlow(now)
		require.NoError(t, coord.TryStartFlow(flow))

		type result struct {
			rail Rail
			acct *domain.UpstreamAccount
			err  error
		}

		start := make(chan struct{})
		results := make(chan result, 50)
		for i := 0; i < 25; i++ {
			go func() {
				<-start
				acct, err := coord.ConsumeCode(requestid.WithContext(context.Background(), "req-race-loopback"), flow.ID, "code-race", flow.State, RailLoopback)
				results <- result{rail: RailLoopback, acct: acct, err: err}
			}()
			go func() {
				<-start
				acct, err := coord.ConsumeCode(requestid.WithContext(context.Background(), "req-race-manual"), flow.ID, "code-race", flow.State, RailManualPaste)
				results <- result{rail: RailManualPaste, acct: acct, err: err}
			}()
		}

		close(start)
		<-provider.exchangeEntered

		outcomes := make([]result, 0, 50)
		for len(outcomes) < 49 {
			outcomes = append(outcomes, <-results)
		}
		close(provider.blockExchange)
		outcomes = append(outcomes, <-results)

		var winnerRail Rail
		successCount := 0
		consumedCount := 0
		for _, outcome := range outcomes {
			switch {
			case outcome.err == nil:
				successCount++
				winnerRail = outcome.rail
				require.NotNil(t, outcome.acct)
			case errors.Is(outcome.err, ErrAlreadyConsumed):
				consumedCount++
			default:
				t.Fatalf("unexpected race outcome: rail=%s err=%v", outcome.rail, outcome.err)
			}
		}

		require.Equal(t, 1, successCount)
		require.Equal(t, 49, consumedCount)
		assert.Equal(t, int32(1), provider.exchangeCalls.Load())
		assert.Equal(t, 1, strings.Count(logs.String(), "oauth_flow_completed"))
		switch winnerRail {
		case RailLoopback:
			assert.Equal(t, 25, countLogLinesWithAll(logs.String(), "level=INFO", "oauth_rail_rejected", "rail=manual_paste", "error_code=already_consumed"))
			assert.Equal(t, 24, countLogLinesWithAll(logs.String(), "level=DEBUG", "oauth_rail_rejected", "rail=loopback", "error_code=already_consumed"))
			assert.Equal(t, 25, countLogLinesWithAll(logs.String(), "level=INFO", "winner_rail=loopback"))
			assert.Equal(t, 24, countLogLinesWithAll(logs.String(), "level=DEBUG", "winner_rail=loopback"))
		case RailManualPaste:
			assert.Equal(t, 24, countLogLinesWithAll(logs.String(), "level=INFO", "oauth_rail_rejected", "rail=manual_paste", "error_code=already_consumed"))
			assert.Equal(t, 25, countLogLinesWithAll(logs.String(), "level=DEBUG", "oauth_rail_rejected", "rail=loopback", "error_code=already_consumed"))
			assert.Equal(t, 24, countLogLinesWithAll(logs.String(), "level=INFO", "winner_rail=manual_paste"))
			assert.Equal(t, 25, countLogLinesWithAll(logs.String(), "level=DEBUG", "winner_rail=manual_paste"))
		default:
			t.Fatalf("winner rail missing")
		}
	})

	t.Run("no current flow and flow id mismatch return ErrFlowNotFound", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		now := time.Date(2026, 4, 21, 16, 45, 0, 0, time.UTC)
		coord := NewCoordinatorWithClock(NewFakeClock(now), &consumeProvider{}, newTestLogger(io.Discard, slog.LevelInfo))
		coord.SetAccountStore(repo)

		_, err := coord.ConsumeCode(context.Background(), "fl_missing", "code-1", "state-good", RailManualPaste)
		require.ErrorIs(t, err, ErrFlowNotFound)

		flow := newPendingBrowserFlow(now)
		require.NoError(t, coord.TryStartFlow(flow))

		_, err = coord.ConsumeCode(context.Background(), "fl_other", "code-1", flow.State, RailManualPaste)
		require.ErrorIs(t, err, ErrFlowNotFound)
	})

	t.Run("late callback after released success still returns ErrAlreadyConsumed", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		now := time.Date(2026, 4, 21, 16, 45, 30, 0, time.UTC)
		provider := &consumeProvider{
			tokens: Tokens{
				AccessToken:  []byte("access-token-v1"),
				RefreshToken: []byte("refresh-token-v1"),
				IDToken: mustJWT(t, map[string]any{
					"email": "late@example.com",
					"auth": map[string]any{
						"chatgpt_account_id": "org_late",
					},
				}),
				ExpiresIn:   15 * time.Minute,
				LastRefresh: now,
			},
		}
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(NewFakeClock(now), provider, newTestLogger(&logs, slog.LevelDebug))
		coord.SetAccountStore(repo)

		flow := newPendingBrowserFlow(now)
		require.NoError(t, coord.TryStartFlow(flow))

		_, err := coord.ConsumeCode(requestid.WithContext(context.Background(), "req-first"), flow.ID, "code-1", flow.State, RailManualPaste)
		require.NoError(t, err)
		assert.Nil(t, coord.CurrentFlow())

		_, err = coord.ConsumeCode(requestid.WithContext(context.Background(), "req-late"), flow.ID, "code-1", flow.State, RailLoopback)
		require.ErrorIs(t, err, ErrAlreadyConsumed)
		assert.Contains(t, logs.String(), "winner_rail=manual_paste")
	})

	t.Run("late callback still resolves old released listener flow after new flow starts", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		now := time.Date(2026, 4, 21, 16, 45, 45, 0, time.UTC)
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(NewFakeClock(now), &consumeProvider{}, newTestLogger(&logs, slog.LevelDebug))
		coord.SetAccountStore(repo)

		oldFlow := newPendingBrowserFlow(now)
		oldFlow.ListenerBound = true
		oldFlow.Consumed.Store(true)
		oldFlow.ConsumedBy = RailManualPaste
		oldFlow.Status = FlowStatusSuccess
		oldFlow.signalTerminal()
		require.NoError(t, coord.TryStartFlow(oldFlow))
		coord.ReleaseFlow(oldFlow.ID)

		newFlow := newPendingBrowserFlow(now.Add(time.Second))
		newFlow.ID = "fl_new"
		newFlow.State = "state-new"
		require.NoError(t, coord.TryStartFlow(newFlow))

		_, err := coord.ConsumeCode(requestid.WithContext(context.Background(), "req-late-after-new"), oldFlow.ID, "code-1", oldFlow.State, RailLoopback)
		require.ErrorIs(t, err, ErrAlreadyConsumed)
		assert.Contains(t, logs.String(), "winner_rail=manual_paste")
	})

	t.Run("malformed id token becomes invalid_response terminal error", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		now := time.Date(2026, 4, 21, 16, 46, 0, 0, time.UTC)
		provider := &consumeProvider{
			tokens: Tokens{
				AccessToken:  []byte("access-token-v1"),
				RefreshToken: []byte("refresh-token-v1"),
				IDToken:      []byte("not-a-jwt"),
				ExpiresIn:    20 * time.Minute,
				LastRefresh:  now,
			},
		}
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(NewFakeClock(now), provider, newTestLogger(&logs, slog.LevelInfo))
		coord.SetAccountStore(repo)

		flow := newPendingBrowserFlow(now)
		require.NoError(t, coord.TryStartFlow(flow))

		_, err := coord.ConsumeCode(requestid.WithContext(context.Background(), "req-bad-id-token"), flow.ID, "code-1", flow.State, RailLoopback)
		require.Error(t, err)
		var exchangeErr *TokenExchangeError
		require.ErrorAs(t, err, &exchangeErr)
		assert.Equal(t, "invalid_response", exchangeErr.Code())
		assert.Nil(t, coord.CurrentFlow())
		assert.Equal(t, FlowStatusError, flow.Status)
		assert.Contains(t, logs.String(), "error_code=invalid_response")
	})

	t.Run("zero last refresh becomes invalid_response terminal error", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		now := time.Date(2026, 4, 21, 16, 47, 0, 0, time.UTC)
		provider := &consumeProvider{
			tokens: Tokens{
				AccessToken:  []byte("access-token-v1"),
				RefreshToken: []byte("refresh-token-v1"),
				IDToken: mustJWT(t, map[string]any{
					"email": "zero-refresh@example.com",
					"auth": map[string]any{
						"chatgpt_account_id": "org_zero",
					},
				}),
				ExpiresIn: 15 * time.Minute,
			},
		}
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(NewFakeClock(now), provider, newTestLogger(&logs, slog.LevelInfo))
		coord.SetAccountStore(repo)

		flow := newPendingBrowserFlow(now)
		require.NoError(t, coord.TryStartFlow(flow))

		_, err := coord.ConsumeCode(requestid.WithContext(context.Background(), "req-zero-refresh"), flow.ID, "code-1", flow.State, RailManualPaste)
		require.Error(t, err)
		var exchangeErr *TokenExchangeError
		require.ErrorAs(t, err, &exchangeErr)
		assert.Equal(t, "invalid_response", exchangeErr.Code())
		assert.Contains(t, exchangeErr.Message(), "missing last_refresh")
		assert.Nil(t, coord.CurrentFlow())
		assert.Equal(t, FlowStatusError, flow.Status)
		assert.Contains(t, logs.String(), "error_code=invalid_response")
	})

	t.Run("winner rail retry resolves publication after first unknown read", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		now := time.Date(2026, 4, 21, 16, 50, 0, 0, time.UTC)
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(NewFakeClock(now), &consumeProvider{}, newTestLogger(&logs, slog.LevelDebug))
		coord.SetAccountStore(repo)

		flow := newPendingBrowserFlow(now)
		flow.Consumed.Store(true)
		require.NoError(t, coord.TryStartFlow(flow))

		originalYield := readWinnerRailYield
		defer func() { readWinnerRailYield = originalYield }()
		readWinnerRailYield = func() {
			flow.mu.Lock()
			flow.ConsumedBy = RailManualPaste
			flow.mu.Unlock()
		}

		_, err := coord.ConsumeCode(requestid.WithContext(context.Background(), "req-winner-retry"), flow.ID, "code-1", flow.State, RailLoopback)
		require.ErrorIs(t, err, ErrAlreadyConsumed)
		assert.Contains(t, logs.String(), "winner_rail=manual_paste")
		assert.Equal(t, 1, strings.Count(logs.String(), "level=DEBUG"))
	})

	t.Run("late callback after terminal error without winner rail is benign", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		now := time.Date(2026, 4, 21, 16, 51, 0, 0, time.UTC)
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(NewFakeClock(now), &consumeProvider{}, newTestLogger(&logs, slog.LevelDebug))
		coord.SetAccountStore(repo)

		flow := newPendingBrowserFlow(now)
		flow.Consumed.Store(true)
		flow.Status = FlowStatusError
		require.NoError(t, coord.TryStartFlow(flow))
		coord.ReleaseFlow(flow.ID)
		assert.Nil(t, coord.CurrentFlow())

		_, err := coord.ConsumeCode(requestid.WithContext(context.Background(), "req-late-error"), flow.ID, "code-1", flow.State, RailLoopback)
		require.ErrorIs(t, err, ErrAlreadyConsumed)
		assert.Equal(t, 1, strings.Count(logs.String(), "level=DEBUG"))
		assert.Contains(t, logs.String(), "oauth_rail_rejected")
	})
}

type consumeProvider struct {
	tokens          Tokens
	err             error
	exchangeCalls   atomic.Int32
	blockExchange   chan struct{}
	exchangeEntered chan struct{}
}

type failingAccountStore struct {
	insertErr         error
	updateErr         error
	getErr            error
	getProjection     *domain.UpstreamAccount
	getProjectionFunc func(context.Context, int64) (*domain.UpstreamAccount, error)
}

func (p *consumeProvider) BuildAuthorizeURL(string, string) (string, error) {
	return "https://auth.example.test/oauth/authorize", nil
}

func (p *consumeProvider) ExchangeCode(_ context.Context, _, _ string) (Tokens, error) {
	p.exchangeCalls.Add(1)
	if p.exchangeEntered != nil {
		select {
		case p.exchangeEntered <- struct{}{}:
		default:
		}
	}
	if p.blockExchange != nil {
		<-p.blockExchange
	}
	if p.err != nil {
		return Tokens{}, p.err
	}
	return p.tokens, nil
}

func (*consumeProvider) Refresh(context.Context, []byte) (Tokens, error) {
	return Tokens{}, errors.New("not used in consume tests")
}

func (*consumeProvider) RequestDeviceCode(context.Context) (DeviceCode, error) {
	return DeviceCode{}, errors.New("not used in consume tests")
}

func (*consumeProvider) PollDeviceCode(context.Context, string, string) (Tokens, error) {
	return Tokens{}, errors.New("not used in consume tests")
}

func (s failingAccountStore) InsertUpstreamAccount(context.Context, *domain.UpstreamAccount) (int64, error) {
	return 0, s.insertErr
}

func (s failingAccountStore) UpdateCredentials(context.Context, int64, store.CredentialPatch) error {
	return s.updateErr
}

func (s failingAccountStore) UpdateStatus(context.Context, int64, string) error {
	return s.updateErr
}

func (s failingAccountStore) UpdateStatusIfCurrent(context.Context, int64, string, time.Time) error {
	return s.updateErr
}

func (s failingAccountStore) UpdateUsage(context.Context, int64, store.UsageSnapshot) error {
	return s.updateErr
}

func (s failingAccountStore) ListActive(context.Context) ([]domain.UpstreamAccount, error) {
	return nil, s.getErr
}

func (s failingAccountStore) GetByID(ctx context.Context, id int64) (*domain.UpstreamAccount, error) {
	return s.GetProjectionByID(ctx, id)
}

func (s failingAccountStore) GetProjectionByID(ctx context.Context, id int64) (*domain.UpstreamAccount, error) {
	if s.getProjectionFunc != nil {
		return s.getProjectionFunc(ctx, id)
	}
	if s.getProjection != nil {
		dup := *s.getProjection
		return &dup, nil
	}
	return nil, s.getErr
}

func setupOAuthConsumeStore(t *testing.T) (*store.AccountRepo, func()) {
	t.Helper()

	s, err := store.New("sqlite3", ":memory:", 1, 1)
	require.NoError(t, err)
	require.NoError(t, s.Migrate("sqlite3"))
	return store.NewAccountRepo(s.Engine()), func() { _ = s.Close() }
}

func newPendingBrowserFlow(now time.Time) *Flow {
	return &Flow{
		ID:           "fl_consume",
		Method:       FlowBrowser,
		ConsumedBy:   RailUnknown,
		State:        "state-good",
		CodeVerifier: "verifier-good",
		ExpiresAt:    now.Add(5 * time.Minute),
		Status:       FlowStatusPending,
		CreatedAt:    now,
		terminalCh:   make(chan struct{}),
	}
}

func mustJWT(t *testing.T, payload map[string]any) []byte {
	t.Helper()

	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	bodyBytes, err := json.Marshal(payload)
	require.NoError(t, err)
	body := base64.RawURLEncoding.EncodeToString(bodyBytes)
	return []byte(header + "." + body + ".sig")
}

func newTestLogger(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
}

func countLogLinesWithAll(logs string, needles ...string) int {
	if logs == "" {
		return 0
	}
	lines := strings.Split(strings.TrimSpace(logs), "\n")
	count := 0
	for _, line := range lines {
		if line == "" {
			continue
		}
		match := true
		for _, needle := range needles {
			if !strings.Contains(line, needle) {
				match = false
				break
			}
		}
		if match {
			count++
		}
	}
	return count
}

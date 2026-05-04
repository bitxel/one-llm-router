package oauth

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/requestid"
)

func TestCallbackHandler(t *testing.T) {
	t.Run("start browser wires default callback handler and success path closes flow", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		now := time.Date(2026, 4, 21, 18, 0, 0, 0, time.UTC)
		provider := &consumeProvider{
			tokens: Tokens{
				AccessToken:  []byte("access-token-v1"),
				RefreshToken: []byte("refresh-token-v1"),
				IDToken: mustJWT(t, map[string]any{
					"email": "alice@example.com",
					"https://api.openai.com/auth": map[string]any{
						"plan_type":          "chatgpt-plus",
						"chatgpt_account_id": "org_alice",
					},
				}),
				ExpiresIn:   45 * time.Minute,
				LastRefresh: now,
			},
		}

		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(NewFakeClock(now), provider, newTestLogger(&logs, slog.LevelInfo))
		coord.SetAccountStore(repo)

		var captured http.Handler
		coord.bindLoopback = func(handler http.Handler) (*http.Server, bool, int, error) {
			captured = handler
			return &http.Server{}, true, 1455, nil
		}

		flow, err := coord.StartBrowser(requestid.WithContext(context.Background(), "req-loopback-wire"), "openai")
		require.NoError(t, err)
		require.NotNil(t, captured)

		req := httptest.NewRequest(http.MethodGet, "/auth/callback?code=code-1&state="+url.QueryEscape(flow.State), nil)
		rec := httptest.NewRecorder()
		captured.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))
		assert.Contains(t, rec.Body.String(), "Authentication completed")
		assert.Equal(t, int32(1), provider.exchangeCalls.Load())
		assert.Equal(t, FlowStatusSuccess, flow.Status)
		assert.Equal(t, RailLoopback, flow.ConsumedBy)
		assert.Nil(t, coord.CurrentFlow())
		assert.Contains(t, logs.String(), "oauth_flow_completed")
		assert.Contains(t, logs.String(), "rail=loopback")
	})

	t.Run("success path via direct callback handler returns success HTML", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		now := time.Date(2026, 4, 21, 18, 5, 0, 0, time.UTC)
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
		coord := NewCoordinatorWithClock(NewFakeClock(now), provider, newTestLogger(&bytes.Buffer{}, slog.LevelInfo))
		coord.SetAccountStore(repo)
		var consumeCalls atomic.Int32
		var consumedRail atomic.Int32
		type consumeArgs struct {
			flowID string
			code   string
			state  string
		}
		var got consumeArgs
		flow := newPendingBrowserFlow(now)
		handler := coord.callbackHandlerWithConsume(flow, func(ctx context.Context, flowID, code, state string, rail Rail) (*domain.UpstreamAccount, error) {
			consumeCalls.Add(1)
			if rail == RailLoopback {
				consumedRail.Store(1)
			}
			got = consumeArgs{flowID: flowID, code: code, state: state}
			return coord.ConsumeCode(ctx, flowID, code, state, rail)
		})
		require.NoError(t, coord.TryStartFlow(flow))

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/auth/callback?code=code-1&state="+url.QueryEscape(flow.State), nil)
		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))
		assert.Contains(t, rec.Body.String(), "Authentication completed")
		assert.Equal(t, int32(1), provider.exchangeCalls.Load())
		assert.Equal(t, int32(1), consumeCalls.Load())
		assert.Equal(t, int32(1), consumedRail.Load())
		assert.Equal(t, flow.ID, got.flowID)
		assert.Equal(t, "code-1", got.code)
		assert.Equal(t, flow.State, got.state)
		assert.Equal(t, FlowStatusSuccess, flow.Status)
		assert.Equal(t, RailLoopback, flow.ConsumedBy)
	})

	t.Run("access denied cancels flow after validated state", func(t *testing.T) {
		now := time.Date(2026, 4, 21, 18, 10, 0, 0, time.UTC)
		var logs bytes.Buffer
		provider := &consumeProvider{}
		coord := NewCoordinatorWithClock(NewFakeClock(now), provider, newTestLogger(&logs, slog.LevelInfo))

		flow := newPendingBrowserFlow(now)
		require.NoError(t, coord.TryStartFlow(flow))

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/auth/callback?error=access_denied&state="+url.QueryEscape(flow.State), nil)
		coord.callbackHandler(flow).ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))
		assert.Contains(t, rec.Body.String(), "Authentication cancelled")
		assert.Equal(t, FlowStatusError, flow.Status)
		assert.Equal(t, RailLoopback, flow.ConsumedBy)
		assert.True(t, flow.Consumed.Load())
		assert.Zero(t, provider.exchangeCalls.Load())
		assert.Contains(t, logs.String(), "oauth_flow_cancelled")
		assert.Contains(t, logs.String(), "rail=loopback")
	})

	t.Run("access denied with mismatched state rejects rail and leaves flow pending", func(t *testing.T) {
		now := time.Date(2026, 4, 21, 18, 15, 0, 0, time.UTC)
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(NewFakeClock(now), &consumeProvider{}, newTestLogger(&logs, slog.LevelInfo))

		flow := newPendingBrowserFlow(now)
		require.NoError(t, coord.TryStartFlow(flow))

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/auth/callback?error=access_denied&state=state-bad", nil)
		coord.callbackHandler(flow).ServeHTTP(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Equal(t, "text/plain; charset=utf-8", rec.Header().Get("Content-Type"))
		assert.Contains(t, rec.Body.String(), "state mismatch")
		assert.Equal(t, FlowStatusPending, flow.Status)
		assert.Equal(t, RailUnknown, flow.ConsumedBy)
		assert.False(t, flow.Consumed.Load())
		assert.Contains(t, logs.String(), "oauth_rail_rejected")
		assert.Contains(t, logs.String(), "error_code=oauth_state_mismatch")
		assert.Contains(t, logs.String(), "rail=loopback")
	})

	t.Run("non access denied with mismatched state still returns bad request", func(t *testing.T) {
		now := time.Date(2026, 4, 21, 18, 17, 0, 0, time.UTC)
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(NewFakeClock(now), &consumeProvider{}, newTestLogger(&logs, slog.LevelInfo))

		flow := newPendingBrowserFlow(now)
		require.NoError(t, coord.TryStartFlow(flow))

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/auth/callback?error=server_error&state=state-bad", nil)
		coord.callbackHandler(flow).ServeHTTP(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Equal(t, "text/plain; charset=utf-8", rec.Header().Get("Content-Type"))
		assert.Contains(t, rec.Body.String(), "state mismatch")
		assert.Contains(t, logs.String(), "oauth_rail_rejected")
		assert.Contains(t, logs.String(), "error_code=oauth_state_mismatch")
		assert.NotContains(t, logs.String(), "error_code=server_error")
	})

	t.Run("code path state mismatch returns bad request", func(t *testing.T) {
		now := time.Date(2026, 4, 21, 18, 20, 0, 0, time.UTC)
		var logs bytes.Buffer
		provider := &consumeProvider{}
		coord := NewCoordinatorWithClock(NewFakeClock(now), provider, newTestLogger(&logs, slog.LevelInfo))

		flow := newPendingBrowserFlow(now)
		require.NoError(t, coord.TryStartFlow(flow))

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/auth/callback?code=code-1&state=state-bad", nil)
		coord.callbackHandler(flow).ServeHTTP(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Equal(t, "text/plain; charset=utf-8", rec.Header().Get("Content-Type"))
		assert.Contains(t, rec.Body.String(), "state mismatch")
		assert.Equal(t, FlowStatusPending, flow.Status)
		assert.False(t, flow.Consumed.Load())
		assert.Zero(t, provider.exchangeCalls.Load())
		assert.Contains(t, logs.String(), "oauth_rail_rejected")
	})

	t.Run("missing code returns bad request without mutating flow", func(t *testing.T) {
		now := time.Date(2026, 4, 21, 18, 25, 0, 0, time.UTC)
		coord := NewCoordinatorWithClock(NewFakeClock(now), &consumeProvider{}, newTestLogger(&bytes.Buffer{}, slog.LevelInfo))

		flow := newPendingBrowserFlow(now)
		require.NoError(t, coord.TryStartFlow(flow))

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/auth/callback?state="+url.QueryEscape(flow.State), nil)
		coord.callbackHandler(flow).ServeHTTP(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Equal(t, "text/plain; charset=utf-8", rec.Header().Get("Content-Type"))
		assert.Contains(t, rec.Body.String(), "missing code")
		assert.Equal(t, FlowStatusPending, flow.Status)
		assert.False(t, flow.Consumed.Load())
	})

	t.Run("other provider error returns 422 and keeps flow pending", func(t *testing.T) {
		now := time.Date(2026, 4, 21, 18, 30, 0, 0, time.UTC)
		var logs bytes.Buffer
		provider := &consumeProvider{}
		coord := NewCoordinatorWithClock(NewFakeClock(now), provider, newTestLogger(&logs, slog.LevelInfo))

		flow := newPendingBrowserFlow(now)
		require.NoError(t, coord.TryStartFlow(flow))

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(
			http.MethodGet,
			"/auth/callback?error=server_error&error_description="+url.QueryEscape("Retry <later>")+"&state="+url.QueryEscape(flow.State),
			nil,
		)
		coord.callbackHandler(flow).ServeHTTP(rec, req)

		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
		assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))
		assert.Contains(t, rec.Body.String(), "server_error")
		assert.NotContains(t, rec.Body.String(), "Retry <later>")
		assert.Contains(t, rec.Body.String(), "Retry &lt;later&gt;")
		assert.Equal(t, FlowStatusPending, flow.Status)
		assert.False(t, flow.Consumed.Load())
		assert.Zero(t, provider.exchangeCalls.Load())
		assert.Contains(t, logs.String(), "oauth_rail_rejected")
		assert.Contains(t, logs.String(), "error_code=server_error")
		assert.Contains(t, logs.String(), "rail=loopback")
		assert.NotContains(t, logs.String(), "oauth_flow_failed")
	})

	t.Run("expired flow returns gone", func(t *testing.T) {
		now := time.Date(2026, 4, 21, 18, 35, 0, 0, time.UTC)
		fake := NewFakeClock(now)
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(fake, &consumeProvider{}, newTestLogger(&logs, slog.LevelInfo))

		flow := newPendingBrowserFlow(now)
		require.NoError(t, coord.TryStartFlow(flow))
		fake.Step(5*time.Minute + time.Second)

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/auth/callback?code=code-1&state="+url.QueryEscape(flow.State), nil)
		coord.callbackHandler(flow).ServeHTTP(rec, req)

		assert.Equal(t, http.StatusGone, rec.Code)
		assert.Equal(t, "text/plain; charset=utf-8", rec.Header().Get("Content-Type"))
		assert.Contains(t, rec.Body.String(), "expired")
		assert.Contains(t, logs.String(), "oauth_flow_expired")
	})

	t.Run("upstream exchange failure returns bad gateway", func(t *testing.T) {
		now := time.Date(2026, 4, 21, 18, 40, 0, 0, time.UTC)
		var logs bytes.Buffer
		provider := &consumeProvider{
			err: &TokenExchangeError{
				code:       "server_error",
				message:    "upstream unavailable",
				httpStatus: http.StatusBadGateway,
			},
		}
		coord := NewCoordinatorWithClock(NewFakeClock(now), provider, newTestLogger(&logs, slog.LevelInfo))

		flow := newPendingBrowserFlow(now)
		require.NoError(t, coord.TryStartFlow(flow))

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/auth/callback?code=code-1&state="+url.QueryEscape(flow.State), nil)
		coord.callbackHandler(flow).ServeHTTP(rec, req)

		assert.Equal(t, http.StatusBadGateway, rec.Code)
		assert.Equal(t, "text/plain; charset=utf-8", rec.Header().Get("Content-Type"))
		assert.Contains(t, rec.Body.String(), "Authentication failed")
		assert.Equal(t, FlowStatusError, flow.Status)
		assert.Contains(t, logs.String(), "oauth_flow_failed")
		assert.Contains(t, logs.String(), "rail=loopback")
	})

	t.Run("cas loss after manual paste success returns lost marker and no extra info logs", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		now := time.Date(2026, 4, 21, 18, 45, 0, 0, time.UTC)
		var logs bytes.Buffer
		provider := &consumeProvider{
			tokens: Tokens{
				AccessToken:  []byte("access-token-v1"),
				RefreshToken: []byte("refresh-token-v1"),
				IDToken: mustJWT(t, map[string]any{
					"email": "carol@example.com",
					"https://api.openai.com/auth": map[string]any{
						"plan_type":          "chatgpt-plus",
						"chatgpt_account_id": "org_carol",
					},
				}),
				ExpiresIn:   30 * time.Minute,
				LastRefresh: now,
			},
		}
		coord := NewCoordinatorWithClock(NewFakeClock(now), provider, newTestLogger(&logs, slog.LevelDebug))
		coord.SetAccountStore(repo)

		flow := newPendingBrowserFlow(now)
		require.NoError(t, coord.TryStartFlow(flow))

		_, err := coord.ConsumeCode(requestid.WithContext(context.Background(), "req-manual-win"), flow.ID, "code-manual", flow.State, RailManualPaste)
		require.NoError(t, err)
		require.Equal(t, int32(1), provider.exchangeCalls.Load())

		before := logs.Len()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/auth/callback?code=code-loopback&state="+url.QueryEscape(flow.State), nil)
		coord.callbackHandler(flow).ServeHTTP(rec, req)
		delta := logs.String()[before:]

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))
		assert.Contains(t, rec.Body.String(), `data-rail="lost"`)
		assert.Equal(t, int32(1), provider.exchangeCalls.Load())
		assert.Equal(t, 0, countLogLinesWithAll(delta, "level=INFO"))
		assert.Equal(t, 0, countLogLinesWithAll(delta, "level=WARN"))
		assert.Equal(t, 0, countLogLinesWithAll(delta, "level=ERROR"))
		assert.Equal(t, 1, countLogLinesWithAll(delta, "level=DEBUG", "oauth_rail_rejected", "error_code=already_consumed"))
	})

	t.Run("access denied cas loss after manual paste cancel returns cancel page without extra info logs", func(t *testing.T) {
		now := time.Date(2026, 4, 21, 18, 48, 0, 0, time.UTC)
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(NewFakeClock(now), &consumeProvider{}, newTestLogger(&logs, slog.LevelDebug))

		flow := newPendingBrowserFlow(now)
		require.NoError(t, coord.TryStartFlow(flow))
		require.NoError(t, coord.cancelFromRail(requestid.WithContext(context.Background(), "req-manual-cancel"), flow.ID, RailManualPaste, "access_denied"))

		before := logs.Len()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/auth/callback?error=access_denied&state="+url.QueryEscape(flow.State), nil)
		coord.callbackHandler(flow).ServeHTTP(rec, req)
		delta := logs.String()[before:]

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))
		assert.Contains(t, rec.Body.String(), "Authentication cancelled")
		assert.Equal(t, 0, countLogLinesWithAll(delta, "level=INFO"))
		assert.Equal(t, 0, countLogLinesWithAll(delta, "level=WARN"))
		assert.Equal(t, 0, countLogLinesWithAll(delta, "level=ERROR"))
		assert.Equal(t, 1, countLogLinesWithAll(delta, "level=DEBUG", "oauth_rail_rejected", "error_code=already_consumed"))
	})

	t.Run("cas loss waits for winner terminal error before rendering cancel page", func(t *testing.T) {
		now := time.Date(2026, 4, 21, 18, 49, 0, 0, time.UTC)
		provider := &consumeProvider{
			err: &TokenExchangeError{
				code:       "server_error",
				message:    "upstream unavailable",
				httpStatus: http.StatusBadGateway,
			},
			blockExchange:   make(chan struct{}),
			exchangeEntered: make(chan struct{}, 1),
		}
		coord := NewCoordinatorWithClock(NewFakeClock(now), provider, newTestLogger(&bytes.Buffer{}, slog.LevelDebug))

		flow := newPendingBrowserFlow(now)
		require.NoError(t, coord.TryStartFlow(flow))

		manualDone := make(chan error, 1)
		go func() {
			_, err := coord.ConsumeCode(context.Background(), flow.ID, "code-manual", flow.State, RailManualPaste)
			manualDone <- err
		}()

		<-provider.exchangeEntered

		type response struct {
			code int
			body string
		}
		respCh := make(chan response, 1)
		go func() {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/auth/callback?code=code-loopback&state="+url.QueryEscape(flow.State), nil)
			coord.callbackHandler(flow).ServeHTTP(rec, req)
			respCh <- response{code: rec.Code, body: rec.Body.String()}
		}()

		assert.Never(t, func() bool { return len(respCh) > 0 }, 100*time.Millisecond, 10*time.Millisecond)

		close(provider.blockExchange)

		require.Error(t, <-manualDone)
		resp := <-respCh

		assert.Equal(t, http.StatusOK, resp.code)
		assert.Contains(t, resp.body, "Authentication cancelled")
		assert.Equal(t, FlowStatusError, flow.Status)
		assert.Equal(t, int32(1), provider.exchangeCalls.Load())
	})

	t.Run("cas loss waits for winner terminal success before rendering lost success page", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		now := time.Date(2026, 4, 21, 18, 49, 15, 0, time.UTC)
		provider := &consumeProvider{
			tokens: Tokens{
				AccessToken:  []byte("access-token-v1"),
				RefreshToken: []byte("refresh-token-v1"),
				IDToken: mustJWT(t, map[string]any{
					"email": "wait-success@example.com",
					"auth": map[string]any{
						"plan_type":          "chatgpt-plus",
						"chatgpt_account_id": "org_wait_success",
					},
				}),
				ExpiresIn:   30 * time.Minute,
				LastRefresh: now,
			},
			blockExchange:   make(chan struct{}),
			exchangeEntered: make(chan struct{}, 1),
		}
		coord := NewCoordinatorWithClock(NewFakeClock(now), provider, newTestLogger(&bytes.Buffer{}, slog.LevelDebug))
		coord.SetAccountStore(repo)

		flow := newPendingBrowserFlow(now)
		require.NoError(t, coord.TryStartFlow(flow))

		manualDone := make(chan error, 1)
		go func() {
			_, err := coord.ConsumeCode(context.Background(), flow.ID, "code-manual", flow.State, RailManualPaste)
			manualDone <- err
		}()

		<-provider.exchangeEntered

		type response struct {
			code int
			body string
		}
		respCh := make(chan response, 1)
		go func() {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/auth/callback?code=code-loopback&state="+url.QueryEscape(flow.State), nil)
			coord.callbackHandler(flow).ServeHTTP(rec, req)
			respCh <- response{code: rec.Code, body: rec.Body.String()}
		}()

		assert.Never(t, func() bool { return len(respCh) > 0 }, 100*time.Millisecond, 10*time.Millisecond)

		close(provider.blockExchange)

		require.NoError(t, <-manualDone)
		resp := <-respCh

		assert.Equal(t, http.StatusOK, resp.code)
		assert.Contains(t, resp.body, `data-rail="lost"`)
		assert.Contains(t, resp.body, "Authentication completed")
		assert.Equal(t, FlowStatusSuccess, flow.Status)
		assert.Equal(t, int32(1), provider.exchangeCalls.Load())
	})

	t.Run("late callback after reaper expiry does not emit duplicate expiry log", func(t *testing.T) {
		now := time.Date(2026, 4, 21, 18, 49, 30, 0, time.UTC)
		fake := NewFakeClock(now)
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(fake, &consumeProvider{}, newTestLogger(&logs, slog.LevelInfo))

		flow := newPendingBrowserFlow(now)
		require.NoError(t, coord.TryStartFlow(flow))
		coord.startExpiryReaper(flow)

		fake.Step(5*time.Minute + time.Second)
		require.Nil(t, coord.CurrentFlow())
		before := countLogLinesWithAll(logs.String(), "oauth_flow_expired")

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/auth/callback?code=code-1&state="+url.QueryEscape(flow.State), nil)
		coord.callbackHandler(flow).ServeHTTP(rec, req)

		assert.Equal(t, http.StatusGone, rec.Code)
		assert.Equal(t, "text/plain; charset=utf-8", rec.Header().Get("Content-Type"))
		assert.Equal(t, before, countLogLinesWithAll(logs.String(), "oauth_flow_expired"))
	})

	t.Run("expired access denied callback returns gone", func(t *testing.T) {
		now := time.Date(2026, 4, 21, 18, 49, 40, 0, time.UTC)
		fake := NewFakeClock(now)
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(fake, &consumeProvider{}, newTestLogger(&logs, slog.LevelInfo))

		flow := newPendingBrowserFlow(now)
		require.NoError(t, coord.TryStartFlow(flow))
		coord.startExpiryReaper(flow)

		fake.Step(5*time.Minute + time.Second)
		require.Nil(t, coord.CurrentFlow())
		before := countLogLinesWithAll(logs.String(), "oauth_flow_expired")

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/auth/callback?error=access_denied&state="+url.QueryEscape(flow.State), nil)
		coord.callbackHandler(flow).ServeHTTP(rec, req)

		assert.Equal(t, http.StatusGone, rec.Code)
		assert.Equal(t, "text/plain; charset=utf-8", rec.Header().Get("Content-Type"))
		assert.Contains(t, rec.Body.String(), "expired")
		assert.Equal(t, before, countLogLinesWithAll(logs.String(), "oauth_flow_expired"))
	})

	t.Run("active expiry fallback logging is deduped", func(t *testing.T) {
		now := time.Date(2026, 4, 21, 18, 49, 45, 0, time.UTC)
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(NewFakeClock(now), &consumeProvider{}, newTestLogger(&logs, slog.LevelInfo))

		flow := newPendingBrowserFlow(now)
		require.NoError(t, coord.TryStartFlow(flow))

		coord.logFlowExpiredIfActive(context.Background(), flow)
		coord.logFlowExpiredIfActive(context.Background(), flow)

		assert.Equal(t, 1, countLogLinesWithAll(logs.String(), "oauth_flow_expired"))
	})

	t.Run("waitForTerminalStatus returns false when context is canceled before terminal signal", func(t *testing.T) {
		flow := newPendingBrowserFlow(time.Date(2026, 4, 21, 18, 49, 50, 0, time.UTC))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		status, ok := waitForTerminalStatus(ctx, flow)
		assert.False(t, ok)
		assert.Equal(t, FlowStatusPending, status)
	})

	t.Run("wrong path returns not found without touching live flow", func(t *testing.T) {
		now := time.Date(2026, 4, 21, 18, 50, 0, 0, time.UTC)
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(NewFakeClock(now), &consumeProvider{}, newTestLogger(&logs, slog.LevelInfo))
		flow := newPendingBrowserFlow(now)
		require.NoError(t, coord.TryStartFlow(flow))

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		coord.callbackHandler(flow).ServeHTTP(rec, req)

		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.Equal(t, FlowStatusPending, flow.Status)
		assert.False(t, flow.Consumed.Load())
		assert.Empty(t, logs.String())
	})
}

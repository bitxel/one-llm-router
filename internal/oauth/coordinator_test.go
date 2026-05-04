package oauth

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/one-llm-router/internal/requestid"
)

func TestCoordinator(t *testing.T) {
	t.Run("start browser and in progress collision", func(t *testing.T) {
		var logs bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logs, nil))
		coord := NewCoordinatorWithClock(
			NewFakeClock(time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)),
			&openAIProvider{},
			logger,
		)
		coord.bindLoopback = func(http.Handler) (*http.Server, bool, int, error) {
			return &http.Server{}, true, 1455, nil
		}
		coord.SetCallbackHandler(func(*Flow) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.NotFound(w, r)
			})
		})

		ctx := requestid.WithContext(context.Background(), "req-start")
		first, err := coord.StartBrowser(ctx, "openai")
		if !assert.NoError(t, err) {
			return
		}

		assert.NotNil(t, coord.CurrentFlow())
		assert.Equal(t, FlowStatusPending, first.Status)
		assert.Equal(t, FlowBrowser, first.Method)
		assert.True(t, first.ListenerBound)
		assert.Equal(t, first.CreatedAt.Add(5*time.Minute), first.ExpiresAt)
		assert.NotEmpty(t, first.ID)
		assert.NotEmpty(t, first.AuthorizeURL())
		assert.Equal(t, openAIRedirectURI, first.CallbackURL())

		_, err = coord.StartBrowser(ctx, "openai")
		if !assert.Error(t, err) {
			return
		}
		assert.ErrorIs(t, err, ErrFlowInProgress)

		info, ok := FlowInProgressInfo(err)
		if assert.True(t, ok) {
			assert.Equal(t, first.ID, info.FlowID)
			assert.Equal(t, first.Method, info.Method)
			assert.Equal(t, first.ExpiresAt, info.ExpiresAt)
			assert.Equal(t, first.CreatedAt, info.CreatedAt)
		}

		assert.Equal(t, 1, strings.Count(logs.String(), "oauth_flow_started"))
		assert.Contains(t, logs.String(), "request_id=req-start")
		assert.Contains(t, logs.String(), "listener_bound=true")
		assert.Contains(t, logs.String(), "flow_id="+first.ID)
	})

	t.Run("in progress collision keeps canonical redirect uri", func(t *testing.T) {
		provider := &openAIProvider{}
		coord := NewCoordinatorWithClock(
			NewFakeClock(time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)),
			provider,
			slog.New(slog.NewTextHandler(io.Discard, nil)),
		)

		coord.bindLoopback = func(http.Handler) (*http.Server, bool, int, error) {
			return &http.Server{}, true, 1456, nil
		}

		first, err := coord.StartBrowser(context.Background(), "openai")
		if !assert.NoError(t, err) {
			return
		}
		assert.Equal(t, openAIRedirectURI, provider.redirectURI)
		assert.Equal(t, openAIRedirectURI, first.CallbackURL())
		assert.Contains(t, first.AuthorizeURL(), url.QueryEscape(openAIRedirectURI))

		_, err = coord.StartBrowser(context.Background(), "openai")
		if !assert.Error(t, err) {
			return
		}
		assert.ErrorIs(t, err, ErrFlowInProgress)
		assert.Equal(t, openAIRedirectURI, provider.redirectURI)
	})

	t.Run("set callback handler nil restores default handler", func(t *testing.T) {
		coord := NewCoordinatorWithClock(
			NewFakeClock(time.Date(2026, 4, 21, 12, 30, 0, 0, time.UTC)),
			&openAIProvider{},
			slog.New(slog.NewTextHandler(io.Discard, nil)),
		)

		coord.SetCallbackHandler(func(*Flow) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusTeapot)
			})
		})
		coord.SetCallbackHandler(nil)

		flow := newPendingBrowserFlow(time.Date(2026, 4, 21, 12, 30, 0, 0, time.UTC))
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/wrong-path", nil)
		coord.callbackHandlerMaker(flow).ServeHTTP(rec, req)

		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("cancel not found", func(t *testing.T) {
		coord := NewCoordinator(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		assert.ErrorIs(t, coord.Cancel("missing"), ErrFlowNotFound)

		flow := &Flow{ID: "fl_live", Method: FlowBrowser}
		assert.NoError(t, coord.TryStartFlow(flow))
		assert.ErrorIs(t, coord.Cancel("wrong-id"), ErrFlowNotFound)
	})

	t.Run("cancel success releases flow and stops expiry", func(t *testing.T) {
		base := time.Date(2026, 4, 21, 14, 0, 0, 0, time.UTC)
		fake := NewFakeClock(base)
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(fake, &openAIProvider{}, slog.New(slog.NewTextHandler(&logs, nil)))
		coord.bindLoopback = func(http.Handler) (*http.Server, bool, int, error) {
			return &http.Server{}, true, 1455, nil
		}

		flow, err := coord.StartBrowser(requestid.WithContext(context.Background(), "req-cancel"), "openai")
		if !assert.NoError(t, err) {
			return
		}

		assert.NoError(t, coord.CancelWithContext(requestid.WithContext(context.Background(), "req-cancel-action"), flow.ID))
		assert.Nil(t, coord.CurrentFlow())
		assert.True(t, flow.Consumed.Load())
		assert.Equal(t, FlowStatusError, flow.Status)
		assert.Equal(t, 1, strings.Count(logs.String(), "oauth_flow_cancelled"))
		assert.Contains(t, logs.String(), "request_id=req-cancel-action")

		fake.Step(6 * time.Minute)
		assert.Equal(t, 0, strings.Count(logs.String(), "oauth_flow_expired"))
	})

	t.Run("cancel with context waits for consumed flow to reach terminal status", func(t *testing.T) {
		coord := NewCoordinator(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		flow := &Flow{
			ID:         "fl_cancel_waits_1234567890",
			Method:     FlowBrowser,
			Status:     FlowStatusPending,
			CreatedAt:  time.Date(2026, 4, 21, 14, 30, 0, 0, time.UTC),
			ExpiresAt:  time.Date(2026, 4, 21, 14, 35, 0, 0, time.UTC),
			terminalCh: make(chan struct{}),
		}
		flow.Consumed.Store(true)
		require.NoError(t, coord.TryStartFlow(flow))

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		done := make(chan error, 1)
		go func() {
			done <- coord.CancelWithContext(ctx, flow.ID)
		}()

		select {
		case err := <-done:
			t.Fatalf("CancelWithContext returned before terminal signal: %v", err)
		case <-time.After(20 * time.Millisecond):
		}

		flow.mu.Lock()
		flow.Status = FlowStatusSuccess
		flow.ConsumedBy = RailLoopback
		flow.mu.Unlock()
		flow.signalTerminal()

		require.NoError(t, <-done)
	})

	t.Run("try start flow is safe under contention", func(t *testing.T) {
		coord := NewCoordinator(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

		var winners atomic.Int32
		var inProgress atomic.Int32
		var wg sync.WaitGroup

		for i := 0; i < 32; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				flow := &Flow{
					ID:        "fl_" + strconv.Itoa(i),
					Method:    FlowBrowser,
					CreatedAt: time.Unix(int64(i), 0).UTC(),
					ExpiresAt: time.Unix(int64(i), 0).UTC().Add(time.Minute),
				}
				err := coord.TryStartFlow(flow)
				switch {
				case err == nil:
					winners.Add(1)
				case errors.Is(err, ErrFlowInProgress):
					inProgress.Add(1)
				default:
					t.Errorf("unexpected error: %v", err)
				}
			}(i)
		}
		wg.Wait()

		assert.Equal(t, int32(1), winners.Load())
		assert.Equal(t, int32(31), inProgress.Load())
		assert.NotNil(t, coord.CurrentFlow())
	})

	t.Run("release flow idempotent", func(t *testing.T) {
		coord := NewCoordinator(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		flow := &Flow{
			ID:             "fl_release",
			Method:         FlowBrowser,
			ListenerBound:  true,
			CallbackServer: &http.Server{},
		}
		assert.NoError(t, coord.TryStartFlow(flow))

		coord.ReleaseFlow(flow.ID)
		assert.Nil(t, coord.CurrentFlow())

		coord.ReleaseFlow(flow.ID)
		assert.Nil(t, coord.CurrentFlow())
	})

	t.Run("new start clears released non-listener flow cache", func(t *testing.T) {
		coord := NewCoordinator(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

		oldFlow := &Flow{ID: "fl_old", Method: FlowBrowser, ListenerBound: false}
		assert.NoError(t, coord.TryStartFlow(oldFlow))
		coord.ReleaseFlow(oldFlow.ID)
		require.NotNil(t, coord.loadReleasedFlow(oldFlow.ID))

		newFlow := &Flow{ID: "fl_new", Method: FlowBrowser}
		assert.NoError(t, coord.TryStartFlow(newFlow))
		assert.Nil(t, coord.loadReleasedFlow(oldFlow.ID))
	})

	t.Run("expiry reaper via fake clock", func(t *testing.T) {
		base := time.Date(2026, 4, 21, 13, 0, 0, 0, time.UTC)
		fake := NewFakeClock(base)
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(fake, &openAIProvider{}, slog.New(slog.NewTextHandler(&logs, nil)))
		coord.bindLoopback = func(http.Handler) (*http.Server, bool, int, error) {
			return nil, false, 0, nil
		}

		ctx := requestid.WithContext(context.Background(), "req-expiry")
		flow, err := coord.StartBrowser(ctx, "openai")
		if !assert.NoError(t, err) {
			return
		}
		assert.Equal(t, base.Add(5*time.Minute), flow.ExpiresAt)

		stop := make(chan struct{})
		var wg sync.WaitGroup
		for range 50 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
						_ = coord.CurrentFlow()
					}
				}
			}()
		}

		fake.Step(5*time.Minute + time.Millisecond)
		assert.Eventually(t, func() bool { return coord.CurrentFlow() == nil }, time.Second, 10*time.Millisecond)
		assert.Contains(t, logs.String(), "oauth_flow_expired")
		assert.Contains(t, logs.String(), "request_id=req-expiry")

		close(stop)
		wg.Wait()
	})
}

func TestCoordinator_CancelFromProviderError_DelegatesToRailCancel(t *testing.T) {
	now := time.Date(2026, 4, 22, 13, 10, 0, 0, time.UTC)
	coord := NewCoordinatorWithClock(NewFakeClock(now), &openAIProvider{}, newTestLogger(io.Discard, slog.LevelDebug))
	flow := newPendingBrowserFlow(now)
	require.NoError(t, coord.TryStartFlow(flow))

	require.NoError(t, coord.CancelFromProviderError(context.Background(), flow.ID, RailLoopback, "access_denied"))
	assert.Nil(t, coord.CurrentFlow())

	err := coord.CancelFromProviderError(context.Background(), "missing", RailLoopback, "server_error")
	require.EqualError(t, err, `oauth.cancelFromRail: provider error "server_error" cannot cancel flow`)
}

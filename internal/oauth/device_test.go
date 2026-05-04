package oauth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/user/one-llm-router/internal/domain"
)

func TestDevice(t *testing.T) {
	t.Run("guard file contains no blocking sleep", func(t *testing.T) {
		source, err := os.ReadFile(filepath.Join(".", "device_test.go"))
		require.NoError(t, err)
		blocked := "time." + "Sleep"
		assert.NotContains(t, string(source), blocked)
	})

	t.Run("happy path polls pending twice then persists success", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		t0 := time.Date(2026, 4, 22, 13, 0, 0, 0, time.UTC)
		fake := NewFakeClock(t0)
		provider := &deviceProvider{
			now: fake.Now,
			deviceCode: DeviceCode{
				DeviceAuthID:    "dev_auth_happy_123",
				UserCode:        "ABCD-1234",
				VerificationURL: openAIDeviceVerificationURL,
				Interval:        5 * time.Second,
				ExpiresIn:       30 * time.Second,
			},
			pollSteps: []devicePollStep{
				{err: &TokenExchangeError{code: "authorization_pending", message: "pending", httpStatus: httpStatusForbidLike}},
				{err: &TokenExchangeError{code: "authorization_pending", message: "pending", httpStatus: httpStatusForbidLike}},
				{tokens: Tokens{
					AccessToken:  []byte("device-access-token"),
					RefreshToken: []byte("device-refresh-token"),
					IDToken: mustJWT(t, map[string]any{
						"email": "device@example.com",
						"https://api.openai.com/auth": map[string]any{
							"plan_type":          "chatgpt-plus",
							"chatgpt_account_id": "acct_device_123",
						},
					}),
					ExpiresIn:   2 * time.Hour,
					LastRefresh: t0.Add(15 * time.Second),
				}},
			},
		}
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(fake, provider, newTestLogger(&logs, slog.LevelInfo))
		coord.SetAccountStore(repo)

		flow, err := coord.StartDevice(context.Background(), "openai")
		require.NoError(t, err)
		require.NotNil(t, flow)
		require.Equal(t, FlowDevice, flow.Method)
		assert.Equal(t, "ABCD-1234", flow.UserCode)
		assert.Equal(t, openAIDeviceVerificationURL, flow.VerificationURL)
		assert.Equal(t, int32(0), provider.pollCalls.Load())
		assert.Equal(t, 1, countLogLinesWithAll(logs.String(), "oauth_flow_started", "method=device", "flow_id="+flow.ID))

		fake.Step(5 * time.Second)
		assert.Equal(t, int32(1), provider.pollCalls.Load())
		snapshot, err := coord.GetFlow()
		require.NoError(t, err)
		assert.Equal(t, FlowStatusPending, snapshot.Status)

		fake.Step(5 * time.Second)
		assert.Equal(t, int32(2), provider.pollCalls.Load())
		snapshot, err = coord.GetFlow()
		require.NoError(t, err)
		assert.Equal(t, FlowStatusPending, snapshot.Status)

		fake.Step(5 * time.Second)
		assert.Equal(t, int32(3), provider.pollCalls.Load())
		assert.Equal(t, []devicePollRequest{
			{DeviceAuthID: "dev_auth_happy_123", UserCode: "ABCD-1234"},
			{DeviceAuthID: "dev_auth_happy_123", UserCode: "ABCD-1234"},
			{DeviceAuthID: "dev_auth_happy_123", UserCode: "ABCD-1234"},
		}, provider.pollRequests())
		assert.Equal(t, []time.Time{
			t0.Add(5 * time.Second),
			t0.Add(10 * time.Second),
			t0.Add(15 * time.Second),
		}, provider.pollTimes())

		snapshot, err = coord.GetFlow()
		require.NoError(t, err)
		require.Equal(t, FlowStatusSuccess, snapshot.Status)
		require.NotNil(t, snapshot.Account)
		assert.Equal(t, domain.AuthMethodOAuthDevice, snapshot.Account.AuthMethod)
		assert.Equal(t, "device@example.com", snapshot.Account.Name)
		assert.Equal(t, t0.Add(15*time.Second), fake.Now())
		assert.Equal(t, 1, countLogLinesWithAll(logs.String(), "oauth_flow_completed", "method=device", "account_id="+fmt.Sprintf("%d", snapshot.Account.ID)))
		assert.Equal(t, 0, countLogLinesWithAll(logs.String(), "oauth_flow_completed", "method=device", "rail="))

		rows, err := repo.ListForAdminAPI(context.Background())
		require.NoError(t, err)
		require.Len(t, rows, 1)
		assert.Equal(t, domain.AuthMethodOAuthDevice, rows[0].AuthMethod)
		assert.Equal(t, "device@example.com", rows[0].Name)
		assert.Nil(t, coord.CurrentFlow())
	})

	t.Run("access denied cancels the flow without rail", func(t *testing.T) {
		t0 := time.Date(2026, 4, 22, 14, 0, 0, 0, time.UTC)
		fake := NewFakeClock(t0)
		provider := &deviceProvider{
			now: fake.Now,
			deviceCode: DeviceCode{
				DeviceAuthID:    "dev_auth_denied_123",
				UserCode:        "WXYZ-9876",
				VerificationURL: openAIDeviceVerificationURL,
				Interval:        5 * time.Second,
				ExpiresIn:       20 * time.Second,
			},
			pollSteps: []devicePollStep{
				{err: &TokenExchangeError{code: "access_denied", message: "denied", httpStatus: 400}},
			},
		}
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(fake, provider, newTestLogger(&logs, slog.LevelInfo))

		flow, err := coord.StartDevice(context.Background(), "openai")
		require.NoError(t, err)
		require.NotNil(t, flow)

		fake.Step(5 * time.Second)

		snapshot, err := coord.GetFlow()
		require.NoError(t, err)
		require.Equal(t, FlowStatusError, snapshot.Status)
		require.NotNil(t, snapshot.Error)
		assert.Equal(t, accessDeniedCode, snapshot.Error.Code)
		assert.Equal(t, 1, countLogLinesWithAll(logs.String(), "oauth_flow_cancelled", "method=device", "flow_id="+flow.ID))
		assert.Equal(t, 0, countLogLinesWithAll(logs.String(), "oauth_flow_cancelled", "method=device", "rail="))
		assert.Nil(t, coord.CurrentFlow())
	})

	t.Run("semantic terminal 403 and 404 stop polling immediately", func(t *testing.T) {
		cases := []struct {
			name       string
			code       string
			httpStatus int
			wantCode   string
		}{
			{name: "access_denied_403", code: "access_denied", httpStatus: http.StatusForbidden, wantCode: accessDeniedCode},
			{name: "expired_token_404", code: "expired_token", httpStatus: http.StatusNotFound, wantCode: expiredTokenCode},
		}

		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				t0 := time.Date(2026, 4, 22, 14, 30, 0, 0, time.UTC)
				fake := NewFakeClock(t0)
				provider := &deviceProvider{
					now: fake.Now,
					deviceCode: DeviceCode{
						DeviceAuthID:    "dev_auth_semantic_terminal_123",
						UserCode:        "TERM-STOP",
						VerificationURL: openAIDeviceVerificationURL,
						Interval:        5 * time.Second,
						ExpiresIn:       20 * time.Second,
					},
					pollSteps: []devicePollStep{
						{err: &TokenExchangeError{code: tc.code, message: tc.code, httpStatus: tc.httpStatus}},
					},
				}
				coord := NewCoordinatorWithClock(fake, provider, newTestLogger(&bytes.Buffer{}, slog.LevelInfo))

				flow, err := coord.StartDevice(context.Background(), "openai")
				require.NoError(t, err)
				require.NotNil(t, flow)

				fake.Step(5 * time.Second)
				snapshot, err := coord.GetFlow()
				require.NoError(t, err)
				require.Equal(t, FlowStatusError, snapshot.Status)
				require.NotNil(t, snapshot.Error)
				assert.Equal(t, tc.wantCode, snapshot.Error.Code)
				assert.Equal(t, int32(1), provider.pollCalls.Load())

				fake.Step(30 * time.Second)
				assert.Equal(t, int32(1), provider.pollCalls.Load())
				assert.Nil(t, coord.CurrentFlow())
			})
		}
	})

	t.Run("slow down doubles the current interval", func(t *testing.T) {
		t0 := time.Date(2026, 4, 22, 15, 0, 0, 0, time.UTC)
		fake := NewFakeClock(t0)
		provider := &deviceProvider{
			now: fake.Now,
			deviceCode: DeviceCode{
				DeviceAuthID:    "dev_auth_slow_123",
				UserCode:        "SLOW-DOWN",
				VerificationURL: openAIDeviceVerificationURL,
				Interval:        5 * time.Second,
				ExpiresIn:       40 * time.Second,
			},
			pollSteps: []devicePollStep{
				{err: &TokenExchangeError{code: "slow_down", message: "slow down", httpStatus: 429}},
				{err: &TokenExchangeError{code: "authorization_pending", message: "pending", httpStatus: httpStatusForbidLike}},
			},
		}
		coord := NewCoordinatorWithClock(fake, provider, newTestLogger(&bytes.Buffer{}, slog.LevelInfo))

		flow, err := coord.StartDevice(context.Background(), "openai")
		require.NoError(t, err)
		require.NotNil(t, flow)

		fake.Step(5 * time.Second)
		assert.Equal(t, int32(1), provider.pollCalls.Load())

		fake.Step(9 * time.Second)
		assert.Equal(t, int32(1), provider.pollCalls.Load())

		fake.Step(1 * time.Second)
		assert.Equal(t, int32(2), provider.pollCalls.Load())
		assert.Equal(t, []time.Time{
			t0.Add(5 * time.Second),
			t0.Add(15 * time.Second),
		}, provider.pollTimes())

		if current := coord.CurrentFlow(); current != nil {
			coord.ReleaseFlow(current.ID)
		}
	})

	t.Run("expiry wins after deadline without terminal response", func(t *testing.T) {
		t0 := time.Date(2026, 4, 22, 16, 0, 0, 0, time.UTC)
		fake := NewFakeClock(t0)
		provider := &deviceProvider{
			now: fake.Now,
			deviceCode: DeviceCode{
				DeviceAuthID:    "dev_auth_expired_123",
				UserCode:        "TIME-OUT1",
				VerificationURL: openAIDeviceVerificationURL,
				Interval:        5 * time.Second,
				ExpiresIn:       15 * time.Second,
			},
			pollSteps: []devicePollStep{
				{err: &TokenExchangeError{code: "authorization_pending", message: "pending", httpStatus: httpStatusForbidLike}},
				{err: &TokenExchangeError{code: "authorization_pending", message: "pending", httpStatus: httpStatusForbidLike}},
				{err: &TokenExchangeError{code: "authorization_pending", message: "pending", httpStatus: httpStatusForbidLike}},
			},
		}
		var logs bytes.Buffer
		coord := NewCoordinatorWithClock(fake, provider, newTestLogger(&logs, slog.LevelInfo))

		flow, err := coord.StartDevice(context.Background(), "openai")
		require.NoError(t, err)
		require.NotNil(t, flow)

		fake.Step(16 * time.Second)

		snapshot, err := coord.GetFlow()
		require.NoError(t, err)
		require.Equal(t, FlowStatusError, snapshot.Status)
		require.NotNil(t, snapshot.Error)
		assert.Equal(t, expiredTokenCode, snapshot.Error.Code)
		assert.Equal(t, 1, countLogLinesWithAll(logs.String(), "oauth_flow_expired", "method=device", "flow_id="+flow.ID))
		assert.Equal(t, 0, countLogLinesWithAll(logs.String(), "oauth_flow_expired", "method=device", "listener_bound="))
		assert.Equal(t, int32(2), provider.pollCalls.Load())
	})

	t.Run("busy flow fails before requesting a new device code", func(t *testing.T) {
		t0 := time.Date(2026, 4, 22, 16, 30, 0, 0, time.UTC)
		fake := NewFakeClock(t0)
		active := &deviceProvider{
			now: fake.Now,
			deviceCode: DeviceCode{
				DeviceAuthID:    "dev_auth_active_123",
				UserCode:        "FLOW-ONE1",
				VerificationURL: openAIDeviceVerificationURL,
				Interval:        5 * time.Second,
				ExpiresIn:       30 * time.Second,
			},
		}
		coord := NewCoordinatorWithClock(fake, active, newTestLogger(&bytes.Buffer{}, slog.LevelInfo))

		first, err := coord.StartDevice(context.Background(), "openai")
		require.NoError(t, err)
		require.NotNil(t, first)

		blocked := &deviceProvider{
			now: fake.Now,
			deviceCode: DeviceCode{
				DeviceAuthID:    "dev_auth_blocked_123",
				UserCode:        "FLOW-TWO2",
				VerificationURL: openAIDeviceVerificationURL,
				Interval:        5 * time.Second,
				ExpiresIn:       30 * time.Second,
			},
		}
		coord.provider = blocked

		_, err = coord.StartDevice(context.Background(), "openai")
		require.ErrorIs(t, err, ErrFlowInProgress)
		assert.Equal(t, int32(0), blocked.requests.Load())
	})

	t.Run("missing verification url fails fast", func(t *testing.T) {
		t0 := time.Date(2026, 4, 22, 16, 45, 0, 0, time.UTC)
		fake := NewFakeClock(t0)
		provider := &deviceProvider{
			now: fake.Now,
			deviceCode: DeviceCode{
				DeviceAuthID: "dev_auth_missing_url_123",
				UserCode:     "MISS-URL1",
				Interval:     5 * time.Second,
				ExpiresIn:    30 * time.Second,
			},
		}
		coord := NewCoordinatorWithClock(fake, provider, newTestLogger(&bytes.Buffer{}, slog.LevelInfo))

		flow, err := coord.StartDevice(context.Background(), "openai")
		require.Nil(t, flow)
		require.EqualError(t, err, "oauth.StartDevice: verificationURL is empty")
		assert.Nil(t, coord.CurrentFlow())
	})

	t.Run("release flow stops pending poll timer", func(t *testing.T) {
		t0 := time.Date(2026, 4, 22, 17, 0, 0, 0, time.UTC)
		fake := NewFakeClock(t0)
		provider := &deviceProvider{
			now: fake.Now,
			deviceCode: DeviceCode{
				DeviceAuthID:    "dev_auth_stop_123",
				UserCode:        "STOP-POLL",
				VerificationURL: openAIDeviceVerificationURL,
				Interval:        5 * time.Second,
				ExpiresIn:       30 * time.Second,
			},
		}
		coord := NewCoordinatorWithClock(fake, provider, newTestLogger(&bytes.Buffer{}, slog.LevelInfo))

		flow, err := coord.StartDevice(context.Background(), "openai")
		require.NoError(t, err)
		require.NotNil(t, flow)

		coord.ReleaseFlow(flow.ID)
		fake.Step(30 * time.Second)

		assert.Equal(t, int32(0), provider.pollCalls.Load())
		assert.Nil(t, coord.CurrentFlow())
	})

	t.Run("bare transient 403 and 404 keep polling", func(t *testing.T) {
		t0 := time.Date(2026, 4, 22, 17, 30, 0, 0, time.UTC)
		fake := NewFakeClock(t0)
		provider := &deviceProvider{
			now: fake.Now,
			deviceCode: DeviceCode{
				DeviceAuthID:    "dev_auth_retry_123",
				UserCode:        "KEEP-TRYN",
				VerificationURL: openAIDeviceVerificationURL,
				Interval:        5 * time.Second,
				ExpiresIn:       30 * time.Second,
			},
			pollSteps: []devicePollStep{
				{err: &TokenExchangeError{code: "http_403", message: http.StatusText(http.StatusForbidden), httpStatus: http.StatusForbidden}},
				{err: &TokenExchangeError{code: "http_404", message: http.StatusText(http.StatusNotFound), httpStatus: http.StatusNotFound}},
				{tokens: Tokens{
					AccessToken:  []byte("device-access-token"),
					RefreshToken: []byte("device-refresh-token"),
					IDToken:      mustJWT(t, map[string]any{"email": "retry@example.com"}),
					ExpiresIn:    time.Hour,
				}},
			},
		}
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()
		coord := NewCoordinatorWithClock(fake, provider, newTestLogger(&bytes.Buffer{}, slog.LevelInfo))
		coord.SetAccountStore(repo)

		flow, err := coord.StartDevice(context.Background(), "openai")
		require.NoError(t, err)
		require.NotNil(t, flow)

		fake.Step(5 * time.Second)
		assert.Equal(t, int32(1), provider.pollCalls.Load())
		require.NotNil(t, coord.CurrentFlow())

		fake.Step(5 * time.Second)
		assert.Equal(t, int32(2), provider.pollCalls.Load())
		require.NotNil(t, coord.CurrentFlow())

		fake.Step(5 * time.Second)
		assert.Equal(t, int32(3), provider.pollCalls.Load())

		snapshot, err := coord.GetFlow()
		require.NoError(t, err)
		require.Equal(t, FlowStatusSuccess, snapshot.Status)
	})

	t.Run("shutdown drops an in-flight device poll before persistence", func(t *testing.T) {
		repo, cleanup := setupOAuthConsumeStore(t)
		defer cleanup()

		t0 := time.Date(2026, 4, 22, 18, 0, 0, 0, time.UTC)
		fake := NewFakeClock(t0)
		pollStarted := make(chan struct{})
		releasePoll := make(chan struct{})
		stepDone := make(chan struct{})
		provider := &deviceProvider{
			now: fake.Now,
			deviceCode: DeviceCode{
				DeviceAuthID:    "dev_auth_shutdown_123",
				UserCode:        "NO-PERSST",
				VerificationURL: openAIDeviceVerificationURL,
				Interval:        5 * time.Second,
				ExpiresIn:       30 * time.Second,
			},
			pollFn: func(context.Context, string, string) (Tokens, error) {
				close(pollStarted)
				<-releasePoll
				return Tokens{
					AccessToken:  []byte("device-access-token"),
					RefreshToken: []byte("device-refresh-token"),
					IDToken: mustJWT(t, map[string]any{
						"email": "shutdown@example.com",
					}),
					ExpiresIn: time.Hour,
				}, nil
			},
		}
		coord := NewCoordinatorWithClock(fake, provider, newTestLogger(&bytes.Buffer{}, slog.LevelInfo))
		coord.SetAccountStore(repo)

		flow, err := coord.StartDevice(context.Background(), "openai")
		require.NoError(t, err)
		require.NotNil(t, flow)

		go func() {
			fake.Step(5 * time.Second)
			close(stepDone)
		}()

		<-pollStarted
		coord.Shutdown()
		close(releasePoll)
		<-stepDone

		rows, err := repo.ListForAdminAPI(context.Background())
		require.NoError(t, err)
		assert.Empty(t, rows)
		assert.Nil(t, coord.CurrentFlow())
	})
}

func TestDefaultDevicePollMessage(t *testing.T) {
	assert.Equal(t, "pending", defaultDevicePollMessage("pending", "authorization_pending", "authorization_pending"))
	assert.Equal(t, "authorization_pending", defaultDevicePollMessage("", "authorization_pending", "authorization_pending"))
	assert.Equal(t, "expired_token", defaultDevicePollMessage("", "", "expired_token"))
	assert.Equal(t, "authorization_pending", defaultDevicePollMessage("", "", ""))
}

const httpStatusForbidLike = 403

type devicePollStep struct {
	tokens Tokens
	err    error
}

type devicePollRequest struct {
	DeviceAuthID string
	UserCode     string
}

type deviceProvider struct {
	now         func() time.Time
	deviceCode  DeviceCode
	requestErr  error
	requests    atomic.Int32
	pollCalls   atomic.Int32
	mu          sync.Mutex
	pollSteps   []devicePollStep
	pollHistory []time.Time
	pollArgs    []devicePollRequest
	pollFn      func(context.Context, string, string) (Tokens, error)
}

func (p *deviceProvider) BuildAuthorizeURL(string, string) (string, error) {
	return "", errors.New("deviceProvider.BuildAuthorizeURL: not implemented")
}

func (p *deviceProvider) ExchangeCode(context.Context, string, string) (Tokens, error) {
	return Tokens{}, errors.New("deviceProvider.ExchangeCode: not implemented")
}

func (p *deviceProvider) Refresh(context.Context, []byte) (Tokens, error) {
	return Tokens{}, errors.New("deviceProvider.Refresh: not implemented")
}

func (p *deviceProvider) RequestDeviceCode(context.Context) (DeviceCode, error) {
	p.requests.Add(1)
	if p.requestErr != nil {
		return DeviceCode{}, p.requestErr
	}
	return p.deviceCode, nil
}

func (p *deviceProvider) PollDeviceCode(ctx context.Context, deviceAuthID, userCode string) (Tokens, error) {
	p.pollCalls.Add(1)

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.now != nil {
		p.pollHistory = append(p.pollHistory, p.now())
	}
	p.pollArgs = append(p.pollArgs, devicePollRequest{
		DeviceAuthID: deviceAuthID,
		UserCode:     userCode,
	})
	if p.pollFn != nil {
		return p.pollFn(ctx, deviceAuthID, userCode)
	}
	if len(p.pollSteps) == 0 {
		return Tokens{}, &TokenExchangeError{code: "authorization_pending", message: "pending", httpStatus: httpStatusForbidLike}
	}
	step := p.pollSteps[0]
	p.pollSteps = p.pollSteps[1:]
	if step.err != nil {
		return Tokens{}, step.err
	}
	if step.tokens.LastRefresh.IsZero() && p.now != nil {
		step.tokens.LastRefresh = p.now().UTC()
	}
	return step.tokens, nil
}

func (p *deviceProvider) pollTimes() []time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]time.Time, len(p.pollHistory))
	copy(out, p.pollHistory)
	return out
}

func (p *deviceProvider) pollRequests() []devicePollRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]devicePollRequest, len(p.pollArgs))
	copy(out, p.pollArgs)
	return out
}

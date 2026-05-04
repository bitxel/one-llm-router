package oauthapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/user/one-llm-router/internal/api/errcode"
	"github.com/user/one-llm-router/internal/api/testutil"
	generatedadminapi "github.com/user/one-llm-router/internal/generated/adminapi"
	"github.com/user/one-llm-router/internal/oauth"
)

func TestStartDeviceContract(t *testing.T) {
	t.Run("happy path returns device start envelope with canonical keys", func(t *testing.T) {
		coord, server := newStartDeviceHarness(t, nil)

		req := httptest.NewRequest(
			http.MethodPost,
			"/api/admin/oauth/device/start",
			strings.NewReader(`{"provider":"openai","ignored":"future"}`),
		)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		server.ServeHTTP(rec, req)

		data := testutil.AssertEnvelopeDataShape(t, rec, 0)
		require.NotContains(t, data, "interval")
		require.NotContains(t, data, "verification_uri_complete")

		body := decodeDeviceStartResponse(t, rec)
		env, err := body.AsDeviceStartEnvelope()
		require.NoError(t, err)

		require.Equal(t, "ABCD-1234", env.Data.UserCode)
		require.Equal(t, "https://auth.openai.com/codex/device", env.Data.VerificationUrl)
		require.Equal(t, 5, env.Data.IntervalSeconds)
		require.Equal(t, generatedadminapi.DeviceStartEnvelopeDataMethodDevice, env.Data.Method)

		flow := coord.CurrentFlow()
		require.NotNil(t, flow)
		assert.Equal(t, flow.ID, string(env.Data.FlowId))
		assert.Equal(t, flow.ExpiresAt, env.Data.ExpiresAt)
	})

	t.Run("in progress collision returns existing device flow metadata", func(t *testing.T) {
		coord, server := newStartDeviceHarness(t, nil)

		first, err := coord.StartDevice(context.Background(), "openai")
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/api/admin/oauth/device/start", strings.NewReader(`{"provider":"openai"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		server.ServeHTTP(rec, req)

		data := testutil.AssertEnvelopeDataShape(t, rec, 3001)
		require.Len(t, data, 4)

		body := decodeDeviceStartResponse(t, rec)
		env, err := body.AsFlowInProgressEnvelope()
		require.NoError(t, err)
		assert.Equal(t, first.ID, env.Data.FlowId)
		assert.Equal(t, generatedadminapi.Device, env.Data.Method)
	})

	t.Run("device auth unavailable maps 404 to 3015", func(t *testing.T) {
		server := newOAuthAPIServerForTest(&stubCoordinator{
			startDeviceFn: func(context.Context, string) (*oauth.Flow, error) {
				return nil, startDeviceExchangeError{code: "device_auth_unavailable", message: "missing plan", httpStatus: http.StatusNotFound}
			},
		})

		req := httptest.NewRequest(http.MethodPost, "/api/admin/oauth/device/start", strings.NewReader(`{"provider":"openai"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		server.ServeHTTP(rec, req)

		data := testutil.AssertEnvelopeDataShape(t, rec, 3015)
		require.Len(t, data, 1)

		body := decodeDeviceStartResponse(t, rec)
		env, err := body.AsDeviceAuthUnavailableEnvelope()
		require.NoError(t, err)
		assert.Equal(t, generatedadminapi.DeviceAuthUnavailableEnvelopeDataProviderOpenai, env.Data.Provider)
	})

	t.Run("invalid provider is business error with field-only data", func(t *testing.T) {
		coord, server := newStartDeviceHarness(t, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/admin/oauth/device/start", strings.NewReader(`{"provider":"claude"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		server.ServeHTTP(rec, req)

		data := testutil.AssertEnvelopeDataShape(t, rec, 3002)
		require.Len(t, data, 1)

		body := decodeDeviceStartResponse(t, rec)
		env, err := body.AsInvalidOAuthProviderEnvelope()
		require.NoError(t, err)
		assert.Equal(t, generatedadminapi.Provider, env.Data.Field)
		assert.Nil(t, coord.CurrentFlow())
	})

	t.Run("upstream 500 maps to oauth upstream error", func(t *testing.T) {
		server := newOAuthAPIServerForTest(&stubCoordinator{
			startDeviceFn: func(context.Context, string) (*oauth.Flow, error) {
				return nil, startDeviceExchangeError{code: "server_error", message: "boom", httpStatus: http.StatusInternalServerError}
			},
		})

		req := httptest.NewRequest(http.MethodPost, "/api/admin/oauth/device/start", strings.NewReader(`{"provider":"openai"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		server.ServeHTTP(rec, req)

		testutil.AssertEnvelopeDataShape(t, rec, 3016)
		body := decodeDeviceStartResponse(t, rec)
		env, err := body.AsOAuthUpstreamErrorEnvelope()
		require.NoError(t, err)
		assert.Equal(t, "server_error", env.Data.ProviderError)
		assert.Equal(t, "boom", *env.Data.ProviderMessage)
		require.NotNil(t, env.Data.HttpStatus)
		assert.Equal(t, 500, *env.Data.HttpStatus)
	})

	t.Run("unexpected start failure is oauth internal error", func(t *testing.T) {
		server := newOAuthAPIServerForTest(&stubCoordinator{
			startDeviceFn: func(context.Context, string) (*oauth.Flow, error) {
				return nil, errors.New("device request exploded")
			},
		})

		req := httptest.NewRequest(http.MethodPost, "/api/admin/oauth/device/start", strings.NewReader(`{"provider":"openai"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		server.ServeHTTP(rec, req)

		testutil.AssertEnvelopeDataShape(t, rec, errcode.OAuthInternalError)
	})
}

func newStartDeviceHarness(t *testing.T, logger *slog.Logger) (*oauth.Coordinator, http.Handler) {
	t.Helper()

	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	fake := oauth.NewFakeClock(time.Date(2026, 4, 21, 16, 0, 0, 0, time.UTC))
	provider := &startDeviceProvider{
		deviceCode: oauth.DeviceCode{
			DeviceAuthID:    "dev_contract_123",
			UserCode:        "ABCD-1234",
			VerificationURL: "https://auth.openai.com/codex/device",
			Interval:        5 * time.Second,
			ExpiresIn:       15 * time.Minute,
		},
	}
	coord := oauth.NewCoordinatorWithClock(fake, provider, logger)
	server := generatedadminapi.HandlerFromMuxWithEnvelope(NewHandler(coord), http.NewServeMux())

	t.Cleanup(func() {
		if flow := coord.CurrentFlow(); flow != nil {
			_ = coord.Cancel(flow.ID)
		}
	})
	return coord, server
}

func decodeDeviceStartResponse(t *testing.T, rec *httptest.ResponseRecorder) generatedadminapi.DeviceStartResponseBody {
	t.Helper()

	var body generatedadminapi.DeviceStartResponseBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body
}

type startDeviceProvider struct {
	deviceCode oauth.DeviceCode
	requestErr error
}

type startDeviceExchangeError struct {
	code       string
	message    string
	httpStatus int
}

func (e startDeviceExchangeError) Error() string { return e.message }
func (e startDeviceExchangeError) Code() string  { return e.code }
func (e startDeviceExchangeError) Message() string {
	return e.message
}
func (e startDeviceExchangeError) HTTPStatus() int { return e.httpStatus }

func (*startDeviceProvider) BuildAuthorizeURL(string, string) (string, error) {
	return "", errors.New("startDeviceProvider.BuildAuthorizeURL: not implemented")
}

func (*startDeviceProvider) ExchangeCode(context.Context, string, string) (oauth.Tokens, error) {
	return oauth.Tokens{}, errors.New("startDeviceProvider.ExchangeCode: not implemented")
}

func (*startDeviceProvider) Refresh(context.Context, []byte) (oauth.Tokens, error) {
	return oauth.Tokens{}, errors.New("startDeviceProvider.Refresh: not implemented")
}

func (p *startDeviceProvider) RequestDeviceCode(context.Context) (oauth.DeviceCode, error) {
	if p.requestErr != nil {
		return oauth.DeviceCode{}, p.requestErr
	}
	return p.deviceCode, nil
}

func (p *startDeviceProvider) PollDeviceCode(context.Context, string, string) (oauth.Tokens, error) {
	return oauth.Tokens{}, startDeviceExchangeError{code: "authorization_pending", message: "pending", httpStatus: http.StatusForbidden}
}

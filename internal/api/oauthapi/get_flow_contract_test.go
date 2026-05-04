package oauthapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/user/one-llm-router/internal/api/errcode"

	"github.com/user/one-llm-router/internal/api/testutil"
	"github.com/user/one-llm-router/internal/domain"
	generatedadminapi "github.com/user/one-llm-router/internal/generated/adminapi"
	"github.com/user/one-llm-router/internal/oauth"
)

func TestGetFlowContract(t *testing.T) {
	base := time.Date(2026, 4, 21, 18, 0, 0, 0, time.UTC)

	t.Run("idle shape", func(t *testing.T) {
		server := newGetFlowServer(oauth.FlowSnapshot{Status: oauth.FlowStatusIdle}, nil)

		rec := getFlow(t, server)

		data := testutil.AssertEnvelopeDataShape(t, rec, 0)
		assert.Equal(t, map[string]any{
			"status": "idle",
		}, data)

		env := decodeFlowStatusResponse(t, rec)
		_, err := env.Data.AsFlowStatusIdle()
		require.NoError(t, err)
	})

	t.Run("pending browser shape", func(t *testing.T) {
		createdAt := base
		expiresAt := createdAt.Add(5 * time.Minute)
		server := newGetFlowServer(oauth.FlowSnapshot{
			Status:        oauth.FlowStatusPending,
			Method:        oauth.FlowBrowser,
			FlowID:        "fl_pending_browser_status_1234567890",
			ListenerBound: true,
			CreatedAt:     createdAt,
			ExpiresAt:     expiresAt,
		}, nil)

		rec := getFlow(t, server)

		data := testutil.AssertEnvelopeDataShape(t, rec, 0)
		assert.Equal(t, map[string]any{
			"status":         "pending",
			"method":         "browser",
			"flow_id":        "fl_pending_browser_status_1234567890",
			"listener_bound": true,
			"expires_at":     jsonTime(expiresAt),
			"created_at":     jsonTime(createdAt),
		}, data)

		env := decodeFlowStatusResponse(t, rec)
		body, err := env.Data.AsFlowStatusPendingBrowser()
		require.NoError(t, err)
		assert.Equal(t, generatedadminapi.FlowStatusPendingBrowserMethodBrowser, body.Method)
		assert.Equal(t, generatedadminapi.FlowStatusPendingBrowserStatusPending, body.Status)
	})

	t.Run("pending device shape", func(t *testing.T) {
		createdAt := base.Add(10 * time.Minute)
		expiresAt := createdAt.Add(15 * time.Minute)
		server := newGetFlowServer(oauth.FlowSnapshot{
			Status:          oauth.FlowStatusPending,
			Method:          oauth.FlowDevice,
			FlowID:          "fl_pending_device_status_1234567890",
			CreatedAt:       createdAt,
			ExpiresAt:       expiresAt,
			UserCode:        "ABCD-1234",
			VerificationURL: "https://auth.openai.com/codex/device",
		}, nil)

		rec := getFlow(t, server)

		data := testutil.AssertEnvelopeDataShape(t, rec, 0)
		assert.Equal(t, map[string]any{
			"status":           "pending",
			"method":           "device",
			"flow_id":          "fl_pending_device_status_1234567890",
			"user_code":        "ABCD-1234",
			"verification_url": "https://auth.openai.com/codex/device",
			"expires_at":       jsonTime(expiresAt),
			"created_at":       jsonTime(createdAt),
		}, data)

		env := decodeFlowStatusResponse(t, rec)
		body, err := env.Data.AsFlowStatusPendingDevice()
		require.NoError(t, err)
		assert.Equal(t, generatedadminapi.FlowStatusPendingDeviceMethodDevice, body.Method)
		assert.Equal(t, generatedadminapi.FlowStatusPendingDeviceStatusPending, body.Status)
	})

	t.Run("success shape returns account and rail", func(t *testing.T) {
		accountCreated := base.Add(-2 * time.Hour)
		accountUpdated := base.Add(-time.Hour)
		server := newGetFlowServer(oauth.FlowSnapshot{
			Status: oauth.FlowStatusSuccess,
			Method: oauth.FlowBrowser,
			FlowID: "fl_success_status_1234567890",
			Rail:   oauth.RailLoopback,
			Account: &domain.UpstreamAccount{
				ID:         77,
				Name:       "op@example.com",
				Provider:   domain.ProviderOpenAI,
				Status:     domain.AccountStatusActive,
				AuthMethod: domain.AuthMethodOAuthBrowser,
				CreatedAt:  accountCreated,
				UpdatedAt:  accountUpdated,
			},
		}, nil)

		rec := getFlow(t, server)

		data := testutil.AssertEnvelopeDataShape(t, rec, 0)
		assert.Equal(t, map[string]any{
			"status":  "success",
			"method":  "browser",
			"flow_id": "fl_success_status_1234567890",
			"rail":    "loopback",
			"account": map[string]any{
				"id":          float64(77),
				"name":        "op@example.com",
				"provider":    "openai",
				"auth_method": "oauth_browser",
				"status":      "active",
				"created_at":  jsonTime(accountCreated),
				"updated_at":  jsonTime(accountUpdated),
			},
		}, data)

		env := decodeFlowStatusResponse(t, rec)
		body, err := env.Data.AsFlowStatusSuccess()
		require.NoError(t, err)
		require.NotNil(t, body.Rail)
		assert.Equal(t, generatedadminapi.Loopback, *body.Rail)
		assert.Equal(t, generatedadminapi.AccountListItemAuthMethodOauthBrowser, body.Account.AuthMethod)
	})

	t.Run("device success omits rail and preserves method", func(t *testing.T) {
		accountCreated := base.Add(-30 * time.Minute)
		accountUpdated := base.Add(-10 * time.Minute)
		server := newGetFlowServer(oauth.FlowSnapshot{
			Status: oauth.FlowStatusSuccess,
			Method: oauth.FlowDevice,
			FlowID: "fl_device_success_status_1234567890",
			Account: &domain.UpstreamAccount{
				ID:         88,
				Name:       "device@example.com",
				Provider:   domain.ProviderOpenAI,
				Status:     domain.AccountStatusActive,
				AuthMethod: domain.AuthMethodOAuthDevice,
				CreatedAt:  accountCreated,
				UpdatedAt:  accountUpdated,
			},
		}, nil)

		rec := getFlow(t, server)

		data := testutil.AssertEnvelopeDataShape(t, rec, 0)
		assert.Equal(t, map[string]any{
			"status":  "success",
			"method":  "device",
			"flow_id": "fl_device_success_status_1234567890",
			"account": map[string]any{
				"id":          float64(88),
				"name":        "device@example.com",
				"provider":    "openai",
				"auth_method": "oauth_device",
				"status":      "active",
				"created_at":  jsonTime(accountCreated),
				"updated_at":  jsonTime(accountUpdated),
			},
		}, data)
		assert.NotContains(t, data, "rail")

		env := decodeFlowStatusResponse(t, rec)
		body, err := env.Data.AsFlowStatusSuccess()
		require.NoError(t, err)
		assert.Equal(t, generatedadminapi.Device, body.Method)
		assert.Nil(t, body.Rail)
		assert.Equal(t, generatedadminapi.AccountListItemAuthMethodOauthDevice, body.Account.AuthMethod)
	})

	t.Run("error shape", func(t *testing.T) {
		server := newGetFlowServer(oauth.FlowSnapshot{
			Status: oauth.FlowStatusError,
			Method: oauth.FlowDevice,
			FlowID: "fl_error_status_1234567890",
			Error: &oauth.FlowTerminalError{
				Code:    "access_denied",
				Message: "User denied consent",
			},
		}, nil)

		rec := getFlow(t, server)

		data := testutil.AssertEnvelopeDataShape(t, rec, 0)
		assert.Equal(t, map[string]any{
			"status":  "error",
			"method":  "device",
			"flow_id": "fl_error_status_1234567890",
			"error": map[string]any{
				"code":    "access_denied",
				"message": "User denied consent",
			},
		}, data)

		env := decodeFlowStatusResponse(t, rec)
		body, err := env.Data.AsFlowStatusError()
		require.NoError(t, err)
		assert.Equal(t, generatedadminapi.Device, body.Method)
		assert.Equal(t, "access_denied", body.Error.Code)
		assert.Equal(t, "User denied consent", body.Error.Message)
	})

	t.Run("unexpected snapshot failure is oauth internal error", func(t *testing.T) {
		server := newGetFlowServer(oauth.FlowSnapshot{}, errors.New("snapshot lost"))

		rec := requestFlow(server)

		testutil.AssertEnvelopeDataShape(t, rec, errcode.OAuthInternalError)
	})
}

type getFlowCoordinator struct {
	snapshot oauth.FlowSnapshot
	err      error
}

func (c *getFlowCoordinator) StartBrowser(context.Context, string) (*oauth.Flow, error) {
	return nil, assert.AnError
}

func (*getFlowCoordinator) StartDevice(context.Context, string) (*oauth.Flow, error) {
	return nil, assert.AnError
}

func (*getFlowCoordinator) BrowserCallbackSnapshot(string) (oauth.BrowserCallbackSnapshot, bool) {
	return oauth.BrowserCallbackSnapshot{}, false
}

func (*getFlowCoordinator) CancelWithContext(context.Context, string) error {
	return assert.AnError
}

func (*getFlowCoordinator) CurrentFlow() *oauth.Flow {
	return nil
}

func (c *getFlowCoordinator) GetFlow() (oauth.FlowSnapshot, error) {
	return c.snapshot, c.err
}

func (*getFlowCoordinator) Now() time.Time {
	return time.Time{}
}

func (*getFlowCoordinator) ConsumeCode(context.Context, string, string, string, oauth.Rail) (*domain.UpstreamAccount, error) {
	return nil, assert.AnError
}

func (*getFlowCoordinator) CancelFromProviderError(context.Context, string, oauth.Rail, string) error {
	return assert.AnError
}

func (*getFlowCoordinator) WinnerRail(string) (oauth.Rail, bool) {
	return oauth.RailUnknown, false
}

func newGetFlowServer(snapshot oauth.FlowSnapshot, err error) http.Handler {
	return generatedadminapi.HandlerFromMuxWithEnvelope(NewHandler(&getFlowCoordinator{
		snapshot: snapshot,
		err:      err,
	}), http.NewServeMux())
}

func getFlow(t *testing.T, server http.Handler) *httptest.ResponseRecorder {
	t.Helper()

	rec := requestFlow(server)
	require.Equal(t, http.StatusOK, rec.Code)
	return rec
}

func requestFlow(server http.Handler) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/admin/oauth/flow?ignored=true", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	return rec
}

func decodeFlowStatusResponse(t *testing.T, rec *httptest.ResponseRecorder) generatedadminapi.FlowStatusEnvelope {
	t.Helper()

	var body generatedadminapi.FlowStatusEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body
}

func jsonTime(v time.Time) string {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return strings.Trim(string(raw), `"`)
}

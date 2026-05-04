package oauthapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/user/one-llm-router/internal/domain"
	generatedadminapi "github.com/user/one-llm-router/internal/generated/adminapi"
	"github.com/user/one-llm-router/internal/oauth"
	"github.com/user/one-llm-router/internal/requestid"
	"github.com/user/one-llm-router/internal/store"
)

func TestManualCallbackContract(t *testing.T) {
	t.Run("happy path returns oauth browser account", func(t *testing.T) {
		h := newManualCallbackHarness(t)
		h.provider.exchangeTokens = oauth.Tokens{
			AccessToken:  []byte("access"),
			RefreshToken: []byte("refresh"),
			IDToken:      testIDToken(t, "op@example.com", "chatgpt-plus", "acct_123"),
			LastRefresh:  h.now,
			ExpiresIn:    30 * time.Minute,
		}
		require.NoError(t, h.coord.TryStartFlow(h.newFlow("s_happy", h.now.Add(5*time.Minute))))

		rec := h.post(t, "req-happy", `{"callback_url":"http://localhost:1455/auth/callback?code=abc123&state=s_happy"}`)

		data := testutil.AssertEnvelopeDataShape(t, rec, 0)
		assert.Equal(t, float64(77), data["account"].(map[string]any)["id"])
		assert.Equal(t, "oauth_browser", data["account"].(map[string]any)["auth_method"])
		assert.Equal(t, "manual_paste", data["rail"])
		assert.Equal(t, "success", data["status"])

		body := decodeManualCallbackResponse(t, rec)
		env, err := body.AsManualCallbackSuccessEnvelope()
		require.NoError(t, err)
		assert.Equal(t, generatedadminapi.AccountListItemAuthMethodOauthBrowser, env.Data.Account.AuthMethod)
		assert.Equal(t, "ChatGPT Plus", derefString(env.Data.Account.PlanTypeLabel))
		assert.Equal(t, generatedadminapi.ManualCallbackSuccessEnvelopeDataRailManualPaste, env.Data.Rail)
		assert.Equal(t, generatedadminapi.ManualCallbackSuccessEnvelopeDataStatusSuccess, env.Data.Status)
		assert.Equal(t, 1, h.provider.exchangeCalls)
		assert.Nil(t, h.coord.CurrentFlow())
	})

	t.Run("state mismatch rejects before mutation and logs", func(t *testing.T) {
		h := newManualCallbackHarness(t)
		require.NoError(t, h.coord.TryStartFlow(h.newFlow("s_expected", h.now.Add(5*time.Minute))))

		rec := h.post(t, "req-state-mismatch", `{"callback_url":"http://localhost:1455/auth/callback?code=abc123&state=s_wrong"}`)

		testutil.AssertEnvelopeDataShape(t, rec, 3003)
		assert.Equal(t, 0, h.provider.exchangeCalls)
		assert.Contains(t, h.logs.String(), "oauth_rail_rejected")
		assert.Contains(t, h.logs.String(), "rail=manual_paste")
		assert.Contains(t, h.logs.String(), "error_code=oauth_state_mismatch")
		current := h.coord.CurrentFlow()
		require.NotNil(t, current)
		assert.Equal(t, "fl_manual_callback_12345678901234567890", current.ID)
		assert.Equal(t, oauth.FlowStatusPending, current.Status)
	})

	t.Run("code path cas loss returns already consumed winner rail", func(t *testing.T) {
		h := newManualCallbackHarness(t)
		flow := h.newFlow("s_cas_loss", h.now.Add(5*time.Minute))
		flow.Consumed.Store(true)
		flow.ConsumedBy = oauth.RailLoopback
		flow.Status = oauth.FlowStatusSuccess
		require.NoError(t, h.coord.TryStartFlow(flow))

		rec := h.post(t, "req-cas-loss", `{"callback_url":"http://localhost:1455/auth/callback?code=abc123&state=s_cas_loss"}`)

		data := testutil.AssertEnvelopeDataShape(t, rec, 3005)
		assert.Equal(t, "loopback", data["rail_won"])
		assert.Equal(t, 0, h.provider.exchangeCalls)
	})

	t.Run("access denied with matching state cancels flow", func(t *testing.T) {
		h := newManualCallbackHarness(t)
		require.NoError(t, h.coord.TryStartFlow(h.newFlow("s_cancel", h.now.Add(5*time.Minute))))

		rec := h.post(t, "req-cancel", `{"callback_url":"http://localhost:1455/auth/callback?error=access_denied&state=s_cancel"}`)

		data := testutil.AssertEnvelopeDataShape(t, rec, 0)
		assert.Equal(t, "manual_paste", data["rail"])
		assert.Equal(t, "cancelled", data["status"])
		assert.Contains(t, h.logs.String(), "oauth_flow_cancelled")
		assert.Contains(t, h.logs.String(), "rail=manual_paste")
		assert.Nil(t, h.coord.CurrentFlow())
	})

	t.Run("access denied cas loss returns already consumed", func(t *testing.T) {
		h := newManualCallbackHarness(t)
		flow := h.newFlow("s_access_denied_lost", h.now.Add(5*time.Minute))
		flow.Consumed.Store(true)
		flow.ConsumedBy = oauth.RailLoopback
		flow.Status = oauth.FlowStatusSuccess
		require.NoError(t, h.coord.TryStartFlow(flow))

		rec := h.post(t, "req-cancel-lost", `{"callback_url":"http://localhost:1455/auth/callback?error=access_denied&state=s_access_denied_lost"}`)

		data := testutil.AssertEnvelopeDataShape(t, rec, 3005)
		assert.Equal(t, "loopback", data["rail_won"])
	})

	t.Run("access denied state mismatch does not cancel", func(t *testing.T) {
		h := newManualCallbackHarness(t)
		require.NoError(t, h.coord.TryStartFlow(h.newFlow("s_expected_cancel", h.now.Add(5*time.Minute))))

		rec := h.post(t, "req-cancel-mismatch", `{"callback_url":"http://localhost:1455/auth/callback?error=access_denied&state=s_wrong"}`)

		testutil.AssertEnvelopeDataShape(t, rec, 3003)
		assert.Contains(t, h.logs.String(), "oauth_rail_rejected")
		assert.NotContains(t, h.logs.String(), "oauth_flow_cancelled")
		assert.NotNil(t, h.coord.CurrentFlow())
	})

	t.Run("provider error query returns upstream error and leaves flow pending", func(t *testing.T) {
		h := newManualCallbackHarness(t)
		require.NoError(t, h.coord.TryStartFlow(h.newFlow("s_provider_error", h.now.Add(5*time.Minute))))

		rec := h.post(t, "req-provider-error", `{"callback_url":"http://localhost:1455/auth/callback?error=server_error&error_description=provider+said+no&state=s_provider_error"}`)

		data := testutil.AssertEnvelopeDataShape(t, rec, 3016)
		assert.Equal(t, "server_error", data["provider_error"])
		assert.Equal(t, "provider said no", data["provider_message"])
		assert.Equal(t, 0, h.provider.exchangeCalls)
		assert.Contains(t, h.logs.String(), "oauth_rail_rejected")
		assert.Contains(t, h.logs.String(), "error_code=server_error")
		current := h.coord.CurrentFlow()
		require.NotNil(t, current)
		assert.Equal(t, "fl_manual_callback_12345678901234567890", current.ID)
		assert.Equal(t, oauth.FlowStatusPending, current.Status)
	})

	t.Run("registered localhost callback port reaches business logic", func(t *testing.T) {
		h := newManualCallbackHarness(t)
		require.NoError(t, h.coord.TryStartFlow(h.newFlow("s_allowed_port", h.now.Add(5*time.Minute))))

		rec := h.post(t, "req-allowed-port", `{"callback_url":"http://localhost:1455/auth/callback?error=server_error&state=s_allowed_port"}`)

		data := testutil.AssertEnvelopeDataShape(t, rec, 3016)
		assert.Equal(t, "server_error", data["provider_error"])
	})

	t.Run("non registered localhost callback ports are rejected", func(t *testing.T) {
		for _, port := range []string{"1454", "1456", "1457", "1458", "1459"} {
			t.Run(port, func(t *testing.T) {
				h := newManualCallbackHarness(t)
				require.NoError(t, h.coord.TryStartFlow(h.newFlow("s_bad_port", h.now.Add(5*time.Minute))))

				rec := h.post(t, "req-bad-port", fmt.Sprintf(
					`{"callback_url":"http://localhost:%s/auth/callback?code=abc123&state=s_bad_port"}`,
					port,
				))

				data := testutil.AssertEnvelopeDataShape(t, rec, 3007)
				assert.Equal(t, "url_prefix_mismatch", data["reason"])
			})
		}
	})

	t.Run("invalid grant on exchange maps to oauth invalid grant", func(t *testing.T) {
		h := newManualCallbackHarness(t)
		h.provider.exchangeErr = fakeExchangeError{code: "invalid_grant", message: "bad verifier"}
		require.NoError(t, h.coord.TryStartFlow(h.newFlow("s_invalid_grant", h.now.Add(5*time.Minute))))

		rec := h.post(t, "req-invalid-grant", `{"callback_url":"http://localhost:1455/auth/callback?code=bad&state=s_invalid_grant"}`)

		data := testutil.AssertEnvelopeDataShape(t, rec, 3009)
		assert.Equal(t, "invalid_grant", data["provider_error"])
		assert.Equal(t, "bad verifier", data["provider_message"])
		assert.Nil(t, h.coord.CurrentFlow())
	})

	t.Run("non invalid grant exchange failure maps to oauth upstream error", func(t *testing.T) {
		h := newManualCallbackHarness(t)
		h.provider.exchangeErr = fakeExchangeError{code: "server_error", message: "upstream 502", httpStatus: 502}
		require.NoError(t, h.coord.TryStartFlow(h.newFlow("s_exchange_error", h.now.Add(5*time.Minute))))

		rec := h.post(t, "req-exchange-error", `{"callback_url":"http://localhost:1455/auth/callback?code=bad&state=s_exchange_error"}`)

		data := testutil.AssertEnvelopeDataShape(t, rec, 3016)
		assert.Equal(t, "server_error", data["provider_error"])
		assert.Equal(t, "upstream 502", data["provider_message"])
		assert.Equal(t, float64(502), data["http_status"])
		assert.Nil(t, h.coord.CurrentFlow())
	})

	t.Run("invalid callback url prefix is rejected", func(t *testing.T) {
		h := newManualCallbackHarness(t)

		rec := h.post(t, "req-invalid-prefix", `{"callback_url":"https://evil.com/auth/callback?code=abc123&state=s_any"}`)

		data := testutil.AssertEnvelopeDataShape(t, rec, 3007)
		assert.Equal(t, "url_prefix_mismatch", data["reason"])
	})

	t.Run("missing code and error is rejected", func(t *testing.T) {
		h := newManualCallbackHarness(t)

		rec := h.post(t, "req-missing-code-and-error", `{"callback_url":"http://localhost:1455/auth/callback?state=s_any"}`)

		data := testutil.AssertEnvelopeDataShape(t, rec, 3007)
		assert.Equal(t, "missing_code_and_error", data["reason"])
	})

	t.Run("no flow in progress returns no flow envelope", func(t *testing.T) {
		h := newManualCallbackHarness(t)

		rec := h.post(t, "req-no-flow", `{"callback_url":"http://localhost:1455/auth/callback?code=abc123&state=s_any"}`)

		testutil.AssertEnvelopeDataShape(t, rec, 3004)
	})

	t.Run("expired flow returns flow expired envelope", func(t *testing.T) {
		h := newManualCallbackHarness(t)
		require.NoError(t, h.coord.TryStartFlow(h.newFlow("s_expired", time.Now().UTC().Add(-time.Minute))))

		rec := h.post(t, "req-expired", `{"callback_url":"http://localhost:1455/auth/callback?code=abc123&state=s_expired"}`)

		testutil.AssertEnvelopeDataShape(t, rec, 3006)
	})

	t.Run("released loopback success still returns already consumed", func(t *testing.T) {
		h := newManualCallbackHarness(t)
		flow := h.newFlow("s_released_success", h.now.Add(5*time.Minute))
		flow.Consumed.Store(true)
		flow.ConsumedBy = oauth.RailLoopback
		flow.Status = oauth.FlowStatusSuccess
		require.NoError(t, h.coord.TryStartFlow(flow))
		h.coord.ReleaseFlow(flow.ID)

		rec := h.post(t, "req-released-success", `{"callback_url":"http://localhost:1455/auth/callback?code=abc123&state=s_released_success"}`)

		data := testutil.AssertEnvelopeDataShape(t, rec, 3005)
		assert.Equal(t, "loopback", data["rail_won"])
	})

	t.Run("released expired flow still returns flow expired", func(t *testing.T) {
		h := newManualCallbackHarness(t)
		flow := h.newFlow("s_released_expired", h.now.Add(-time.Minute))
		flow.Consumed.Store(true)
		flow.Status = oauth.FlowStatusError
		require.NoError(t, h.coord.TryStartFlow(flow))
		h.coord.ReleaseFlow(flow.ID)

		rec := h.post(t, "req-released-expired", `{"callback_url":"http://localhost:1455/auth/callback?code=abc123&state=s_released_expired"}`)

		testutil.AssertEnvelopeDataShape(t, rec, 3006)
	})

	t.Run("store failure is oauth store failed system error", func(t *testing.T) {
		now := time.Date(2026, 4, 21, 22, 0, 0, 0, time.UTC)
		server := newOAuthAPIServerForTest(&stubCoordinator{
			browserCallbackSnapshotFn: func(string) (oauth.BrowserCallbackSnapshot, bool) {
				return oauth.BrowserCallbackSnapshot{
					FlowID:    "fl_manual_store_failure_1234567890",
					State:     "s_store",
					ExpiresAt: now.Add(5 * time.Minute),
					Status:    oauth.FlowStatusPending,
				}, true
			},
			nowFn: func() time.Time {
				return now
			},
			consumeCodeFn: func(context.Context, string, string, string, oauth.Rail) (*domain.UpstreamAccount, error) {
				return nil, &oauth.StoreError{Op: "insert account", Err: errors.New("disk full")}
			},
		})

		req := httptest.NewRequest(
			http.MethodPost,
			"/api/admin/oauth/browser/manual-callback",
			strings.NewReader(`{"callback_url":"http://localhost:1455/auth/callback?code=abc123&state=s_store"}`),
		)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		server.ServeHTTP(rec, req)

		testutil.AssertEnvelopeDataShape(t, rec, errcode.OAuthStoreFailed)
	})
}

type manualCallbackHarness struct {
	coord    *oauth.Coordinator
	provider *manualCallbackProvider
	store    *manualCallbackStore
	server   http.Handler
	logs     *bytes.Buffer
	now      time.Time
}

func newManualCallbackHarness(t *testing.T) *manualCallbackHarness {
	t.Helper()

	now := time.Now().UTC().Truncate(time.Second)
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	provider := &manualCallbackProvider{}
	store := &manualCallbackStore{nextID: 77}
	coord := oauth.NewCoordinatorWithClock(oauth.NewFakeClock(now), provider, logger)
	coord.SetAccountStore(store)
	server := generatedadminapi.HandlerFromMuxWithEnvelope(NewHandlerWithLogger(coord, logger), http.NewServeMux())

	t.Cleanup(func() {
		if flow := coord.CurrentFlow(); flow != nil {
			coord.ReleaseFlow(flow.ID)
		}
	})

	return &manualCallbackHarness{
		coord:    coord,
		provider: provider,
		store:    store,
		server:   server,
		logs:     &logs,
		now:      now,
	}
}

func (h *manualCallbackHarness) newFlow(state string, expiresAt time.Time) *oauth.Flow {
	return &oauth.Flow{
		ID:           "fl_manual_callback_12345678901234567890",
		Method:       oauth.FlowBrowser,
		State:        state,
		CodeVerifier: "verifier",
		ExpiresAt:    expiresAt,
		Status:       oauth.FlowStatusPending,
		CreatedAt:    h.now,
	}
}

func (h *manualCallbackHarness) post(t *testing.T, reqID, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/api/admin/oauth/browser/manual-callback", strings.NewReader(body))
	req = req.WithContext(requestid.WithContext(req.Context(), reqID))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.server.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	return rec
}

type manualCallbackProvider struct {
	exchangeTokens oauth.Tokens
	exchangeErr    error
	exchangeCalls  int
}

func (p *manualCallbackProvider) BuildAuthorizeURL(string, string) (string, error) {
	return "https://auth.openai.com/oauth/authorize", nil
}

func (p *manualCallbackProvider) ExchangeCode(_ context.Context, _ string, _ string) (oauth.Tokens, error) {
	p.exchangeCalls++
	if p.exchangeErr != nil {
		return oauth.Tokens{}, p.exchangeErr
	}
	return p.exchangeTokens, nil
}

func (*manualCallbackProvider) Refresh(context.Context, []byte) (oauth.Tokens, error) {
	return oauth.Tokens{}, errors.New("manualCallbackProvider.Refresh: not implemented")
}

func (*manualCallbackProvider) RequestDeviceCode(context.Context) (oauth.DeviceCode, error) {
	return oauth.DeviceCode{}, errors.New("manualCallbackProvider.RequestDeviceCode: not implemented")
}

func (*manualCallbackProvider) PollDeviceCode(context.Context, string, string) (oauth.Tokens, error) {
	return oauth.Tokens{}, errors.New("manualCallbackProvider.PollDeviceCode: not implemented")
}

type manualCallbackStore struct {
	nextID   int64
	inserted *domain.UpstreamAccount
}

func (s *manualCallbackStore) InsertUpstreamAccount(_ context.Context, account *domain.UpstreamAccount) (int64, error) {
	dup := *account
	s.inserted = &dup
	return s.nextID, nil
}

func (*manualCallbackStore) GetByID(context.Context, int64) (*domain.UpstreamAccount, error) {
	return nil, errors.New("manualCallbackStore.GetByID: not implemented")
}

func (*manualCallbackStore) UpdateCredentials(context.Context, int64, store.CredentialPatch) error {
	return errors.New("manualCallbackStore.UpdateCredentials: not implemented")
}

func (*manualCallbackStore) UpdateStatusIfCurrent(context.Context, int64, string, time.Time) error {
	return errors.New("manualCallbackStore.UpdateStatusIfCurrent: not implemented")
}

func (*manualCallbackStore) GetProjectionByID(context.Context, int64) (*domain.UpstreamAccount, error) {
	return nil, errors.New("manualCallbackStore.GetProjectionByID: not implemented")
}

type fakeExchangeError struct {
	code       string
	message    string
	httpStatus int
}

func (e fakeExchangeError) Error() string   { return e.code + ": " + e.message }
func (e fakeExchangeError) Code() string    { return e.code }
func (e fakeExchangeError) Message() string { return e.message }
func (e fakeExchangeError) HTTPStatus() int { return e.httpStatus }

func testIDToken(t *testing.T, email, planType, chatGPTAccountID string) []byte {
	t.Helper()

	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payloadBytes, err := json.Marshal(map[string]any{
		"email": email,
		"https://api.openai.com/auth": map[string]any{
			"plan_type":          planType,
			"chatgpt_account_id": chatGPTAccountID,
		},
	})
	require.NoError(t, err)
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	return []byte(header + "." + payload + ".sig")
}

func decodeManualCallbackResponse(t *testing.T, rec *httptest.ResponseRecorder) generatedadminapi.ManualCallbackResponseBody {
	t.Helper()

	var body generatedadminapi.ManualCallbackResponseBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body
}

func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

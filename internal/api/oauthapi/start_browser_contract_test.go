package oauthapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
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

func TestStartBrowserContract(t *testing.T) {
	t.Run("happy path ignores unknown keys and returns callback url always", func(t *testing.T) {
		coord, server := newStartBrowserHarness(t)

		req := httptest.NewRequest(
			http.MethodPost,
			"/api/admin/oauth/browser/start",
			strings.NewReader(`{"provider":"openai","future_flag":true,"nested":{"hint":"ignored"}}`),
		)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		server.ServeHTTP(rec, req)

		data := testutil.AssertEnvelopeDataShape(t, rec, 0)
		require.NotContains(t, data, "mode")

		body := decodeBrowserStartResponse(t, rec)
		env, err := body.AsBrowserStartEnvelope()
		require.NoError(t, err)

		flow := coord.CurrentFlow()
		require.NotNil(t, flow)

		assert.Equal(t, flow.ID, env.Data.FlowId)
		assert.Equal(t, flow.AuthorizeURL(), env.Data.AuthorizeUrl)
		assert.Equal(t, time.Date(2026, 4, 21, 15, 5, 0, 0, time.UTC), env.Data.ExpiresAt)
		assert.Equal(t, flow.ListenerBound, env.Data.ListenerBound)
		assert.Equal(t, generatedadminapi.BrowserStartEnvelopeDataMethodBrowser, env.Data.Method)

		callbackURL, err := url.Parse(env.Data.CallbackUrl)
		require.NoError(t, err)
		assert.Equal(t, "http", callbackURL.Scheme)
		assert.Equal(t, "localhost", callbackURL.Hostname())
		assert.Equal(t, "/auth/callback", callbackURL.Path)
		port, err := strconv.Atoi(callbackURL.Port())
		require.NoError(t, err)
		assert.Equal(t, 1455, port)

		assert.Contains(t, env.Data.AuthorizeUrl, "redirect_uri="+url.QueryEscape(env.Data.CallbackUrl))
	})

	t.Run("in progress collision returns existing flow metadata", func(t *testing.T) {
		coord, server := newStartBrowserHarness(t)

		first, err := coord.StartBrowser(context.Background(), "openai")
		require.NoError(t, err)

		req := httptest.NewRequest(
			http.MethodPost,
			"/api/admin/oauth/browser/start",
			strings.NewReader(`{"provider":"openai"}`),
		)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		server.ServeHTTP(rec, req)

		data := testutil.AssertEnvelopeDataShape(t, rec, 3001)
		require.Len(t, data, 4)

		body := decodeBrowserStartResponse(t, rec)
		env, err := body.AsFlowInProgressEnvelope()
		require.NoError(t, err)

		assert.Equal(t, first.ID, env.Data.FlowId)
		assert.Equal(t, generatedadminapi.Browser, env.Data.Method)
		assert.Equal(t, first.ExpiresAt, env.Data.ExpiresAt)
		assert.Equal(t, first.CreatedAt, env.Data.CreatedAt)
	})

	t.Run("invalid provider is business error with field-only data", func(t *testing.T) {
		coord, server := newStartBrowserHarness(t)

		req := httptest.NewRequest(
			http.MethodPost,
			"/api/admin/oauth/browser/start",
			strings.NewReader(`{"provider":"claude"}`),
		)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		server.ServeHTTP(rec, req)

		data := testutil.AssertEnvelopeDataShape(t, rec, 3002)
		require.Len(t, data, 1)

		body := decodeBrowserStartResponse(t, rec)
		env, err := body.AsInvalidOAuthProviderEnvelope()
		require.NoError(t, err)

		assert.Equal(t, generatedadminapi.Provider, env.Data.Field)
		assert.Nil(t, coord.CurrentFlow())
	})

	t.Run("unexpected start failure is oauth internal error", func(t *testing.T) {
		server := newOAuthAPIServerForTest(&stubCoordinator{
			startBrowserFn: func(context.Context, string) (*oauth.Flow, error) {
				return nil, errors.New("listener bind exploded")
			},
		})

		req := httptest.NewRequest(
			http.MethodPost,
			"/api/admin/oauth/browser/start",
			strings.NewReader(`{"provider":"openai"}`),
		)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		server.ServeHTTP(rec, req)

		testutil.AssertEnvelopeDataShape(t, rec, errcode.OAuthInternalError)
	})

	t.Run("malformed flow in progress sentinel is oauth internal error", func(t *testing.T) {
		server := newOAuthAPIServerForTest(&stubCoordinator{
			startBrowserFn: func(context.Context, string) (*oauth.Flow, error) {
				return nil, oauth.ErrFlowInProgress
			},
		})

		req := httptest.NewRequest(
			http.MethodPost,
			"/api/admin/oauth/browser/start",
			strings.NewReader(`{"provider":"openai"}`),
		)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		server.ServeHTTP(rec, req)

		testutil.AssertEnvelopeDataShape(t, rec, errcode.OAuthInternalError)
	})
}

func newStartBrowserHarness(t *testing.T) (*oauth.Coordinator, http.Handler) {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	coord := oauth.NewCoordinatorWithClock(
		oauth.NewFakeClock(time.Date(2026, 4, 21, 15, 0, 0, 0, time.UTC)),
		nil,
		logger,
	)
	server := generatedadminapi.HandlerFromMuxWithEnvelope(NewHandler(coord), http.NewServeMux())

	t.Cleanup(func() {
		if flow := coord.CurrentFlow(); flow != nil {
			_ = coord.Cancel(flow.ID)
		}
	})

	return coord, server
}

func decodeBrowserStartResponse(t *testing.T, rec *httptest.ResponseRecorder) generatedadminapi.BrowserStartResponseBody {
	t.Helper()

	var body generatedadminapi.BrowserStartResponseBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body
}

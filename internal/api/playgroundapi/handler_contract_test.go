package playgroundapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/one-llm-router/internal/api"
	"github.com/user/one-llm-router/internal/api/errcode"
	"github.com/user/one-llm-router/internal/api/testutil"
	"github.com/user/one-llm-router/internal/clientip"
	"github.com/user/one-llm-router/internal/core"
	"github.com/user/one-llm-router/internal/domain"
	generatedadminapi "github.com/user/one-llm-router/internal/generated/adminapi"
)

func TestPlaygroundRunContract(t *testing.T) {
	t.Run("success returns generated success envelope and forwards request fields", func(t *testing.T) {
		runner := &stubRunner{result: playgroundSuccessResult()}
		server := newPlaygroundContractServer(t, runner)

		rec := postPlayground(t, server, `{
			"selection_mode":"account",
			"endpoint":"chat_completions",
			"account_id":42,
			"session_key":"sticky-session",
			"model":"gpt-5.4-mini",
			"text":"hello",
			"max_output_tokens":128,
			"include_raw_response":true
		}`)

		data := testutil.AssertEnvelopeDataShape(t, rec, errcode.OK)
		require.NotContains(t, rec.Body.String(), "sk-secret")
		require.NotContains(t, data, "api_key")
		require.Equal(t, 1, runner.calls)
		require.Equal(t, core.PlaygroundSelectionAccount, runner.last.SelectionMode)
		assert.Equal(t, core.PlaygroundEndpointChatCompletions, runner.last.Endpoint)
		require.NotNil(t, runner.last.AccountID)
		assert.Equal(t, int64(42), *runner.last.AccountID)
		require.NotNil(t, runner.last.SessionKey)
		assert.Equal(t, "sticky-session", *runner.last.SessionKey)
		assert.Equal(t, "gpt-5.4-mini", runner.last.Model)
		assert.Equal(t, "hello", runner.last.Text)
		require.NotNil(t, runner.last.MaxOutputTokens)
		assert.Equal(t, 128, *runner.last.MaxOutputTokens)
		assert.True(t, runner.last.IncludeRawResponse)

		body := decodePlaygroundRunBody(t, rec)
		env, err := body.AsPlaygroundRunSuccessEnvelope()
		require.NoError(t, err)
		assert.Equal(t, generatedadminapi.PlaygroundRunSelectionModeAccount, env.Data.Run.SelectionMode)
		assert.Equal(t, generatedadminapi.PlaygroundRunEndpointChatCompletions, env.Data.Run.Endpoint)
		assert.Equal(t, generatedadminapi.Success, env.Data.Run.Outcome)
		assert.Equal(t, int64(42), env.Data.Account.Id)
		assert.Equal(t, generatedadminapi.OauthBrowser, env.Data.Account.AuthMethod)
		assert.Equal(t, "Hello.", env.Data.Output.Text)
		assert.True(t, env.Data.Output.TextAvailable)
		assert.True(t, env.Data.Output.RawResponseAvailable)
		require.NotNil(t, env.Data.Output.RawResponse)
		assert.Equal(t, "resp_123", (*env.Data.Output.RawResponse)["id"])
	})

	t.Run("middleware stores remote addr client IP and ignores forwarded headers", func(t *testing.T) {
		runner := &stubRunner{result: playgroundSuccessResult()}
		mux := http.NewServeMux()
		RegisterHandler(
			mux,
			NewHandler(runner, slog.New(slog.NewTextHandler(io.Discard, nil))),
			api.RequestIDMiddleware(),
		)

		req := httptest.NewRequest(http.MethodPost, "/api/admin/playground/run", strings.NewReader(`{
			"selection_mode":"auto",
			"model":"gpt-5.4-mini",
			"text":"ping"
		}`))
		req.RemoteAddr = "203.0.113.88:54321"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", "198.51.100.99")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "203.0.113.88", runner.lastClientIP)
	})

	t.Run("business errors map to registered 004 envelopes", func(t *testing.T) {
		account := playgroundAccountSummary()
		cases := []struct {
			name       string
			err        error
			wantCode   int
			assertBody func(*testing.T, generatedadminapi.PlaygroundRunResponseBody)
		}{
			{
				name:     "validation",
				err:      &core.PlaygroundValidationError{Field: core.PlaygroundFieldText, Reason: "empty"},
				wantCode: errcode.InvalidPlaygroundRequest,
				assertBody: func(t *testing.T, body generatedadminapi.PlaygroundRunResponseBody) {
					env, err := body.AsInvalidPlaygroundRequestEnvelope()
					require.NoError(t, err)
					assert.Equal(t, generatedadminapi.InvalidPlaygroundRequestEnvelopeDataFieldText, env.Data.Field)
					require.NotNil(t, env.Data.Reason)
					assert.Equal(t, "empty", *env.Data.Reason)
				},
			},
			{
				name:     "no_active_account",
				err:      core.ErrPlaygroundNoActiveAccount,
				wantCode: errcode.PlaygroundNoActiveAccount,
				assertBody: func(t *testing.T, body generatedadminapi.PlaygroundRunResponseBody) {
					env, err := body.AsPlaygroundNoActiveAccountEnvelope()
					require.NoError(t, err)
					assert.Empty(t, env.Data)
				},
			},
			{
				name:     "account_unavailable",
				err:      &core.PlaygroundAccountUnavailableError{AccountID: 99, Reason: core.PlaygroundAccountDisabled},
				wantCode: errcode.PlaygroundAccountUnavailable,
				assertBody: func(t *testing.T, body generatedadminapi.PlaygroundRunResponseBody) {
					env, err := body.AsPlaygroundAccountUnavailableEnvelope()
					require.NoError(t, err)
					assert.Equal(t, int64(99), env.Data.RequestedAccountId)
					assert.Equal(t, generatedadminapi.Disabled, env.Data.Reason)
				},
			},
			{
				name: "upstream_error",
				err: &core.PlaygroundUpstreamError{
					AccountID:       42,
					Account:         &account,
					UpstreamStatus:  429,
					ProviderError:   strPtr("rate_limit_exceeded"),
					ProviderMessage: strPtr("sanitized provider message"),
				},
				wantCode: errcode.PlaygroundUpstreamError,
				assertBody: func(t *testing.T, body generatedadminapi.PlaygroundRunResponseBody) {
					env, err := body.AsPlaygroundUpstreamErrorEnvelope()
					require.NoError(t, err)
					assert.Equal(t, int64(42), env.Data.AccountId)
					require.NotNil(t, env.Data.Account)
					assert.Equal(t, "plus", env.Data.Account.Name)
					assert.Equal(t, 429, env.Data.UpstreamStatus)
					require.NotNil(t, env.Data.ProviderError)
					assert.Equal(t, "rate_limit_exceeded", *env.Data.ProviderError)
					require.NotNil(t, env.Data.ProviderMessage)
					assert.Equal(t, "sanitized provider message", *env.Data.ProviderMessage)
				},
			},
			{
				name:     "timeout",
				err:      &core.PlaygroundUpstreamTimeoutError{AccountID: 42, Account: &account, WaitLimitMS: 30000},
				wantCode: errcode.PlaygroundUpstreamTimeout,
				assertBody: func(t *testing.T, body generatedadminapi.PlaygroundRunResponseBody) {
					env, err := body.AsPlaygroundUpstreamTimeoutEnvelope()
					require.NoError(t, err)
					assert.Equal(t, int64(42), env.Data.AccountId)
					assert.Equal(t, generatedadminapi.ThirtySeconds, env.Data.WaitLimitMs)
				},
			},
			{
				name:     "malformed",
				err:      &core.PlaygroundResponseMalformedError{AccountID: 42, Account: &account, Reason: core.PlaygroundMalformedInvalidJSON},
				wantCode: errcode.PlaygroundResponseMalformed,
				assertBody: func(t *testing.T, body generatedadminapi.PlaygroundRunResponseBody) {
					env, err := body.AsPlaygroundResponseMalformedEnvelope()
					require.NoError(t, err)
					assert.Equal(t, generatedadminapi.InvalidJson, env.Data.Reason)
				},
			},
			{
				name:     "too_large",
				err:      &core.PlaygroundResponseTooLargeError{AccountID: 42, Account: &account, LimitBytes: core.PlaygroundResponseReadLimitBytes},
				wantCode: errcode.PlaygroundResponseTooLarge,
				assertBody: func(t *testing.T, body generatedadminapi.PlaygroundRunResponseBody) {
					env, err := body.AsPlaygroundResponseTooLargeEnvelope()
					require.NoError(t, err)
					assert.Equal(t, generatedadminapi.OneMiB, env.Data.LimitBytes)
				},
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				runner := &stubRunner{err: tc.err}
				server := newPlaygroundContractServer(t, runner)
				rec := postPlayground(t, server, `{"selection_mode":"auto","model":"gpt-5.4-mini","text":"hello"}`)

				testutil.AssertEnvelopeDataShape(t, rec, tc.wantCode)
				tc.assertBody(t, decodePlaygroundRunBody(t, rec))
			})
		}
	})

	t.Run("unexpected service error is 4900 system envelope without leaking internals", func(t *testing.T) {
		runner := &stubRunner{err: errors.New("db exploded at /tmp/secret with sk-secret-token")}
		server := newPlaygroundContractServer(t, runner)

		rec := postPlayground(t, server, `{"selection_mode":"auto","model":"gpt-5.4-mini","text":"hello"}`)

		testutil.AssertEnvelopeDataShape(t, rec, errcode.PlaygroundInternalError)
		assert.NotContains(t, rec.Body.String(), "/tmp/secret")
		assert.NotContains(t, rec.Body.String(), "sk-secret-token")
	})

	t.Run("unexpected service error log redacts token-like strings", func(t *testing.T) {
		runner := &stubRunner{err: errors.New("db exploded with Bearer sk-secret-token")}
		var logs bytes.Buffer
		mux := http.NewServeMux()
		RegisterHandler(
			mux,
			NewHandler(runner, slog.New(slog.NewTextHandler(&logs, nil))),
			nil,
		)

		rec := postPlayground(t, mux, `{"selection_mode":"auto","model":"gpt-5.4-mini","text":"hello"}`)

		testutil.AssertEnvelopeDataShape(t, rec, errcode.PlaygroundInternalError)
		assert.Contains(t, logs.String(), "playground run failed")
		assert.Contains(t, logs.String(), "[redacted]")
		assert.NotContains(t, logs.String(), "sk-secret-token")
	})

	t.Run("nil runner fails fast with system envelope", func(t *testing.T) {
		resp, err := NewHandler(nil, nil).PlaygroundRun(
			context.Background(),
			generatedadminapi.PlaygroundRunRequestObject{},
		)

		require.NoError(t, err)
		system, ok := resp.(generatedadminapi.PlaygroundRun500JSONResponse)
		require.True(t, ok)
		assert.Equal(t, generatedadminapi.N4900, system.Code)
		assert.Equal(t, errcode.Symbol(errcode.PlaygroundInternalError), system.Msg)
	})

	t.Run("usage operation is not routed through playground handler", func(t *testing.T) {
		_, err := NewHandler(&stubRunner{}, nil).UsageGet(
			context.Background(),
			generatedadminapi.UsageGetRequestObject{},
		)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "playgroundapi.Handler.UsageGet: not implemented")
	})
}

func newPlaygroundContractServer(t *testing.T, runner *stubRunner) http.Handler {
	t.Helper()

	mux := http.NewServeMux()
	RegisterHandler(mux, NewHandler(runner, slog.New(slog.NewTextHandler(io.Discard, nil))), nil)
	return mux
}

func postPlayground(t *testing.T, server http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/api/admin/playground/run", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	return rec
}

func decodePlaygroundRunBody(t *testing.T, rec *httptest.ResponseRecorder) generatedadminapi.PlaygroundRunResponseBody {
	t.Helper()

	var body generatedadminapi.PlaygroundRunResponseBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body
}

type stubRunner struct {
	result       core.PlaygroundRunResult
	err          error
	calls        int
	last         core.PlaygroundRunRequest
	lastClientIP string
}

func (s *stubRunner) Run(ctx context.Context, request core.PlaygroundRunRequest) (core.PlaygroundRunResult, error) {
	s.calls++
	s.last = request
	s.lastClientIP = clientip.FromContext(ctx)
	if s.err != nil {
		return core.PlaygroundRunResult{}, s.err
	}
	return s.result, nil
}

func playgroundSuccessResult() core.PlaygroundRunResult {
	account := playgroundAccountSummary()
	rawReason := core.PlaygroundRawNotRequested
	status := 200
	return core.PlaygroundRunResult{
		Run: core.PlaygroundRunMeta{
			SelectionMode: core.PlaygroundSelectionAccount,
			Endpoint:      core.PlaygroundEndpointChatCompletions,
			Outcome:       core.PlaygroundOutcomeSuccess,
			LatencyMS:     123,
		},
		Account: account,
		Upstream: core.PlaygroundUpstreamSummary{
			StatusCode:   &status,
			ResponseMode: domain.ResponseModeJSON,
		},
		Output: core.PlaygroundOutput{
			Text:                 "Hello.",
			TextAvailable:        true,
			RawResponse:          domain.JSONMap{"id": "resp_123"},
			RawResponseAvailable: true,
			// This value must be omitted by the handler when raw response is available.
			RawResponseOmittedReason: &rawReason,
		},
		Usage: map[string]int{"input": 10, "output": 3},
	}
}

func playgroundAccountSummary() core.PlaygroundAccountSummary {
	return core.PlaygroundAccountSummary{
		ID:         42,
		Name:       "plus",
		Provider:   domain.ProviderOpenAI,
		AuthMethod: domain.AuthMethodOAuthBrowser,
		Status:     domain.AccountStatusActive,
		Email:      strPtr("operator@example.com"),
		PlanType:   strPtr("chatgpt-plus"),
	}
}

func strPtr(v string) *string { return &v }

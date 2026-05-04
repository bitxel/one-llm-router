package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/one-llm-router/internal/domain"
)

func TestPlaygroundService_Upstream(t *testing.T) {
	t.Run("success with no extractable text remains successful result", func(t *testing.T) {
		svc := playgroundServiceWithUpstream(t, &fakePlaygroundUpstream{
			result: &PlaygroundUpstreamResult{
				StatusCode:           200,
				ResponseMode:         domain.ResponseModeJSON,
				TextAvailable:        false,
				RawResponse:          domain.JSONMap{"id": "resp_no_text"},
				RawResponseAvailable: true,
			},
		})

		got, err := svc.Run(context.Background(), validPlaygroundRequest())

		require.NoError(t, err)
		assert.Equal(t, PlaygroundOutcomeNoExtractableText, got.Run.Outcome)
		assert.False(t, got.Output.TextAvailable)
		assert.Equal(t, "", got.Output.Text)
		assert.True(t, got.Output.RawResponseAvailable)
	})

	t.Run("safe raw response redacts secret keys and token-like values", func(t *testing.T) {
		svc := playgroundServiceWithUpstream(t, &fakePlaygroundUpstream{
			result: &PlaygroundUpstreamResult{
				StatusCode:    200,
				ResponseMode:  domain.ResponseModeJSON,
				Text:          "ok",
				TextAvailable: true,
				RawResponse: domain.JSONMap{
					"id":           "resp_123",
					"access_token": "secret-access",
					"nested": map[string]any{
						"message": "Bearer sk-secret-token",
					},
				},
				RawResponseAvailable: true,
			},
		})

		got, err := svc.Run(context.Background(), validPlaygroundRequest())

		require.NoError(t, err)
		assert.Equal(t, "[redacted]", got.Output.RawResponse["access_token"])
		nested, ok := got.Output.RawResponse["nested"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "[redacted]", nested["message"])
	})

	t.Run("provider error attaches account and sanitizes fields", func(t *testing.T) {
		svc := playgroundServiceWithUpstream(t, &fakePlaygroundUpstream{
			err: &PlaygroundUpstreamError{
				UpstreamStatus:  401,
				ProviderError:   strPtrCore(strings.Repeat("e", 150)),
				ProviderMessage: strPtrCore(strings.Repeat("m", 600)),
			},
		})

		_, err := svc.Run(context.Background(), validPlaygroundRequest())

		require.Error(t, err)
		var upstreamErr *PlaygroundUpstreamError
		require.ErrorAs(t, err, &upstreamErr)
		assert.Equal(t, int64(7), upstreamErr.AccountID)
		require.NotNil(t, upstreamErr.Account)
		assert.Equal(t, "account", upstreamErr.Account.Name)
		require.NotNil(t, upstreamErr.ProviderError)
		assert.Len(t, []rune(*upstreamErr.ProviderError), 128)
		require.NotNil(t, upstreamErr.ProviderMessage)
		assert.Len(t, []rune(*upstreamErr.ProviderMessage), 512)
	})

	t.Run("token-like provider message is redacted", func(t *testing.T) {
		svc := playgroundServiceWithUpstream(t, &fakePlaygroundUpstream{
			err: &PlaygroundUpstreamError{
				UpstreamStatus:  401,
				ProviderMessage: strPtrCore("Bearer sk-secret-token"),
			},
		})

		_, err := svc.Run(context.Background(), validPlaygroundRequest())

		require.Error(t, err)
		var upstreamErr *PlaygroundUpstreamError
		require.ErrorAs(t, err, &upstreamErr)
		require.NotNil(t, upstreamErr.ProviderMessage)
		assert.Equal(t, "[redacted]", *upstreamErr.ProviderMessage)
	})

	t.Run("typed timeout malformed and too-large errors receive account context", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			err  error
			as   func(*testing.T, error)
		}{
			{
				name: "timeout",
				err:  &PlaygroundUpstreamTimeoutError{},
				as: func(t *testing.T, err error) {
					var got *PlaygroundUpstreamTimeoutError
					require.ErrorAs(t, err, &got)
					assert.Equal(t, int64(7), got.AccountID)
					assert.Equal(t, int(PlaygroundWaitLimit/time.Millisecond), got.WaitLimitMS)
					require.NotNil(t, got.Account)
				},
			},
			{
				name: "malformed",
				err:  &PlaygroundResponseMalformedError{},
				as: func(t *testing.T, err error) {
					var got *PlaygroundResponseMalformedError
					require.ErrorAs(t, err, &got)
					assert.Equal(t, int64(7), got.AccountID)
					assert.Equal(t, PlaygroundMalformedInvalidJSON, got.Reason)
					require.NotNil(t, got.Account)
				},
			},
			{
				name: "too_large",
				err:  &PlaygroundResponseTooLargeError{},
				as: func(t *testing.T, err error) {
					var got *PlaygroundResponseTooLargeError
					require.ErrorAs(t, err, &got)
					assert.Equal(t, int64(7), got.AccountID)
					assert.Equal(t, PlaygroundResponseReadLimitBytes, got.LimitBytes)
					require.NotNil(t, got.Account)
				},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				svc := playgroundServiceWithUpstream(t, &fakePlaygroundUpstream{err: tc.err})
				_, err := svc.Run(context.Background(), validPlaygroundRequest())
				require.Error(t, err)
				tc.as(t, err)
			})
		}
	})
}

func playgroundServiceWithUpstream(t *testing.T, upstream *fakePlaygroundUpstream) *PlaygroundService {
	t.Helper()

	accounts := makeAccounts(7)
	accounts[0].AuthMethod = domain.AuthMethodAPIKey
	repo := &mockAccountRepo{active: accounts}
	return NewPlaygroundService(repo, NewAccountSelector(repo, nil), upstream)
}

func strPtrCore(v string) *string { return &v }

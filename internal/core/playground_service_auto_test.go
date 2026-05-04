package core

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/one-llm-router/internal/domain"
)

func TestPlaygroundService_Auto(t *testing.T) {
	t.Run("selects an active account, passes session key, and returns upstream result", func(t *testing.T) {
		accounts := makeAccounts(7)
		accounts[0].AuthMethod = domain.AuthMethodAPIKey
		repo := &mockAccountRepo{active: accounts}
		router := &recordingSessionRouter{}
		selector := NewAccountSelector(repo, router)
		upstream := &fakePlaygroundUpstream{
			result: &PlaygroundUpstreamResult{
				StatusCode:    200,
				ResponseMode:  domain.ResponseModeJSON,
				Text:          "pong",
				TextAvailable: true,
				Usage:         map[string]int{"input": 1, "output": 1},
			},
		}
		svc := NewPlaygroundService(repo, selector, upstream)
		sessionKey := "sticky"

		got, err := svc.Run(context.Background(), PlaygroundRunRequest{
			SelectionMode: PlaygroundSelectionAuto,
			SessionKey:    &sessionKey,
			Model:         "gpt-5.4-mini",
			Text:          "ping",
		})

		require.NoError(t, err)
		assert.Equal(t, "sticky", router.sessionKey)
		require.Equal(t, 1, upstream.calls)
		assert.Equal(t, int64(7), upstream.account.ID)
		assert.Equal(t, []byte("sk-test"), upstream.token)
		assert.Equal(t, int64(7), got.Account.ID)
		assert.Equal(t, domain.AuthMethodAPIKey, got.Account.AuthMethod)
		assert.Equal(t, PlaygroundSelectionAuto, got.Run.SelectionMode)
		require.NotNil(t, got.Run.SessionKey)
		assert.Equal(t, "sticky", *got.Run.SessionKey)
		assert.Equal(t, PlaygroundOutcomeSuccess, got.Run.Outcome)
		assert.Equal(t, "pong", got.Output.Text)
		assert.True(t, got.Output.TextAvailable)
		assert.Equal(t, map[string]int{"input": 1, "output": 1}, got.Usage)
	})

	t.Run("no active accounts returns 4002 sentinel and does not call upstream", func(t *testing.T) {
		repo := &mockAccountRepo{}
		upstream := &fakePlaygroundUpstream{}
		svc := NewPlaygroundService(repo, NewAccountSelector(repo, nil), upstream)

		_, err := svc.Run(context.Background(), PlaygroundRunRequest{
			SelectionMode: PlaygroundSelectionAuto,
			Model:         "gpt-5.4-mini",
			Text:          "ping",
		})

		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrPlaygroundNoActiveAccount))
		assert.Equal(t, 0, upstream.calls)
	})
}

type recordingSessionRouter struct {
	sessionKey string
}

func (r *recordingSessionRouter) Route(sessionKey string, activeAccounts []domain.UpstreamAccount) (domain.UpstreamAccount, error) {
	r.sessionKey = sessionKey
	if len(activeAccounts) == 0 {
		return domain.UpstreamAccount{}, domain.ErrNoCapacity
	}
	return activeAccounts[0], nil
}

type fakePlaygroundUpstream struct {
	result  *PlaygroundUpstreamResult
	err     error
	calls   int
	account domain.UpstreamAccount
	token   []byte
	request PlaygroundRunRequest
}

func (f *fakePlaygroundUpstream) RunPlayground(_ context.Context, account domain.UpstreamAccount, token []byte, request PlaygroundRunRequest) (*PlaygroundUpstreamResult, error) {
	f.calls++
	f.account = account
	f.token = cloneBytes(token)
	f.request = request
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

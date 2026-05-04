package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/one-llm-router/internal/domain"
)

func TestPlaygroundService_AccountMode(t *testing.T) {
	t.Run("uses the requested active account id", func(t *testing.T) {
		accounts := makeAccounts(1, 42)
		accounts[0].AuthMethod = domain.AuthMethodAPIKey
		accounts[1].AuthMethod = domain.AuthMethodAPIKey
		repo := &mockAccountRepo{active: accounts}
		upstream := &fakePlaygroundUpstream{
			result: &PlaygroundUpstreamResult{
				StatusCode:    200,
				ResponseMode:  domain.ResponseModeJSON,
				Text:          "account pong",
				TextAvailable: true,
			},
		}
		router := &recordingSessionRouter{}
		svc := NewPlaygroundService(repo, NewAccountSelector(repo, router), upstream)
		accountID := int64(42)

		got, err := svc.Run(context.Background(), PlaygroundRunRequest{
			SelectionMode: PlaygroundSelectionAccount,
			AccountID:     &accountID,
			Model:         "gpt-5.4-mini",
			Text:          "ping",
		})

		require.NoError(t, err)
		assert.Empty(t, router.sessionKey, "account mode must not fall back to automatic session routing")
		require.Equal(t, 1, upstream.calls)
		assert.Equal(t, int64(42), upstream.account.ID)
		assert.Equal(t, int64(42), got.Account.ID)
		assert.Equal(t, PlaygroundSelectionAccount, got.Run.SelectionMode)
		assert.Nil(t, got.Run.SessionKey)
	})

	t.Run("disabled deleted and missing accounts map to 4003 without upstream call", func(t *testing.T) {
		accounts := makeAccounts(2, 3)
		accounts[0].Status = domain.AccountStatusDisabled
		accounts[1].Status = domain.AccountStatusDeleted
		repo := &mockAccountRepo{active: accounts}

		for _, tc := range []struct {
			name      string
			accountID int64
			reason    PlaygroundAccountUnavailableReason
		}{
			{name: "disabled", accountID: 2, reason: PlaygroundAccountDisabled},
			{name: "deleted", accountID: 3, reason: PlaygroundAccountDeleted},
			{name: "missing", accountID: 999, reason: PlaygroundAccountMissing},
		} {
			t.Run(tc.name, func(t *testing.T) {
				upstream := &fakePlaygroundUpstream{}
				svc := NewPlaygroundService(repo, NewAccountSelector(repo, nil), upstream)

				_, err := svc.Run(context.Background(), PlaygroundRunRequest{
					SelectionMode: PlaygroundSelectionAccount,
					AccountID:     &tc.accountID,
					Model:         "gpt-5.4-mini",
					Text:          "ping",
				})

				require.Error(t, err)
				var unavailable *PlaygroundAccountUnavailableError
				require.ErrorAs(t, err, &unavailable)
				assert.Equal(t, tc.accountID, unavailable.AccountID)
				assert.Equal(t, tc.reason, unavailable.Reason)
				assert.Equal(t, 0, upstream.calls)
			})
		}
	})
}

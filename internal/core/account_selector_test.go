package core

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/user/one-llm-router/internal/domain"
)

type mockAccountRepo struct {
	active []domain.UpstreamAccount
}

func (m *mockAccountRepo) Create(_ context.Context, _ *domain.UpstreamAccount) error { return nil }
func (m *mockAccountRepo) GetByID(_ context.Context, id int64) (*domain.UpstreamAccount, error) {
	for i := range m.active {
		if m.active[i].ID == id {
			return &m.active[i], nil
		}
	}
	return nil, domain.ErrAccountNotFound
}
func (m *mockAccountRepo) List(_ context.Context, _ []string) ([]domain.UpstreamAccount, error) {
	return m.active, nil
}
func (m *mockAccountRepo) ListActive(_ context.Context) ([]domain.UpstreamAccount, error) {
	return m.active, nil
}
func (m *mockAccountRepo) UpdateStatus(_ context.Context, _ int64, _ string) error { return nil }

type listActiveFailsRepo struct {
	mockAccountRepo
}

func (listActiveFailsRepo) ListActive(_ context.Context) ([]domain.UpstreamAccount, error) {
	return nil, errors.New("list active failed")
}

func TestAccountSelector_ListActiveError(t *testing.T) {
	repo := &listActiveFailsRepo{}
	selector := NewAccountSelector(repo, nil)

	_, err := selector.SelectAccount(context.Background(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list active accounts")
}

func TestPreForward_ListActiveErrorStillSurfaces(t *testing.T) {
	repo := &listActiveFailsRepo{}
	selector := NewAccountSelector(repo, nil)

	_, _, _, err := selector.Select(context.Background(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list active accounts")
}

func TestAccountSelector_RoundRobin(t *testing.T) {
	accounts := makeAccounts(1, 2, 3)
	repo := &mockAccountRepo{active: accounts}
	selector := NewAccountSelector(repo, nil)

	hits := make(map[int64]int)
	for range 9 {
		acct, err := selector.SelectAccount(context.Background(), "")
		require.NoError(t, err)
		hits[acct.ID]++
	}

	assert.Equal(t, 3, hits[int64(1)])
	assert.Equal(t, 3, hits[int64(2)])
	assert.Equal(t, 3, hits[int64(3)])
}

func TestAccountSelector_NoCapacity(t *testing.T) {
	repo := &mockAccountRepo{active: nil}
	selector := NewAccountSelector(repo, nil)

	_, err := selector.SelectAccount(context.Background(), "")
	assert.ErrorIs(t, err, domain.ErrNoCapacity)
}

func TestAccountSelector_SessionRouting(t *testing.T) {
	accounts := makeAccounts(1, 2, 3)
	repo := &mockAccountRepo{active: accounts}
	router := NewConsistentHashRouter()
	selector := NewAccountSelector(repo, router)

	first, err := selector.SelectAccount(context.Background(), "session-xyz")
	require.NoError(t, err)

	for range 10 {
		acct, err := selector.SelectAccount(context.Background(), "session-xyz")
		require.NoError(t, err)
		assert.Equal(t, first.ID, acct.ID, "same session key should route to same account")
	}
}

func TestAccountSelector_EmptySessionUsesRoundRobin(t *testing.T) {
	accounts := makeAccounts(1, 2)
	repo := &mockAccountRepo{active: accounts}
	router := NewConsistentHashRouter()
	selector := NewAccountSelector(repo, router)

	hits := make(map[int64]int)
	for range 10 {
		acct, err := selector.SelectAccount(context.Background(), "")
		require.NoError(t, err)
		hits[acct.ID]++
	}

	assert.Equal(t, 5, hits[int64(1)])
	assert.Equal(t, 5, hits[int64(2)])
}

func TestPreForward_DefaultsToAPIKey(t *testing.T) {
	repo := &mockAccountRepo{active: makeAccounts(1)}
	selector := NewAccountSelector(repo, nil)

	acct, token, usedFallback, err := selector.Select(context.Background(), "")
	require.NoError(t, err)
	assert.Equal(t, int64(1), acct.ID)
	assert.Equal(t, []byte("sk-test"), token)
	assert.False(t, usedFallback)
}

func TestPreForward_UsesHook(t *testing.T) {
	repo := &mockAccountRepo{active: makeAccounts(7)}
	selector := NewAccountSelector(repo, nil)
	called := false
	sourceToken := []byte("oauth-access-token")
	selector.PreForward = func(_ context.Context, acct *domain.UpstreamAccount) ([]byte, bool, error) {
		called = true
		require.NotNil(t, acct)
		assert.Equal(t, int64(7), acct.ID)
		return sourceToken, true, nil
	}

	acct, token, usedFallback, err := selector.Select(context.Background(), "")
	require.NoError(t, err)
	assert.True(t, called)
	assert.Equal(t, int64(7), acct.ID)
	assert.Equal(t, []byte("oauth-access-token"), token)
	assert.True(t, usedFallback)

	sourceToken[0] = 'X'
	assert.Equal(t, []byte("oauth-access-token"), token, "selector must not leak hook-owned slice aliases")
}

func TestAccountSelector_SelectEligibleFiltersBeforeRouting(t *testing.T) {
	accounts := makeAccounts(1, 2)
	accounts[0].AuthMethod = domain.AuthMethodOAuthBrowser
	accounts[0].APIKey = ""
	accounts[1].AuthMethod = domain.AuthMethodAPIKey
	repo := &mockAccountRepo{active: accounts}
	selector := NewAccountSelector(repo, NewConsistentHashRouter())

	acct, token, usedFallback, err := selector.SelectEligible(context.Background(), "stable-session", func(acct domain.UpstreamAccount) bool {
		return acct.AuthMethod == domain.AuthMethodAPIKey
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), acct.ID)
	assert.Equal(t, []byte("sk-test"), token)
	assert.False(t, usedFallback)
}

func TestAccountSelector_SelectEligibleNoCapacity(t *testing.T) {
	accounts := makeAccounts(1)
	accounts[0].AuthMethod = domain.AuthMethodOAuthBrowser
	accounts[0].APIKey = ""
	repo := &mockAccountRepo{active: accounts}
	selector := NewAccountSelector(repo, nil)

	_, _, _, err := selector.SelectEligible(context.Background(), "", func(acct domain.UpstreamAccount) bool {
		return acct.AuthMethod == domain.AuthMethodAPIKey
	})
	assert.ErrorIs(t, err, domain.ErrNoCapacity)
}

func TestAccountSelector_ListEligiblePrepared(t *testing.T) {
	accounts := makeAccounts(1, 2, 3)
	accounts[1].AuthMethod = domain.AuthMethodOAuthBrowser
	accounts[1].APIKey = ""
	repo := &mockAccountRepo{active: accounts}
	selector := NewAccountSelector(repo, nil)
	selector.PreForward = func(_ context.Context, acct *domain.UpstreamAccount) ([]byte, bool, error) {
		if acct.ID == 2 {
			return []byte("oauth-token"), true, nil
		}
		return []byte(acct.APIKey), false, nil
	}

	prepared, err := selector.ListEligiblePrepared(context.Background(), func(acct domain.UpstreamAccount) bool {
		return acct.ID != 3
	})
	require.NoError(t, err)
	require.Len(t, prepared, 2)
	assert.Equal(t, int64(1), prepared[0].Account.ID)
	assert.Equal(t, []byte("sk-test"), prepared[0].Token)
	assert.Equal(t, int64(2), prepared[1].Account.ID)
	assert.Equal(t, []byte("oauth-token"), prepared[1].Token)

	prepared[1].Token[0] = 'X'
	again, err := selector.ListEligiblePrepared(context.Background(), func(acct domain.UpstreamAccount) bool {
		return acct.ID == 2
	})
	require.NoError(t, err)
	require.Len(t, again, 1)
	assert.Equal(t, []byte("oauth-token"), again[0].Token, "selector must not leak prepared token slice aliases")
}

func TestAccountSelector_ListEligiblePreparedNoCapacity(t *testing.T) {
	repo := &mockAccountRepo{active: makeAccounts(1)}
	selector := NewAccountSelector(repo, nil)

	_, err := selector.ListEligiblePrepared(context.Background(), func(domain.UpstreamAccount) bool {
		return false
	})
	assert.ErrorIs(t, err, domain.ErrNoCapacity)
}

func TestPreForward_WrapsErrors(t *testing.T) {
	repo := &mockAccountRepo{active: makeAccounts(9)}
	selector := NewAccountSelector(repo, nil)
	refreshErr := errors.New("refresh failed")
	selector.PreForward = func(context.Context, *domain.UpstreamAccount) ([]byte, bool, error) {
		return nil, false, refreshErr
	}

	acct, token, usedFallback, err := selector.Select(context.Background(), "")
	require.Error(t, err)
	assert.Equal(t, int64(9), acct.ID)
	assert.Nil(t, token)
	assert.False(t, usedFallback)
	assert.ErrorIs(t, err, ErrPreForward)
	assert.ErrorIs(t, err, refreshErr)
	assert.Contains(t, err.Error(), "credential refresh error")
	assert.NotContains(t, err.Error(), "refresh failed")
}

func TestPreForward_RejectsEmptyToken(t *testing.T) {
	repo := &mockAccountRepo{active: makeAccounts(11)}
	selector := NewAccountSelector(repo, nil)
	selector.PreForward = func(context.Context, *domain.UpstreamAccount) ([]byte, bool, error) {
		return []byte{}, false, nil
	}

	_, _, _, err := selector.Select(context.Background(), "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPreForward)
	assert.Contains(t, err.Error(), "empty access token")
}

func TestAccountSelector_SelectByID(t *testing.T) {
	accounts := makeAccounts(1, 2, 3, 4)
	accounts[1].AuthMethod = domain.AuthMethodOAuthBrowser
	accounts[1].APIKey = ""
	accounts[2].Status = domain.AccountStatusDisabled
	accounts[3].Status = domain.AccountStatusDeleted

	repo := &mockAccountRepo{active: accounts}
	selector := NewAccountSelector(repo, nil)
	selector.PreForward = func(_ context.Context, acct *domain.UpstreamAccount) ([]byte, bool, error) {
		if acct.ID == 2 {
			return []byte("oauth-token"), true, nil
		}
		return []byte(acct.APIKey), false, nil
	}

	t.Run("active api key", func(t *testing.T) {
		acct, token, usedFallback, err := selector.SelectByID(context.Background(), 1)
		require.NoError(t, err)
		assert.Equal(t, int64(1), acct.ID)
		assert.Equal(t, []byte("sk-test"), token)
		assert.False(t, usedFallback)
	})

	t.Run("active oauth uses preforward hook", func(t *testing.T) {
		acct, token, usedFallback, err := selector.SelectByID(context.Background(), 2)
		require.NoError(t, err)
		assert.Equal(t, int64(2), acct.ID)
		assert.Equal(t, []byte("oauth-token"), token)
		assert.True(t, usedFallback)
	})

	t.Run("invalid id fails before repository work", func(t *testing.T) {
		_, _, _, err := selector.SelectByID(context.Background(), 0)
		require.Error(t, err)
		var selectionErr *AccountSelectionError
		require.ErrorAs(t, err, &selectionErr)
		assert.Equal(t, AccountUnavailableMissing, selectionErr.Reason)
	})

	t.Run("missing account", func(t *testing.T) {
		_, _, _, err := selector.SelectByID(context.Background(), 999)
		require.Error(t, err)
		var selectionErr *AccountSelectionError
		require.ErrorAs(t, err, &selectionErr)
		assert.Equal(t, int64(999), selectionErr.AccountID)
		assert.Equal(t, AccountUnavailableMissing, selectionErr.Reason)
	})

	t.Run("disabled account", func(t *testing.T) {
		_, _, _, err := selector.SelectByID(context.Background(), 3)
		require.Error(t, err)
		var selectionErr *AccountSelectionError
		require.ErrorAs(t, err, &selectionErr)
		assert.Equal(t, int64(3), selectionErr.AccountID)
		assert.Equal(t, AccountUnavailableDisabled, selectionErr.Reason)
	})

	t.Run("deleted account", func(t *testing.T) {
		_, _, _, err := selector.SelectByID(context.Background(), 4)
		require.Error(t, err)
		var selectionErr *AccountSelectionError
		require.ErrorAs(t, err, &selectionErr)
		assert.Equal(t, int64(4), selectionErr.AccountID)
		assert.Equal(t, AccountUnavailableDeleted, selectionErr.Reason)
	})

	t.Run("preforward failure is preserved", func(t *testing.T) {
		refreshErr := errors.New("refresh failed")
		selector.PreForward = func(context.Context, *domain.UpstreamAccount) ([]byte, bool, error) {
			return nil, false, refreshErr
		}
		acct, token, usedFallback, err := selector.SelectByID(context.Background(), 2)
		require.Error(t, err)
		assert.Equal(t, int64(2), acct.ID)
		assert.Nil(t, token)
		assert.False(t, usedFallback)
		assert.ErrorIs(t, err, ErrPreForward)
		assert.ErrorIs(t, err, refreshErr)
		assert.Contains(t, err.Error(), "credential refresh error")
		assert.NotContains(t, err.Error(), "refresh failed")
	})
}

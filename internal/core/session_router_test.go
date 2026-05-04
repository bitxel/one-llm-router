package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/user/one-llm-router/internal/domain"
)

func makeAccounts(ids ...int64) []domain.UpstreamAccount {
	accts := make([]domain.UpstreamAccount, len(ids))
	for i, id := range ids {
		accts[i] = domain.UpstreamAccount{
			ID:       id,
			Name:     "account",
			Provider: domain.ProviderOpenAI,
			APIKey:   "sk-test",
			Status:   domain.AccountStatusActive,
		}
	}
	return accts
}

func TestConsistentHashRouter_Deterministic(t *testing.T) {
	router := NewConsistentHashRouter()
	accounts := makeAccounts(1, 2, 3)

	first, err := router.Route("session-abc", accounts)
	require.NoError(t, err)

	for range 100 {
		got, err := router.Route("session-abc", accounts)
		require.NoError(t, err)
		assert.Equal(t, first.ID, got.ID, "same session key must always route to same account")
	}
}

func TestConsistentHashRouter_Distribution(t *testing.T) {
	router := NewConsistentHashRouter()
	accounts := makeAccounts(1, 2, 3, 4, 5)

	hits := make(map[int64]int)
	for i := range 1000 {
		key := "session-" + string(rune(i))
		acct, err := router.Route(key, accounts)
		require.NoError(t, err)
		hits[acct.ID]++
	}

	assert.Len(t, hits, 5, "all accounts should receive traffic")
	for id, count := range hits {
		assert.Greater(t, count, 0, "account %d should have at least one hit", id)
	}
}

func TestConsistentHashRouter_MinimalReassignment(t *testing.T) {
	router := NewConsistentHashRouter()
	accountsFull := makeAccounts(1, 2, 3, 4)
	accountsReduced := makeAccounts(1, 2, 4) // account 3 removed

	sessions := make([]string, 200)
	for i := range sessions {
		sessions[i] = "sess-" + string(rune(i+100))
	}

	originalMapping := make(map[string]int64)
	for _, sk := range sessions {
		acct, err := router.Route(sk, accountsFull)
		require.NoError(t, err)
		originalMapping[sk] = acct.ID
	}

	changed := 0
	for _, sk := range sessions {
		acct, err := router.Route(sk, accountsReduced)
		require.NoError(t, err)
		if acct.ID != originalMapping[sk] {
			changed++
		}
	}

	maxExpectedReassignment := len(sessions) / 2
	assert.Less(t, changed, maxExpectedReassignment,
		"removing 1 of 4 accounts should reassign roughly 1/4, not %d of %d", changed, len(sessions))
}

func TestConsistentHashRouter_EmptyAccounts(t *testing.T) {
	router := NewConsistentHashRouter()
	_, err := router.Route("any-key", nil)
	assert.ErrorIs(t, err, domain.ErrNoCapacity)
}

func TestConsistentHashRouter_SingleAccount(t *testing.T) {
	router := NewConsistentHashRouter()
	accounts := makeAccounts(42)

	acct, err := router.Route("any-session", accounts)
	require.NoError(t, err)
	assert.Equal(t, int64(42), acct.ID)
}

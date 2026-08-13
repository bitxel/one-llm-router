package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/one-llm-router/internal/domain"
)

func TestAccountModelRepo_AccountsWithoutModels(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	accountRepo := NewAccountRepo(s.Engine())
	modelRepo := NewAccountModelRepo(s.Engine())

	withModel := apiKeyRow("with-model")
	require.NoError(t, accountRepo.Create(context.Background(), withModel))
	withoutModel := apiKeyRow("without-model")
	require.NoError(t, accountRepo.Create(context.Background(), withoutModel))
	deleted := apiKeyRow("deleted")
	require.NoError(t, accountRepo.Create(context.Background(), deleted))
	require.NoError(t, accountRepo.UpdateStatus(context.Background(), deleted.ID, domain.AccountStatusDeleted))

	require.NoError(t, modelRepo.Insert(context.Background(), withModel.ID, "gpt-4o", domain.AccountModelSourceManual))

	ids, err := modelRepo.AccountsWithoutModels(context.Background())
	require.NoError(t, err)

	got := make(map[int64]bool, len(ids))
	for _, id := range ids {
		got[id] = true
	}
	assert.True(t, got[withoutModel.ID], "account without models must be in the agnostic set")
	assert.False(t, got[withModel.ID], "account with models must be excluded")
	assert.False(t, got[deleted.ID], "deleted account must be excluded")
}

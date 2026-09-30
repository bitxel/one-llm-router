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

func TestAccountModelRepo_ReplaceUpstreamModels_StoresMetadata(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	accountRepo := NewAccountRepo(s.Engine())
	modelRepo := NewAccountModelRepo(s.Engine())

	acct := apiKeyRow("meta-acct")
	require.NoError(t, accountRepo.Create(context.Background(), acct))

	added, kept, err := modelRepo.ReplaceUpstreamModels(context.Background(), acct.ID, []domain.AccountModelDraft{
		{ID: "gpt-4o", Metadata: []byte(`{"id":"gpt-4o","object":"model","owned_by":"openai"}`)},
		{ID: "gpt-4o-mini", Metadata: []byte(`{"id":"gpt-4o-mini","object":"model"}`)},
		{ID: ""},
		{ID: "gpt-4o"}, // duplicate
	})
	require.NoError(t, err)
	assert.Equal(t, 2, added)
	assert.Equal(t, 0, kept)

	rows, err := modelRepo.ListByAccounts(context.Background(), []int64{acct.ID})
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, row := range rows {
		require.NotNil(t, row.Metadata, "upstream rows must carry metadata for %s", row.ModelID)
		assert.Contains(t, *row.Metadata, row.ModelID)
	}
}

func TestAccountModelRepo_ReplaceUpstreamModels_KeepsManualAndOverwritesMetadata(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	accountRepo := NewAccountRepo(s.Engine())
	modelRepo := NewAccountModelRepo(s.Engine())

	acct := apiKeyRow("manual-conflict")
	require.NoError(t, accountRepo.Create(context.Background(), acct))

	// Manual row first with NULL metadata — must not block refresh (choice A).
	require.NoError(t, modelRepo.Insert(context.Background(), acct.ID, "gpt-4o", domain.AccountModelSourceManual))

	firstMeta := `{"id":"gpt-4o","object":"model","owned_by":"v1"}`
	added, kept, err := modelRepo.ReplaceUpstreamModels(context.Background(), acct.ID, []domain.AccountModelDraft{
		{ID: "gpt-4o", Metadata: []byte(firstMeta)},
		{ID: "gpt-new", Metadata: []byte(`{"id":"gpt-new","object":"model"}`)},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, added, "only gpt-new is a new upstream row")
	assert.Equal(t, 1, kept, "manual gpt-4o preserved")

	rows, err := modelRepo.ListByAccount(context.Background(), acct.ID)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	byID := map[string]domain.AccountModel{}
	for _, r := range rows {
		byID[r.ModelID] = r
	}
	manual, ok := byID["gpt-4o"]
	require.True(t, ok)
	assert.Equal(t, domain.AccountModelSourceManual, manual.Source, "manual source must survive refresh")
	require.NotNil(t, manual.Metadata)
	assert.Equal(t, firstMeta, *manual.Metadata, "metadata overwritten on every refresh")

	// Second refresh overwrites metadata again.
	secondMeta := `{"id":"gpt-4o","object":"model","owned_by":"v2"}`
	_, _, err = modelRepo.ReplaceUpstreamModels(context.Background(), acct.ID, []domain.AccountModelDraft{
		{ID: "gpt-4o", Metadata: []byte(secondMeta)},
	})
	require.NoError(t, err)
	rows, err = modelRepo.ListByAccount(context.Background(), acct.ID)
	require.NoError(t, err)
	byID = map[string]domain.AccountModel{}
	for _, r := range rows {
		byID[r.ModelID] = r
	}
	require.NotNil(t, byID["gpt-4o"].Metadata)
	assert.Equal(t, secondMeta, *byID["gpt-4o"].Metadata, "metadata must be overwritten on subsequent refresh")
	assert.Equal(t, domain.AccountModelSourceManual, byID["gpt-4o"].Source)
}

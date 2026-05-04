package core

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/user/one-llm-router/internal/domain"
)

func TestHealthService_Healthy(t *testing.T) {
	repo := newInMemoryAccountRepo()
	svc := NewHealthService(repo)

	_ = repo.Create(context.Background(), &domain.UpstreamAccount{
		Name: "acct-1", Provider: "openai", APIKey: "sk-1", Status: domain.AccountStatusActive,
	})
	_ = repo.Create(context.Background(), &domain.UpstreamAccount{
		Name: "acct-2", Provider: "openai", APIKey: "sk-2", Status: domain.AccountStatusActive,
	})

	health, err := svc.GetHealth(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "healthy", health.Status)
	assert.Equal(t, 2, health.ActiveAccounts)
	assert.Equal(t, 0, health.DisabledAccounts)
	assert.Equal(t, 0, health.DeletedAccounts)
	assert.NotEmpty(t, health.Uptime)
}

func TestHealthService_NoCapacity(t *testing.T) {
	repo := newInMemoryAccountRepo()
	svc := NewHealthService(repo)

	health, err := svc.GetHealth(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "no_capacity", health.Status)
	assert.Equal(t, 0, health.ActiveAccounts)
}

type listFailsHealthRepo struct {
	*inMemoryAccountRepo
}

func (r *listFailsHealthRepo) List(_ context.Context, _ []string) ([]domain.UpstreamAccount, error) {
	return nil, errors.New("list failed")
}

func TestHealthService_ListError(t *testing.T) {
	repo := &listFailsHealthRepo{inMemoryAccountRepo: newInMemoryAccountRepo()}
	svc := NewHealthService(repo)

	health, err := svc.GetHealth(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list accounts for health")
	assert.Equal(t, "degraded", health.Status)
	assert.NotEmpty(t, health.Uptime)
}

func TestHealthService_MixedStatuses(t *testing.T) {
	repo := newInMemoryAccountRepo()
	svc := NewHealthService(repo)

	_ = repo.Create(context.Background(), &domain.UpstreamAccount{
		Name: "active", Provider: "openai", APIKey: "sk-1", Status: domain.AccountStatusActive,
	})
	_ = repo.Create(context.Background(), &domain.UpstreamAccount{
		Name: "disabled", Provider: "openai", APIKey: "sk-2", Status: domain.AccountStatusDisabled,
	})
	_ = repo.Create(context.Background(), &domain.UpstreamAccount{
		Name: "deleted", Provider: "openai", APIKey: "sk-3", Status: domain.AccountStatusDeleted,
	})

	health, err := svc.GetHealth(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "healthy", health.Status)
	assert.Equal(t, 1, health.ActiveAccounts)
	assert.Equal(t, 1, health.DisabledAccounts)
	assert.Equal(t, 1, health.DeletedAccounts)
}

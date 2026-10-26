package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/user/one-llm-router/internal/core"
	"github.com/user/one-llm-router/internal/domain"
)

func TestUpdateDetailsSystemOne(t *testing.T) {
	for _, snapshot := range []any{nil, "null", "[]", `[ "op.openai.responses" ]`} {
		t.Run("snapshot_"+stringifySnapshot(snapshot), func(t *testing.T) {
			s, cleanup := setupTestStore(t)
			defer cleanup()
			repo := NewAccountRepo(s.Engine())
			ctx := context.Background()
			id, err := repo.InsertUpstreamAccount(ctx, apiKeyRow("systemone"))
			require.NoError(t, err)
			_, err = s.Engine().Exec("UPDATE upstream_accounts SET capabilities = ? WHERE id = ?", snapshot, id)
			require.NoError(t, err)
			base := "https://example.com/inference/v1"
			patch := core.AccountDetailsPatch{BaseURL: &base, Capabilities: []string{domain.CapabilitySystemOne, "op.openai.responses"}}
			for range 2 {
				got, err := repo.UpdateDetails(ctx, id, patch)
				require.NoError(t, err)
				require.Equal(t, patch.Capabilities, got.Capabilities)
				require.Equal(t, &base, got.BaseURL)
			}
			empty := ""
			_, err = repo.UpdateDetails(ctx, id, core.AccountDetailsPatch{BaseURL: &empty})
			require.Error(t, err, "must not clear base URL while System One remains selected")
			got, err := repo.UpdateDetails(ctx, id, core.AccountDetailsPatch{BaseURL: &empty, Capabilities: []string{"op.openai.responses"}})
			require.NoError(t, err)
			require.Nil(t, got.BaseURL)
		})
	}
}

func stringifySnapshot(value any) string {
	if value == nil {
		return "SQL_NULL"
	}
	return value.(string)
}

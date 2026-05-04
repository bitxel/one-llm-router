package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/one-llm-router/internal/domain"
)

type importStoreStub struct {
	account *domain.UpstreamAccount
	err     error
}

func (s *importStoreStub) InsertUpstreamAccount(_ context.Context, account *domain.UpstreamAccount) (int64, error) {
	if s.err != nil {
		return 0, s.err
	}
	s.account = account
	account.ID = 41
	return account.ID, nil
}

func TestAuthJSONImportService(t *testing.T) {
	now := time.Date(2026, 4, 22, 8, 0, 0, 0, time.UTC)

	t.Run("happy path stores oauth_import row and preserves claims", func(t *testing.T) {
		store := &importStoreStub{}
		svc := NewAuthJSONImportService(store, func() time.Time { return now })
		raw := mustImportPayload(t, map[string]any{
			"tokens": map[string]any{
				"access_token":  "fixture_access_123",
				"refresh_token": "fixture_refresh_456",
				"id_token": makeJWT(t, map[string]any{
					"email": "import@example.com",
					"https://api.openai.com/auth": map[string]any{
						"plan_type":          "chatgpt-plus",
						"chatgpt_account_id": "acct_import_123",
					},
					"exp": float64(now.Add(30 * time.Minute).Unix()),
				}),
				"account_id": "acct_payload_fallback",
			},
			"last_refresh": now.Add(-15 * time.Minute).Format(time.RFC3339),
		})

		result, err := svc.Import(context.Background(), raw)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Account)
		assert.Empty(t, result.FallbackReasons)
		require.NotNil(t, store.account)
		assert.Equal(t, domain.AuthMethodOAuthImport, store.account.AuthMethod)
		assert.Equal(t, domain.AccountStatusActive, store.account.Status)
		assert.Equal(t, "import@example.com", store.account.Name)
		require.NotNil(t, store.account.Email)
		assert.Equal(t, "import@example.com", *store.account.Email)
		require.NotNil(t, store.account.PlanType)
		assert.Equal(t, "chatgpt-plus", *store.account.PlanType)
		require.NotNil(t, store.account.ChatGPTAccountID)
		assert.Equal(t, "acct_import_123", *store.account.ChatGPTAccountID)
		require.NotNil(t, store.account.LastRefresh)
		assert.Equal(t, now.Add(-15*time.Minute), store.account.LastRefresh.UTC())
		require.NotNil(t, store.account.AccessExpiresAt)
		assert.Equal(t, now.Add(30*time.Minute), store.account.AccessExpiresAt.UTC())
	})

	t.Run("missing last_refresh and exp use documented fallbacks", func(t *testing.T) {
		store := &importStoreStub{}
		svc := NewAuthJSONImportService(store, func() time.Time { return now })
		raw := mustImportPayload(t, map[string]any{
			"tokens": map[string]any{
				"access_token":  "fixture_access_123",
				"refresh_token": "fixture_refresh_456",
				"id_token": makeJWT(t, map[string]any{
					"email": "fallback@example.com",
				}),
			},
		})

		result, err := svc.Import(context.Background(), raw)
		require.NoError(t, err)
		require.Equal(t, []string{"payload_last_refresh_absent", "id_token_exp_absent"}, result.FallbackReasons)
		require.NotNil(t, store.account.LastRefresh)
		assert.Equal(t, now, store.account.LastRefresh.UTC())
		require.NotNil(t, store.account.AccessExpiresAt)
		assert.Equal(t, now.Add(5*time.Minute), store.account.AccessExpiresAt.UTC())
	})

	t.Run("payload account id backfills identity when claims omit email", func(t *testing.T) {
		store := &importStoreStub{}
		svc := NewAuthJSONImportService(store, func() time.Time { return now })
		raw := mustImportPayload(t, map[string]any{
			"tokens": map[string]any{
				"access_token":  "fixture_access_123",
				"refresh_token": "fixture_refresh_456",
				"id_token":      makeJWT(t, map[string]any{"sub": "opaque-user"}),
				"account_id":    "acct_payload_only",
			},
		})

		result, err := svc.Import(context.Background(), raw)
		require.NoError(t, err)
		assert.Equal(t, "acct_payload_only", result.Account.Name)
		require.NotNil(t, result.Account.ChatGPTAccountID)
		assert.Equal(t, "acct_payload_only", *result.Account.ChatGPTAccountID)
	})

	t.Run("future and unparseable last_refresh are clamped with reasons", func(t *testing.T) {
		store := &importStoreStub{}
		svc := NewAuthJSONImportService(store, func() time.Time { return now })

		futureRaw := mustImportPayload(t, map[string]any{
			"tokens": map[string]any{
				"access_token":  "fixture_access_123",
				"refresh_token": "fixture_refresh_456",
				"id_token":      makeJWT(t, map[string]any{"email": "future@example.com"}),
			},
			"last_refresh": now.Add(10 * time.Minute).Format(time.RFC3339),
		})
		result, err := svc.Import(context.Background(), futureRaw)
		require.NoError(t, err)
		assert.Contains(t, result.FallbackReasons, "payload_last_refresh_future")
		assert.Equal(t, now, store.account.LastRefresh.UTC())

		store = &importStoreStub{}
		svc = NewAuthJSONImportService(store, func() time.Time { return now })
		unparseableRaw := mustImportPayload(t, map[string]any{
			"tokens": map[string]any{
				"access_token":  "fixture_access_123",
				"refresh_token": "fixture_refresh_456",
				"id_token":      makeJWT(t, map[string]any{"email": "broken@example.com"}),
			},
			"last_refresh": "not-a-timestamp",
		})
		result, err = svc.Import(context.Background(), unparseableRaw)
		require.NoError(t, err)
		assert.Contains(t, result.FallbackReasons, "payload_last_refresh_unparseable")
		assert.Equal(t, now, store.account.LastRefresh.UTC())
	})

	t.Run("invalid structures and tokens fail fast with typed errors", func(t *testing.T) {
		store := &importStoreStub{}
		svc := NewAuthJSONImportService(store, func() time.Time { return now })

		_, err := svc.Import(context.Background(), []byte(``))
		require.ErrorIs(t, err, ErrInvalidAuthJSONStructure)

		_, err = svc.Import(context.Background(), []byte(`{"tokens":[]}`))
		require.ErrorIs(t, err, ErrInvalidAuthJSONStructure)

		_, err = svc.Import(context.Background(), mustImportPayload(t, map[string]any{
			"tokens": map[string]any{
				"access_token":  "fixture_access_123",
				"refresh_token": "fixture_refresh_456",
				"id_token":      "not-a-jwt",
			},
		}))
		var invalid *InvalidAuthJSONError
		require.ErrorAs(t, err, &invalid)
		assert.Equal(t, "malformed_id_token", invalid.Reason)
		assert.Equal(t, []string{"tokens.id_token"}, invalid.MissingFields)

		_, err = svc.Import(context.Background(), mustImportPayload(t, map[string]any{
			"tokens": map[string]any{
				"access_token":  "",
				"refresh_token": "fixture_refresh_456",
				"id_token":      makeJWT(t, map[string]any{"email": "missing@example.com"}),
			},
		}))
		require.ErrorAs(t, err, &invalid)
		assert.Equal(t, "missing_required_fields", invalid.Reason)
		assert.Contains(t, invalid.MissingFields, "tokens.access_token")

		_, err = svc.Import(context.Background(), mustImportPayload(t, map[string]any{
			"tokens": map[string]any{
				"access_token":  "fixture_access_123",
				"refresh_token": "fixture_refresh_456",
				"id_token":      makeJWT(t, map[string]any{"sub": "no-identity"}),
			},
		}))
		require.ErrorAs(t, err, &invalid)
		assert.Equal(t, "missing_identity_claims", invalid.Reason)
		assert.Contains(t, invalid.MissingFields, "email")
	})

	t.Run("store failures are wrapped and nil services fail fast", func(t *testing.T) {
		store := &importStoreStub{err: errors.New("insert failed")}
		svc := NewAuthJSONImportService(store, func() time.Time { return now })
		_, err := svc.Import(context.Background(), mustImportPayload(t, map[string]any{
			"tokens": map[string]any{
				"access_token":  "fixture_access_123",
				"refresh_token": "fixture_refresh_456",
				"id_token":      makeJWT(t, map[string]any{"email": "wrap@example.com"}),
			},
		}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "insert imported auth.json account")

		var nilSvc *AuthJSONImportService
		_, err = nilSvc.Import(context.Background(), []byte(`{}`))
		require.EqualError(t, err, "oauth.AuthJSONImportService.Import: store is nil")
	})
}

func TestParseLoopbackCallbackURL(t *testing.T) {
	t.Run("accepts registered localhost auth callback", func(t *testing.T) {
		parsed, err := ParseLoopbackCallbackURL("http://localhost:1455/auth/callback?code=abc&state=def")
		require.NoError(t, err)
		assert.Equal(t, "localhost", parsed.Hostname())
		assert.Equal(t, "1455", parsed.Port())
		assert.Equal(t, "/auth/callback", parsed.Path)
	})

	t.Run("rejects non localhost, missing port, and out of range ports", func(t *testing.T) {
		for _, raw := range []string{
			"https://localhost:1455/auth/callback",
			"http://127.0.0.1:1455/auth/callback",
			"http://localhost/auth/callback",
			"http://localhost:1454/auth/callback",
			"http://localhost:1456/auth/callback",
			"http://localhost:1458/auth/callback",
			"http://localhost:1459/auth/callback",
			"://bad",
		} {
			_, err := ParseLoopbackCallbackURL(raw)
			require.Error(t, err, raw)
		}
	})
}

func TestBrowserCallbackSnapshot(t *testing.T) {
	now := time.Date(2026, 4, 22, 8, 0, 0, 0, time.UTC)
	coord := NewCoordinatorWithClock(NewFakeClock(now), nil, nil)
	flow := &Flow{
		ID:        "fl_browser_snapshot_123",
		Method:    FlowBrowser,
		Status:    FlowStatusError,
		State:     "state_123",
		CreatedAt: now,
		ExpiresAt: now.Add(5 * time.Minute),
		terminalError: &FlowTerminalError{
			Code:    accessDeniedCode,
			Message: accessDeniedMessage,
		},
	}
	flow.Consumed.Store(true)
	flow.ConsumedBy = RailManualPaste
	require.NoError(t, coord.TryStartFlow(flow))

	t.Run("current browser flow returns callback snapshot", func(t *testing.T) {
		snapshot, ok := coord.BrowserCallbackSnapshot(flow.ID)
		require.True(t, ok)
		assert.Equal(t, flow.ID, snapshot.FlowID)
		assert.Equal(t, flow.State, snapshot.State)
		assert.True(t, snapshot.Consumed)
		assert.Equal(t, RailManualPaste, snapshot.Winner)
		require.NotNil(t, snapshot.Error)
		assert.Equal(t, accessDeniedCode, snapshot.Error.Code)
	})

	t.Run("released browser flow remains available for late consumers", func(t *testing.T) {
		coord.ReleaseFlow(flow.ID)
		snapshot, ok := coord.BrowserCallbackSnapshot(flow.ID)
		require.True(t, ok)
		assert.Equal(t, flow.ID, snapshot.FlowID)
	})

	t.Run("non browser flows are rejected", func(t *testing.T) {
		device := &Flow{
			ID:              "fl_device_snapshot_456",
			Method:          FlowDevice,
			Status:          FlowStatusPending,
			CreatedAt:       now,
			ExpiresAt:       now.Add(5 * time.Minute),
			UserCode:        "ABCD-1234",
			VerificationURL: openAIDeviceVerificationURL,
		}
		require.NoError(t, coord.TryStartFlow(device))
		_, ok := coord.BrowserCallbackSnapshot(device.ID)
		assert.False(t, ok)
	})
}

func mustImportPayload(t *testing.T, payload map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	return raw
}

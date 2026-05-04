package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/one-llm-router/internal/domain"
)

func TestAccountRepo_CreateIdempotentByName_InsertsOnce(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	repo := NewAccountRepo(s.Engine())
	now := time.Now().UTC().Truncate(time.Second)

	first := &domain.UpstreamAccount{
		Name:      "primary",
		Provider:  "openai",
		APIKey:    "sk-live-1",
		Status:    domain.AccountStatusActive,
		CreatedAt: now,
		UpdatedAt: now,
	}
	require.NoError(t, repo.CreateIdempotentByName(context.Background(), first))
	require.NotZero(t, first.ID)

	all, err := repo.List(context.Background(), nil)
	require.NoError(t, err)
	assert.Len(t, all, 1, "first call must insert exactly one row")
}

func TestAccountRepo_CreateIdempotentByName_SecondCallReusesRow(t *testing.T) {
	// R-1 scenario: wizard commit succeeds through DB but crashes before
	// config.json rename. Operator retries the wizard with the same
	// inputs; the second CreateIdempotentByName must NOT produce a
	// duplicate account. Instead it should return the existing row's
	// identity via the mutated account argument.
	s, cleanup := setupTestStore(t)
	defer cleanup()

	repo := NewAccountRepo(s.Engine())
	now := time.Now().UTC().Truncate(time.Second)

	original := &domain.UpstreamAccount{
		Name: "primary", Provider: "openai", APIKey: "sk-live-1",
		Status: domain.AccountStatusActive, CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, repo.CreateIdempotentByName(context.Background(), original))
	originalID := original.ID
	require.NotZero(t, originalID)

	retry := &domain.UpstreamAccount{
		Name: "primary", Provider: "openai", APIKey: "sk-retry-with-rotated-key",
		Status: domain.AccountStatusActive,
		// Different timestamps on retry — implementation must keep the
		// stored created_at, not overwrite it.
		CreatedAt: now.Add(time.Hour),
		UpdatedAt: now.Add(time.Hour),
	}
	require.NoError(t, repo.CreateIdempotentByName(context.Background(), retry))

	assert.Equal(t, originalID, retry.ID, "retry must reuse the existing row's id")
	assert.Equal(t, now, retry.CreatedAt.UTC().Truncate(time.Second),
		"retry must preserve the original CreatedAt")

	all, err := repo.List(context.Background(), nil)
	require.NoError(t, err)
	assert.Len(t, all, 1, "retry must not produce a duplicate row")
}

func TestAccountRepo_CreateIdempotentByName_DistinctNamesInsertBoth(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	repo := NewAccountRepo(s.Engine())
	now := time.Now().UTC().Truncate(time.Second)

	a := &domain.UpstreamAccount{
		Name: "primary", Provider: "openai", APIKey: "sk-a",
		Status: domain.AccountStatusActive, CreatedAt: now, UpdatedAt: now,
	}
	b := &domain.UpstreamAccount{
		Name: "secondary", Provider: "openai", APIKey: "sk-b",
		Status: domain.AccountStatusActive, CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, repo.CreateIdempotentByName(context.Background(), a))
	require.NoError(t, repo.CreateIdempotentByName(context.Background(), b))
	assert.NotEqual(t, a.ID, b.ID)

	all, err := repo.List(context.Background(), nil)
	require.NoError(t, err)
	assert.Len(t, all, 2)
}

// -------- 003: multi-mode auth store methods ----------------------------

func oauthRow(name string) *domain.UpstreamAccount {
	now := time.Now().UTC().Truncate(time.Second)
	later := now.Add(time.Hour)
	email := "alice@example.com"
	plan := "chatgpt-plus"
	acctID := "org_7f2b9a3e"
	return &domain.UpstreamAccount{
		Name:             name,
		Provider:         domain.ProviderOpenAI,
		Status:           domain.AccountStatusActive,
		AuthMethod:       domain.AuthMethodOAuthBrowser,
		AccessToken:      []byte("access-bytes-v1"),
		RefreshToken:     []byte("refresh-bytes-v1"),
		IDToken:          []byte("id-bytes-v1"),
		LastRefresh:      &now,
		AccessExpiresAt:  &later,
		Email:            &email,
		PlanType:         &plan,
		ChatGPTAccountID: &acctID,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
}

func apiKeyRow(name string) *domain.UpstreamAccount {
	now := time.Now().UTC().Truncate(time.Second)
	return &domain.UpstreamAccount{
		Name:       name,
		Provider:   domain.ProviderOpenAI,
		APIKey:     "sk-live-" + name,
		Status:     domain.AccountStatusActive,
		AuthMethod: domain.AuthMethodAPIKey,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

func TestAccountsStore_InsertUpstreamAccount_APIKeyRoundTrip(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	repo := NewAccountRepo(s.Engine())

	row := apiKeyRow("k1")
	id, err := repo.InsertUpstreamAccount(context.Background(), row)
	require.NoError(t, err)
	assert.NotZero(t, id)

	items, err := repo.ListForAdminAPI(context.Background())
	require.NoError(t, err)
	require.Len(t, items, 1)
	it := items[0]
	assert.Equal(t, domain.AuthMethodAPIKey, it.AuthMethod)
	assert.Nil(t, it.Email)
	assert.Nil(t, it.PlanType)
	assert.Nil(t, it.LastRefresh)
	assert.Nil(t, it.AccessExpiresAt)
}

func TestAccountsStore_InsertUpstreamAccount_OAuthRoundTrip(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	repo := NewAccountRepo(s.Engine())

	row := oauthRow("o1")
	id, err := repo.InsertUpstreamAccount(context.Background(), row)
	require.NoError(t, err)
	assert.NotZero(t, id)

	// Verify api_key column is NULL on the inserted row (not "").
	var apiKeyIsNull bool
	_, err = s.Engine().SQL(
		"SELECT api_key IS NULL FROM upstream_accounts WHERE id = ?", id,
	).Get(&apiKeyIsNull)
	require.NoError(t, err)
	assert.True(t, apiKeyIsNull, "OAuth insert must leave api_key NULL, not empty string")

	items, err := repo.ListForAdminAPI(context.Background())
	require.NoError(t, err)
	require.Len(t, items, 1)
	it := items[0]
	assert.Equal(t, domain.AuthMethodOAuthBrowser, it.AuthMethod)
	require.NotNil(t, it.Email)
	assert.Equal(t, "alice@example.com", *it.Email)
	require.NotNil(t, it.PlanType)
	assert.Equal(t, "chatgpt-plus", *it.PlanType)
	require.NotNil(t, it.LastRefresh)
	require.NotNil(t, it.AccessExpiresAt)
}

func TestAccountsStore_InsertUpstreamAccount_ValidateRejects(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	repo := NewAccountRepo(s.Engine())

	bad := apiKeyRow("k1")
	bad.AccessToken = []byte("leak")
	id, err := repo.InsertUpstreamAccount(context.Background(), bad)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrInvalidAccountShape)
	assert.Zero(t, id)

	// DB must be untouched.
	items, err := repo.ListForAdminAPI(context.Background())
	require.NoError(t, err)
	assert.Empty(t, items, "Validate failure must not write to DB")
}

func TestAccountsStore_ListForAdminAPI_MixedNoTokenLeak(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	repo := NewAccountRepo(s.Engine())

	_, err := repo.InsertUpstreamAccount(context.Background(), apiKeyRow("k1"))
	require.NoError(t, err)
	_, err = repo.InsertUpstreamAccount(context.Background(), oauthRow("o1"))
	require.NoError(t, err)
	_, err = repo.InsertUpstreamAccount(context.Background(), oauthRow("o2"))
	require.NoError(t, err)

	items, err := repo.ListForAdminAPI(context.Background())
	require.NoError(t, err)
	require.Len(t, items, 3)

	// JSON-marshal the list and grep for token bytes — a leaky
	// projection would surface them here.
	b, err := json.Marshal(items)
	require.NoError(t, err)
	body := string(b)
	for _, needle := range []string{"access-bytes-v1", "refresh-bytes-v1", "id-bytes-v1", "sk-live-"} {
		assert.NotContainsf(t, body, needle, "ListForAdminAPI leaked %q in JSON: %s", needle, body)
	}
	// JSON keys for tokens must not appear as object keys. We check
	// `"<key>":` (with trailing colon) rather than the bare key so a
	// field *value* that happens to contain the string "api_key"
	// (e.g. auth_method="api_key") doesn't produce a false positive.
	for _, k := range []string{"access_token", "refresh_token", "id_token", "api_key"} {
		assert.NotContainsf(t, body, `"`+k+`":`, "ListForAdminAPI leaked key %q in JSON", k)
	}
}

func TestAccountsStore_GetForExport_OAuthOK(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	repo := NewAccountRepo(s.Engine())

	id, err := repo.InsertUpstreamAccount(context.Background(), oauthRow("o1"))
	require.NoError(t, err)

	exp, err := repo.GetForExport(context.Background(), id)
	require.NoError(t, err)
	require.NotNil(t, exp)
	assert.Equal(t, []byte("access-bytes-v1"), exp.AccessToken)
	assert.Equal(t, []byte("refresh-bytes-v1"), exp.RefreshToken)
	assert.Equal(t, []byte("id-bytes-v1"), exp.IDToken)
	require.NotNil(t, exp.LastRefresh)
	require.NotNil(t, exp.ChatGPTAccountID)
	assert.Equal(t, "org_7f2b9a3e", *exp.ChatGPTAccountID)
}

func TestAccountsStore_GetProjectionByID_TokenFree(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	repo := NewAccountRepo(s.Engine())

	id, err := repo.InsertUpstreamAccount(context.Background(), oauthRow("o1"))
	require.NoError(t, err)

	projected, err := repo.GetProjectionByID(context.Background(), id)
	require.NoError(t, err)
	require.NotNil(t, projected)
	assert.Equal(t, id, projected.ID)
	assert.Equal(t, domain.AuthMethodOAuthBrowser, projected.AuthMethod)
	assert.Nil(t, projected.AccessToken)
	assert.Nil(t, projected.RefreshToken)
	assert.Nil(t, projected.IDToken)
	assert.Empty(t, projected.APIKey)
	require.NotNil(t, projected.Email)
	assert.Equal(t, "alice@example.com", *projected.Email)
}

func TestAccountsStore_GetProjectionByID_NotFound(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	repo := NewAccountRepo(s.Engine())

	_, err := repo.GetProjectionByID(context.Background(), 9999)
	assert.ErrorIs(t, err, domain.ErrAccountNotFound)
}

func TestAccountsStore_GetForExport_APIKeyRejected(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	repo := NewAccountRepo(s.Engine())

	id, err := repo.InsertUpstreamAccount(context.Background(), apiKeyRow("k1"))
	require.NoError(t, err)

	_, err = repo.GetForExport(context.Background(), id)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrInvalidAccountShape)
}

func TestAccountsStore_GetForExport_NotFound(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	repo := NewAccountRepo(s.Engine())

	_, err := repo.GetForExport(context.Background(), 99999)
	assert.ErrorIs(t, err, domain.ErrAccountNotFound)
}

func TestAccountsStore_UpdateCredentials_OAuthRefresh(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	repo := NewAccountRepo(s.Engine())

	id, err := repo.InsertUpstreamAccount(context.Background(), oauthRow("o1"))
	require.NoError(t, err)

	newRefreshAt := time.Now().UTC().Add(30 * time.Minute).Truncate(time.Second)
	newExpiresAt := newRefreshAt.Add(time.Hour)
	email := "bob@example.com"
	plan := "chatgpt-team"
	ctxID := "org_newer"

	err = repo.UpdateCredentials(context.Background(), id, CredentialPatch{
		AuthMethod:       domain.AuthMethodOAuthBrowser,
		AccessToken:      []byte("access-bytes-v2"),
		RefreshToken:     []byte("refresh-bytes-v2"),
		IDToken:          []byte("id-bytes-v2"),
		LastRefresh:      &newRefreshAt,
		AccessExpiresAt:  &newExpiresAt,
		Email:            &email,
		PlanType:         &plan,
		ChatGPTAccountID: &ctxID,
	})
	require.NoError(t, err)

	exp, err := repo.GetForExport(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, []byte("access-bytes-v2"), exp.AccessToken)
	assert.Equal(t, []byte("refresh-bytes-v2"), exp.RefreshToken)
	assert.Equal(t, []byte("id-bytes-v2"), exp.IDToken)
	// Round-2 external review: export payload's LastRefresh MUST
	// reflect the patch, not the original row, so the emitted
	// auth.json carries the freshly-rotated refresh timestamp.
	// Previously the test only asserted token bytes, so a mutation
	// that dropped `last_refresh` from the UPDATE column list went
	// uncaught.
	require.NotNil(t, exp.LastRefresh)
	assert.True(t, exp.LastRefresh.Equal(newRefreshAt),
		"exported last_refresh = %v, want %v (refresh patch must be persisted)",
		*exp.LastRefresh, newRefreshAt)

	items, err := repo.ListForAdminAPI(context.Background())
	require.NoError(t, err)
	require.Len(t, items, 1)
	// The raw plan slug is what the store persists; the human-readable
	// label is computed by the adminapi handler (see
	// adminapi.PlanTypeLabel), deliberately NOT present on the
	// store DTO after the D7 layering refactor.
	require.NotNil(t, items[0].PlanType)
	assert.Equal(t, "chatgpt-team", *items[0].PlanType)
	// PlanType + Email + ChatGPTAccountID must ALSO have persisted.
	// These are part of the OAuth refresh column set per data-
	// model.md; dropping any of them from the UPDATE would be
	// observable by the admin list (email/plan) or by the exporter
	// (chatgpt_account_id).
	require.NotNil(t, items[0].Email)
	assert.Equal(t, email, *items[0].Email, "email must be persisted by OAuth credential update")
	require.NotNil(t, items[0].PlanType)
	assert.Equal(t, plan, *items[0].PlanType, "plan_type must be persisted by OAuth credential update")
	require.NotNil(t, exp.ChatGPTAccountID)
	assert.Equal(t, ctxID, *exp.ChatGPTAccountID, "chatgpt_account_id must be persisted by OAuth credential update")

	// access_expires_at is not surfaced on ExportPayload (only the
	// refresh logic consumes it), so re-read the full row to prove
	// the patch landed on disk.
	fullRow, err := repo.GetByID(context.Background(), id)
	require.NoError(t, err)
	require.NotNil(t, fullRow.AccessExpiresAt)
	assert.True(t, fullRow.AccessExpiresAt.Equal(newExpiresAt),
		"access_expires_at = %v, want %v", *fullRow.AccessExpiresAt, newExpiresAt)
}

func TestAccountsStore_UpdateCredentials_OAuthRefreshConditionalConflict(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	repo := NewAccountRepo(s.Engine())

	row := oauthRow("o-conflict")
	require.NotNil(t, row.LastRefresh)
	originalLastRefresh := *row.LastRefresh

	id, err := repo.InsertUpstreamAccount(context.Background(), row)
	require.NoError(t, err)

	winningRefreshAt := originalLastRefresh.Add(15 * time.Minute)
	winningExpiresAt := winningRefreshAt.Add(time.Hour)
	winningEmail := "winner@example.com"
	winningPlan := "chatgpt-team"
	winningAccountID := "acct-winner"
	err = repo.UpdateCredentials(context.Background(), id, CredentialPatch{
		AuthMethod:       domain.AuthMethodOAuthBrowser,
		AccessToken:      []byte("access-bytes-winner"),
		RefreshToken:     []byte("refresh-bytes-winner"),
		IDToken:          []byte("id-bytes-winner"),
		LastRefresh:      &winningRefreshAt,
		AccessExpiresAt:  &winningExpiresAt,
		Email:            &winningEmail,
		PlanType:         &winningPlan,
		ChatGPTAccountID: &winningAccountID,
	})
	require.NoError(t, err)

	losingRefreshAt := winningRefreshAt.Add(15 * time.Minute)
	losingExpiresAt := losingRefreshAt.Add(time.Hour)
	losingEmail := "loser@example.com"
	losingPlan := "chatgpt-enterprise"
	losingAccountID := "acct-loser"
	err = repo.UpdateCredentials(context.Background(), id, CredentialPatch{
		AuthMethod:          domain.AuthMethodOAuthBrowser,
		AccessToken:         []byte("access-bytes-loser"),
		RefreshToken:        []byte("refresh-bytes-loser"),
		IDToken:             []byte("id-bytes-loser"),
		LastRefresh:         &losingRefreshAt,
		ExpectedLastRefresh: &originalLastRefresh,
		AccessExpiresAt:     &losingExpiresAt,
		Email:               &losingEmail,
		PlanType:            &losingPlan,
		ChatGPTAccountID:    &losingAccountID,
	})
	require.ErrorIs(t, err, ErrConditionalUpdateConflict)

	exported, err := repo.GetForExport(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, []byte("access-bytes-winner"), exported.AccessToken)
	assert.Equal(t, []byte("refresh-bytes-winner"), exported.RefreshToken)
	require.NotNil(t, exported.LastRefresh)
	assert.True(t, exported.LastRefresh.Equal(winningRefreshAt))
}

func TestAccountsStore_UpdateCredentials_OAuthRefreshConditionalSuccess(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	repo := NewAccountRepo(s.Engine())

	row := oauthRow("o-conditional-success")
	require.NotNil(t, row.LastRefresh)
	originalLastRefresh := time.Date(2026, 4, 22, 8, 0, 0, 123400000, time.UTC)
	originalExpiresAt := originalLastRefresh.Add(time.Hour)
	row.LastRefresh = &originalLastRefresh
	row.AccessExpiresAt = &originalExpiresAt

	id, err := repo.InsertUpstreamAccount(context.Background(), row)
	require.NoError(t, err)

	storedBeforeUpdate, err := repo.GetByID(context.Background(), id)
	require.NoError(t, err)
	require.NotNil(t, storedBeforeUpdate.LastRefresh)
	assert.True(t, storedBeforeUpdate.LastRefresh.Equal(originalLastRefresh.Truncate(time.Second)))
	expectedLastRefresh := *storedBeforeUpdate.LastRefresh

	refreshedAt := originalLastRefresh.Add(30 * time.Minute)
	expiresAt := refreshedAt.Add(2 * time.Hour)
	email := "updated@example.com"
	plan := "chatgpt-pro"
	accountID := "acct-conditional-success"
	err = repo.UpdateCredentials(context.Background(), id, CredentialPatch{
		AuthMethod:          domain.AuthMethodOAuthBrowser,
		AccessToken:         []byte("access-bytes-updated"),
		RefreshToken:        []byte("refresh-bytes-updated"),
		IDToken:             []byte("id-bytes-updated"),
		LastRefresh:         &refreshedAt,
		ExpectedLastRefresh: &expectedLastRefresh,
		AccessExpiresAt:     &expiresAt,
		Email:               &email,
		PlanType:            &plan,
		ChatGPTAccountID:    &accountID,
	})
	require.NoError(t, err)

	exported, err := repo.GetForExport(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, []byte("access-bytes-updated"), exported.AccessToken)
	assert.Equal(t, []byte("refresh-bytes-updated"), exported.RefreshToken)
	require.NotNil(t, exported.LastRefresh)
	assert.True(t, exported.LastRefresh.Equal(refreshedAt.Truncate(time.Second)))
}

func TestAccountsStore_UpdateStatusIfCurrent(t *testing.T) {
	t.Run("matching active row is updated", func(t *testing.T) {
		s, cleanup := setupTestStore(t)
		defer cleanup()
		repo := NewAccountRepo(s.Engine())

		row := oauthRow("o-status-conditional")
		require.NotNil(t, row.LastRefresh)
		id, err := repo.InsertUpstreamAccount(context.Background(), row)
		require.NoError(t, err)

		stored, err := repo.GetByID(context.Background(), id)
		require.NoError(t, err)
		require.NotNil(t, stored.LastRefresh)

		err = repo.UpdateStatusIfCurrent(context.Background(), id, domain.AccountStatusDisabled, *stored.LastRefresh)
		require.NoError(t, err)

		got, err := repo.GetByID(context.Background(), id)
		require.NoError(t, err)
		assert.Equal(t, domain.AccountStatusDisabled, got.Status)
	})

	t.Run("mismatched snapshot returns conditional conflict", func(t *testing.T) {
		s, cleanup := setupTestStore(t)
		defer cleanup()
		repo := NewAccountRepo(s.Engine())

		row := oauthRow("o-status-conflict")
		require.NotNil(t, row.LastRefresh)
		id, err := repo.InsertUpstreamAccount(context.Background(), row)
		require.NoError(t, err)

		stored, err := repo.GetByID(context.Background(), id)
		require.NoError(t, err)
		require.NotNil(t, stored.LastRefresh)

		err = repo.UpdateStatus(context.Background(), id, domain.AccountStatusDeleted)
		require.NoError(t, err)

		err = repo.UpdateStatusIfCurrent(context.Background(), id, domain.AccountStatusDisabled, *stored.LastRefresh)
		require.ErrorIs(t, err, ErrConditionalUpdateConflict)

		got, err := repo.GetByID(context.Background(), id)
		require.NoError(t, err)
		assert.Equal(t, domain.AccountStatusDeleted, got.Status)
	})
}

// TestAccountsStore_OAuthTimestamps_RoundTripUTC asserts the
// data-model.md §Timestamp types invariant: whatever zone a caller
// passes, the timestamp is persisted and read back as UTC, equal in
// absolute instant. Regression-guards normaliseUTC in the insert/
// update paths — without it, a refresh executed by a router running
// in `Asia/Shanghai` would write +08:00 wall-clock into a SQLite
// row that is then compared against `time.Now().UTC()` on a UTC
// router, yielding an 8-hour skew in `RefreshIfStale`.
func TestAccountsStore_OAuthTimestamps_RoundTripUTC(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	repo := NewAccountRepo(s.Engine())

	// Build a non-UTC time and prove it survives the write/read cycle
	// as UTC with the same absolute instant. We do not depend on
	// time.Local because the test host's zone is unpredictable — we
	// build a FixedZone with a non-zero offset explicitly.
	shanghai := time.FixedZone("test-plus-eight", 8*3600)
	localRefresh := time.Date(2026, 4, 20, 18, 30, 15, 0, shanghai)
	localExpiry := localRefresh.Add(time.Hour)
	wantRefreshUTC := localRefresh.UTC()
	wantExpiryUTC := localExpiry.UTC()

	row := oauthRow("o-utc")
	row.LastRefresh = &localRefresh
	row.AccessExpiresAt = &localExpiry

	id, err := repo.InsertUpstreamAccount(context.Background(), row)
	require.NoError(t, err)

	got, err := repo.GetByID(context.Background(), id)
	require.NoError(t, err)
	require.NotNil(t, got.LastRefresh)
	require.NotNil(t, got.AccessExpiresAt)
	assert.True(t, got.LastRefresh.Equal(wantRefreshUTC),
		"round-trip last_refresh = %v, want %v (absolute instant must match)",
		got.LastRefresh, wantRefreshUTC)
	assert.True(t, got.AccessExpiresAt.Equal(wantExpiryUTC),
		"round-trip access_expires_at = %v, want %v (absolute instant must match)",
		got.AccessExpiresAt, wantExpiryUTC)

	// Now update with another non-UTC time and prove UpdateCredentials
	// also normalises.
	newLocal := time.Date(2026, 4, 20, 20, 0, 0, 0, shanghai)
	newLocalExpiry := newLocal.Add(2 * time.Hour)
	err = repo.UpdateCredentials(context.Background(), id, CredentialPatch{
		AuthMethod:      domain.AuthMethodOAuthBrowser,
		AccessToken:     []byte("a2"),
		RefreshToken:    []byte("r2"),
		IDToken:         []byte("i2"),
		LastRefresh:     &newLocal,
		AccessExpiresAt: &newLocalExpiry,
	})
	require.NoError(t, err)

	got, err = repo.GetByID(context.Background(), id)
	require.NoError(t, err)
	require.NotNil(t, got.LastRefresh)
	require.NotNil(t, got.AccessExpiresAt)
	assert.True(t, got.LastRefresh.Equal(newLocal.UTC()))
	assert.True(t, got.AccessExpiresAt.Equal(newLocalExpiry.UTC()))
}

func TestAccountsStore_UpdateCredentials_APIKeyRotation(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	repo := NewAccountRepo(s.Engine())

	id, err := repo.InsertUpstreamAccount(context.Background(), apiKeyRow("k1"))
	require.NoError(t, err)

	err = repo.UpdateCredentials(context.Background(), id, CredentialPatch{
		AuthMethod: domain.AuthMethodAPIKey,
		APIKey:     "sk-rotated",
	})
	require.NoError(t, err)

	got, err := repo.GetByID(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, "sk-rotated", got.APIKey)
}

func TestAccountsStore_UpdateCredentials_RejectsShapeMismatch(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	repo := NewAccountRepo(s.Engine())

	id, err := repo.InsertUpstreamAccount(context.Background(), oauthRow("o1"))
	require.NoError(t, err)

	// API-key patch on an OAuth row with extra OAuth bytes set:
	// shape-check rejects it before the UPDATE.
	err = repo.UpdateCredentials(context.Background(), id, CredentialPatch{
		AuthMethod:  domain.AuthMethodAPIKey,
		APIKey:      "sk-ok",
		AccessToken: []byte("should-fail"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "OAuth fields must be zero")

	// OAuth patch missing AccessExpiresAt: rejected.
	now := time.Now()
	err = repo.UpdateCredentials(context.Background(), id, CredentialPatch{
		AuthMethod:   domain.AuthMethodOAuthBrowser,
		AccessToken:  []byte("a"),
		RefreshToken: []byte("r"),
		IDToken:      []byte("i"),
		LastRefresh:  &now,
		// AccessExpiresAt nil
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "access_expires_at are required together")

	// Unknown method.
	err = repo.UpdateCredentials(context.Background(), id, CredentialPatch{
		AuthMethod: domain.AuthMethod("made-up"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown AuthMethod")
}

func TestAccountsStore_UpdateCredentials_NotFound(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	repo := NewAccountRepo(s.Engine())

	err := repo.UpdateCredentials(context.Background(), 99999, CredentialPatch{
		AuthMethod: domain.AuthMethodAPIKey,
		APIKey:     "sk-x",
	})
	assert.ErrorIs(t, err, domain.ErrAccountNotFound)
}

func TestAccountsStore_UpdateCredentials_DeletedRowRejected(t *testing.T) {
	t.Run("api key patch on deleted row returns not found", func(t *testing.T) {
		s, cleanup := setupTestStore(t)
		defer cleanup()
		repo := NewAccountRepo(s.Engine())

		id, err := repo.InsertUpstreamAccount(context.Background(), apiKeyRow("k-deleted"))
		require.NoError(t, err)
		require.NoError(t, repo.UpdateStatus(context.Background(), id, domain.AccountStatusDeleted))

		err = repo.UpdateCredentials(context.Background(), id, CredentialPatch{
			AuthMethod: domain.AuthMethodAPIKey,
			APIKey:     "sk-should-not-write",
		})
		require.ErrorIs(t, err, domain.ErrAccountNotFound)

		got, err := repo.GetByID(context.Background(), id)
		require.NoError(t, err)
		assert.Equal(t, domain.AccountStatusDeleted, got.Status)
		assert.Equal(t, "sk-live-k-deleted", got.APIKey)
	})

	t.Run("oauth patch on deleted row returns not found instead of conflict", func(t *testing.T) {
		s, cleanup := setupTestStore(t)
		defer cleanup()
		repo := NewAccountRepo(s.Engine())

		row := oauthRow("o-deleted")
		require.NotNil(t, row.LastRefresh)
		id, err := repo.InsertUpstreamAccount(context.Background(), row)
		require.NoError(t, err)

		stored, err := repo.GetByID(context.Background(), id)
		require.NoError(t, err)
		require.NotNil(t, stored.LastRefresh)
		require.NoError(t, repo.UpdateStatus(context.Background(), id, domain.AccountStatusDeleted))

		refreshedAt := stored.LastRefresh.Add(30 * time.Minute)
		expiresAt := refreshedAt.Add(time.Hour)
		err = repo.UpdateCredentials(context.Background(), id, CredentialPatch{
			AuthMethod:          domain.AuthMethodOAuthBrowser,
			AccessToken:         []byte("access-after-delete"),
			RefreshToken:        []byte("refresh-after-delete"),
			IDToken:             []byte("id-after-delete"),
			LastRefresh:         &refreshedAt,
			ExpectedLastRefresh: stored.LastRefresh,
			AccessExpiresAt:     &expiresAt,
		})
		require.ErrorIs(t, err, domain.ErrAccountNotFound)
		assert.NotErrorIs(t, err, ErrConditionalUpdateConflict)

		got, err := repo.GetByID(context.Background(), id)
		require.NoError(t, err)
		assert.Equal(t, domain.AccountStatusDeleted, got.Status)
		assert.Equal(t, []byte("access-bytes-v1"), got.AccessToken)
		assert.Equal(t, []byte("refresh-bytes-v1"), got.RefreshToken)
		assert.Equal(t, []byte("id-bytes-v1"), got.IDToken)
	})
}

// TestAccountsStore_UpdateCredentials_RejectsCrossRowShapeChange is
// the regression guard for data-model.md §Invariants rule 3
// ("auth_method is immutable within a row"). Before 003-Rv2, a caller
// who accidentally passed {AuthMethod: api_key, APIKey: "sk-x"}
// against an OAuth row would silently corrupt the row into a mixed
// shape (api_key non-NULL AND tokens non-NULL AND auth_method=
// oauth_browser). This test locks the defence.
func TestAccountsStore_UpdateCredentials_RejectsCrossRowShapeChange(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	repo := NewAccountRepo(s.Engine())

	t.Run("api_key patch against OAuth row is rejected", func(t *testing.T) {
		oauthID, err := repo.InsertUpstreamAccount(context.Background(), oauthRow("o-cross-1"))
		require.NoError(t, err)

		err = repo.UpdateCredentials(context.Background(), oauthID, CredentialPatch{
			AuthMethod: domain.AuthMethodAPIKey,
			APIKey:     "sk-would-corrupt",
		})
		require.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrAuthMethodMismatch,
			"store must surface ErrAuthMethodMismatch so callers can distinguish invariant violations")
		assert.Contains(t, err.Error(), "patch auth_method=\"api_key\" does not match stored auth_method=\"oauth_browser\"")

		// Verify the row on disk stayed legal: api_key IS NULL and
		// tokens intact. A corrupting write would have set api_key
		// to 'sk-would-corrupt'.
		var apiKeyIsNull bool
		_, err = s.Engine().SQL(
			"SELECT api_key IS NULL FROM upstream_accounts WHERE id = ?", oauthID,
		).Get(&apiKeyIsNull)
		require.NoError(t, err)
		assert.True(t, apiKeyIsNull, "store must NOT have mutated api_key — row should still be legal OAuth shape")

		exp, err := repo.GetForExport(context.Background(), oauthID)
		require.NoError(t, err)
		assert.Equal(t, []byte("access-bytes-v1"), exp.AccessToken, "tokens must be untouched")
	})

	t.Run("OAuth patch against api_key row is rejected", func(t *testing.T) {
		apiID, err := repo.InsertUpstreamAccount(context.Background(), apiKeyRow("k-cross-1"))
		require.NoError(t, err)

		now := time.Now().UTC().Truncate(time.Second)
		later := now.Add(time.Hour)
		err = repo.UpdateCredentials(context.Background(), apiID, CredentialPatch{
			AuthMethod:      domain.AuthMethodOAuthBrowser,
			AccessToken:     []byte("injected-at"),
			RefreshToken:    []byte("injected-rt"),
			IDToken:         []byte("injected-it"),
			LastRefresh:     &now,
			AccessExpiresAt: &later,
		})
		require.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrAuthMethodMismatch)
		assert.Contains(t, err.Error(), "patch auth_method=\"oauth_browser\" does not match stored auth_method=\"api_key\"")

		got, err := repo.GetByID(context.Background(), apiID)
		require.NoError(t, err)
		assert.Equal(t, "sk-live-k-cross-1", got.APIKey, "api_key row must NOT have been converted to an OAuth row")
		assert.Equal(t, domain.AuthMethodAPIKey, got.AuthMethod)
		assert.Nil(t, got.AccessToken, "no token bytes must have been injected")
	})

	t.Run("mismatch check runs before patch-shape check when row id is missing", func(t *testing.T) {
		// Patch itself would be rejected anyway (missing id), but we
		// verify mismatch-check only runs for FOUND rows — a 404 path
		// should produce ErrAccountNotFound, not ErrAuthMethodMismatch.
		err := repo.UpdateCredentials(context.Background(), 98765, CredentialPatch{
			AuthMethod: domain.AuthMethodAPIKey,
			APIKey:     "sk-x",
		})
		assert.ErrorIs(t, err, domain.ErrAccountNotFound)
	})
}

// TestAccountsStore_UpdateCredentials_ImmutabilityFullMatrix is the
// round-2 external-review strengthening of the rule-3 guard: rather
// than covering only the api_key↔oauth_browser pair, assert EVERY
// ordered pair of (stored, patch) auth_methods where stored != patch
// is rejected with ErrAuthMethodMismatch. A future relaxation that
// "same credential family" is acceptable (e.g. oauth_browser ->
// oauth_device) would violate data-model.md rule 3 just as badly as
// api_key -> oauth_*, because the row's identity and login history
// are method-specific — this test kills that mutation.
func TestAccountsStore_UpdateCredentials_ImmutabilityFullMatrix(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	repo := NewAccountRepo(s.Engine())

	methods := []domain.AuthMethod{
		domain.AuthMethodAPIKey,
		domain.AuthMethodOAuthBrowser,
		domain.AuthMethodOAuthDevice,
		domain.AuthMethodOAuthImport,
	}

	insertForMethod := func(t *testing.T, name string, m domain.AuthMethod) int64 {
		t.Helper()
		if m == domain.AuthMethodAPIKey {
			id, err := repo.InsertUpstreamAccount(context.Background(), apiKeyRow(name))
			require.NoError(t, err)
			return id
		}
		row := oauthRow(name)
		row.AuthMethod = m
		id, err := repo.InsertUpstreamAccount(context.Background(), row)
		require.NoError(t, err)
		return id
	}

	patchForMethod := func(m domain.AuthMethod) CredentialPatch {
		if m == domain.AuthMethodAPIKey {
			return CredentialPatch{
				AuthMethod: m,
				APIKey:     "sk-cross-family",
			}
		}
		now := time.Now().UTC().Truncate(time.Second)
		later := now.Add(time.Hour)
		return CredentialPatch{
			AuthMethod:      m,
			AccessToken:     []byte("cross-at"),
			RefreshToken:    []byte("cross-rt"),
			IDToken:         []byte("cross-it"),
			LastRefresh:     &now,
			AccessExpiresAt: &later,
		}
	}

	for _, stored := range methods {
		for _, patch := range methods {
			if stored == patch {
				continue
			}
			stored, patch := stored, patch
			t.Run(string(stored)+"_to_"+string(patch), func(t *testing.T) {
				id := insertForMethod(t, "matrix-"+string(stored)+"-"+string(patch), stored)
				err := repo.UpdateCredentials(context.Background(), id, patchForMethod(patch))
				require.Error(t, err, "stored=%s patch=%s: must reject auth_method change", stored, patch)
				assert.ErrorIs(t, err, domain.ErrAuthMethodMismatch,
					"stored=%s patch=%s: must surface ErrAuthMethodMismatch", stored, patch)
			})
		}
	}
}

// TestAccountsStore_LegacyCreate_DefaultsAuthMethod is the
// regression guard for the external expert reviewer's P0 blocker
// (round-1, Codex MCP): repo.Create was the 001/002 insert path and
// its callers (AccountService.Create, setup.commit, admin handler)
// never set AuthMethod because the field did not exist before 003.
// xorm's INSERT builder sends the Go zero value for every mapped
// column, so auth_method==” was landing on disk instead of the
// SQL-level DEFAULT 'api_key' kicking in. On SQLite that's silent
// corruption; on PostgreSQL / MySQL 8.0.16+ the new CHECK constraint
// refuses the write entirely, turning a 003 migration into an
// immediate outage for every existing 001/002 deployment.
//
// The fix normalises AuthMethod inside Create /
// CreateIdempotentByName; this test asserts the normalisation still
// happens end-to-end by reading the raw column back after an insert
// that does NOT set AuthMethod.
func TestAccountsStore_LegacyCreate_DefaultsAuthMethod(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	repo := NewAccountRepo(s.Engine())

	t.Run("Create without AuthMethod lands api_key on disk", func(t *testing.T) {
		acct := &domain.UpstreamAccount{
			Name:     "legacy-1",
			Provider: domain.ProviderOpenAI,
			APIKey:   "sk-legacy",
			Status:   domain.AccountStatusActive,
			// AuthMethod intentionally zero-value — mirrors every
			// 001/002 writer in the tree.
		}
		require.NoError(t, repo.Create(context.Background(), acct))

		var methodOnDisk string
		_, err := s.Engine().SQL(
			"SELECT auth_method FROM upstream_accounts WHERE id = ?", acct.ID,
		).Get(&methodOnDisk)
		require.NoError(t, err)
		assert.Equal(t, string(domain.AuthMethodAPIKey), methodOnDisk,
			"zero-value AuthMethod must normalise to 'api_key' on disk — otherwise PG/MySQL CHECK rejects and SQLite silently stores ''")

		// And the in-memory account is also mutated, so any follow-up
		// reads/log lines see the canonical value.
		assert.Equal(t, domain.AuthMethodAPIKey, acct.AuthMethod)
	})

	t.Run("CreateIdempotentByName without AuthMethod lands api_key on disk", func(t *testing.T) {
		acct := &domain.UpstreamAccount{
			Name:     "legacy-idempotent-1",
			Provider: domain.ProviderOpenAI,
			APIKey:   "sk-legacy-idemp",
			Status:   domain.AccountStatusActive,
		}
		require.NoError(t, repo.CreateIdempotentByName(context.Background(), acct))

		var methodOnDisk string
		_, err := s.Engine().SQL(
			"SELECT auth_method FROM upstream_accounts WHERE id = ?", acct.ID,
		).Get(&methodOnDisk)
		require.NoError(t, err)
		assert.Equal(t, string(domain.AuthMethodAPIKey), methodOnDisk)
		assert.Equal(t, domain.AuthMethodAPIKey, acct.AuthMethod)
	})

	t.Run("Create still rejects invalid api_key shape (no api_key string)", func(t *testing.T) {
		// Defaulting AuthMethod doesn't weaken the other shape
		// invariants: an account with AuthMethod=='' and APIKey=''
		// must still fail Validate after normalisation.
		acct := &domain.UpstreamAccount{
			Name:     "no-key",
			Provider: domain.ProviderOpenAI,
			Status:   domain.AccountStatusActive,
		}
		err := repo.Create(context.Background(), acct)
		require.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrInvalidAccountShape)
	})
}

// TestAccountsStore_GetForExport_RejectsCorruptedOAuthRow is the
// regression guard for the second external review finding: a row
// tagged auth_method='oauth_browser' but with missing critical
// OAuth columns previously sailed through GetForExport and returned
// an ExportPayload with empty values. The downstream Codex client
// would then receive an auth.json that *looks* valid structurally
// but fails at the next authenticated call, with no clear error
// path back to the router operator.
//
// Parametrised across every mandatory OAuth column (rule 1 of
// data-model.md §Invariants): a corruption of any ONE of them is
// sufficient to reject the export. access_expires_at is included
// per round-2 external review — even though that column is not
// written into auth.json, its absence breaks RefreshIfStale and
// would ship a bundle the router itself considers broken.
func TestAccountsStore_GetForExport_RejectsCorruptedOAuthRow(t *testing.T) {
	cases := []struct {
		col string
		sql string
	}{
		{"access_token", "UPDATE upstream_accounts SET access_token = NULL WHERE id = ?"},
		{"refresh_token", "UPDATE upstream_accounts SET refresh_token = NULL WHERE id = ?"},
		{"id_token", "UPDATE upstream_accounts SET id_token = NULL WHERE id = ?"},
		{"last_refresh", "UPDATE upstream_accounts SET last_refresh = NULL WHERE id = ?"},
		{"access_expires_at", "UPDATE upstream_accounts SET access_expires_at = NULL WHERE id = ?"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.col, func(t *testing.T) {
			s, cleanup := setupTestStore(t)
			defer cleanup()
			repo := NewAccountRepo(s.Engine())

			id, err := repo.InsertUpstreamAccount(context.Background(), oauthRow("intact-"+tc.col))
			require.NoError(t, err)

			_, err = s.Engine().Exec(tc.sql, id)
			require.NoError(t, err)

			_, err = repo.GetForExport(context.Background(), id)
			require.Error(t, err)
			assert.ErrorIs(t, err, domain.ErrInvalidAccountShape)
			assert.Contains(t, err.Error(), tc.col)
			assert.Contains(t, err.Error(), "corrupted row")
		})
	}

	t.Run("multiple_columns_missing_all_reported", func(t *testing.T) {
		s, cleanup := setupTestStore(t)
		defer cleanup()
		repo := NewAccountRepo(s.Engine())

		id, err := repo.InsertUpstreamAccount(context.Background(), oauthRow("multi-corrupt"))
		require.NoError(t, err)

		_, err = s.Engine().Exec(
			"UPDATE upstream_accounts SET access_token = NULL, refresh_token = NULL, id_token = NULL, last_refresh = NULL, access_expires_at = NULL WHERE id = ?",
			id,
		)
		require.NoError(t, err)

		_, err = repo.GetForExport(context.Background(), id)
		require.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrInvalidAccountShape)
		for _, col := range []string{"access_token", "refresh_token", "id_token", "last_refresh", "access_expires_at"} {
			assert.Contains(t, err.Error(), col, "combined-corruption error must list every missing column")
		}
	})
}

// TestAccountsStore_GetForExport_RejectsNonOAuthAuthMethod proves
// that GetForExport guards against the defensive "auth_method is
// somehow not one of the OAuth variants" case — a bug or migration
// artefact that leaves auth_method=” would previously have returned
// an empty ExportPayload instead of an error.
func TestAccountsStore_GetForExport_RejectsNonOAuthAuthMethod(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	repo := NewAccountRepo(s.Engine())

	// Insert an api_key row, then flip auth_method to '' at the DB
	// layer to simulate a broken migration. The store helper must
	// still reject the export.
	id, err := repo.InsertUpstreamAccount(context.Background(), apiKeyRow("corrupt"))
	require.NoError(t, err)

	_, err = s.Engine().Exec(
		"UPDATE upstream_accounts SET auth_method = '' WHERE id = ?", id,
	)
	require.NoError(t, err)

	_, err = repo.GetForExport(context.Background(), id)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrInvalidAccountShape,
		"non-OAuth rows (including empty auth_method) must not be exportable")
	assert.Contains(t, err.Error(), "only oauth_* rows are exportable")
}

func TestAccountsStore_GetForExport_DeletedRowIsNotFound(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	repo := NewAccountRepo(s.Engine())

	id, err := repo.InsertUpstreamAccount(context.Background(), oauthRow("deleted-export"))
	require.NoError(t, err)

	_, err = s.Engine().ID(id).Cols("status").Update(&domain.UpstreamAccount{
		Status: domain.AccountStatusDeleted,
	})
	require.NoError(t, err)

	_, err = repo.GetForExport(context.Background(), id)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrAccountNotFound)
}

// TestAccountsStore_ListForAdminAPI_EmptyAuthMethodFallback exercises
// the defence-in-depth fallback in ListForAdminAPI that tags rows
// with auth_method=” as api_key. Live DBs never produce this state
// (NOT NULL DEFAULT 'api_key'), but bypassing writes via raw SQL is
// how real corruption is introduced, so the admin-list must not
// crash-loop.
func TestAccountsStore_ListForAdminAPI_EmptyAuthMethodFallback(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	repo := NewAccountRepo(s.Engine())

	id, err := repo.InsertUpstreamAccount(context.Background(), apiKeyRow("survivor"))
	require.NoError(t, err)

	_, err = s.Engine().Exec(
		"UPDATE upstream_accounts SET auth_method = '' WHERE id = ?", id,
	)
	require.NoError(t, err)

	items, err := repo.ListForAdminAPI(context.Background())
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, domain.AuthMethodAPIKey, items[0].AuthMethod,
		"empty auth_method must default to api_key so the admin list stays renderable")
}

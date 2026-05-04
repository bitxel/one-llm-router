package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/user/one-llm-router/internal/domain"
)

type fakeDashboardRepo struct {
	gotQuery DashboardQuery
	result   DashboardAggregation
	err      error
}

func (r *fakeDashboardRepo) Dashboard(_ context.Context, query DashboardQuery) (DashboardAggregation, error) {
	r.gotQuery = query
	if r.err != nil {
		return DashboardAggregation{}, r.err
	}
	return r.result, nil
}

type listFailsDashboardAccountRepo struct {
	*inMemoryAccountRepo
}

func (r *listFailsDashboardAccountRepo) List(_ context.Context, _ []string) ([]domain.UpstreamAccount, error) {
	return nil, errors.New("forced account list failure")
}

func TestDashboardService_DashboardBuildsSnapshotAndPreservesSelectedAccount(t *testing.T) {
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	accountRepo := newInMemoryAccountRepo()
	baseURL := "https://api.openai.com"
	email := "plus@example.com"
	plan := "ChatGPT Plus"
	chatGPTAccountID := "acct_plus"
	lastRefresh := now.Add(-time.Hour)
	accessExpiresAt := now.Add(time.Hour)

	apiKey := &domain.UpstreamAccount{
		Name:       "api-key",
		Provider:   domain.ProviderOpenAI,
		BaseURL:    &baseURL,
		APIKey:     "sk-api-key",
		AuthMethod: domain.AuthMethodAPIKey,
		Status:     domain.AccountStatusActive,
		CreatedAt:  now.Add(-48 * time.Hour),
		UpdatedAt:  now.Add(-47 * time.Hour),
	}
	require.NoError(t, accountRepo.Create(context.Background(), apiKey))
	oauth := &domain.UpstreamAccount{
		Name:             "oauth",
		Provider:         domain.ProviderOpenAI,
		AuthMethod:       domain.AuthMethodOAuthBrowser,
		Status:           domain.AccountStatusDisabled,
		Email:            &email,
		PlanType:         &plan,
		ChatGPTAccountID: &chatGPTAccountID,
		LastRefresh:      &lastRefresh,
		AccessExpiresAt:  &accessExpiresAt,
		CreatedAt:        now.Add(-24 * time.Hour),
		UpdatedAt:        now.Add(-23 * time.Hour),
	}
	require.NoError(t, accountRepo.Create(context.Background(), oauth))
	require.NoError(t, accountRepo.Create(context.Background(), &domain.UpstreamAccount{
		Name:       "deleted",
		Provider:   domain.ProviderOpenAI,
		APIKey:     "sk-deleted",
		AuthMethod: domain.AuthMethodAPIKey,
		Status:     domain.AccountStatusDeleted,
	}))

	aggregation := DashboardAggregation{
		Range:         DashboardRange7D,
		WindowStart:   now.Add(-7 * 24 * time.Hour),
		WindowEnd:     now,
		BucketSeconds: int((6 * time.Hour).Seconds()),
		Requests:      DashboardRequestsCard{Total: 8},
		Tokens: DashboardTokensCard{
			Totals: DashboardTokenBreakdown{InputCached: 1, InputNonCached: 2, Output: 3},
		},
		ErrorRate: DashboardErrorRateCard{Value: 0.25, Total: 8, Errors: 2},
		TTFT:      DashboardTTFTCard{P95MS: intPtr(420), SampleCount: 4},
	}
	recordRepo := &fakeDashboardRepo{result: aggregation}
	svc := NewDashboardService(recordRepo, accountRepo)
	svc.SetClock(func() time.Time { return now })

	snapshot, err := svc.Dashboard(context.Background(), DashboardQuery{AccountID: &oauth.ID})
	require.NoError(t, err)

	assert.Equal(t, DashboardRange7D, recordRepo.gotQuery.Range)
	assert.Equal(t, now, recordRepo.gotQuery.Now)
	require.NotNil(t, recordRepo.gotQuery.AccountID)
	assert.Equal(t, oauth.ID, *recordRepo.gotQuery.AccountID)
	assert.Equal(t, aggregation, snapshot.DashboardAggregation)
	assert.Equal(t, 1, snapshot.ActiveAccounts)
	require.Len(t, snapshot.AccountOptions, 2)
	options := dashboardOptionsByLabel(snapshot.AccountOptions)
	require.Contains(t, options, "api-key")
	require.Contains(t, options, "oauth")
	assert.Nil(t, options["api-key"].Email)
	assert.Equal(t, &email, options["oauth"].Email)
	assert.Equal(t, &plan, options["oauth"].PlanType)
	assert.Equal(t, &chatGPTAccountID, options["oauth"].ChatGPTAccountID)
	assert.Equal(t, &lastRefresh, options["oauth"].LastRefresh)
	assert.Equal(t, &accessExpiresAt, options["oauth"].AccessExpiresAt)
	require.NotNil(t, snapshot.SelectedAccountID)
	assert.Equal(t, oauth.ID, *snapshot.SelectedAccountID)
}

func TestDashboardService_DashboardRejectsUnknownSelectedAccount(t *testing.T) {
	accountRepo := newInMemoryAccountRepo()
	recordRepo := &fakeDashboardRepo{}
	svc := NewDashboardService(recordRepo, accountRepo)
	missingID := int64(99)

	_, err := svc.Dashboard(context.Background(), DashboardQuery{AccountID: &missingID})

	var invalid *DashboardInvalidFilterError
	require.ErrorAs(t, err, &invalid)
	assert.Equal(t, "account_id", invalid.Field)
	assert.Equal(t, "account not found", invalid.Reason)
	assert.Zero(t, recordRepo.gotQuery.Range)
}

func TestDashboardService_DashboardWrapsRepositoryErrors(t *testing.T) {
	t.Run("account list", func(t *testing.T) {
		accountRepo := &listFailsDashboardAccountRepo{inMemoryAccountRepo: newInMemoryAccountRepo()}
		svc := NewDashboardService(&fakeDashboardRepo{}, accountRepo)

		_, err := svc.Dashboard(context.Background(), DashboardQuery{})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "list dashboard accounts")
	})

	t.Run("record aggregation", func(t *testing.T) {
		accountRepo := newInMemoryAccountRepo()
		require.NoError(t, accountRepo.Create(context.Background(), &domain.UpstreamAccount{
			Name: "active", Provider: domain.ProviderOpenAI, APIKey: "sk-active", Status: domain.AccountStatusActive,
		}))
		svc := NewDashboardService(&fakeDashboardRepo{err: errors.New("aggregate failed")}, accountRepo)

		_, err := svc.Dashboard(context.Background(), DashboardQuery{})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "aggregate dashboard records")
	})
}

func TestDashboardService_DashboardRejectsNilDependencies(t *testing.T) {
	_, err := (*DashboardService)(nil).Dashboard(context.Background(), DashboardQuery{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "records repository is nil")

	_, err = NewDashboardService(nil, newInMemoryAccountRepo()).Dashboard(context.Background(), DashboardQuery{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "records repository is nil")

	_, err = NewDashboardService(&fakeDashboardRepo{}, nil).Dashboard(context.Background(), DashboardQuery{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "account repository is nil")
}

func TestNormalizeDashboardQuery(t *testing.T) {
	fallback := time.Date(2026, 4, 25, 12, 0, 0, 0, time.FixedZone("SGT", 8*60*60))

	normalized, err := NormalizeDashboardQuery(DashboardQuery{}, fallback)
	require.NoError(t, err)
	assert.Equal(t, DashboardRange7D, normalized.Range)
	assert.Equal(t, fallback.UTC(), normalized.Now)

	badAccountID := int64(0)
	_, err = NormalizeDashboardQuery(DashboardQuery{Range: DashboardRange1H, AccountID: &badAccountID}, fallback)
	var invalid *DashboardInvalidFilterError
	require.ErrorAs(t, err, &invalid)
	assert.Equal(t, "account_id", invalid.Field)
	assert.Equal(t, "must be positive", invalid.Reason)

	_, err = NormalizeDashboardQuery(DashboardQuery{Range: DashboardRange("90d")}, fallback)
	require.ErrorAs(t, err, &invalid)
	assert.Equal(t, "range", invalid.Field)
	assert.Equal(t, "must be one of 1h, 1d, 7d, 30d", invalid.Reason)
}

func TestDashboardWindow(t *testing.T) {
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name       string
		rangeValue DashboardRange
		wantStart  time.Time
		wantBucket time.Duration
	}{
		{name: "1h", rangeValue: DashboardRange1H, wantStart: now.Add(-time.Hour), wantBucket: time.Minute},
		{name: "1d", rangeValue: DashboardRange1D, wantStart: now.Add(-24 * time.Hour), wantBucket: time.Hour},
		{name: "7d", rangeValue: DashboardRange7D, wantStart: now.Add(-7 * 24 * time.Hour), wantBucket: 6 * time.Hour},
		{name: "30d", rangeValue: DashboardRange30D, wantStart: now.Add(-30 * 24 * time.Hour), wantBucket: 24 * time.Hour},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start, end, bucket, err := DashboardWindow(DashboardQuery{Range: tc.rangeValue, Now: now})
			require.NoError(t, err)
			assert.Equal(t, tc.wantStart, start)
			assert.Equal(t, now, end)
			assert.Equal(t, tc.wantBucket, bucket)
		})
	}
}

func TestDashboardInvalidFilterError_Error(t *testing.T) {
	assert.Equal(t, "dashboard invalid filter", (*DashboardInvalidFilterError)(nil).Error())
	assert.Equal(t, "dashboard invalid filter: range", (&DashboardInvalidFilterError{Field: "range"}).Error())
	assert.Equal(t, "dashboard invalid filter: range: bad", (&DashboardInvalidFilterError{Field: "range", Reason: "bad"}).Error())
}

func intPtr(value int) *int {
	return &value
}

func dashboardOptionsByLabel(options []DashboardAccountOption) map[string]DashboardAccountOption {
	result := make(map[string]DashboardAccountOption, len(options))
	for _, option := range options {
		result[option.Label] = option
	}
	return result
}

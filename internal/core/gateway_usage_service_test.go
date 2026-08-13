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

func TestGatewayUsageServiceAdminUsageAggregatesRetainedRecords(t *testing.T) {
	t.Parallel()

	repo := &gatewayUsageRecordRepo{
		records: []domain.RequestRecord{
			{ID: 1, TokenUsage: domain.JSONMap{"input": 10, "output": 5, "cached_input": 2}},
			{ID: 2, TokenUsage: domain.JSONMap{"input_tokens": float64(3), "output_tokens": float64(7), "cached_input_tokens": float64(1)}},
			{ID: 3},
		},
	}
	svc := NewGatewayUsageService(repo, &gatewayUsageAccountRepo{
		accounts: []domain.UpstreamAccount{{
			AuthMethod: domain.AuthMethodOAuthBrowser,
			Status:     domain.AccountStatusActive,
			PlanType:   stringPtrCore("chatgpt-plus"),
		}},
	})

	usage, err := svc.AdminUsage(context.Background())

	require.NoError(t, err)
	assert.Equal(t, 3, usage.RequestCount)
	assert.Equal(t, 25, usage.TotalTokens)
	assert.Equal(t, 3, usage.CachedInputTokens)
	assert.Equal(t, 0.0, usage.TotalCostUSD)
	assert.Empty(t, usage.Limits)
	assert.Equal(t, "chatgpt-plus", usage.Codex.PlanType)
	assert.Nil(t, usage.Codex.RateLimit)
	assert.Nil(t, usage.Codex.Credits)
	assert.Empty(t, usage.Codex.AdditionalRateLimits)
}

func TestGatewayUsageServiceAdminUsageUsesRepositorySummary(t *testing.T) {
	t.Parallel()

	repo := &gatewayUsageSummaryRepo{
		summary: RequestUsageSummary{
			RequestCount:      42,
			TotalTokens:       99,
			CachedInputTokens: 7,
		},
	}
	svc := NewGatewayUsageService(repo, &gatewayUsageAccountRepo{})

	usage, err := svc.AdminUsage(context.Background())

	require.NoError(t, err)
	assert.Equal(t, 42, usage.RequestCount)
	assert.Equal(t, 99, usage.TotalTokens)
	assert.Equal(t, 7, usage.CachedInputTokens)
	assert.Empty(t, usage.Limits)
	assert.Equal(t, "guest", usage.Codex.PlanType)
	assert.False(t, repo.queryCalled, "summary-capable repositories must avoid unbounded Query")
}

func TestGatewayUsageServiceAdminUsageCodexPlanType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		accounts []domain.UpstreamAccount
		want     string
	}{
		{name: "no oauth accounts", want: "guest"},
		{
			name: "single oauth plan",
			accounts: []domain.UpstreamAccount{{
				AuthMethod: domain.AuthMethodOAuthBrowser,
				Status:     domain.AccountStatusActive,
				PlanType:   stringPtrCore("chatgpt-plus"),
			}},
			want: "chatgpt-plus",
		},
		{
			name: "unknown oauth plan",
			accounts: []domain.UpstreamAccount{{
				AuthMethod: domain.AuthMethodOAuthImport,
				Status:     domain.AccountStatusActive,
			}},
			want: "unknown",
		},
		{
			name: "mixed oauth plans",
			accounts: []domain.UpstreamAccount{
				{AuthMethod: domain.AuthMethodOAuthBrowser, Status: domain.AccountStatusActive, PlanType: stringPtrCore("chatgpt-plus")},
				{AuthMethod: domain.AuthMethodOAuthDevice, Status: domain.AccountStatusActive, PlanType: stringPtrCore("chatgpt-pro")},
			},
			want: "mixed",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc := NewGatewayUsageService(&gatewayUsageRecordRepo{}, &gatewayUsageAccountRepo{accounts: tc.accounts})
			usage, err := svc.AdminUsage(context.Background())

			require.NoError(t, err)
			assert.Equal(t, tc.want, usage.Codex.PlanType)
			assert.Nil(t, usage.Codex.RateLimit)
			assert.Nil(t, usage.Codex.Credits)
			assert.Empty(t, usage.Codex.AdditionalRateLimits)
		})
	}
}

func TestGatewayUsageServiceAdminUsagePropagatesDependencies(t *testing.T) {
	t.Parallel()

	recordErr := errors.New("record store offline")
	accountErr := errors.New("account store offline")
	tests := []struct {
		name     string
		records  RequestRecordRepository
		accounts AccountRepository
		wantErr  string
	}{
		{
			name:     "record summary failure",
			records:  &gatewayUsageRecordRepo{err: recordErr},
			accounts: &gatewayUsageAccountRepo{},
			wantErr:  "summarize request records",
		},
		{
			name:     "account list failure",
			records:  &gatewayUsageRecordRepo{},
			accounts: &gatewayUsageAccountRepo{err: accountErr},
			wantErr:  "list active accounts",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc := NewGatewayUsageService(tc.records, tc.accounts)
			_, err := svc.AdminUsage(context.Background())

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

type gatewayUsageRecordRepo struct {
	records []domain.RequestRecord
	err     error
}

func (r *gatewayUsageRecordRepo) Insert(context.Context, *domain.RequestRecord) error { return nil }
func (r *gatewayUsageRecordRepo) GetByID(context.Context, int64) (*domain.RequestRecord, error) {
	return nil, domain.ErrRequestRecordNotFound
}
func (r *gatewayUsageRecordRepo) Query(_ context.Context, _ QueryParams) ([]domain.RequestRecord, error) {
	return r.records, nil
}
func (r *gatewayUsageRecordRepo) UsageSummary(context.Context) (RequestUsageSummary, error) {
	if r.err != nil {
		return RequestUsageSummary{}, r.err
	}
	summary := RequestUsageSummary{RequestCount: len(r.records)}
	for _, record := range r.records {
		input := usageInt(record.TokenUsage, "input", "input_tokens")
		output := usageInt(record.TokenUsage, "output", "output_tokens")
		summary.TotalTokens += input + output
		summary.CachedInputTokens += usageInt(record.TokenUsage, "cached_input", "cached_input_tokens")
	}
	return summary, nil
}
func (r *gatewayUsageRecordRepo) FilterOptions(context.Context, QueryParams) (RequestFilterOptions, error) {
	return RequestFilterOptions{}, nil
}
func (r *gatewayUsageRecordRepo) DeleteBefore(context.Context, time.Time) (int64, error) {
	return 0, nil
}

type gatewayUsageSummaryRepo struct {
	gatewayUsageRecordRepo
	summary     RequestUsageSummary
	queryCalled bool
}

func (r *gatewayUsageSummaryRepo) UsageSummary(context.Context) (RequestUsageSummary, error) {
	return r.summary, nil
}

func (r *gatewayUsageSummaryRepo) Query(context.Context, QueryParams) ([]domain.RequestRecord, error) {
	r.queryCalled = true
	return nil, nil
}

type gatewayUsageAccountRepo struct {
	accounts []domain.UpstreamAccount
	err      error
}

func (r *gatewayUsageAccountRepo) Create(context.Context, *domain.UpstreamAccount) error { return nil }
func (r *gatewayUsageAccountRepo) GetByID(context.Context, int64) (*domain.UpstreamAccount, error) {
	return nil, domain.ErrAccountNotFound
}
func (r *gatewayUsageAccountRepo) List(context.Context, []string) ([]domain.UpstreamAccount, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.accounts, nil
}
func (r *gatewayUsageAccountRepo) ListActive(context.Context) ([]domain.UpstreamAccount, error) {
	return r.accounts, nil
}
func (r *gatewayUsageAccountRepo) UpdateStatus(context.Context, int64, string) error { return nil }
func (r *gatewayUsageAccountRepo) UpdateDetails(context.Context, int64, AccountDetailsPatch) (*domain.UpstreamAccount, error) {
	return nil, domain.ErrAccountNotFound
}

func stringPtrCore(value string) *string { return &value }

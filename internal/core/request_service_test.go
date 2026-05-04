package core

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/user/one-llm-router/internal/domain"
)

// limitSpyRepo records the QueryParams the service forwards so we can
// assert on the effective (post-default) limit value.
type limitSpyRepo struct {
	last    QueryParams
	records []domain.RequestRecord
	options RequestFilterOptions
}

func (s *limitSpyRepo) Insert(_ context.Context, _ *domain.RequestRecord) error { return nil }

func (s *limitSpyRepo) GetByID(_ context.Context, id int64) (*domain.RequestRecord, error) {
	for _, record := range s.records {
		if record.ID == id {
			return &record, nil
		}
	}
	return nil, domain.ErrRequestRecordNotFound
}

func (s *limitSpyRepo) Query(_ context.Context, params QueryParams) ([]domain.RequestRecord, error) {
	s.last = params
	return s.records, nil
}

func (s *limitSpyRepo) UsageSummary(context.Context) (RequestUsageSummary, error) {
	return RequestUsageSummary{}, nil
}

func (s *limitSpyRepo) FilterOptions(_ context.Context, params QueryParams) (RequestFilterOptions, error) {
	s.last = params
	return s.options, nil
}

func (s *limitSpyRepo) DeleteBefore(_ context.Context, _ time.Time) (int64, error) { return 0, nil }

func TestRequestService_QueryLimitDefaulting(t *testing.T) {
	cases := []struct {
		name  string
		input int
		want  int
	}{
		{"zero_defaults_to_50", 0, 50},
		{"negative_defaults_to_50", -1, 50},
		{"over_max_defaults_to_50", 999, 50},
		{"valid_200_kept", 200, 200},
		{"valid_1_kept", 1, 1},
		{"valid_50_kept", 50, 50},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spy := &limitSpyRepo{}
			svc := NewRequestService(spy)
			_, err := svc.Query(context.Background(), QueryParams{Limit: tc.input})
			require.NoError(t, err)
			assert.Equal(t, tc.want, spy.last.Limit, "effective Limit passed to repo")
		})
	}
}

func TestRequestService_Query_PropagatesFilters(t *testing.T) {
	spy := &limitSpyRepo{}
	svc := NewRequestService(spy)

	acct := int64(42)
	outcome := domain.OutcomeSuccess
	_, err := svc.Query(context.Background(), QueryParams{Limit: 10, AccountID: &acct, Outcome: &outcome})
	require.NoError(t, err)
	assert.Equal(t, int64(42), *spy.last.AccountID)
	require.NotNil(t, spy.last.Outcome)
	assert.Equal(t, domain.OutcomeSuccess, *spy.last.Outcome)
}

func TestRequestService_QueryPage_FetchesOneExtraAndReturnsCursor(t *testing.T) {
	spy := &limitSpyRepo{
		records: []domain.RequestRecord{
			{ID: 10, RequestID: "req_10"},
			{ID: 9, RequestID: "req_9"},
			{ID: 8, RequestID: "req_8"},
		},
	}
	svc := NewRequestService(spy)

	page, err := svc.QueryPage(context.Background(), QueryParams{Limit: 2})
	require.NoError(t, err)

	assert.Equal(t, 3, spy.last.Limit)
	require.Len(t, page.Records, 2)
	assert.True(t, page.HasMore)
	require.NotNil(t, page.NextBeforeID)
	assert.Equal(t, int64(9), *page.NextBeforeID)
}

func TestRequestService_FilterOptions_NormalizesSearchAndLimit(t *testing.T) {
	spy := &limitSpyRepo{}
	svc := NewRequestService(spy)
	search := "  req_123  "

	_, err := svc.FilterOptions(context.Background(), QueryParams{Search: &search, Limit: 999})
	require.NoError(t, err)

	require.NotNil(t, spy.last.Search)
	assert.Equal(t, "req_123", *spy.last.Search)
	assert.Equal(t, 0, spy.last.Limit)
}

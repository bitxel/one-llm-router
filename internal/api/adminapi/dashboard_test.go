package adminapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/user/one-llm-router/internal/api/errcode"
	"github.com/user/one-llm-router/internal/api/testutil"
	"github.com/user/one-llm-router/internal/core"
)

func TestDashboardHandler_DefaultSuccessEnvelope(t *testing.T) {
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	service := &fakeDashboardService{
		snapshot: core.DashboardSnapshot{
			DashboardAggregation: core.DashboardAggregation{
				Range:         core.DashboardRange7D,
				WindowStart:   now.Add(-7 * 24 * time.Hour),
				WindowEnd:     now,
				BucketSeconds: 6 * 60 * 60,
				Requests: core.DashboardRequestsCard{
					Total: 12,
					Series: []core.DashboardCountPoint{
						{Timestamp: now, Count: 12},
					},
				},
				Tokens: core.DashboardTokensCard{
					Totals: core.DashboardTokenBreakdown{InputCached: 4, InputNonCached: 8, Output: 6},
					Series: []core.DashboardTokenPoint{
						{
							Timestamp:               now,
							DashboardTokenBreakdown: core.DashboardTokenBreakdown{InputCached: 4, InputNonCached: 8, Output: 6},
						},
					},
				},
				ErrorRate: core.DashboardErrorRateCard{Value: 0.25, Total: 12, Errors: 3},
				TTFT:      core.DashboardTTFTCard{P95MS: intPtr(240), SampleCount: 2},
			},
			ActiveAccounts: 2,
			AccountOptions: []core.DashboardAccountOption{
				{ID: 7, Label: "plus-account"},
			},
		},
	}

	rec := runDashboardRequest(t, service, "/api/admin/dashboard")

	data := testutil.AssertEnvelopeDataShape(t, rec, errcode.OK)
	assert.Equal(t, "7d", data["range"])
	assert.Equal(t, float64(21600), data["bucket_seconds"])
	cards := data["cards"].(map[string]any)
	active := cards["active_accounts"].(map[string]any)
	assert.Equal(t, float64(2), active["value"])
	assert.Equal(t, core.DashboardRange7D, service.lastQuery.Range)
	assert.Nil(t, service.lastQuery.AccountID)
}

func TestDashboardHandler_ParsesRangeAndAccountFilter(t *testing.T) {
	service := &fakeDashboardService{snapshot: minimalDashboardSnapshot()}

	rec := runDashboardRequest(t, service, "/api/admin/dashboard?range=1h&account_id=7")

	testutil.AssertEnvelopeDataShape(t, rec, errcode.OK)
	require.NotNil(t, service.lastQuery.AccountID)
	assert.Equal(t, int64(7), *service.lastQuery.AccountID)
	assert.Equal(t, core.DashboardRange1H, service.lastQuery.Range)
}

func TestDashboardHandler_InvalidRange(t *testing.T) {
	service := &fakeDashboardService{snapshot: minimalDashboardSnapshot()}

	rec := runDashboardRequest(t, service, "/api/admin/dashboard?range=90d")

	data := testutil.AssertEnvelopeDataShape(t, rec, errcode.DashboardInvalidFilter)
	assert.Equal(t, "range", data["field"])
	assert.False(t, service.called)
}

func TestDashboardHandler_ServiceSystemError(t *testing.T) {
	service := &fakeDashboardService{err: errors.New("store closed")}

	rec := runDashboardRequest(t, service, "/api/admin/dashboard")

	testutil.AssertEnvelopeDataShape(t, rec, errcode.DashboardInternalError)
}

func runDashboardRequest(t *testing.T, service *fakeDashboardService, target string) *httptest.ResponseRecorder {
	t.Helper()

	mux := http.NewServeMux()
	RegisterDashboardHandler(mux, NewDashboardHandler(service, slog.Default()), nil)
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

type fakeDashboardService struct {
	called    bool
	lastQuery core.DashboardQuery
	snapshot  core.DashboardSnapshot
	err       error
}

func (f *fakeDashboardService) Dashboard(_ context.Context, query core.DashboardQuery) (core.DashboardSnapshot, error) {
	f.called = true
	f.lastQuery = query
	if f.err != nil {
		return core.DashboardSnapshot{}, f.err
	}
	return f.snapshot, nil
}

func minimalDashboardSnapshot() core.DashboardSnapshot {
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	return core.DashboardSnapshot{
		DashboardAggregation: core.DashboardAggregation{
			Range:         core.DashboardRange7D,
			WindowStart:   now.Add(-7 * 24 * time.Hour),
			WindowEnd:     now,
			BucketSeconds: 6 * 60 * 60,
		},
	}
}

func intPtr(v int) *int {
	return &v
}

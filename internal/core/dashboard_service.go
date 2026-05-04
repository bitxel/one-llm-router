package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/user/one-llm-router/internal/domain"
)

type DashboardRange string

const (
	DashboardRange1H  DashboardRange = "1h"
	DashboardRange1D  DashboardRange = "1d"
	DashboardRange7D  DashboardRange = "7d"
	DashboardRange30D DashboardRange = "30d"
)

type DashboardQuery struct {
	Range     DashboardRange
	AccountID *int64
	Now       time.Time
}

type DashboardAggregation struct {
	Range         DashboardRange
	WindowStart   time.Time
	WindowEnd     time.Time
	BucketSeconds int
	Requests      DashboardRequestsCard
	Tokens        DashboardTokensCard
	ErrorRate     DashboardErrorRateCard
	TTFT          DashboardTTFTCard
}

type DashboardSnapshot struct {
	DashboardAggregation
	SelectedAccountID *int64
	ActiveAccounts    int
	AccountOptions    []DashboardAccountOption
}

type DashboardAccountOption struct {
	ID               int64
	Label            string
	Name             string
	Provider         string
	BaseURL          *string
	AuthMethod       domain.AuthMethod
	Status           string
	Email            *string
	PlanType         *string
	ChatGPTAccountID *string
	LastRefresh      *time.Time
	AccessExpiresAt  *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type DashboardCountPoint struct {
	Timestamp time.Time
	Count     int
}

type DashboardRequestsCard struct {
	Total  int
	Series []DashboardCountPoint
}

type DashboardTokenBreakdown struct {
	InputCached    int
	InputNonCached int
	Output         int
}

type DashboardTokenPoint struct {
	Timestamp time.Time
	DashboardTokenBreakdown
}

type DashboardTokensCard struct {
	Totals DashboardTokenBreakdown
	Series []DashboardTokenPoint
}

type DashboardRatePoint struct {
	Timestamp time.Time
	Total     int
	Errors    int
	Rate      float64
}

type DashboardErrorRateCard struct {
	Value  float64
	Total  int
	Errors int
	Series []DashboardRatePoint
}

type DashboardTTFTPoint struct {
	Timestamp   time.Time
	P95MS       *int
	SampleCount int
}

type DashboardTTFTCard struct {
	P95MS       *int
	SampleCount int
	Series      []DashboardTTFTPoint
}

type DashboardRepository interface {
	Dashboard(ctx context.Context, query DashboardQuery) (DashboardAggregation, error)
}

type DashboardInvalidFilterError struct {
	Field  string
	Reason string
}

func (e *DashboardInvalidFilterError) Error() string {
	if e == nil {
		return "dashboard invalid filter"
	}
	if e.Reason == "" {
		return fmt.Sprintf("dashboard invalid filter: %s", e.Field)
	}
	return fmt.Sprintf("dashboard invalid filter: %s: %s", e.Field, e.Reason)
}

type DashboardService struct {
	records  DashboardRepository
	accounts AccountRepository
	now      func() time.Time
}

func NewDashboardService(records DashboardRepository, accounts AccountRepository) *DashboardService {
	return &DashboardService{
		records:  records,
		accounts: accounts,
		now:      func() time.Time { return time.Now().UTC() },
	}
}

func (s *DashboardService) SetClock(now func() time.Time) {
	if now != nil {
		s.now = now
	}
}

func (s *DashboardService) Dashboard(ctx context.Context, query DashboardQuery) (DashboardSnapshot, error) {
	if s == nil || s.records == nil {
		return DashboardSnapshot{}, errors.New("dashboard service: records repository is nil")
	}
	if s.accounts == nil {
		return DashboardSnapshot{}, errors.New("dashboard service: account repository is nil")
	}
	normalized, err := NormalizeDashboardQuery(query, s.now())
	if err != nil {
		return DashboardSnapshot{}, err
	}

	accounts, err := s.accounts.List(ctx, nil)
	if err != nil {
		return DashboardSnapshot{}, fmt.Errorf("list dashboard accounts: %w", err)
	}
	activeAccounts := 0
	options := make([]DashboardAccountOption, 0, len(accounts))
	accountExists := normalized.AccountID == nil
	for _, account := range accounts {
		if account.Status == domain.AccountStatusDeleted {
			continue
		}
		if account.Status == domain.AccountStatusActive {
			activeAccounts++
		}
		if normalized.AccountID != nil && account.ID == *normalized.AccountID {
			accountExists = true
		}
		options = append(options, dashboardAccountOption(account))
	}
	if !accountExists {
		return DashboardSnapshot{}, &DashboardInvalidFilterError{Field: "account_id", Reason: "account not found"}
	}

	aggregation, err := s.records.Dashboard(ctx, normalized)
	if err != nil {
		return DashboardSnapshot{}, fmt.Errorf("aggregate dashboard records: %w", err)
	}
	return DashboardSnapshot{
		DashboardAggregation: aggregation,
		SelectedAccountID:    normalized.AccountID,
		ActiveAccounts:       activeAccounts,
		AccountOptions:       options,
	}, nil
}

func NormalizeDashboardQuery(query DashboardQuery, fallbackNow time.Time) (DashboardQuery, error) {
	if query.Range == "" {
		query.Range = DashboardRange7D
	}
	if !DashboardRangeValid(query.Range) {
		return DashboardQuery{}, &DashboardInvalidFilterError{Field: "range", Reason: "must be one of 1h, 1d, 7d, 30d"}
	}
	if query.AccountID != nil && *query.AccountID <= 0 {
		return DashboardQuery{}, &DashboardInvalidFilterError{Field: "account_id", Reason: "must be positive"}
	}
	if query.Now.IsZero() {
		query.Now = fallbackNow
	}
	if query.Now.IsZero() {
		query.Now = time.Now().UTC()
	}
	query.Now = query.Now.UTC()
	return query, nil
}

func DashboardRangeValid(value DashboardRange) bool {
	switch value {
	case DashboardRange1H, DashboardRange1D, DashboardRange7D, DashboardRange30D:
		return true
	default:
		return false
	}
}

func DashboardWindow(query DashboardQuery) (time.Time, time.Time, time.Duration, error) {
	normalized, err := NormalizeDashboardQuery(query, time.Now().UTC())
	if err != nil {
		return time.Time{}, time.Time{}, 0, err
	}
	switch normalized.Range {
	case DashboardRange1H:
		return normalized.Now.Add(-time.Hour), normalized.Now, time.Minute, nil
	case DashboardRange1D:
		return normalized.Now.Add(-24 * time.Hour), normalized.Now, time.Hour, nil
	case DashboardRange7D:
		return normalized.Now.Add(-7 * 24 * time.Hour), normalized.Now, 6 * time.Hour, nil
	case DashboardRange30D:
		return normalized.Now.Add(-30 * 24 * time.Hour), normalized.Now, 24 * time.Hour, nil
	default:
		return time.Time{}, time.Time{}, 0, &DashboardInvalidFilterError{Field: "range", Reason: "must be one of 1h, 1d, 7d, 30d"}
	}
}

func dashboardAccountOption(account domain.UpstreamAccount) DashboardAccountOption {
	label := account.Name
	if label == "" {
		label = fmt.Sprintf("#%d", account.ID)
	}
	option := DashboardAccountOption{
		ID:         account.ID,
		Label:      label,
		Name:       account.Name,
		Provider:   account.Provider,
		BaseURL:    account.BaseURL,
		AuthMethod: account.AuthMethod,
		Status:     account.Status,
		CreatedAt:  account.CreatedAt,
		UpdatedAt:  account.UpdatedAt,
	}
	if account.AuthMethod != domain.AuthMethodAPIKey {
		option.Email = account.Email
		option.PlanType = account.PlanType
		option.ChatGPTAccountID = account.ChatGPTAccountID
		option.LastRefresh = account.LastRefresh
		option.AccessExpiresAt = account.AccessExpiresAt
	}
	return option
}

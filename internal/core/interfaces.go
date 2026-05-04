package core

import (
	"context"
	"time"

	"github.com/user/one-llm-router/internal/domain"
)

type AccountRepository interface {
	Create(ctx context.Context, account *domain.UpstreamAccount) error
	GetByID(ctx context.Context, id int64) (*domain.UpstreamAccount, error)
	List(ctx context.Context, statusFilter []string) ([]domain.UpstreamAccount, error)
	ListActive(ctx context.Context) ([]domain.UpstreamAccount, error)
	UpdateStatus(ctx context.Context, id int64, status string) error
}

type RequestRecordRepository interface {
	Insert(ctx context.Context, record *domain.RequestRecord) error
	GetByID(ctx context.Context, id int64) (*domain.RequestRecord, error)
	Query(ctx context.Context, params QueryParams) ([]domain.RequestRecord, error)
	UsageSummary(ctx context.Context) (RequestUsageSummary, error)
	FilterOptions(ctx context.Context, params QueryParams) (RequestFilterOptions, error)
	DeleteBefore(ctx context.Context, before time.Time) (int64, error)
}

type SessionRouter interface {
	Route(sessionKey string, activeAccounts []domain.UpstreamAccount) (domain.UpstreamAccount, error)
}

type QueryParams struct {
	Start         *time.Time
	End           *time.Time
	AccountID     *int64
	AccountIDs    []int64
	Outcome       *string
	Outcomes      []string
	Models        []string
	ResponseModes []string
	Search        *string
	Limit         int
	BeforeID      *int64
}

type RequestFilterOptions struct {
	AccountIDs    []int64
	Outcomes      []string
	Models        []string
	ResponseModes []string
}

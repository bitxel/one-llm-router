package core

import (
	"context"
	"strings"

	"github.com/user/one-llm-router/internal/domain"
)

type RequestService struct {
	repo RequestRecordRepository
}

type RequestRecordPage struct {
	Records      []domain.RequestRecord
	HasMore      bool
	NextBeforeID *int64
}

func NewRequestService(repo RequestRecordRepository) *RequestService {
	return &RequestService{repo: repo}
}

func (s *RequestService) Query(ctx context.Context, params QueryParams) ([]domain.RequestRecord, error) {
	params = normalizeRequestQueryParams(params)
	return s.repo.Query(ctx, params)
}

func (s *RequestService) QueryPage(ctx context.Context, params QueryParams) (RequestRecordPage, error) {
	params = normalizeRequestQueryParams(params)
	limit := params.Limit
	params.Limit = limit + 1

	records, err := s.repo.Query(ctx, params)
	if err != nil {
		return RequestRecordPage{}, err
	}
	if len(records) <= limit {
		return RequestRecordPage{Records: records}, nil
	}

	records = records[:limit]
	next := records[len(records)-1].ID
	return RequestRecordPage{
		Records:      records,
		HasMore:      true,
		NextBeforeID: &next,
	}, nil
}

func (s *RequestService) GetByID(ctx context.Context, id int64) (*domain.RequestRecord, error) {
	if id <= 0 {
		return nil, domain.ErrRequestRecordNotFound
	}
	return s.repo.GetByID(ctx, id)
}

func (s *RequestService) FilterOptions(ctx context.Context, params QueryParams) (RequestFilterOptions, error) {
	params = normalizeRequestQueryParams(params)
	params.Limit = 0
	return s.repo.FilterOptions(ctx, params)
}

func normalizeRequestQueryParams(params QueryParams) QueryParams {
	if params.Limit <= 0 || params.Limit > 200 {
		params.Limit = 50
	}
	if params.Search != nil {
		search := strings.TrimSpace(*params.Search)
		if search == "" {
			params.Search = nil
		} else {
			params.Search = &search
		}
	}
	return params
}

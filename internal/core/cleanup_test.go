package core

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/user/one-llm-router/internal/domain"
)

type deleteBeforeErrRepo struct {
	fakeRecordRepo
}

func (r *deleteBeforeErrRepo) DeleteBefore(_ context.Context, _ time.Time) (int64, error) {
	return 0, errors.New("delete failed")
}

type fakeRecordRepo struct {
	records []*domain.RequestRecord
}

func (r *fakeRecordRepo) Insert(_ context.Context, rec *domain.RequestRecord) error {
	rec.ID = int64(len(r.records) + 1)
	clone := *rec
	r.records = append(r.records, &clone)
	return nil
}

func (r *fakeRecordRepo) GetByID(_ context.Context, id int64) (*domain.RequestRecord, error) {
	for _, rec := range r.records {
		if rec.ID == id {
			clone := *rec
			return &clone, nil
		}
	}
	return nil, domain.ErrRequestRecordNotFound
}

func (r *fakeRecordRepo) Query(_ context.Context, params QueryParams) ([]domain.RequestRecord, error) {
	var result []domain.RequestRecord
	for _, rec := range r.records {
		result = append(result, *rec)
	}
	return result, nil
}

func (r *fakeRecordRepo) UsageSummary(context.Context) (RequestUsageSummary, error) {
	return RequestUsageSummary{}, nil
}

func (r *fakeRecordRepo) FilterOptions(context.Context, QueryParams) (RequestFilterOptions, error) {
	return RequestFilterOptions{}, nil
}

func (r *fakeRecordRepo) DeleteBefore(_ context.Context, before time.Time) (int64, error) {
	var kept []*domain.RequestRecord
	var deleted int64
	for _, rec := range r.records {
		if rec.CreatedAt.Before(before) {
			deleted++
		} else {
			kept = append(kept, rec)
		}
	}
	r.records = kept
	return deleted, nil
}

func TestCleanupWorker_DeletesOldRecords(t *testing.T) {
	repo := &fakeRecordRepo{}
	ctx := context.Background()

	oldTime := time.Now().AddDate(0, 0, -31)
	rec := &domain.RequestRecord{
		RequestID:    domain.NewRequestID(),
		Method:       "POST",
		Path:         "/v1/responses",
		StatusCode:   200,
		LatencyMs:    50,
		Outcome:      domain.OutcomeSuccess,
		ResponseMode: domain.ResponseModeJSON,
		CreatedAt:    oldTime,
	}
	_ = repo.Insert(ctx, rec)
	repo.records[0].CreatedAt = oldTime

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	worker := NewCleanupWorker(repo, 30, logger, 0)

	cancelCtx, cancel := context.WithCancel(ctx)
	worker.Start(cancelCtx)

	time.Sleep(200 * time.Millisecond)
	cancel()

	records, _ := repo.Query(ctx, QueryParams{Limit: 10})
	assert.Len(t, records, 0, "old record should have been cleaned up")
}

func TestCleanupWorker_KeepsRecentRecords(t *testing.T) {
	repo := &fakeRecordRepo{}
	ctx := context.Background()

	rec := &domain.RequestRecord{
		RequestID:    domain.NewRequestID(),
		Method:       "POST",
		Path:         "/v1/responses",
		StatusCode:   200,
		LatencyMs:    50,
		Outcome:      domain.OutcomeSuccess,
		ResponseMode: domain.ResponseModeJSON,
		CreatedAt:    time.Now(),
	}
	require.NoError(t, repo.Insert(ctx, rec))

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	worker := NewCleanupWorker(repo, 30, logger, 0)

	cancelCtx, cancel := context.WithCancel(ctx)
	worker.Start(cancelCtx)

	time.Sleep(200 * time.Millisecond)
	cancel()

	records, _ := repo.Query(ctx, QueryParams{Limit: 10})
	assert.Len(t, records, 1, "recent record should be kept")
}

func TestCleanupWorker_DeleteBeforeError(t *testing.T) {
	repo := &deleteBeforeErrRepo{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	worker := NewCleanupWorker(repo, 30, logger, 0)

	ctx, cancel := context.WithCancel(context.Background())
	worker.Start(ctx)
	time.Sleep(100 * time.Millisecond)
	cancel()
}

type countingDeleteRepo struct {
	fakeRecordRepo
	deleteCalls atomic.Int64
}

func (r *countingDeleteRepo) DeleteBefore(ctx context.Context, before time.Time) (int64, error) {
	r.deleteCalls.Add(1)
	return r.fakeRecordRepo.DeleteBefore(ctx, before)
}

func TestCleanupWorker_PeriodicCleanupRuns(t *testing.T) {
	repo := &countingDeleteRepo{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	worker := NewCleanupWorker(repo, 30, logger, 25*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	worker.Start(ctx)
	time.Sleep(80 * time.Millisecond)
	cancel()

	assert.GreaterOrEqual(t, repo.deleteCalls.Load(), int64(2), "initial cleanup plus at least one tick-triggered cleanup")
}

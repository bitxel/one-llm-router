package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/user/one-llm-router/internal/domain"
)

type insertFailsRecordRepo struct{}

func (insertFailsRecordRepo) Insert(_ context.Context, _ *domain.RequestRecord) error {
	return errors.New("insert failed")
}

func (insertFailsRecordRepo) Query(_ context.Context, _ QueryParams) ([]domain.RequestRecord, error) {
	return nil, nil
}

func (insertFailsRecordRepo) UsageSummary(context.Context) (RequestUsageSummary, error) {
	return RequestUsageSummary{}, nil
}

func (insertFailsRecordRepo) GetByID(_ context.Context, _ int64) (*domain.RequestRecord, error) {
	return nil, domain.ErrRequestRecordNotFound
}

func (insertFailsRecordRepo) FilterOptions(context.Context, QueryParams) (RequestFilterOptions, error) {
	return RequestFilterOptions{}, nil
}

func (insertFailsRecordRepo) DeleteBefore(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}

func TestRequestRecorder_AsyncRecord(t *testing.T) {
	repo := &fakeRecordRepo{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	recorder := NewRequestRecorder(repo, logger)
	recorder.Start()

	rec := domain.RequestRecord{
		RequestID:    domain.NewRequestID(),
		Method:       "POST",
		Path:         "/v1/responses",
		StatusCode:   200,
		LatencyMs:    100,
		Outcome:      domain.OutcomeSuccess,
		ResponseMode: domain.ResponseModeJSON,
	}
	recorder.Record(context.Background(), rec)

	require.NoError(t, recorder.Close(context.Background()))

	require.Len(t, repo.records, 1)
	assert.Equal(t, rec.RequestID, repo.records[0].RequestID)
}

func TestRequestRecorder_MultipleRecords(t *testing.T) {
	repo := &fakeRecordRepo{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	recorder := NewRequestRecorder(repo, logger)
	recorder.Start()

	for range 50 {
		recorder.Record(context.Background(), domain.RequestRecord{
			RequestID:    domain.NewRequestID(),
			Method:       "POST",
			Path:         "/v1/responses",
			StatusCode:   200,
			LatencyMs:    50,
			Outcome:      domain.OutcomeSuccess,
			ResponseMode: domain.ResponseModeJSON,
		})
	}

	require.NoError(t, recorder.Close(context.Background()))
	assert.Len(t, repo.records, 50)
}

func TestRequestRecorder_CloseWaitsForDrain(t *testing.T) {
	repo := &fakeRecordRepo{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	recorder := NewRequestRecorder(repo, logger)
	recorder.Start()

	for range 10 {
		recorder.Record(context.Background(), domain.RequestRecord{
			RequestID:    domain.NewRequestID(),
			Method:       "POST",
			Path:         "/v1/responses",
			StatusCode:   200,
			LatencyMs:    50,
			Outcome:      domain.OutcomeSuccess,
			ResponseMode: domain.ResponseModeJSON,
		})
	}

	done := make(chan struct{})
	go func() {
		_ = recorder.Close(context.Background())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return within timeout")
	}

	assert.Len(t, repo.records, 10, "all records should be flushed before Close returns")
}

func TestRequestRecorder_ChannelFull(t *testing.T) {
	repo := &fakeRecordRepo{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	recorder := NewRequestRecorder(repo, logger)

	base := domain.RequestRecord{
		Method:       "POST",
		Path:         "/v1/responses",
		StatusCode:   200,
		LatencyMs:    1,
		Outcome:      domain.OutcomeSuccess,
		ResponseMode: domain.ResponseModeJSON,
	}
	for i := range recorderBufferSize {
		rec := base
		rec.RequestID = fmt.Sprintf("req_buffered_%d", i)
		recorder.Record(context.Background(), rec)
	}

	syncRec := base
	syncRec.RequestID = "req_sync_fallback"
	recorder.Record(context.Background(), syncRec)

	require.Len(t, repo.records, 1)
	assert.Equal(t, "req_sync_fallback", repo.records[0].RequestID)
}

func TestRequestRecorder_ChannelFull_SyncInsertError(t *testing.T) {
	repo := &insertFailsRecordRepo{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	recorder := NewRequestRecorder(repo, logger)

	base := domain.RequestRecord{
		Method:       "POST",
		Path:         "/v1/responses",
		StatusCode:   200,
		LatencyMs:    1,
		Outcome:      domain.OutcomeSuccess,
		ResponseMode: domain.ResponseModeJSON,
	}
	for i := range recorderBufferSize {
		rec := base
		rec.RequestID = fmt.Sprintf("req_buf_%d", i)
		recorder.Record(context.Background(), rec)
	}
	recorder.Record(context.Background(), domain.RequestRecord{
		RequestID:    "req_sync_insert_err",
		Method:       "POST",
		Path:         "/v1/responses",
		StatusCode:   200,
		LatencyMs:    1,
		Outcome:      domain.OutcomeSuccess,
		ResponseMode: domain.ResponseModeJSON,
	})
}

func TestRequestRecorder_RecordAfterCloseDropsSafely(t *testing.T) {
	repo := &fakeRecordRepo{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	recorder := NewRequestRecorder(repo, logger)
	recorder.Start()

	require.NoError(t, recorder.Close(context.Background()))

	assert.NotPanics(t, func() {
		recorder.Record(context.Background(), domain.RequestRecord{
			RequestID:    "req_after_close",
			Method:       "POST",
			Path:         "/v1/responses",
			StatusCode:   200,
			LatencyMs:    1,
			Outcome:      domain.OutcomeSuccess,
			ResponseMode: domain.ResponseModeJSON,
		})
	})
	assert.Len(t, repo.records, 0, "records after Close must be dropped, not inserted")
}

func TestRequestRecorder_DoubleCloseIdempotent(t *testing.T) {
	repo := &fakeRecordRepo{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	recorder := NewRequestRecorder(repo, logger)
	recorder.Start()

	require.NoError(t, recorder.Close(context.Background()))
	assert.NotPanics(t, func() {
		_ = recorder.Close(context.Background())
	})
}

func TestRequestRecorder_InsertError(t *testing.T) {
	repo := insertFailsRecordRepo{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	recorder := NewRequestRecorder(&repo, logger)
	recorder.Start()

	recorder.Record(context.Background(), domain.RequestRecord{
		RequestID:    "req_insert_err",
		Method:       "POST",
		Path:         "/v1/responses",
		StatusCode:   200,
		LatencyMs:    1,
		Outcome:      domain.OutcomeSuccess,
		ResponseMode: domain.ResponseModeJSON,
	})

	require.NoError(t, recorder.Close(context.Background()))
}

// hangingRepo blocks Insert until release is closed. Used to prove that a
// wedged DB cannot pin Close() indefinitely when the caller passes a ctx
// with deadline.
type hangingRepo struct{ release chan struct{} }

func (h *hangingRepo) Insert(ctx context.Context, _ *domain.RequestRecord) error {
	select {
	case <-h.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *hangingRepo) Query(_ context.Context, _ QueryParams) ([]domain.RequestRecord, error) {
	return nil, nil
}

func (h *hangingRepo) UsageSummary(context.Context) (RequestUsageSummary, error) {
	return RequestUsageSummary{}, nil
}

func (h *hangingRepo) GetByID(_ context.Context, _ int64) (*domain.RequestRecord, error) {
	return nil, domain.ErrRequestRecordNotFound
}

func (h *hangingRepo) FilterOptions(context.Context, QueryParams) (RequestFilterOptions, error) {
	return RequestFilterOptions{}, nil
}

func (h *hangingRepo) DeleteBefore(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}

func TestRequestRecorder_CloseHonorsCtxDeadline(t *testing.T) {
	repo := &hangingRepo{release: make(chan struct{})}
	defer close(repo.release)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	recorder := NewRequestRecorder(repo, logger)
	recorder.Start()

	recorder.Record(context.Background(), domain.RequestRecord{
		RequestID:    "req_stuck",
		Method:       "POST",
		Path:         "/v1/responses",
		StatusCode:   200,
		Outcome:      domain.OutcomeSuccess,
		ResponseMode: domain.ResponseModeJSON,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := recorder.Close(ctx)
	elapsed := time.Since(start)

	require.ErrorIs(t, err, context.DeadlineExceeded, "Close should surface ctx deadline")
	assert.Less(t, elapsed, 1*time.Second, "Close must not block past ctx deadline (got %v)", elapsed)
}

// slowRepo simulates a repo where every insert exceeds the recorder insert
// timeout. Verifies each insert is bounded independently so one slow row does
// not cascade into an unbounded Close.
func TestRequestRecorder_SyncFallbackUsesDetachedCtx(t *testing.T) {
	repo := &fakeRecordRepo{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	recorder := NewRequestRecorder(repo, logger)

	// Fill channel without starting worker so sync-fallback path is forced.
	base := domain.RequestRecord{
		Method:       "POST",
		Path:         "/v1/responses",
		StatusCode:   200,
		Outcome:      domain.OutcomeSuccess,
		ResponseMode: domain.ResponseModeJSON,
	}
	for i := range recorderBufferSize {
		rec := base
		rec.RequestID = fmt.Sprintf("buf_%d", i)
		recorder.Record(context.Background(), rec)
	}

	// Caller's ctx is already cancelled (simulates client disconnect). The
	// sync fallback MUST still persist the row because the recorder uses a
	// detached context internally.
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	sync := base
	sync.RequestID = "client_disconnected"
	recorder.Record(cancelled, sync)

	require.Len(t, repo.records, 1)
	assert.Equal(t, "client_disconnected", repo.records[0].RequestID)
}

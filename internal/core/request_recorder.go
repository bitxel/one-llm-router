package core

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/user/one-llm-router/internal/domain"
)

const (
	recorderBufferSize = 1024
	// recorderInsertTimeout caps a single DB insert attempt so that a hung
	// database cannot block the worker (and therefore Close) indefinitely.
	recorderInsertTimeout = 30 * time.Second
)

type RequestRecorder struct {
	repo      RequestRecordRepository
	ch        chan domain.RequestRecord
	wg        sync.WaitGroup
	logger    *slog.Logger
	mu        sync.RWMutex
	closed    bool
	closeOnce sync.Once
}

func NewRequestRecorder(repo RequestRecordRepository, logger *slog.Logger) *RequestRecorder {
	return &RequestRecorder{
		repo:   repo,
		ch:     make(chan domain.RequestRecord, recorderBufferSize),
		logger: logger,
	}
}

func (r *RequestRecorder) Start() {
	r.wg.Add(1)
	go r.worker()
}

func (r *RequestRecorder) worker() {
	defer r.wg.Done()
	for rec := range r.ch {
		r.insertBounded(rec)
	}
}

// insertBounded wraps a single insert with a hard timeout so a stalled DB
// cannot deadlock Close(). The context is intentionally detached from the
// original request so audit rows are not lost when the client disconnects.
func (r *RequestRecorder) insertBounded(rec domain.RequestRecord) {
	ctx, cancel := context.WithTimeout(context.Background(), recorderInsertTimeout)
	defer cancel()
	if err := r.repo.Insert(ctx, &rec); err != nil {
		r.logger.Error("failed to insert request record",
			"request_id", rec.RequestID,
			"error", err,
		)
	}
}

// Record enqueues rec onto the async channel. If the channel is full we fall
// back to a synchronous insert on a DETACHED context (so client disconnect
// does not silently drop audit rows). The RLock is released before the slow
// DB call so a hung insert cannot block Close().
func (r *RequestRecorder) Record(_ context.Context, rec domain.RequestRecord) {
	r.mu.RLock()
	if r.closed {
		r.mu.RUnlock()
		r.logger.Warn("record dropped: recorder is closed",
			"request_id", rec.RequestID,
		)
		return
	}

	select {
	case r.ch <- rec:
		r.mu.RUnlock()
		return
	default:
	}
	r.mu.RUnlock()

	r.logger.Warn("recorder channel full, inserting synchronously",
		"request_id", rec.RequestID,
	)
	r.insertBounded(rec)
}

// Close signals the worker to drain and exit. It respects the caller's ctx
// so a wedged DB does not block the whole shutdown sequence.
func (r *RequestRecorder) Close(ctx context.Context) error {
	r.closeOnce.Do(func() {
		r.mu.Lock()
		r.closed = true
		close(r.ch)
		r.mu.Unlock()
	})

	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		r.logger.Error("recorder close timed out, records may be dropped",
			"error", ctx.Err(),
		)
		return ctx.Err()
	}
}

package core

import (
	"context"
	"log/slog"
	"time"
)

const cleanupBatchSize = 1000

type CleanupWorker struct {
	repo          RequestRecordRepository
	retentionDays int
	logger        *slog.Logger
	tickInterval  time.Duration
}

func NewCleanupWorker(repo RequestRecordRepository, retentionDays int, logger *slog.Logger, tickInterval time.Duration) *CleanupWorker {
	if tickInterval <= 0 {
		tickInterval = time.Hour
	}
	return &CleanupWorker{
		repo:          repo,
		retentionDays: retentionDays,
		logger:        logger,
		tickInterval:  tickInterval,
	}
}

func (w *CleanupWorker) Start(ctx context.Context) {
	go w.run(ctx)
}

func (w *CleanupWorker) run(ctx context.Context) {
	ticker := time.NewTicker(w.tickInterval)
	defer ticker.Stop()

	w.cleanup(ctx)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("cleanup worker stopped")
			return
		case <-ticker.C:
			w.cleanup(ctx)
		}
	}
}

func (w *CleanupWorker) cleanup(ctx context.Context) {
	cutoff := time.Now().AddDate(0, 0, -w.retentionDays)
	var totalDeleted int64

	for {
		deleted, err := w.repo.DeleteBefore(ctx, cutoff)
		if err != nil {
			w.logger.Error("cleanup batch failed", "error", err)
			break
		}
		totalDeleted += deleted
		if deleted < cleanupBatchSize {
			break
		}
	}

	if totalDeleted > 0 {
		w.logger.Info("cleanup completed",
			"deleted", totalDeleted,
			"retention_days", w.retentionDays,
		)
	}
}

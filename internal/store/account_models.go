package store

import (
	"context"
	"fmt"
	"strings"

	"xorm.io/xorm"

	"github.com/user/one-llm-router/internal/domain"
)

var ErrAccountModelDuplicate = domain.ErrAccountModelDuplicate

type AccountModelRepo struct {
	engine *xorm.Engine
}

func NewAccountModelRepo(engine *xorm.Engine) *AccountModelRepo {
	return &AccountModelRepo{engine: engine}
}

// HasModel checks whether a specific account supports a specific model.
func (r *AccountModelRepo) HasModel(_ context.Context, accountID int64, modelID string) (bool, error) {
	if modelID == "" {
		return false, nil
	}
	exists, err := r.engine.Table("account_models").
		Where("account_id = ? AND model_id = ?", accountID, modelID).
		Exist()
	if err != nil {
		return false, fmt.Errorf("check account model: %w", err)
	}
	return exists, nil
}

// ListByAccount returns all models for a given account, ordered by model_id.
func (r *AccountModelRepo) ListByAccount(_ context.Context, accountID int64) ([]domain.AccountModel, error) {
	var models []domain.AccountModel
	err := r.engine.
		Where("account_id = ?", accountID).
		OrderBy("model_id ASC").
		Find(&models)
	if err != nil {
		return nil, fmt.Errorf("list account models: %w", err)
	}
	return models, nil
}

// Insert adds a model to an account with the given source.
// Returns ErrAccountModelDuplicate when the model already exists on the account.
func (r *AccountModelRepo) Insert(_ context.Context, accountID int64, modelID string, source string) error {
	if modelID == "" {
		return fmt.Errorf("insert account model: empty model_id")
	}
	am := domain.AccountModel{
		AccountID: accountID,
		ModelID:   modelID,
		Source:    source,
	}
	_, err := r.engine.Insert(&am)
	if err != nil {
		if isDuplicateKeyError(err) {
			return fmt.Errorf("insert account model: %w", ErrAccountModelDuplicate)
		}
		return fmt.Errorf("insert account model: %w", err)
	}
	return nil
}

// isDuplicateKeyError detects unique constraint violation errors across
// the supported database dialects (SQLite, PostgreSQL, MySQL).
func isDuplicateKeyError(err error) bool {
	errStr := err.Error()
	return strings.Contains(errStr, "UNIQUE constraint") ||
		strings.Contains(errStr, "duplicate key") ||
		strings.Contains(errStr, "Duplicate entry")
}

// Delete removes a model from an account regardless of source.
func (r *AccountModelRepo) Delete(_ context.Context, accountID int64, modelID string) error {
	_, err := r.engine.
		Where("account_id = ? AND model_id = ?", accountID, modelID).
		Delete(&domain.AccountModel{})
	if err != nil {
		return fmt.Errorf("delete account model: %w", err)
	}
	return nil
}

// ReplaceUpstreamModels atomically replaces all upstream-sourced models for an account.
// It preserves manual rows and returns the counts of added upstream models and kept manual models.
func (r *AccountModelRepo) ReplaceUpstreamModels(ctx context.Context, accountID int64, modelIDs []string) (added int, keptManual int, err error) {
	sess := r.engine.NewSession()
	defer func() { _ = sess.Close() }()

	if err := sess.Begin(); err != nil {
		return 0, 0, fmt.Errorf("replace upstream models tx begin: %w", err)
	}

	// Count manual rows that will be kept.
	manualCount, err := sess.Table("account_models").
		Where("account_id = ? AND source = 'manual'", accountID).
		Count()
	if err != nil {
		_ = sess.Rollback()
		return 0, 0, fmt.Errorf("count manual models: %w", err)
	}

	// Delete all upstream rows.
	_, err = sess.
		Where("account_id = ? AND source = 'upstream'", accountID).
		Delete(&domain.AccountModel{})
	if err != nil {
		_ = sess.Rollback()
		return 0, 0, fmt.Errorf("delete upstream models: %w", err)
	}

	// Deduplicate input to avoid UNIQUE constraint violations.
	seen := make(map[string]struct{}, len(modelIDs))
	deduped := make([]domain.AccountModel, 0, len(modelIDs))
	for _, modelID := range modelIDs {
		if _, ok := seen[modelID]; ok {
			continue
		}
		seen[modelID] = struct{}{}
		deduped = append(deduped, domain.AccountModel{
			AccountID: accountID,
			ModelID:   modelID,
			Source:    domain.AccountModelSourceUpstream,
		})
	}

	// Batch insert deduplicated upstream rows.
	if len(deduped) > 0 {
		if _, err := sess.Insert(&deduped); err != nil {
			_ = sess.Rollback()
			return 0, 0, fmt.Errorf("batch insert upstream models: %w", err)
		}
	}

	if err := sess.Commit(); err != nil {
		return 0, 0, fmt.Errorf("replace upstream models tx commit: %w", err)
	}

	return len(deduped), int(manualCount), nil
}

// AccountsWithModel returns the account IDs that have a specific model.
func (r *AccountModelRepo) AccountsWithModel(_ context.Context, modelID string) ([]int64, error) {
	var accountIDs []int64
	err := r.engine.Table("account_models").
		Where("model_id = ?", modelID).
		Cols("account_id").
		Distinct("account_id").
		Find(&accountIDs)
	if err != nil {
		return nil, fmt.Errorf("find accounts with model: %w", err)
	}
	return accountIDs, nil
}

// DistinctModelsForAccounts returns the distinct model IDs declared by the given accounts.
func (r *AccountModelRepo) DistinctModelsForAccounts(_ context.Context, accountIDs []int64) ([]string, error) {
	if len(accountIDs) == 0 {
		return nil, nil
	}
	var modelIDs []string
	ids := make([]interface{}, len(accountIDs))
	for i, id := range accountIDs {
		ids[i] = id
	}
	err := r.engine.Table("account_models").
		In("account_id", ids...).
		Distinct("model_id").
		Cols("model_id").
		Find(&modelIDs)
	if err != nil {
		return nil, fmt.Errorf("distinct models for accounts: %w", err)
	}
	return modelIDs, nil
}

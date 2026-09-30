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

// ReplaceUpstreamModels atomically replaces all upstream-sourced models for an
// account with the given drafts (model ID + optional metadata JSON).
// Manual rows are preserved (source stays 'manual'); their metadata is
// overwritten from the upstream draft on every refresh. Deduplication keeps
// the first occurrence. Returns the number of newly inserted upstream rows
// and the number of preserved manual rows (including manual rows hit by
// upstream drafts).
func (r *AccountModelRepo) ReplaceUpstreamModels(ctx context.Context, accountID int64, models []domain.AccountModelDraft) (added int, keptManual int, err error) {
	sess := r.engine.NewSession()
	defer func() { _ = sess.Close() }()

	if err := sess.Begin(); err != nil {
		return 0, 0, fmt.Errorf("replace upstream models tx begin: %w", err)
	}

	// Load existing manual model IDs for this account.
	var manualRows []domain.AccountModel
	if err := sess.
		Where("account_id = ? AND source = 'manual'", accountID).
		Cols("model_id").
		Find(&manualRows); err != nil {
		_ = sess.Rollback()
		return 0, 0, fmt.Errorf("load manual models: %w", err)
	}
	manualIDs := make(map[string]struct{}, len(manualRows))
	for _, row := range manualRows {
		manualIDs[row.ModelID] = struct{}{}
	}
	keptManual = len(manualRows)

	// Delete all upstream rows (manual rows survive).
	_, err = sess.
		Where("account_id = ? AND source = 'upstream'", accountID).
		Delete(&domain.AccountModel{})
	if err != nil {
		_ = sess.Rollback()
		return 0, 0, fmt.Errorf("delete upstream models: %w", err)
	}

	// Deduplicate drafts, preserving first-occurrence order.
	seen := make(map[string]struct{}, len(models))
	deduped := make([]domain.AccountModelDraft, 0, len(models))
	for _, m := range models {
		if m.ID == "" {
			continue
		}
		if _, ok := seen[m.ID]; ok {
			continue
		}
		seen[m.ID] = struct{}{}
		deduped = append(deduped, m)
	}

	// Split into insert (new upstream rows) vs update (manual rows that
	// already exist — portable across dialects, no ON CONFLICT).
	toInsert := make([]domain.AccountModel, 0, len(deduped))
	for _, draft := range deduped {
		meta := draftMetadataString(draft.Metadata)
		if _, isManual := manualIDs[draft.ID]; isManual {
			// Choice A: keep source='manual', overwrite metadata every refresh.
			if meta == nil {
				// No metadata from upstream — leave existing NULL as-is.
				continue
			}
			if _, err := sess.
				Where("account_id = ? AND model_id = ? AND source = 'manual'", accountID, draft.ID).
				Cols("metadata").
				Update(&domain.AccountModel{Metadata: meta}); err != nil {
				_ = sess.Rollback()
				return 0, 0, fmt.Errorf("update manual model metadata: %w", err)
			}
			continue
		}
		toInsert = append(toInsert, domain.AccountModel{
			AccountID: accountID,
			ModelID:   draft.ID,
			Source:    domain.AccountModelSourceUpstream,
			Metadata:  meta,
		})
	}

	if len(toInsert) > 0 {
		if _, err := sess.Insert(&toInsert); err != nil {
			_ = sess.Rollback()
			return 0, 0, fmt.Errorf("batch insert upstream models: %w", err)
		}
	}

	if err := sess.Commit(); err != nil {
		return 0, 0, fmt.Errorf("replace upstream models tx commit: %w", err)
	}

	return len(toInsert), keptManual, nil
}

func draftMetadataString(raw []byte) *string {
	if len(raw) == 0 {
		return nil
	}
	s := string(raw)
	return &s
}

// ListByAccounts returns all model rows for the given account IDs,
// ordered by model_id then account_id for stable metadata merge.
func (r *AccountModelRepo) ListByAccounts(_ context.Context, accountIDs []int64) ([]domain.AccountModel, error) {
	if len(accountIDs) == 0 {
		return nil, nil
	}
	ids := make([]interface{}, len(accountIDs))
	for i, id := range accountIDs {
		ids[i] = id
	}
	var models []domain.AccountModel
	err := r.engine.
		In("account_id", ids...).
		OrderBy("model_id ASC, account_id ASC").
		Find(&models)
	if err != nil {
		return nil, fmt.Errorf("list models for accounts: %w", err)
	}
	return models, nil
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

// AccountsWithoutModels returns the account IDs that have NO rows in
// account_models at all. Such accounts carry no explicit model
// restriction, so the router treats them as model-agnostic — eligible
// for any model request until an upstream refresh (or manual add)
// populates a list that constrains them.
func (r *AccountModelRepo) AccountsWithoutModels(ctx context.Context) ([]int64, error) {
	var withModels []int64
	if err := r.engine.Table("account_models").
		Distinct("account_id").
		Find(&withModels); err != nil {
		return nil, fmt.Errorf("find accounts with models: %w", err)
	}
	var accounts []domain.UpstreamAccount
	if err := r.engine.
		Where("status != ?", domain.AccountStatusDeleted).
		Cols("id").
		Find(&accounts); err != nil {
		return nil, fmt.Errorf("list accounts for model-agnostic set: %w", err)
	}
	withSet := make(map[int64]struct{}, len(withModels))
	for _, id := range withModels {
		withSet[id] = struct{}{}
	}
	out := make([]int64, 0, len(accounts))
	for _, a := range accounts {
		if _, ok := withSet[a.ID]; !ok {
			out = append(out, a.ID)
		}
	}
	return out, nil
}

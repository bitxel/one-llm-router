package adminapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/user/one-llm-router/internal/api"
	"github.com/user/one-llm-router/internal/api/errcode"
	"github.com/user/one-llm-router/internal/api/httpio"
	"github.com/user/one-llm-router/internal/core"
	"github.com/user/one-llm-router/internal/domain"
)

type AccountModelHandler struct {
	accountRepo    AccountReader
	modelRepo      ModelRepo
	modelRefresher *core.ModelRefresher
	logger         *slog.Logger
}

type AccountReader interface {
	GetByID(ctx context.Context, id int64) (*domain.UpstreamAccount, error)
}

type ModelRepo interface {
	ListByAccount(ctx context.Context, accountID int64) ([]domain.AccountModel, error)
	Insert(ctx context.Context, accountID int64, modelID string, source string) error
	Delete(ctx context.Context, accountID int64, modelID string) error
	HasModel(ctx context.Context, accountID int64, modelID string) (bool, error)
	ReplaceUpstreamModels(ctx context.Context, accountID int64, modelIDs []string) (added int, keptManual int, err error)
}

func NewAccountModelHandler(accountRepo AccountReader, modelRepo ModelRepo, modelRefresher *core.ModelRefresher, logger *slog.Logger) *AccountModelHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &AccountModelHandler{
		accountRepo:    accountRepo,
		modelRepo:      modelRepo,
		modelRefresher: modelRefresher,
		logger:         logger,
	}
}

func (h *AccountModelHandler) ListModels(w http.ResponseWriter, r *http.Request) {
	reqID := api.RequestIDFromContext(r.Context())
	accountID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		api.WriteBizErr(w, reqID, errcode.AccountNotFound,
			errcode.Symbol(errcode.AccountNotFound), map[string]any{})
		return
	}
	acct, err := h.accountRepo.GetByID(r.Context(), accountID)
	if err != nil || acct == nil {
		api.WriteBizErr(w, reqID, errcode.AccountNotFound,
			errcode.Symbol(errcode.AccountNotFound), map[string]any{})
		return
	}
	models, err := h.modelRepo.ListByAccount(r.Context(), accountID)
	if err != nil {
		h.logger.Warn("list account models failed", "account_id", accountID, "error", err)
		api.WriteSysErr(w, reqID, errcode.AccountModelInternalError,
			errcode.Symbol(errcode.AccountModelInternalError))
		return
	}
	if models == nil {
		models = []domain.AccountModel{}
	}
	api.WriteOK(w, reqID, map[string]any{
		"account_id": accountID,
		"models":     models,
	})
}

type addModelRequest struct {
	ModelID string `json:"model_id"`
}

func (h *AccountModelHandler) AddModel(w http.ResponseWriter, r *http.Request) {
	reqID := api.RequestIDFromContext(r.Context())
	accountID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		api.WriteBizErr(w, reqID, errcode.AccountNotFound,
			errcode.Symbol(errcode.AccountNotFound), map[string]any{})
		return
	}
	acct, err := h.accountRepo.GetByID(r.Context(), accountID)
	if err != nil || acct == nil {
		api.WriteBizErr(w, reqID, errcode.AccountNotFound,
			errcode.Symbol(errcode.AccountNotFound), map[string]any{})
		return
	}
	body, ok := httpio.DecodeJSON[addModelRequest](w, r, reqID, httpio.DefaultAdminBodyCap)
	if !ok {
		return
	}
	if body.ModelID == "" {
		api.WriteBizErr(w, reqID, errcode.MalformedBody,
			errcode.Symbol(errcode.MalformedBody),
			map[string]any{"detail": "model_id must be non-empty"})
		return
	}
	err = h.modelRepo.Insert(r.Context(), accountID, body.ModelID, domain.AccountModelSourceManual)
	if err != nil {
		if isDuplicate(err) {
			api.WriteBizErr(w, reqID, errcode.AccountModelDuplicate,
				errcode.Symbol(errcode.AccountModelDuplicate), map[string]any{})
			return
		}
		h.logger.Warn("add account model failed", "account_id", accountID, "model_id", body.ModelID, "error", err)
		api.WriteSysErr(w, reqID, errcode.AccountModelInternalError,
			errcode.Symbol(errcode.AccountModelInternalError))
		return
	}
	h.logger.Warn("account_model_added",
		"request_id", reqID,
		"account_id", accountID,
		"model_id", body.ModelID,
	)
	api.WriteOK(w, reqID, map[string]any{
		"account_id": accountID,
		"model_id":   body.ModelID,
	})
}

type removeModelRequest struct {
	ModelID string `json:"model_id"`
}

func (h *AccountModelHandler) RemoveModel(w http.ResponseWriter, r *http.Request) {
	reqID := api.RequestIDFromContext(r.Context())
	accountID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		api.WriteBizErr(w, reqID, errcode.AccountNotFound,
			errcode.Symbol(errcode.AccountNotFound), map[string]any{})
		return
	}
	acct, err := h.accountRepo.GetByID(r.Context(), accountID)
	if err != nil || acct == nil {
		api.WriteBizErr(w, reqID, errcode.AccountNotFound,
			errcode.Symbol(errcode.AccountNotFound), map[string]any{})
		return
	}
	body, ok := httpio.DecodeJSON[removeModelRequest](w, r, reqID, httpio.DefaultAdminBodyCap)
	if !ok {
		return
	}
	if body.ModelID == "" {
		api.WriteBizErr(w, reqID, errcode.MalformedBody,
			errcode.Symbol(errcode.MalformedBody),
			map[string]any{"detail": "model_id must be non-empty"})
		return
	}
	has, err := h.modelRepo.HasModel(r.Context(), accountID, body.ModelID)
	if err != nil {
		h.logger.Warn("check model existence failed", "account_id", accountID, "model_id", body.ModelID, "error", err)
		api.WriteSysErr(w, reqID, errcode.AccountModelInternalError,
			errcode.Symbol(errcode.AccountModelInternalError))
		return
	}
	if !has {
		api.WriteBizErr(w, reqID, errcode.AccountNotFound,
			errcode.Symbol(errcode.AccountNotFound),
			map[string]any{"detail": fmt.Sprintf("model %q not found on account %d", body.ModelID, accountID)})
		return
	}
	err = h.modelRepo.Delete(r.Context(), accountID, body.ModelID)
	if err != nil {
		h.logger.Warn("remove account model failed", "account_id", accountID, "model_id", body.ModelID, "error", err)
		api.WriteSysErr(w, reqID, errcode.AccountModelInternalError,
			errcode.Symbol(errcode.AccountModelInternalError))
		return
	}
	h.logger.Warn("account_model_removed",
		"request_id", reqID,
		"account_id", accountID,
		"model_id", body.ModelID,
	)
	api.WriteOK(w, reqID, map[string]any{
		"account_id": accountID,
		"model_id":   body.ModelID,
	})
}

func (h *AccountModelHandler) RefreshModels(w http.ResponseWriter, r *http.Request) {
	reqID := api.RequestIDFromContext(r.Context())
	accountID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		api.WriteBizErr(w, reqID, errcode.AccountNotFound,
			errcode.Symbol(errcode.AccountNotFound), map[string]any{})
		return
	}
	acct, err := h.accountRepo.GetByID(r.Context(), accountID)
	if err != nil || acct == nil {
		api.WriteBizErr(w, reqID, errcode.AccountNotFound,
			errcode.Symbol(errcode.AccountNotFound), map[string]any{})
		return
	}
	added, keptManual, totalFetched, err := h.modelRefresher.Refresh(r.Context(), acct, h.modelRepo)
	if err != nil {
		h.logger.Warn("account_models_refresh_failed",
			"request_id", reqID,
			"account_id", accountID,
			"error", err,
		)
		if isStoreError(err) {
			api.WriteSysErr(w, reqID, errcode.AccountModelInternalError,
				errcode.Symbol(errcode.AccountModelInternalError))
			return
		}
		api.WriteBizErr(w, reqID, errcode.AccountModelRefreshFailed,
			errcode.Symbol(errcode.AccountModelRefreshFailed),
			map[string]any{"detail": err.Error()})
		return
	}
	h.logger.Warn("account_models_refreshed",
		"request_id", reqID,
		"account_id", accountID,
		"added", added,
		"kept_manual", keptManual,
	)
	api.WriteOK(w, reqID, map[string]any{
		"account_id":   accountID,
		"added":        added,
		"kept_manual":  keptManual,
		"total_fetched": totalFetched,
	})
}

func isStoreError(err error) bool {
	return errors.Is(err, core.ErrModelRefreshStoreFailed)
}

func isDuplicate(err error) bool {
	return errors.Is(err, domain.ErrAccountModelDuplicate)
}

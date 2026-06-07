package adminapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/user/one-llm-router/internal/api"
	"github.com/user/one-llm-router/internal/api/errcode"
	"github.com/user/one-llm-router/internal/api/httpio"
	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/provider/openai"
)

type AccountModelHandler struct {
	accountRepo  AccountReader
	modelRepo    ModelRepo
	client       *http.Client
	codexBackend string
	logger       *slog.Logger
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

func NewAccountModelHandler(accountRepo AccountReader, modelRepo ModelRepo, client *http.Client, codexBackend string, logger *slog.Logger) *AccountModelHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &AccountModelHandler{
		accountRepo:  accountRepo,
		modelRepo:    modelRepo,
		client:       client,
		codexBackend: codexBackend,
		logger:       logger,
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
	upstreamModels, err := h.fetchUpstreamModels(r.Context(), acct)
	if err != nil {
		h.logger.Warn("account_models_refresh_failed",
			"request_id", reqID,
			"account_id", accountID,
			"error", err,
		)
		api.WriteBizErr(w, reqID, errcode.AccountModelRefreshFailed,
			errcode.Symbol(errcode.AccountModelRefreshFailed),
			map[string]any{"detail": err.Error()})
		return
	}
	added, keptManual, err := h.modelRepo.ReplaceUpstreamModels(r.Context(), accountID, upstreamModels)
	if err != nil {
		h.logger.Warn("replace upstream models failed",
			"request_id", reqID,
			"account_id", accountID,
			"error", err,
		)
		api.WriteSysErr(w, reqID, errcode.AccountModelInternalError,
			errcode.Symbol(errcode.AccountModelInternalError))
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
		"total_fetched": len(upstreamModels),
	})
}

func (h *AccountModelHandler) fetchUpstreamModels(ctx context.Context, acct *domain.UpstreamAccount) ([]string, error) {
	var endpoint string
	if acct.IsOAuth() {
		codexBackend := h.codexBackend
		if codexBackend == "" {
			codexBackend = openai.ChatGPTBackendBaseURL
		}
		endpoint = strings.TrimSuffix(codexBackend, "/") + "/codex/models?client_version=0.120.0"
	} else {
		baseURL := strings.TrimSuffix(acct.EffectiveBaseURL(), "/v1")
		endpoint = baseURL + "/v1/models"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	var token string
	if acct.IsOAuth() {
		token = string(acct.AccessToken)
	} else {
		token = acct.APIKey
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	h.logger.Warn("fetch_upstream_models",
		"account_id", acct.ID,
		"auth_method", acct.AuthMethod,
		"endpoint", endpoint,
		"has_credential", token != "",
	)

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upstream request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read upstream response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream returned status %d: %s", resp.StatusCode, truncateBody(body, 256))
	}

	modelIDs, err := parseModelIDs(body)
	if err != nil {
		return nil, fmt.Errorf("parse models: %w", err)
	}
	seen := make(map[string]struct{}, len(modelIDs))
	deduped := make([]string, 0, len(modelIDs))
	for _, id := range modelIDs {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		deduped = append(deduped, id)
	}
	return deduped, nil
}

func parseModelIDs(body []byte) ([]string, error) {
	var probe struct {
		Models []json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(body, &probe); err == nil && probe.Models != nil {
		var codexPayload struct {
			Models []struct {
				Slug string `json:"slug"`
			} `json:"models"`
		}
		if err := json.Unmarshal(body, &codexPayload); err != nil {
			return nil, fmt.Errorf("decode codex models: %w", err)
		}
		ids := make([]string, 0, len(codexPayload.Models))
		for _, m := range codexPayload.Models {
			if m.Slug != "" {
				ids = append(ids, m.Slug)
			}
		}
		return ids, nil
	}
	var openAIPayload struct {
		Object string `json:"object"`
		Data   []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &openAIPayload); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if openAIPayload.Object != "" && openAIPayload.Object != "list" {
		return nil, fmt.Errorf("unexpected object type: %s", openAIPayload.Object)
	}
	ids := make([]string, 0, len(openAIPayload.Data))
	for _, m := range openAIPayload.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	return ids, nil
}

func isDuplicate(err error) bool {
	return errors.Is(err, domain.ErrAccountModelDuplicate)
}

func truncateBody(body []byte, max int) string {
	s := string(body)
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}



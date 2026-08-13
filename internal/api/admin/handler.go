package admin

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/user/one-llm-router/internal/core"
	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/presentation"
	"github.com/user/one-llm-router/internal/store"
)

// Handler bundles the 001-era admin CRUD handlers (accounts, requests,
// health, probe). It is the long-lived receiver registered by
// RegisterRoutes under /admin/* and is unaware of the 002 envelope —
// those responses remain the 001 native JSON shapes documented in the
// 001 tech-design. The 002 envelope-backed admin APIs will land under
// /api/admin/* in a separate package (T-301+).
//
// Handler is safe for concurrent use as long as every embedded service
// pointer is. All services are injected (no globals) so tests can
// substitute fakes.
type Handler struct {
	accounts            *core.AccountService
	accountListProvider accountListProvider
	requests            *core.RequestService
	health              *core.HealthService
	selector            *core.AccountSelector
	logger              *slog.Logger
}

type accountListProvider interface {
	ListForAdminAPI(ctx context.Context) ([]store.AccountListItem, error)
}

// NewHandler wires the admin CRUD surface with its backing services.
// All arguments are required; passing nil produces a receiver that
// panics on first use rather than silently 500-ing. logger must be
// non-nil — admin writes audit lines per request.
func NewHandler(
	accounts *core.AccountService,
	requests *core.RequestService,
	health *core.HealthService,
	selector *core.AccountSelector,
	logger *slog.Logger,
) *Handler {
	return &Handler{
		accounts: accounts,
		requests: requests,
		health:   health,
		selector: selector,
		logger:   logger,
	}
}

// SetAccountListProvider installs the token-free admin projection reader used
// by /api/admin/accounts. Leaving it nil preserves the legacy 001 code path for
// tests that only inject the minimal core.AccountRepository fake.
func (h *Handler) SetAccountListProvider(provider accountListProvider) {
	h.accountListProvider = provider
}

type createAccountRequest struct {
	Name         string   `json:"name"`
	Provider     string   `json:"provider,omitempty"`
	APIKey       string   `json:"api_key"`
	BaseURL      *string  `json:"base_url,omitempty"`
	AuthMode     string   `json:"auth_method,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

func (h *Handler) CreateAccount(w http.ResponseWriter, r *http.Request) {
	var req createAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	method := normaliseRequestedAuthMethod(req.AuthMode)
	switch method {
	case domain.AuthMethodAPIKey:
		// Continue below.
	case domain.AuthMethodOAuthBrowser, domain.AuthMethodOAuthDevice, domain.AuthMethodOAuthImport:
		writeJSON(w, http.StatusBadRequest, createAccountErrorResponse{
			Error:       "oauth_mode_requires_flow_endpoint",
			AllowedHere: []string{string(domain.AuthMethodAPIKey)},
			Got:         string(method),
		})
		return
	default:
		writeJSON(w, http.StatusBadRequest, createAccountErrorResponse{
			Error: "invalid auth_method",
			Field: "auth_method",
		})
		return
	}
	if !domain.ValidUpstreamAPIKey(req.APIKey) {
		writeJSON(w, http.StatusBadRequest, createAccountErrorResponse{
			Error: "api_key must be 1..256 characters",
			Field: "api_key",
		})
		return
	}

	account, err := h.accounts.Create(r.Context(), req.Name, req.Provider, req.APIKey, req.BaseURL, req.Capabilities)
	if err != nil {
		var validationErr *domain.ValidationError
		if errors.As(err, &validationErr) {
			h.logger.Warn("create account validation failed",
				"field", validationErr.Field,
				"error", validationErr.Error(),
			)
			writeJSON(w, http.StatusBadRequest, createAccountErrorResponse{
				Error: validationErr.Error(),
				Field: validationErr.Field,
			})
			return
		}
		h.logger.Error("create account failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	writeJSON(w, http.StatusCreated, newAccountCreateResponse(*account))
}

func (h *Handler) ListAccounts(w http.ResponseWriter, r *http.Request) {
	var statusFilter []string
	if s := r.URL.Query().Get("status"); s != "" {
		statusFilter = []string{s}
	}

	if h.accountListProvider != nil {
		items, err := h.accountListProvider.ListForAdminAPI(r.Context())
		if err != nil {
			h.logger.Error("list accounts failed", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		filtered := filterAccountListItems(items, statusFilter)
		resp := make([]accountListItemResponse, 0, len(filtered))
		for _, item := range filtered {
			resp = append(resp, newAccountListProjectionResponse(item))
		}
		writeJSON(w, http.StatusOK, map[string]any{"accounts": resp, "total": len(resp)})
		return
	}

	accounts, err := h.accounts.List(r.Context(), statusFilter)
	if err != nil {
		h.logger.Error("list accounts failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	if accounts == nil {
		accounts = []domain.UpstreamAccount{}
	}
	items := make([]accountListItemResponse, 0, len(accounts))
	for _, account := range accounts {
		items = append(items, newAccountListItemResponse(account))
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": items, "total": len(items)})
}

func (h *Handler) GetAccount(w http.ResponseWriter, r *http.Request) {
	id, err := parseIDFromPath(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid account ID"})
		return
	}

	account, err := h.accounts.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrAccountNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "account not found"})
			return
		}
		h.logger.Error("get account failed", "error", err, "account_id", id)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	writeJSON(w, http.StatusOK, newAccountListItemResponse(*account))
}

// UpdateAccount handles POST /api/admin/accounts/{id}/update — the
// api_key-row edit surface. Only api_key accounts are editable; OAuth
// rows return a 400 via handleAccountError. Field errors surface with a
// `field` key so the envelope adapter can map them onto
// invalid_account_payload.
func (h *Handler) UpdateAccount(w http.ResponseWriter, r *http.Request) {
	id, err := parseIDFromPath(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid account ID"})
		return
	}

	var req updateAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	account, err := h.accounts.UpdateDetails(r.Context(), id, core.AccountDetailsPatch{
		Name:         req.Name,
		APIKey:       req.APIKey,
		BaseURL:      req.BaseURL,
		Capabilities: req.Capabilities,
	})
	if err != nil {
		var validationErr *domain.ValidationError
		if errors.As(err, &validationErr) {
			h.logger.Warn("update account validation failed",
				"account_id", id, "field", validationErr.Field, "error", validationErr.Error())
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": validationErr.Error(),
				"field": validationErr.Field,
			})
			return
		}
		h.handleAccountError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, newAccountListItemResponse(*account))
}

func (h *Handler) EnableAccount(w http.ResponseWriter, r *http.Request) {
	id, err := parseIDFromPath(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid account ID"})
		return
	}

	if err := h.accounts.Enable(r.Context(), id); err != nil {
		h.handleAccountError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "enabled"})
}

func (h *Handler) DisableAccount(w http.ResponseWriter, r *http.Request) {
	id, err := parseIDFromPath(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid account ID"})
		return
	}

	if err := h.accounts.Disable(r.Context(), id); err != nil {
		h.handleAccountError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "disabled"})
}

func (h *Handler) DeleteAccount(w http.ResponseWriter, r *http.Request) {
	id, err := parseIDFromPath(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid account ID"})
		return
	}

	if err := h.accounts.Delete(r.Context(), id); err != nil {
		h.handleAccountError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handler) QueryRequests(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	params := core.QueryParams{}

	startStr := q.Get("start")
	if startStr == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "start parameter is required (ISO 8601)"})
		return
	}
	startTime, err := time.Parse(time.RFC3339, startStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid start time (ISO 8601 required)"})
		return
	}
	params.Start = &startTime

	if v := q.Get("end"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid end time (ISO 8601 required)"})
			return
		}
		params.End = &t
	} else {
		now := time.Now()
		params.End = &now
	}

	if v := q.Get("account_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid account_id"})
			return
		}
		params.AccountID = &id
	}

	if v := q.Get("outcome"); v != "" {
		params.Outcome = &v
	}

	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid limit"})
			return
		}
		params.Limit = n
	}

	if v := q.Get("before"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid before cursor"})
			return
		}
		params.BeforeID = &id
	}

	records, err := h.requests.Query(r.Context(), params)
	if err != nil {
		h.logger.Error("query requests failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	if records == nil {
		records = []domain.RequestRecord{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"records": records, "total": len(records)})
}

func (h *Handler) ResolveSession(w http.ResponseWriter, r *http.Request) {
	sessionKey := r.URL.Query().Get("session_key")
	if sessionKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session_key parameter is required"})
		return
	}

	account, err := h.selector.SelectAccount(r.Context(), sessionKey)
	if err != nil {
		if errors.Is(err, domain.ErrNoCapacity) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no active accounts"})
			return
		}
		h.logger.Error("resolve session failed", "error", err, "session_key", sessionKey)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"session_key":  sessionKey,
		"account_id":   account.ID,
		"account_name": account.Name,
		"provider":     account.Provider,
	})
}

type createAccountErrorResponse struct {
	Error       string   `json:"error"`
	Field       string   `json:"field,omitempty"`
	AllowedHere []string `json:"allowed_here,omitempty"`
	Got         string   `json:"got,omitempty"`
}

type accountCreateResponse struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	Provider     string    `json:"provider"`
	BaseURL      *string   `json:"base_url,omitempty"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Capabilities []string  `json:"capabilities,omitempty"`
}

// updateAccountRequest is the optional-field edit payload for
// POST /api/admin/accounts/{id}/update. A pointer field is nil when the
// key is absent (keep current) and non-nil when present (set; api_key
// "" and base_url "" clear / no-op per the service-layer rules). This is
// the api_key-row edit surface — OAuth rows are rejected downstream.
type updateAccountRequest struct {
	Name         *string  `json:"name,omitempty"`
	APIKey       *string  `json:"api_key,omitempty"`
	BaseURL      *string  `json:"base_url,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

type accountListItemResponse struct {
	ID         int64             `json:"id"`
	Name       string            `json:"name"`
	Provider   string            `json:"provider"`
	AuthMethod domain.AuthMethod `json:"auth_method"`
	Status     string            `json:"status"`
	BaseURL    *string           `json:"base_url,omitempty"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`

	Email            *string    `json:"email,omitempty"`
	PlanType         *string    `json:"plan_type,omitempty"`
	PlanTypeLabel    *string    `json:"plan_type_label,omitempty"`
	ChatGPTAccountID *string    `json:"chatgpt_account_id,omitempty"`
	LastRefresh      *time.Time `json:"last_refresh,omitempty"`
	AccessExpiresAt  *time.Time `json:"access_expires_at,omitempty"`

	Capabilities []string `json:"capabilities,omitempty"`

	PrimaryUsedPercent     *float64   `json:"primary_used_percent,omitempty"`
	SecondaryUsedPercent   *float64   `json:"secondary_used_percent,omitempty"`
	UsageUpdatedAt         *time.Time `json:"usage_updated_at,omitempty"`
	PrimaryResetAt         *time.Time `json:"primary_reset_at,omitempty"`
	SecondaryResetAt       *time.Time `json:"secondary_reset_at,omitempty"`
	PrimaryWindowSeconds   *int64     `json:"primary_window_seconds,omitempty"`
	SecondaryWindowSeconds *int64     `json:"secondary_window_seconds,omitempty"`
}

func normaliseRequestedAuthMethod(raw string) domain.AuthMethod {
	method := strings.TrimSpace(raw)
	if method == "" {
		return domain.AuthMethodAPIKey
	}
	return domain.AuthMethod(method)
}

func usedPercent(value *float64) *float64 {
	if value == nil {
		return nil
	}
	if math.IsNaN(*value) || math.IsInf(*value, 0) {
		return nil
	}
	used := *value
	if used < 0 {
		used = 0
	} else if used > 100 {
		used = 100
	}
	return &used
}

func normalisedAccountAuthMethod(method domain.AuthMethod) domain.AuthMethod {
	if method == "" {
		return domain.AuthMethodAPIKey
	}
	return method
}

func newAccountCreateResponse(account domain.UpstreamAccount) accountCreateResponse {
	return accountCreateResponse{
		ID:           account.ID,
		Name:         account.Name,
		Provider:     account.Provider,
		BaseURL:      account.BaseURL,
		Status:       account.Status,
		CreatedAt:    account.CreatedAt,
		UpdatedAt:    account.UpdatedAt,
		Capabilities: account.Capabilities,
	}
}

func newAccountListItemResponse(account domain.UpstreamAccount) accountListItemResponse {
	method := normalisedAccountAuthMethod(account.AuthMethod)
	resp := accountListItemResponse{
		ID:           account.ID,
		Name:         account.Name,
		Provider:     account.Provider,
		AuthMethod:   method,
		Status:       account.Status,
		BaseURL:      account.BaseURL,
		CreatedAt:    account.CreatedAt,
		UpdatedAt:    account.UpdatedAt,
		Capabilities: account.Capabilities,
	}
	if method == domain.AuthMethodAPIKey {
		return resp
	}
	resp.Email = account.Email
	resp.PlanType = account.PlanType
	if account.PlanType != nil {
		label := presentation.PlanTypeLabel(*account.PlanType)
		resp.PlanTypeLabel = &label
	}
	resp.ChatGPTAccountID = account.ChatGPTAccountID
	resp.LastRefresh = account.LastRefresh
	resp.AccessExpiresAt = account.AccessExpiresAt
	resp.PrimaryUsedPercent = usedPercent(account.PrimaryUsedPercent)
	resp.SecondaryUsedPercent = usedPercent(account.SecondaryUsedPercent)
	resp.UsageUpdatedAt = account.UsageUpdatedAt
	resp.PrimaryResetAt = account.PrimaryResetAt
	resp.SecondaryResetAt = account.SecondaryResetAt
	resp.PrimaryWindowSeconds = account.PrimaryWindowSeconds
	resp.SecondaryWindowSeconds = account.SecondaryWindowSeconds
	return resp
}

func newAccountListProjectionResponse(account store.AccountListItem) accountListItemResponse {
	method := normalisedAccountAuthMethod(account.AuthMethod)
	resp := accountListItemResponse{
		ID:           account.ID,
		Name:         account.Name,
		Provider:     account.Provider,
		AuthMethod:   method,
		Status:       account.Status,
		BaseURL:      account.BaseURL,
		CreatedAt:    account.CreatedAt,
		UpdatedAt:    account.UpdatedAt,
		Capabilities: account.Capabilities,
	}
	if method == domain.AuthMethodAPIKey {
		return resp
	}
	resp.Email = account.Email
	resp.PlanType = account.PlanType
	if account.PlanType != nil {
		label := presentation.PlanTypeLabel(*account.PlanType)
		resp.PlanTypeLabel = &label
	}
	resp.ChatGPTAccountID = account.ChatGPTAccountID
	resp.LastRefresh = account.LastRefresh
	resp.AccessExpiresAt = account.AccessExpiresAt
	resp.PrimaryUsedPercent = usedPercent(account.PrimaryUsedPercent)
	resp.SecondaryUsedPercent = usedPercent(account.SecondaryUsedPercent)
	resp.UsageUpdatedAt = account.UsageUpdatedAt
	resp.PrimaryResetAt = account.PrimaryResetAt
	resp.SecondaryResetAt = account.SecondaryResetAt
	resp.PrimaryWindowSeconds = account.PrimaryWindowSeconds
	resp.SecondaryWindowSeconds = account.SecondaryWindowSeconds
	return resp
}

func filterAccountListItems(items []store.AccountListItem, statusFilter []string) []store.AccountListItem {
	if len(statusFilter) == 0 {
		return items
	}
	allowed := make(map[string]struct{}, len(statusFilter))
	for _, status := range statusFilter {
		allowed[status] = struct{}{}
	}
	filtered := make([]store.AccountListItem, 0, len(items))
	for _, item := range items {
		if _, ok := allowed[item.Status]; ok {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func (h *Handler) GetHealth(w http.ResponseWriter, r *http.Request) {
	health, err := h.health.GetHealth(r.Context())
	if err != nil {
		h.logger.Error("health check failed", "error", err)
	}

	status := http.StatusOK
	if health.Status != "healthy" {
		status = http.StatusServiceUnavailable
	}

	writeJSON(w, status, health)
}

func (h *Handler) handleAccountError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrAccountNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "account not found"})
	case errors.Is(err, domain.ErrAccountDeleted):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "account is deleted"})
	case errors.Is(err, domain.ErrInvalidTransition):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, domain.ErrInvalidAccountShape):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	default:
		h.logger.Error("unexpected account error", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
}

func parseIDFromPath(r *http.Request) (int64, error) {
	idStr := r.PathValue("id")
	return strconv.ParseInt(idStr, 10, 64)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

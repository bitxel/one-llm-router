package adminapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"time"

	"github.com/user/one-llm-router/internal/api"
	"github.com/user/one-llm-router/internal/api/errcode"
	"github.com/user/one-llm-router/internal/core"
	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/oauth"
	"github.com/user/one-llm-router/internal/presentation"
)

const (
	ImportAuthJSONRoute = "/api/admin/accounts/import-auth-json"

	ImportAuthJSONEnvelopeLimitBytes int64 = 64 << 10
	ImportAuthJSONPartLimitBytes     int64 = 16 << 10
	importMultipartMemoryLimit       int64 = (16 << 10) + (4 << 10)

	importRejectedEvent       = "auth_json_import_rejected"
	importFallbackWarnEvent   = "auth_json_import_last_refresh_fallback"
	importSuccessAuditEvent   = "account_created"
	importAnonymousOperatorID = "anonymous"
)

type ImportAuthJSONHandler struct {
	importer       authJSONImporter
	modelRefresher *core.ModelRefresher
	modelRepo      core.ModelRefresherRepo
	logger         *slog.Logger
}

type authJSONImporter interface {
	Import(context.Context, []byte) (*oauth.AuthJSONImportResult, error)
}

func NewImportAuthJSONHandler(importer authJSONImporter, logger *slog.Logger) *ImportAuthJSONHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &ImportAuthJSONHandler{
		importer: importer,
		logger:   logger,
	}
}

func (h *ImportAuthJSONHandler) SetModelRefresher(refresher *core.ModelRefresher, modelRepo core.ModelRefresherRepo) {
	h.modelRefresher = refresher
	h.modelRepo = modelRepo
}

func RegisterImportAuthJSONHandler(mux *http.ServeMux, handler *ImportAuthJSONHandler, chain func(http.Handler) http.Handler) {
	if chain == nil {
		chain = func(next http.Handler) http.Handler { return next }
	}
	mux.Handle("POST "+ImportAuthJSONRoute, chain(handler))
}

func (h *ImportAuthJSONHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	reqID := api.RequestIDFromContext(r.Context())
	if h == nil || h.importer == nil {
		importLogger(h).Error("import handler misconfigured",
			"request_id", reqID,
			"error", errors.New("adminapi.ImportAuthJSONHandler: importer is nil"),
		)
		api.WriteSysErr(w, reqID, errcode.OAuthInternalError, errcode.Symbol(errcode.OAuthInternalError))
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, ImportAuthJSONEnvelopeLimitBytes)
	defer func() { _ = r.Body.Close() }()
	if r.ContentLength > ImportAuthJSONEnvelopeLimitBytes {
		h.writeRejectedTooLarge(w, reqID, "envelope")
		return
	}

	raw, ok := h.readRequestAuthJSON(w, r, reqID)
	if !ok {
		return
	}

	result, err := h.importer.Import(r.Context(), raw)
	if err != nil {
		var invalid *oauth.InvalidAuthJSONError
		switch {
		case errors.Is(err, oauth.ErrInvalidAuthJSONStructure):
			h.writeRejectedStructure(w, reqID, "malformed_structure")
			return
		case errors.As(err, &invalid):
			h.writeRejectedInvalidJSON(w, reqID, invalid.Reason, invalid.MissingFields)
			return
		default:
			h.logSystemError(reqID, "persist imported auth.json account", err)
			api.WriteSysErr(w, reqID, errcode.OAuthStoreFailed, errcode.Symbol(errcode.OAuthStoreFailed))
			return
		}
	}

	account := result.Account
	for _, reason := range result.FallbackReasons {
		h.logImportFallback(reqID, account.ID, reason)
	}
	h.logger.Info(importSuccessAuditEvent,
		"request_id", reqID,
		"account_id", account.ID,
		"name", account.Name,
		"provider", account.Provider,
	)

	if h.modelRefresher != nil && h.modelRepo != nil && account.Status == domain.AccountStatusActive {
		go func() { _, _, _, _ = h.modelRefresher.Refresh(context.Background(), account, h.modelRepo) }()
	}

	api.WriteOK(w, reqID, importAuthJSONSuccessData(account))
}

type importAuthJSONAccountResponse struct {
	ID               int64             `json:"id"`
	Name             string            `json:"name"`
	Provider         string            `json:"provider"`
	AuthMethod       domain.AuthMethod `json:"auth_method"`
	Status           string            `json:"status"`
	BaseURL          *string           `json:"base_url,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
	Email            *string           `json:"email,omitempty"`
	PlanType         *string           `json:"plan_type,omitempty"`
	PlanTypeLabel    *string           `json:"plan_type_label,omitempty"`
	ChatGPTAccountID *string           `json:"chatgpt_account_id,omitempty"`
	LastRefresh      *time.Time        `json:"last_refresh,omitempty"`
	AccessExpiresAt  *time.Time        `json:"access_expires_at,omitempty"`
}

func importAuthJSONSuccessData(account *domain.UpstreamAccount) map[string]any {
	item := importAuthJSONAccountResponse{
		ID:               account.ID,
		Name:             account.Name,
		Provider:         account.Provider,
		AuthMethod:       account.AuthMethod,
		Status:           account.Status,
		BaseURL:          account.BaseURL,
		CreatedAt:        account.CreatedAt,
		UpdatedAt:        account.UpdatedAt,
		Email:            account.Email,
		PlanType:         account.PlanType,
		ChatGPTAccountID: account.ChatGPTAccountID,
		LastRefresh:      account.LastRefresh,
		AccessExpiresAt:  account.AccessExpiresAt,
	}
	if account.PlanType != nil {
		label := presentation.PlanTypeLabel(*account.PlanType)
		item.PlanTypeLabel = &label
	}
	return map[string]any{"account": item}
}

func (h *ImportAuthJSONHandler) readRequestAuthJSON(w http.ResponseWriter, r *http.Request, reqID string) ([]byte, bool) {
	mediaType, ok := h.singleContentType(w, r, reqID)
	if !ok {
		return nil, false
	}

	switch mediaType {
	case "multipart/form-data":
		if err := r.ParseMultipartForm(importMultipartMemoryLimit); err != nil {
			var maxErr *http.MaxBytesError
			switch {
			case errors.As(err, &maxErr), r.ContentLength > ImportAuthJSONEnvelopeLimitBytes:
				h.writeRejectedTooLarge(w, reqID, "envelope")
			default:
				h.writeRejectedStructure(w, reqID, "missing_multipart_auth_json_part")
			}
			return nil, false
		}
		if r.MultipartForm != nil {
			defer func() { _ = r.MultipartForm.RemoveAll() }()
		}
		return h.readAuthJSONPart(w, r, reqID)
	case "application/json":
		return h.readAuthJSONFromJSONBody(w, r, reqID)
	default:
		h.writeRejectedStructure(w, reqID, "unsupported_auth_json_content_type")
		return nil, false
	}
}

func (h *ImportAuthJSONHandler) singleContentType(w http.ResponseWriter, r *http.Request, reqID string) (string, bool) {
	cts := r.Header.Values("Content-Type")
	if len(cts) != 1 {
		h.writeRejectedStructure(w, reqID, "missing_or_duplicate_content_type")
		return "", false
	}
	mediaType, params, err := mime.ParseMediaType(cts[0])
	if err != nil {
		h.writeRejectedStructure(w, reqID, "invalid_content_type")
		return "", false
	}
	if mediaType == "multipart/form-data" && params["boundary"] == "" {
		h.writeRejectedStructure(w, reqID, "missing_multipart_auth_json_part")
		return "", false
	}
	return mediaType, true
}

func (h *ImportAuthJSONHandler) readAuthJSONPart(w http.ResponseWriter, r *http.Request, reqID string) ([]byte, bool) {
	if r.MultipartForm == nil || len(r.MultipartForm.File["auth_json"]) == 0 {
		h.writeRejectedStructure(w, reqID, "missing_multipart_auth_json_part")
		return nil, false
	}

	file, err := r.MultipartForm.File["auth_json"][0].Open()
	if err != nil {
		h.logSystemError(reqID, "open auth_json part", err)
		api.WriteSysErr(w, reqID, errcode.OAuthInternalError, errcode.Symbol(errcode.OAuthInternalError))
		return nil, false
	}
	defer func() { _ = file.Close() }()

	limited := io.LimitReader(file, ImportAuthJSONPartLimitBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		h.logSystemError(reqID, "read auth_json part", err)
		api.WriteSysErr(w, reqID, errcode.OAuthInternalError, errcode.Symbol(errcode.OAuthInternalError))
		return nil, false
	}
	if int64(len(raw)) > ImportAuthJSONPartLimitBytes {
		h.writeRejectedTooLarge(w, reqID, "part")
		return nil, false
	}
	return raw, true
}

func (h *ImportAuthJSONHandler) readAuthJSONFromJSONBody(w http.ResponseWriter, r *http.Request, reqID string) ([]byte, bool) {
	var payload struct {
		AuthJSON string `json:"auth_json"`
	}
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&payload); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			h.writeRejectedTooLarge(w, reqID, "envelope")
			return nil, false
		}
		h.writeRejectedStructure(w, reqID, "malformed_json_request_body")
		return nil, false
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		h.writeRejectedStructure(w, reqID, "malformed_json_request_body")
		return nil, false
	}
	if payload.AuthJSON == "" {
		h.writeRejectedStructure(w, reqID, "missing_json_auth_json_field")
		return nil, false
	}
	raw := []byte(payload.AuthJSON)
	if int64(len(raw)) > ImportAuthJSONPartLimitBytes {
		h.writeRejectedTooLarge(w, reqID, "part")
		return nil, false
	}
	return raw, true
}

func (h *ImportAuthJSONHandler) writeRejectedStructure(w http.ResponseWriter, reqID, reason string) {
	h.logRejected(reqID, reason, errcode.InvalidAuthJSONStructure, "")
	api.WriteBizErr(w, reqID, errcode.InvalidAuthJSONStructure, errcode.Symbol(errcode.InvalidAuthJSONStructure), nil)
}

func (h *ImportAuthJSONHandler) writeRejectedInvalidJSON(w http.ResponseWriter, reqID, reason string, missingFields []string) {
	h.logRejected(reqID, reason, errcode.InvalidAuthJSON, "")
	api.WriteBizErr(w, reqID, errcode.InvalidAuthJSON, errcode.Symbol(errcode.InvalidAuthJSON), map[string]any{
		"missing_fields": missingFields,
	})
}

func (h *ImportAuthJSONHandler) writeRejectedTooLarge(w http.ResponseWriter, reqID, scope string) {
	h.logRejected(reqID, "request_body_too_large", errcode.RequestBodyTooLarge, scope)
	limit := ImportAuthJSONEnvelopeLimitBytes
	if scope == "part" {
		limit = ImportAuthJSONPartLimitBytes
	}
	api.WriteBizErr(w, reqID, errcode.RequestBodyTooLarge, errcode.Symbol(errcode.RequestBodyTooLarge), map[string]any{
		"scope":       scope,
		"limit_bytes": limit,
	})
}

func (h *ImportAuthJSONHandler) logRejected(reqID, reason string, code int, scope string) {
	attrs := []any{
		"request_id", reqID,
		"operator_id", importAnonymousOperatorID,
		"reason", reason,
		"error_code", errcode.Symbol(code),
	}
	if scope != "" {
		attrs = append(attrs, "scope", scope)
	}
	h.logger.Info(importRejectedEvent, attrs...)
}

func (h *ImportAuthJSONHandler) logImportFallback(reqID string, accountID int64, reason string) {
	h.logger.Warn(importFallbackWarnEvent,
		"request_id", reqID,
		"account_id", accountID,
		"reason", reason,
	)
}

func (h *ImportAuthJSONHandler) logSystemError(reqID, msg string, err error) {
	importLogger(h).Error(msg,
		"request_id", reqID,
		"error", err,
	)
}

func importLogger(h *ImportAuthJSONHandler) *slog.Logger {
	if h == nil || h.logger == nil {
		return slog.Default()
	}
	return h.logger
}

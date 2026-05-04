package exportapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/user/one-llm-router/internal/api"
	"github.com/user/one-llm-router/internal/api/errcode"
	"github.com/user/one-llm-router/internal/domain"
	generatedadminapi "github.com/user/one-llm-router/internal/generated/adminapi"
	"github.com/user/one-llm-router/internal/oauth"
	"github.com/user/one-llm-router/internal/requestid"
	"github.com/user/one-llm-router/internal/store"
)

const (
	ExportAuthJSONRoute       = "/api/admin/accounts/{id}/export-auth-json"
	exportAttachmentFilename  = "auth.json"
	exportedEventName         = "oauth_auth_json_exported"
	exportRejectedEventName   = "oauth_auth_json_export_rejected"
	exportAnonymousOperatorID = "anonymous"
	exportSuccessContentType  = "application/json; charset=utf-8"
	exportSuccessCacheControl = "no-store, private"
	exportSuccessContentDisp  = `attachment; filename="auth.json"`
	exportSuccessNoSniff      = "nosniff"
)

type accountExporter interface {
	GetForExport(ctx context.Context, id int64) (*store.ExportPayload, error)
	GetProjectionByID(ctx context.Context, id int64) (*domain.UpstreamAccount, error)
}

type ExportAuthJSONHandler struct {
	accounts accountExporter
	logger   *slog.Logger
}

type codexAuthJSONDocument struct {
	OPENAIAPIKEY *string `json:"OPENAI_API_KEY"`
	Tokens       struct {
		AccessToken  string  `json:"access_token"`
		RefreshToken string  `json:"refresh_token"`
		IDToken      string  `json:"id_token"`
		AccountID    *string `json:"account_id"`
	} `json:"tokens"`
	LastRefresh *time.Time `json:"last_refresh"`
}

// We intentionally keep a local export-only projection instead of
// marshaling generatedadminapi.CodexAuthJSON directly: the generated
// type currently tags OPENAI_API_KEY and tokens.account_id with
// `omitempty`, which would drop the contract-required explicit nulls
// on missing values. We still feed the resulting bytes back through
// generatedadminapi.ExportAuthJSONResponseBody + the safe export visit
// wrappers so the success/error branch headers stay coupled to the
// code-generated surface.

func NewExportAuthJSONHandler(accounts accountExporter, logger *slog.Logger) *ExportAuthJSONHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &ExportAuthJSONHandler{
		accounts: accounts,
		logger:   logger,
	}
}

func RegisterExportAuthJSONHandler(mux *http.ServeMux, handler *ExportAuthJSONHandler, chain func(http.Handler) http.Handler) {
	if chain == nil {
		chain = func(next http.Handler) http.Handler { return next }
	}
	mux.Handle("POST "+ExportAuthJSONRoute, chain(handler))
}

func (h *ExportAuthJSONHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	reqID := api.RequestIDFromContext(r.Context())
	if h == nil || h.accounts == nil {
		exportLogger(h).Error("export auth.json handler misconfigured",
			"request_id", reqID,
			"error", errors.New("exportapi.ExportAuthJSONHandler: accounts is nil"),
		)
		api.WriteSysErr(w, reqID, errcode.OAuthInternalError, errcode.Symbol(errcode.OAuthInternalError))
		return
	}

	id, err := parseExportAccountID(r)
	if err != nil {
		api.WriteBizErr(w, reqID, errcode.InvalidAccountPayload, errcode.Symbol(errcode.InvalidAccountPayload), map[string]any{
			"field": "id",
		})
		return
	}

	payload, err := h.accounts.GetForExport(r.Context(), id)
	if err != nil {
		h.writeExportLookupError(r.Context(), w, id, err)
		return
	}

	doc := buildCodexAuthJSONDocument(payload)
	body, err := json.Marshal(doc)
	if err != nil {
		exportLogger(h).Error("marshal exported auth.json",
			"request_id", reqID,
			"account_id", id,
			"error", err,
		)
		api.WriteSysErr(w, reqID, errcode.OAuthInternalError, errcode.Symbol(errcode.OAuthInternalError))
		return
	}

	claims, claimsErr := oauth.ExtractClaims(payload.IDToken)
	email := ""
	if claimsErr == nil {
		email = claims.Email
	}

	h.logger.Warn(exportedEventName,
		"request_id", reqID,
		"account_id", payload.AccountID,
		"operator_id", exportAnonymousOperatorID,
		"email", email,
		"exported_at", time.Now().UTC(),
	)
	exportBody, err := exportSuccessBody(body)
	if err != nil {
		exportLogger(h).Error("build export response body",
			"request_id", reqID,
			"account_id", id,
			"error", err,
		)
		api.WriteSysErr(w, reqID, errcode.OAuthInternalError, errcode.Symbol(errcode.OAuthInternalError))
		return
	}
	if err := writeExportResponseObject(w, reqID, generatedadminapi.ExportAuthJSONAttachmentResponse{
		Body: exportBody,
	}); err != nil {
		exportLogger(h).Error("write export attachment response",
			"request_id", reqID,
			"account_id", id,
			"error", err,
		)
		api.WriteSysErr(w, reqID, errcode.OAuthInternalError, errcode.Symbol(errcode.OAuthInternalError))
	}
}

func (h *ExportAuthJSONHandler) writeExportLookupError(ctx context.Context, w http.ResponseWriter, id int64, err error) {
	reqID := requestid.FromContext(ctx)
	switch {
	case errors.Is(err, domain.ErrAccountNotFound):
		h.logger.Info(exportRejectedEventName,
			"request_id", reqID,
			"account_id", id,
			"error_code", errcode.Symbol(errcode.AccountNotFound),
		)
		response, respErr := accountNotFoundResponse()
		if respErr != nil {
			exportLogger(h).Error("build account_not_found export envelope",
				"request_id", reqID,
				"account_id", id,
				"error", respErr,
			)
			api.WriteSysErr(w, reqID, errcode.OAuthInternalError, errcode.Symbol(errcode.OAuthInternalError))
			return
		}
		if respErr := writeExportResponseObject(w, reqID, response); respErr != nil {
			exportLogger(h).Error("write account_not_found export envelope",
				"request_id", reqID,
				"account_id", id,
				"error", respErr,
			)
			api.WriteSysErr(w, reqID, errcode.OAuthInternalError, errcode.Symbol(errcode.OAuthInternalError))
		}
		return
	case errors.Is(err, domain.ErrInvalidAccountShape):
		row, rowErr := h.accounts.GetProjectionByID(ctx, id)
		switch {
		case rowErr == nil && row.Status == domain.AccountStatusDeleted:
			h.logger.Info(exportRejectedEventName,
				"request_id", reqID,
				"account_id", id,
				"error_code", errcode.Symbol(errcode.AccountNotFound),
			)
			response, respErr := accountNotFoundResponse()
			if respErr != nil {
				exportLogger(h).Error("build account_not_found export envelope after invalid shape",
					"request_id", reqID,
					"account_id", id,
					"error", respErr,
				)
				api.WriteSysErr(w, reqID, errcode.OAuthInternalError, errcode.Symbol(errcode.OAuthInternalError))
				return
			}
			if respErr := writeExportResponseObject(w, reqID, response); respErr != nil {
				exportLogger(h).Error("write account_not_found export envelope after invalid shape",
					"request_id", reqID,
					"account_id", id,
					"error", respErr,
				)
				api.WriteSysErr(w, reqID, errcode.OAuthInternalError, errcode.Symbol(errcode.OAuthInternalError))
			}
			return
		case rowErr == nil && row.AuthMethod == domain.AuthMethodAPIKey:
			h.logger.Info(exportRejectedEventName,
				"request_id", reqID,
				"account_id", id,
				"error_code", errcode.Symbol(errcode.NotOAuthAccount),
			)
			response, respErr := notOAuthAccountResponse()
			if respErr != nil {
				exportLogger(h).Error("build not_oauth_account export envelope",
					"request_id", reqID,
					"account_id", id,
					"error", respErr,
				)
				api.WriteSysErr(w, reqID, errcode.OAuthInternalError, errcode.Symbol(errcode.OAuthInternalError))
				return
			}
			if respErr := writeExportResponseObject(w, reqID, response); respErr != nil {
				exportLogger(h).Error("write not_oauth_account export envelope",
					"request_id", reqID,
					"account_id", id,
					"error", respErr,
				)
				api.WriteSysErr(w, reqID, errcode.OAuthInternalError, errcode.Symbol(errcode.OAuthInternalError))
			}
			return
		case rowErr == nil && row.AuthMethod != domain.AuthMethodAPIKey:
			exportLogger(h).Error("export auth.json rejected corrupted oauth row",
				"request_id", reqID,
				"account_id", id,
				"auth_method", row.AuthMethod,
				"error", err,
			)
		case rowErr != nil && !errors.Is(rowErr, domain.ErrAccountNotFound):
			exportLogger(h).Error("load export projection after invalid shape",
				"request_id", reqID,
				"account_id", id,
				"error", rowErr,
			)
		}
	}

	exportLogger(h).Error("read export auth.json payload failed",
		"request_id", reqID,
		"account_id", id,
		"error", err,
	)
	api.WriteSysErr(w, reqID, errcode.OAuthExportReadFailed, errcode.Symbol(errcode.OAuthExportReadFailed))
}

func buildCodexAuthJSONDocument(payload *store.ExportPayload) codexAuthJSONDocument {
	var doc codexAuthJSONDocument
	if payload == nil {
		return doc
	}
	doc.Tokens.AccessToken = string(payload.AccessToken)
	doc.Tokens.RefreshToken = string(payload.RefreshToken)
	doc.Tokens.IDToken = string(payload.IDToken)
	doc.Tokens.AccountID = payload.ChatGPTAccountID
	doc.LastRefresh = payload.LastRefresh
	return doc
}

func exportSuccessBody(raw []byte) (generatedadminapi.ExportAuthJSONResponseBody, error) {
	var body generatedadminapi.ExportAuthJSONResponseBody
	if err := body.UnmarshalJSON(raw); err != nil {
		return generatedadminapi.ExportAuthJSONResponseBody{}, fmt.Errorf("unmarshal exported auth.json union: %w", err)
	}
	return body, nil
}

func accountNotFoundResponse() (generatedadminapi.ExportAuthJSONEnvelopeResponse, error) {
	var body generatedadminapi.ExportAuthJSONResponseBody
	if err := body.FromAccountNotFoundEnvelope(generatedadminapi.AccountNotFoundEnvelope{
		Code: generatedadminapi.N1001,
		Data: map[string]any{},
		Msg:  generatedadminapi.AccountNotFound,
	}); err != nil {
		return generatedadminapi.ExportAuthJSONEnvelopeResponse{}, fmt.Errorf("build account_not_found export envelope: %w", err)
	}
	return generatedadminapi.ExportAuthJSONEnvelopeResponse{Body: body}, nil
}

func notOAuthAccountResponse() (generatedadminapi.ExportAuthJSONEnvelopeResponse, error) {
	var env generatedadminapi.NotOAuthAccountEnvelope
	env.Code = generatedadminapi.N3014
	env.Data.AuthMethod = generatedadminapi.NotOAuthAccountEnvelopeDataAuthMethod(domain.AuthMethodAPIKey)
	env.Msg = generatedadminapi.NotOauthAccount

	var body generatedadminapi.ExportAuthJSONResponseBody
	if err := body.FromNotOAuthAccountEnvelope(env); err != nil {
		return generatedadminapi.ExportAuthJSONEnvelopeResponse{}, fmt.Errorf("build not_oauth_account export envelope: %w", err)
	}
	return generatedadminapi.ExportAuthJSONEnvelopeResponse{Body: body}, nil
}

func writeExportResponseObject(w http.ResponseWriter, reqID string, response generatedadminapi.AccountsExportAuthJSONResponseObject) error {
	if reqID != "" {
		w.Header().Set("X-Request-Id", reqID)
	}
	if err := response.VisitAccountsExportAuthJSONResponse(w); err != nil {
		return fmt.Errorf("write export response object: %w", err)
	}
	return nil
}

func parseExportAccountID(r *http.Request) (int64, error) {
	if r == nil {
		return 0, errors.New("nil request")
	}
	raw := strings.TrimSpace(r.PathValue("id"))
	if raw == "" {
		return 0, errors.New("missing id")
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("parse export account id %q: %w", raw, err)
	}
	return id, nil
}

func exportLogger(h *ExportAuthJSONHandler) *slog.Logger {
	if h == nil || h.logger == nil {
		return slog.Default()
	}
	return h.logger
}

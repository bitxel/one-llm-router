package adminapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/user/one-llm-router/internal/api/errcode"
	"github.com/user/one-llm-router/internal/core"
	generatedadminapi "github.com/user/one-llm-router/internal/generated/adminapi"
)

const usageRoute = "/api/admin/usage"

type UsageReader interface {
	AdminUsage(ctx context.Context) (core.AdminUsageSummary, error)
}

type UsageHandler struct {
	usage  UsageReader
	logger *slog.Logger
}

var _ generatedadminapi.StrictServerInterface = (*UsageHandler)(nil)

func NewUsageHandler(usage UsageReader, logger *slog.Logger) *UsageHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &UsageHandler{usage: usage, logger: logger}
}

func RegisterUsageHandler(mux *http.ServeMux, handler *UsageHandler, chain func(http.Handler) http.Handler) {
	if chain == nil {
		chain = func(next http.Handler) http.Handler { return next }
	}
	innerMux := http.NewServeMux()
	root := generatedadminapi.HandlerFromMuxWithEnvelope(handler, innerMux)
	mux.Handle("GET "+usageRoute, chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		root.ServeHTTP(w, r)
	})))
}

func (h *UsageHandler) UsageGet(ctx context.Context, _ generatedadminapi.UsageGetRequestObject) (generatedadminapi.UsageGetResponseObject, error) {
	if h == nil || h.usage == nil {
		return usageSystemError(errors.New("adminapi.UsageHandler.UsageGet: service is nil")), nil
	}
	summary, err := h.usage.AdminUsage(ctx)
	if err != nil {
		if h.logger != nil {
			h.logger.Error("usage summary failed", "error", err)
		}
		return usageSystemError(fmt.Errorf("usage summary: %w", err)), nil
	}
	return usageSuccess(summary), nil
}

func usageSuccess(summary core.AdminUsageSummary) generatedadminapi.UsageGetResponseObject {
	env := generatedadminapi.UsageEnvelope{
		Code: generatedadminapi.N0,
		Msg:  generatedadminapi.Ok,
		Data: generatedadminapi.UsageData{
			RequestCount:      summary.RequestCount,
			TotalTokens:       summary.TotalTokens,
			CachedInputTokens: summary.CachedInputTokens,
			TotalCostUsd:      summary.TotalCostUSD,
			Limits:            mapsOrEmpty(summary.Limits),
			Codex: generatedadminapi.UsageCodexStatus{
				PlanType:             summary.Codex.PlanType,
				RateLimit:            nullableObject(summary.Codex.RateLimit),
				Credits:              nullableObject(summary.Codex.Credits),
				AdditionalRateLimits: mapsOrEmpty(summary.Codex.AdditionalRateLimits),
			},
		},
	}
	return generatedadminapi.UsageGet200JSONResponse(env)
}

func mapsOrEmpty(values []map[string]any) []map[string]interface{} {
	if values == nil {
		return []map[string]interface{}{}
	}
	return values
}

func nullableObject(value map[string]any) *map[string]interface{} {
	if value == nil {
		return nil
	}
	out := map[string]interface{}(value)
	return &out
}

func usageSystemError(error) generatedadminapi.UsageGetResponseObject {
	return generatedadminapi.UsageGet500JSONResponse{
		SystemErrorJSONResponse: generatedadminapi.SystemErrorJSONResponse{
			Code: generatedadminapi.N6900,
			Data: map[string]interface{}{},
			Msg:  errcode.Symbol(errcode.UsageInternalError),
		},
	}
}

func (h *UsageHandler) AccountsImportAuthJSON(context.Context, generatedadminapi.AccountsImportAuthJSONRequestObject) (generatedadminapi.AccountsImportAuthJSONResponseObject, error) {
	panic("UsageHandler.AccountsImportAuthJSON not routed")
}

func (h *UsageHandler) AccountsExportAuthJSON(context.Context, generatedadminapi.AccountsExportAuthJSONRequestObject) (generatedadminapi.AccountsExportAuthJSONResponseObject, error) {
	panic("UsageHandler.AccountsExportAuthJSON not routed")
}

func (h *UsageHandler) DashboardGet(context.Context, generatedadminapi.DashboardGetRequestObject) (generatedadminapi.DashboardGetResponseObject, error) {
	panic("UsageHandler.DashboardGet not routed")
}

func (h *UsageHandler) OauthBrowserManualCallback(context.Context, generatedadminapi.OauthBrowserManualCallbackRequestObject) (generatedadminapi.OauthBrowserManualCallbackResponseObject, error) {
	panic("UsageHandler.OauthBrowserManualCallback not routed")
}

func (h *UsageHandler) OauthBrowserStart(context.Context, generatedadminapi.OauthBrowserStartRequestObject) (generatedadminapi.OauthBrowserStartResponseObject, error) {
	panic("UsageHandler.OauthBrowserStart not routed")
}

func (h *UsageHandler) OauthCancel(context.Context, generatedadminapi.OauthCancelRequestObject) (generatedadminapi.OauthCancelResponseObject, error) {
	panic("UsageHandler.OauthCancel not routed")
}

func (h *UsageHandler) OauthDeviceStart(context.Context, generatedadminapi.OauthDeviceStartRequestObject) (generatedadminapi.OauthDeviceStartResponseObject, error) {
	panic("UsageHandler.OauthDeviceStart not routed")
}

func (h *UsageHandler) OauthFlowStatus(context.Context, generatedadminapi.OauthFlowStatusRequestObject) (generatedadminapi.OauthFlowStatusResponseObject, error) {
	panic("UsageHandler.OauthFlowStatus not routed")
}

func (h *UsageHandler) PlaygroundRun(context.Context, generatedadminapi.PlaygroundRunRequestObject) (generatedadminapi.PlaygroundRunResponseObject, error) {
	panic("UsageHandler.PlaygroundRun not routed")
}

func (h *UsageHandler) RequestsList(context.Context, generatedadminapi.RequestsListRequestObject) (generatedadminapi.RequestsListResponseObject, error) {
	panic("UsageHandler.RequestsList not routed")
}

func (h *UsageHandler) RequestsOptions(context.Context, generatedadminapi.RequestsOptionsRequestObject) (generatedadminapi.RequestsOptionsResponseObject, error) {
	panic("UsageHandler.RequestsOptions not routed")
}

func (h *UsageHandler) RequestsGet(context.Context, generatedadminapi.RequestsGetRequestObject) (generatedadminapi.RequestsGetResponseObject, error) {
	panic("UsageHandler.RequestsGet not routed")
}

func (h *UsageHandler) SettingsGet(context.Context, generatedadminapi.SettingsGetRequestObject) (generatedadminapi.SettingsGetResponseObject, error) {
	panic("UsageHandler.SettingsGet not routed")
}

func (h *UsageHandler) SettingsUpdate(context.Context, generatedadminapi.SettingsUpdateRequestObject) (generatedadminapi.SettingsUpdateResponseObject, error) {
	panic("UsageHandler.SettingsUpdate not routed")
}

func (h *UsageHandler) AccountModelsList(ctx context.Context, request generatedadminapi.AccountModelsListRequestObject) (generatedadminapi.AccountModelsListResponseObject, error) {
	return generatedadminapi.AccountModelsList500JSONResponse{
		SystemErrorJSONResponse: generatedadminapi.SystemErrorJSONResponse{
			Code: generatedadminapi.N1901,
			Msg:  "internal_error",
			Data: map[string]any{},
		},
	}, nil
}

func (h *UsageHandler) AccountModelAdd(ctx context.Context, request generatedadminapi.AccountModelAddRequestObject) (generatedadminapi.AccountModelAddResponseObject, error) {
	return generatedadminapi.AccountModelAdd500JSONResponse{
		SystemErrorJSONResponse: generatedadminapi.SystemErrorJSONResponse{
			Code: generatedadminapi.N1901,
			Msg:  "internal_error",
			Data: map[string]any{},
		},
	}, nil
}

func (h *UsageHandler) AccountModelRefresh(ctx context.Context, request generatedadminapi.AccountModelRefreshRequestObject) (generatedadminapi.AccountModelRefreshResponseObject, error) {
	return generatedadminapi.AccountModelRefresh500JSONResponse{
		SystemErrorJSONResponse: generatedadminapi.SystemErrorJSONResponse{
			Code: generatedadminapi.N1901,
			Msg:  "internal_error",
			Data: map[string]any{},
		},
	}, nil
}

func (h *UsageHandler) AccountModelRemove(ctx context.Context, request generatedadminapi.AccountModelRemoveRequestObject) (generatedadminapi.AccountModelRemoveResponseObject, error) {
	return generatedadminapi.AccountModelRemove500JSONResponse{
		SystemErrorJSONResponse: generatedadminapi.SystemErrorJSONResponse{
			Code: generatedadminapi.N1901,
			Msg:  "internal_error",
			Data: map[string]any{},
		},
	}, nil
}

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

const dashboardRoute = "/api/admin/dashboard"

type DashboardReader interface {
	Dashboard(ctx context.Context, query core.DashboardQuery) (core.DashboardSnapshot, error)
}

type DashboardHandler struct {
	dashboard DashboardReader
	logger    *slog.Logger
}

var _ generatedadminapi.StrictServerInterface = (*DashboardHandler)(nil)

func NewDashboardHandler(dashboard DashboardReader, logger *slog.Logger) *DashboardHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &DashboardHandler{dashboard: dashboard, logger: logger}
}

func RegisterDashboardHandler(mux *http.ServeMux, handler *DashboardHandler, chain func(http.Handler) http.Handler) {
	if chain == nil {
		chain = func(next http.Handler) http.Handler { return next }
	}
	innerMux := http.NewServeMux()
	root := generatedadminapi.HandlerFromMuxWithEnvelope(handler, innerMux)
	mux.Handle("GET "+dashboardRoute, chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		root.ServeHTTP(w, r)
	})))
}

func (h *DashboardHandler) DashboardGet(ctx context.Context, request generatedadminapi.DashboardGetRequestObject) (generatedadminapi.DashboardGetResponseObject, error) {
	if h == nil || h.dashboard == nil {
		return dashboardSystemError(errors.New("adminapi.DashboardHandler.DashboardGet: service is nil")), nil
	}

	query, invalid := dashboardQueryFromParams(request.Params)
	if invalid != nil {
		return dashboardInvalidFilter(invalid.Field, invalid.Reason)
	}

	snapshot, err := h.dashboard.Dashboard(ctx, query)
	if err != nil {
		var invalidErr *core.DashboardInvalidFilterError
		if errors.As(err, &invalidErr) {
			return dashboardInvalidFilter(invalidErr.Field, invalidErr.Reason)
		}
		if h.logger != nil {
			h.logger.Error("dashboard snapshot failed", "error", err)
		}
		return dashboardSystemError(fmt.Errorf("dashboard snapshot: %w", err)), nil
	}
	return dashboardSuccess(snapshot)
}

func dashboardQueryFromParams(params generatedadminapi.DashboardGetParams) (core.DashboardQuery, *core.DashboardInvalidFilterError) {
	query := core.DashboardQuery{Range: core.DashboardRange7D, AccountID: params.AccountId}
	if params.Range != nil {
		query.Range = core.DashboardRange(*params.Range)
	}
	if !core.DashboardRangeValid(query.Range) {
		return core.DashboardQuery{}, &core.DashboardInvalidFilterError{Field: "range", Reason: "must be one of 1h, 1d, 7d, 30d"}
	}
	if query.AccountID != nil && *query.AccountID <= 0 {
		return core.DashboardQuery{}, &core.DashboardInvalidFilterError{Field: "account_id", Reason: "must be positive"}
	}
	return query, nil
}

func dashboardSuccess(snapshot core.DashboardSnapshot) (generatedadminapi.DashboardGetResponseObject, error) {
	env := generatedadminapi.DashboardEnvelope{
		Code: generatedadminapi.DashboardEnvelopeCodeN0,
		Msg:  generatedadminapi.DashboardEnvelopeMsgOk,
		Data: generatedadminapi.DashboardData{
			Range:             generatedadminapi.DashboardRange(snapshot.Range),
			SelectedAccountId: snapshot.SelectedAccountID,
			WindowStart:       snapshot.WindowStart,
			WindowEnd:         snapshot.WindowEnd,
			BucketSeconds:     snapshot.BucketSeconds,
			AccountOptions:    generatedDashboardAccountOptions(snapshot.AccountOptions),
			Cards: generatedadminapi.DashboardCards{
				ActiveAccounts: generatedadminapi.DashboardActiveAccountsCard{Value: snapshot.ActiveAccounts},
				Requests:       generatedDashboardRequests(snapshot.Requests),
				Tokens:         generatedDashboardTokens(snapshot.Tokens),
				ErrorRate:      generatedDashboardErrorRate(snapshot.ErrorRate),
				Ttft:           generatedDashboardTTFT(snapshot.TTFT),
			},
		},
	}
	var body generatedadminapi.DashboardResponseBody
	if err := body.FromDashboardEnvelope(env); err != nil {
		return nil, err
	}
	return generatedadminapi.DashboardGet200JSONResponse(body), nil
}

func dashboardInvalidFilter(field, reason string) (generatedadminapi.DashboardGetResponseObject, error) {
	env := generatedadminapi.DashboardInvalidFilterEnvelope{
		Code: generatedadminapi.N5001,
		Msg:  generatedadminapi.DashboardInvalidFilter,
	}
	if field != "" {
		value := generatedadminapi.DashboardInvalidFilterEnvelopeDataField(field)
		env.Data.Field = &value
	}
	if reason != "" {
		env.Data.Reason = &reason
	}
	var body generatedadminapi.DashboardResponseBody
	if err := body.FromDashboardInvalidFilterEnvelope(env); err != nil {
		return nil, err
	}
	return generatedadminapi.DashboardGet200JSONResponse(body), nil
}

func dashboardSystemError(error) generatedadminapi.DashboardGetResponseObject {
	return generatedadminapi.DashboardGet500JSONResponse{
		SystemErrorJSONResponse: generatedadminapi.SystemErrorJSONResponse{
			Code: generatedadminapi.N5900,
			Data: map[string]interface{}{},
			Msg:  errcode.Symbol(errcode.DashboardInternalError),
		},
	}
}

func generatedDashboardAccountOptions(options []core.DashboardAccountOption) []generatedadminapi.DashboardAccountOption {
	out := make([]generatedadminapi.DashboardAccountOption, 0, len(options))
	for _, option := range options {
		item := generatedadminapi.DashboardAccountOption{
			Id:    option.ID,
			Label: option.Label,
		}
		account := generatedadminapi.AccountListItem{
			Id:               option.ID,
			Name:             option.Name,
			Provider:         option.Provider,
			BaseUrl:          option.BaseURL,
			AuthMethod:       generatedadminapi.AccountListItemAuthMethod(option.AuthMethod),
			Status:           generatedadminapi.AccountListItemStatus(option.Status),
			CreatedAt:        &option.CreatedAt,
			UpdatedAt:        &option.UpdatedAt,
			Email:            option.Email,
			PlanType:         option.PlanType,
			ChatgptAccountId: option.ChatGPTAccountID,
			LastRefresh:      option.LastRefresh,
			AccessExpiresAt:  option.AccessExpiresAt,
		}
		if option.PlanType != nil {
			label := PlanTypeLabel(*option.PlanType)
			account.PlanTypeLabel = &label
		}
		item.Account = &account
		out = append(out, item)
	}
	return out
}

func generatedDashboardRequests(card core.DashboardRequestsCard) generatedadminapi.DashboardRequestsCard {
	series := make([]generatedadminapi.DashboardCountPoint, 0, len(card.Series))
	for _, point := range card.Series {
		series = append(series, generatedadminapi.DashboardCountPoint{
			Timestamp: point.Timestamp,
			Count:     point.Count,
		})
	}
	return generatedadminapi.DashboardRequestsCard{Total: card.Total, Series: series}
}

func generatedDashboardTokens(card core.DashboardTokensCard) generatedadminapi.DashboardTokensCard {
	series := make([]generatedadminapi.DashboardTokenPoint, 0, len(card.Series))
	for _, point := range card.Series {
		series = append(series, generatedadminapi.DashboardTokenPoint{
			Timestamp:      point.Timestamp,
			InputCached:    point.InputCached,
			InputNonCached: point.InputNonCached,
			Output:         point.Output,
		})
	}
	return generatedadminapi.DashboardTokensCard{
		Totals: generatedadminapi.DashboardTokenBreakdown{
			InputCached:    card.Totals.InputCached,
			InputNonCached: card.Totals.InputNonCached,
			Output:         card.Totals.Output,
		},
		Series: series,
	}
}

func generatedDashboardErrorRate(card core.DashboardErrorRateCard) generatedadminapi.DashboardErrorRateCard {
	series := make([]generatedadminapi.DashboardRatePoint, 0, len(card.Series))
	for _, point := range card.Series {
		series = append(series, generatedadminapi.DashboardRatePoint{
			Timestamp: point.Timestamp,
			Total:     point.Total,
			Errors:    point.Errors,
			Rate:      point.Rate,
		})
	}
	return generatedadminapi.DashboardErrorRateCard{
		Value:  card.Value,
		Total:  card.Total,
		Errors: card.Errors,
		Series: series,
	}
}

func generatedDashboardTTFT(card core.DashboardTTFTCard) generatedadminapi.DashboardTTFTCard {
	series := make([]generatedadminapi.DashboardTTFTPoint, 0, len(card.Series))
	for _, point := range card.Series {
		series = append(series, generatedadminapi.DashboardTTFTPoint{
			Timestamp:   point.Timestamp,
			P95Ms:       point.P95MS,
			SampleCount: point.SampleCount,
		})
	}
	return generatedadminapi.DashboardTTFTCard{
		P95Ms:       card.P95MS,
		SampleCount: card.SampleCount,
		Series:      series,
	}
}

func (h *DashboardHandler) AccountsImportAuthJSON(context.Context, generatedadminapi.AccountsImportAuthJSONRequestObject) (generatedadminapi.AccountsImportAuthJSONResponseObject, error) {
	return nil, errors.New("adminapi.DashboardHandler.AccountsImportAuthJSON: not implemented")
}

func (h *DashboardHandler) AccountsExportAuthJSON(context.Context, generatedadminapi.AccountsExportAuthJSONRequestObject) (generatedadminapi.AccountsExportAuthJSONResponseObject, error) {
	return nil, errors.New("adminapi.DashboardHandler.AccountsExportAuthJSON: not implemented")
}

func (h *DashboardHandler) OauthBrowserManualCallback(context.Context, generatedadminapi.OauthBrowserManualCallbackRequestObject) (generatedadminapi.OauthBrowserManualCallbackResponseObject, error) {
	return nil, errors.New("adminapi.DashboardHandler.OauthBrowserManualCallback: not implemented")
}

func (h *DashboardHandler) OauthBrowserStart(context.Context, generatedadminapi.OauthBrowserStartRequestObject) (generatedadminapi.OauthBrowserStartResponseObject, error) {
	return nil, errors.New("adminapi.DashboardHandler.OauthBrowserStart: not implemented")
}

func (h *DashboardHandler) OauthCancel(context.Context, generatedadminapi.OauthCancelRequestObject) (generatedadminapi.OauthCancelResponseObject, error) {
	return nil, errors.New("adminapi.DashboardHandler.OauthCancel: not implemented")
}

func (h *DashboardHandler) OauthDeviceStart(context.Context, generatedadminapi.OauthDeviceStartRequestObject) (generatedadminapi.OauthDeviceStartResponseObject, error) {
	return nil, errors.New("adminapi.DashboardHandler.OauthDeviceStart: not implemented")
}

func (h *DashboardHandler) OauthFlowStatus(context.Context, generatedadminapi.OauthFlowStatusRequestObject) (generatedadminapi.OauthFlowStatusResponseObject, error) {
	return nil, errors.New("adminapi.DashboardHandler.OauthFlowStatus: not implemented")
}

func (h *DashboardHandler) PlaygroundRun(context.Context, generatedadminapi.PlaygroundRunRequestObject) (generatedadminapi.PlaygroundRunResponseObject, error) {
	return nil, errors.New("adminapi.DashboardHandler.PlaygroundRun: not implemented")
}

func (h *DashboardHandler) RequestsList(context.Context, generatedadminapi.RequestsListRequestObject) (generatedadminapi.RequestsListResponseObject, error) {
	return nil, errors.New("adminapi.DashboardHandler.RequestsList: not implemented")
}

func (h *DashboardHandler) RequestsOptions(context.Context, generatedadminapi.RequestsOptionsRequestObject) (generatedadminapi.RequestsOptionsResponseObject, error) {
	return nil, errors.New("adminapi.DashboardHandler.RequestsOptions: not implemented")
}

func (h *DashboardHandler) RequestsGet(context.Context, generatedadminapi.RequestsGetRequestObject) (generatedadminapi.RequestsGetResponseObject, error) {
	return nil, errors.New("adminapi.DashboardHandler.RequestsGet: not implemented")
}

func (h *DashboardHandler) UsageGet(context.Context, generatedadminapi.UsageGetRequestObject) (generatedadminapi.UsageGetResponseObject, error) {
	return nil, errors.New("adminapi.DashboardHandler.UsageGet: not implemented")
}

func (h *DashboardHandler) SettingsGet(context.Context, generatedadminapi.SettingsGetRequestObject) (generatedadminapi.SettingsGetResponseObject, error) {
	return nil, errors.New("adminapi.DashboardHandler.SettingsGet: not implemented")
}

func (h *DashboardHandler) SettingsUpdate(context.Context, generatedadminapi.SettingsUpdateRequestObject) (generatedadminapi.SettingsUpdateResponseObject, error) {
	return nil, errors.New("adminapi.DashboardHandler.SettingsUpdate: not implemented")
}

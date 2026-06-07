package adminapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/user/one-llm-router/internal/api/errcode"
	"github.com/user/one-llm-router/internal/core"
	"github.com/user/one-llm-router/internal/domain"
	generatedadminapi "github.com/user/one-llm-router/internal/generated/adminapi"
	"github.com/user/one-llm-router/internal/provider/openai"
	"github.com/user/one-llm-router/internal/store"
)

const (
	requestsListRoute    = "/api/admin/requests"
	requestsOptionsRoute = "/api/admin/requests/options"
	requestsGetRoute     = "/api/admin/requests/{id}"

	requestLogFilterMaxItems = 50
)

type RequestLogAccountProvider interface {
	ListForAdminAPI(ctx context.Context) ([]store.AccountListItem, error)
}

type RequestLogsHandler struct {
	requests *core.RequestService
	accounts RequestLogAccountProvider
	logger   *slog.Logger
}

var _ generatedadminapi.StrictServerInterface = (*RequestLogsHandler)(nil)

func NewRequestLogsHandler(
	requests *core.RequestService,
	accounts RequestLogAccountProvider,
	logger *slog.Logger,
) *RequestLogsHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &RequestLogsHandler{requests: requests, accounts: accounts, logger: logger}
}

func RegisterRequestLogsHandler(mux *http.ServeMux, handler *RequestLogsHandler, chain func(http.Handler) http.Handler) {
	if chain == nil {
		chain = func(next http.Handler) http.Handler { return next }
	}
	innerMux := http.NewServeMux()
	root := generatedadminapi.HandlerFromMuxWithEnvelope(handler, innerMux)
	for _, pattern := range []string{
		"GET " + requestsListRoute,
		"GET " + requestsOptionsRoute,
		"GET " + requestsGetRoute,
	} {
		mux.Handle(pattern, chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			root.ServeHTTP(w, r)
		})))
	}
}

func (h *RequestLogsHandler) RequestsList(ctx context.Context, request generatedadminapi.RequestsListRequestObject) (generatedadminapi.RequestsListResponseObject, error) {
	if h == nil || h.requests == nil {
		return requestsListSystemError(errors.New("request logs handler: request service is nil")), nil
	}
	params, invalid := queryParamsFromRequestsList(request.Params)
	if invalid != nil {
		return requestsListInvalidFilter(invalid.field, invalid.reason)
	}

	page, err := h.requests.QueryPage(ctx, params)
	if err != nil {
		h.logError("request logs list failed", err)
		return requestsListSystemError(fmt.Errorf("query request logs: %w", err)), nil
	}
	accountIndex, err := h.accountIndex(ctx)
	if err != nil {
		h.logError("request logs account projection failed", err)
		return requestsListSystemError(fmt.Errorf("load request log accounts: %w", err)), nil
	}

	records := make([]generatedadminapi.RequestLogRow, 0, len(page.Records))
	for _, record := range page.Records {
		row, err := requestLogRowFromDomain(record, accountIndex)
		if err != nil {
			h.logError("request logs list projection failed", err)
			return requestsListSystemError(fmt.Errorf("project request log row: %w", err)), nil
		}
		records = append(records, row)
	}
	env := generatedadminapi.RequestsListEnvelope{
		Code: generatedadminapi.RequestsListEnvelopeCodeN0,
		Msg:  generatedadminapi.RequestsListEnvelopeMsgOk,
		Data: generatedadminapi.RequestsListData{
			HasMore:      page.HasMore,
			NextBeforeId: page.NextBeforeID,
			Records:      records,
		},
	}
	var body generatedadminapi.RequestsListResponseBody
	if err := body.FromRequestsListEnvelope(env); err != nil {
		return nil, err
	}
	return generatedadminapi.RequestsList200JSONResponse(body), nil
}

func (h *RequestLogsHandler) RequestsOptions(ctx context.Context, request generatedadminapi.RequestsOptionsRequestObject) (generatedadminapi.RequestsOptionsResponseObject, error) {
	if h == nil || h.requests == nil {
		return requestsOptionsSystemError(errors.New("request logs handler: request service is nil")), nil
	}
	params, invalid := queryParamsFromRequestsOptions(request.Params)
	if invalid != nil {
		return requestsOptionsInvalidFilter(invalid.field, invalid.reason)
	}

	options, err := h.requests.FilterOptions(ctx, params)
	if err != nil {
		h.logError("request log options failed", err)
		return requestsOptionsSystemError(fmt.Errorf("query request log options: %w", err)), nil
	}
	accountIndex, err := h.accountIndex(ctx)
	if err != nil {
		h.logError("request log option account projection failed", err)
		return requestsOptionsSystemError(fmt.Errorf("load request log option accounts: %w", err)), nil
	}

	outcomes, err := requestOutcomes(options.Outcomes)
	if err != nil {
		h.logError("request log option outcome projection failed", err)
		return requestsOptionsSystemError(fmt.Errorf("project request log outcomes: %w", err)), nil
	}
	responseModes, err := requestResponseModes(options.ResponseModes)
	if err != nil {
		h.logError("request log option response mode projection failed", err)
		return requestsOptionsSystemError(fmt.Errorf("project request log response modes: %w", err)), nil
	}

	accounts := make([]generatedadminapi.RequestLogAccountOption, 0, len(options.AccountIDs))
	for _, id := range options.AccountIDs {
		item := generatedadminapi.RequestLogAccountOption{
			Id:    id,
			Label: fmt.Sprintf("#%d", id),
		}
		if account, ok := accountIndex[id]; ok {
			item.Account = &account
			item.Label = account.Name
		}
		accounts = append(accounts, item)
	}

	env := generatedadminapi.RequestsOptionsEnvelope{
		Code: generatedadminapi.RequestsOptionsEnvelopeCodeN0,
		Msg:  generatedadminapi.RequestsOptionsEnvelopeMsgOk,
		Data: generatedadminapi.RequestsOptionsData{
			Accounts:      accounts,
			Models:        options.Models,
			Outcomes:      outcomes,
			ResponseModes: responseModes,
		},
	}
	var body generatedadminapi.RequestsOptionsResponseBody
	if err := body.FromRequestsOptionsEnvelope(env); err != nil {
		return nil, err
	}
	return generatedadminapi.RequestsOptions200JSONResponse(body), nil
}

func (h *RequestLogsHandler) RequestsGet(ctx context.Context, request generatedadminapi.RequestsGetRequestObject) (generatedadminapi.RequestsGetResponseObject, error) {
	if h == nil || h.requests == nil {
		return requestsGetSystemError(errors.New("request logs handler: request service is nil")), nil
	}
	if request.Id <= 0 {
		return requestsGetInvalidFilter("id", "must be positive")
	}

	record, err := h.requests.GetByID(ctx, request.Id)
	if err != nil {
		if errors.Is(err, domain.ErrRequestRecordNotFound) {
			return requestsGetNotFound()
		}
		h.logError("request log detail failed", err)
		return requestsGetSystemError(fmt.Errorf("get request log detail: %w", err)), nil
	}
	accountIndex, err := h.accountIndex(ctx)
	if err != nil {
		h.logError("request log detail account projection failed", err)
		return requestsGetSystemError(fmt.Errorf("load request log detail accounts: %w", err)), nil
	}

	detail, err := requestLogDetailFromDomain(*record, accountIndex)
	if err != nil {
		h.logError("request log detail projection failed", err)
		return requestsGetSystemError(fmt.Errorf("project request log detail: %w", err)), nil
	}

	env := generatedadminapi.RequestDetailEnvelope{
		Code: generatedadminapi.RequestDetailEnvelopeCodeN0,
		Msg:  generatedadminapi.RequestDetailEnvelopeMsgOk,
		Data: generatedadminapi.RequestDetailData{
			Record: detail,
		},
	}
	var body generatedadminapi.RequestDetailResponseBody
	if err := body.FromRequestDetailEnvelope(env); err != nil {
		return nil, err
	}
	return generatedadminapi.RequestsGet200JSONResponse(body), nil
}

func (h *RequestLogsHandler) accountIndex(ctx context.Context) (map[int64]generatedadminapi.AccountListItem, error) {
	if h == nil || h.accounts == nil {
		return nil, errors.New("request logs handler: account provider is nil")
	}
	items, err := h.accounts.ListForAdminAPI(ctx)
	if err != nil {
		return nil, err
	}
	index := make(map[int64]generatedadminapi.AccountListItem, len(items))
	for _, item := range items {
		index[item.ID] = accountListItemFromStore(item)
	}
	return index, nil
}

func (h *RequestLogsHandler) logError(msg string, err error) {
	if h != nil && h.logger != nil {
		h.logger.Error(msg, "error", err)
	}
}

type invalidRequestFilter struct {
	field  string
	reason string
}

func queryParamsFromRequestsList(params generatedadminapi.RequestsListParams) (core.QueryParams, *invalidRequestFilter) {
	out := core.QueryParams{
		Start:    params.Start,
		End:      params.End,
		BeforeID: params.Before,
	}
	if params.Limit != nil {
		if *params.Limit < 1 || *params.Limit > 200 {
			return core.QueryParams{}, &invalidRequestFilter{field: "limit", reason: "must be between 1 and 200"}
		}
		out.Limit = *params.Limit
	}
	if params.AccountId != nil {
		out.AccountIDs = append(out.AccountIDs, (*params.AccountId)...)
	}
	if params.Outcome != nil {
		for _, outcome := range *params.Outcome {
			out.Outcomes = append(out.Outcomes, string(outcome))
		}
	}
	if params.Model != nil {
		out.Models = append(out.Models, (*params.Model)...)
	}
	if params.ResponseMode != nil {
		for _, mode := range *params.ResponseMode {
			out.ResponseModes = append(out.ResponseModes, string(mode))
		}
	}
	out.Search = params.Search
	return out, validateRequestLogQuery(out)
}

func queryParamsFromRequestsOptions(params generatedadminapi.RequestsOptionsParams) (core.QueryParams, *invalidRequestFilter) {
	out := core.QueryParams{
		Start: params.Start,
		End:   params.End,
	}
	if params.AccountId != nil {
		out.AccountIDs = append(out.AccountIDs, (*params.AccountId)...)
	}
	if params.Outcome != nil {
		for _, outcome := range *params.Outcome {
			out.Outcomes = append(out.Outcomes, string(outcome))
		}
	}
	if params.Model != nil {
		out.Models = append(out.Models, (*params.Model)...)
	}
	if params.ResponseMode != nil {
		for _, mode := range *params.ResponseMode {
			out.ResponseModes = append(out.ResponseModes, string(mode))
		}
	}
	out.Search = params.Search
	return out, validateRequestLogQuery(out)
}

func validateRequestLogQuery(params core.QueryParams) *invalidRequestFilter {
	if params.Start != nil && params.End != nil && params.Start.After(*params.End) {
		return &invalidRequestFilter{field: "start", reason: "start must be before or equal to end"}
	}
	if params.BeforeID != nil && *params.BeforeID <= 0 {
		return &invalidRequestFilter{field: "before", reason: "must be positive"}
	}
	if params.Limit < 0 || params.Limit > 200 {
		return &invalidRequestFilter{field: "limit", reason: "must be between 1 and 200"}
	}
	for _, id := range params.AccountIDs {
		if id <= 0 {
			return &invalidRequestFilter{field: "account_id", reason: "must be positive"}
		}
	}
	if len(params.AccountIDs) > requestLogFilterMaxItems {
		return &invalidRequestFilter{field: "account_id", reason: fmt.Sprintf("must contain at most %d values", requestLogFilterMaxItems)}
	}
	for _, outcome := range params.Outcomes {
		if !generatedadminapi.RequestOutcome(outcome).Valid() {
			return &invalidRequestFilter{field: "outcome", reason: "unknown outcome"}
		}
	}
	if len(params.Outcomes) > requestLogFilterMaxItems {
		return &invalidRequestFilter{field: "outcome", reason: fmt.Sprintf("must contain at most %d values", requestLogFilterMaxItems)}
	}
	for _, model := range params.Models {
		if strings.TrimSpace(model) == "" || len([]rune(strings.TrimSpace(model))) > 128 {
			return &invalidRequestFilter{field: "model", reason: "must be 1..128 characters"}
		}
	}
	if len(params.Models) > requestLogFilterMaxItems {
		return &invalidRequestFilter{field: "model", reason: fmt.Sprintf("must contain at most %d values", requestLogFilterMaxItems)}
	}
	for _, mode := range params.ResponseModes {
		if !generatedadminapi.RequestResponseMode(mode).Valid() {
			return &invalidRequestFilter{field: "response_mode", reason: "unknown response mode"}
		}
	}
	if len(params.ResponseModes) > requestLogFilterMaxItems {
		return &invalidRequestFilter{field: "response_mode", reason: fmt.Sprintf("must contain at most %d values", requestLogFilterMaxItems)}
	}
	if params.Search != nil {
		search := strings.TrimSpace(*params.Search)
		if search == "" {
			return &invalidRequestFilter{field: "search", reason: "must be 1..256 characters"}
		}
		if len([]rune(search)) > 256 {
			return &invalidRequestFilter{field: "search", reason: "must be 1..256 characters"}
		}
	}
	return nil
}

func requestLogRowFromDomain(record domain.RequestRecord, accounts map[int64]generatedadminapi.AccountListItem) (generatedadminapi.RequestLogRow, error) {
	outcome := generatedadminapi.RequestOutcome(record.Outcome)
	if !outcome.Valid() {
		return generatedadminapi.RequestLogRow{}, fmt.Errorf("request record %d has invalid outcome %q", record.ID, record.Outcome)
	}
	responseMode := generatedadminapi.RequestResponseMode(record.ResponseMode)
	if !responseMode.Valid() {
		return generatedadminapi.RequestLogRow{}, fmt.Errorf("request record %d has invalid response_mode %q", record.ID, record.ResponseMode)
	}
	row := generatedadminapi.RequestLogRow{
		Id:                record.ID,
		RequestId:         record.RequestID,
		CreatedAt:         record.CreatedAt,
		ClientIp:          record.ClientIP,
		UpstreamAccountId: record.UpstreamAccountID,
		SessionKey:        record.SessionKey,
		Method:            record.Method,
		Path:              record.Path,
		StatusCode:        record.StatusCode,
		LatencyMs:         record.LatencyMs,
		TtftMs:            record.TTFTMs,
		Outcome:           outcome,
		ErrorCode:         record.ErrorCode,
		Model:             record.Model,
		ModelParams:       jsonMapPtr(record.ModelParams),
		ResponseMode:      responseMode,
		RouterMetadata:    jsonMapPtr(record.RouterMetadata),
		TokenUsage:        jsonMapPtr(record.TokenUsage),
	}
	if record.UpstreamAccountID != nil {
		if account, ok := accounts[*record.UpstreamAccountID]; ok {
			row.Account = &account
		}
	}
	return row, nil
}

func requestLogDetailFromDomain(record domain.RequestRecord, accounts map[int64]generatedadminapi.AccountListItem) (generatedadminapi.RequestLogDetail, error) {
	row, err := requestLogRowFromDomain(record, accounts)
	if err != nil {
		return generatedadminapi.RequestLogDetail{}, err
	}
	return generatedadminapi.RequestLogDetail{
		Id:                   row.Id,
		RequestId:            row.RequestId,
		CreatedAt:            row.CreatedAt,
		ClientIp:             row.ClientIp,
		UpstreamAccountId:    row.UpstreamAccountId,
		Account:              row.Account,
		SessionKey:           row.SessionKey,
		Method:               row.Method,
		Path:                 row.Path,
		StatusCode:           row.StatusCode,
		LatencyMs:            row.LatencyMs,
		TtftMs:               row.TtftMs,
		Outcome:              row.Outcome,
		ErrorCode:            row.ErrorCode,
		Model:                row.Model,
		ModelParams:          row.ModelParams,
		ResponseMode:         row.ResponseMode,
		RouterMetadata:       row.RouterMetadata,
		TokenUsage:           row.TokenUsage,
		ClientRequestBody:    redactedRequestBody(record.ClientRequestBody),
		UpstreamRequestBody:  redactedRequestBody(record.UpstreamRequestBody),
		UpstreamResponseBody: redactedUpstreamResponseBody(record.UpstreamResponseBody),
	}, nil
}

func redactedRequestBody(value *string) *string {
	if value == nil {
		return nil
	}
	redacted := core.RedactCapturedBody([]byte(*value))
	return &redacted
}

func redactedUpstreamResponseBody(value *string) *string {
	if value == nil {
		return nil
	}
	aggregate, ok, err := openai.CollectResponsesSSEBytes([]byte(*value), false)
	if err == nil && ok {
		redacted := core.RedactCapturedBody(aggregate)
		return &redacted
	}
	return redactedRequestBody(value)
}

func jsonMapPtr(value domain.JSONMap) *map[string]interface{} {
	if value == nil {
		return nil
	}
	out := map[string]interface{}(value)
	return &out
}

func accountListItemFromStore(item store.AccountListItem) generatedadminapi.AccountListItem {
	createdAt := item.CreatedAt
	updatedAt := item.UpdatedAt
	out := generatedadminapi.AccountListItem{
		Id:               item.ID,
		Name:             item.Name,
		Provider:         item.Provider,
		BaseUrl:          item.BaseURL,
		AuthMethod:       generatedadminapi.AccountListItemAuthMethod(item.AuthMethod),
		Status:           generatedadminapi.AccountListItemStatus(item.Status),
		CreatedAt:        &createdAt,
		UpdatedAt:        &updatedAt,
		Email:            item.Email,
		PlanType:         item.PlanType,
		ChatgptAccountId: item.ChatGPTAccountID,
		LastRefresh:      item.LastRefresh,
		AccessExpiresAt:  item.AccessExpiresAt,
	}
	if item.PlanType != nil {
		label := PlanTypeLabel(*item.PlanType)
		out.PlanTypeLabel = &label
	}
	return out
}

func requestOutcomes(values []string) ([]generatedadminapi.RequestOutcome, error) {
	out := make([]generatedadminapi.RequestOutcome, 0, len(values))
	for _, value := range values {
		outcome := generatedadminapi.RequestOutcome(value)
		if !outcome.Valid() {
			return nil, fmt.Errorf("invalid outcome option %q", value)
		}
		out = append(out, outcome)
	}
	return out, nil
}

func requestResponseModes(values []string) ([]generatedadminapi.RequestResponseMode, error) {
	out := make([]generatedadminapi.RequestResponseMode, 0, len(values))
	for _, value := range values {
		mode := generatedadminapi.RequestResponseMode(value)
		if !mode.Valid() {
			return nil, fmt.Errorf("invalid response_mode option %q", value)
		}
		out = append(out, mode)
	}
	return out, nil
}

func invalidRequestFilterEnvelope(field, reason string) generatedadminapi.InvalidRequestFilterEnvelope {
	env := generatedadminapi.InvalidRequestFilterEnvelope{
		Code: generatedadminapi.N1006,
		Msg:  generatedadminapi.InvalidRequestFilter,
	}
	if field != "" {
		env.Data.Field = &field
	}
	if reason != "" {
		env.Data.Reason = &reason
	}
	return env
}

func requestsListInvalidFilter(field, reason string) (generatedadminapi.RequestsListResponseObject, error) {
	var body generatedadminapi.RequestsListResponseBody
	if err := body.FromInvalidRequestFilterEnvelope(invalidRequestFilterEnvelope(field, reason)); err != nil {
		return nil, err
	}
	return generatedadminapi.RequestsList200JSONResponse(body), nil
}

func requestsOptionsInvalidFilter(field, reason string) (generatedadminapi.RequestsOptionsResponseObject, error) {
	var body generatedadminapi.RequestsOptionsResponseBody
	if err := body.FromInvalidRequestFilterEnvelope(invalidRequestFilterEnvelope(field, reason)); err != nil {
		return nil, err
	}
	return generatedadminapi.RequestsOptions200JSONResponse(body), nil
}

func requestsGetInvalidFilter(field, reason string) (generatedadminapi.RequestsGetResponseObject, error) {
	var body generatedadminapi.RequestDetailResponseBody
	if err := body.FromInvalidRequestFilterEnvelope(invalidRequestFilterEnvelope(field, reason)); err != nil {
		return nil, err
	}
	return generatedadminapi.RequestsGet200JSONResponse(body), nil
}

func requestsGetNotFound() (generatedadminapi.RequestsGetResponseObject, error) {
	env := generatedadminapi.RequestRecordNotFoundEnvelope{
		Code: generatedadminapi.N1005,
		Msg:  generatedadminapi.RequestRecordNotFound,
		Data: map[string]interface{}{},
	}
	var body generatedadminapi.RequestDetailResponseBody
	if err := body.FromRequestRecordNotFoundEnvelope(env); err != nil {
		return nil, err
	}
	return generatedadminapi.RequestsGet200JSONResponse(body), nil
}

func requestLogsSystemEnvelope(err error) generatedadminapi.SystemErrorJSONResponse {
	return generatedadminapi.SystemErrorJSONResponse{
		Code: generatedadminapi.EnvelopeSystemErrorCode(errcode.InternalError),
		Msg:  errcode.Symbol(errcode.InternalError),
		Data: map[string]interface{}{},
	}
}

func requestsListSystemError(err error) generatedadminapi.RequestsListResponseObject {
	_ = err
	return generatedadminapi.RequestsList500JSONResponse{SystemErrorJSONResponse: requestLogsSystemEnvelope(err)}
}

func requestsOptionsSystemError(err error) generatedadminapi.RequestsOptionsResponseObject {
	_ = err
	return generatedadminapi.RequestsOptions500JSONResponse{SystemErrorJSONResponse: requestLogsSystemEnvelope(err)}
}

func requestsGetSystemError(err error) generatedadminapi.RequestsGetResponseObject {
	_ = err
	return generatedadminapi.RequestsGet500JSONResponse{SystemErrorJSONResponse: requestLogsSystemEnvelope(err)}
}

func (h *RequestLogsHandler) AccountsImportAuthJSON(context.Context, generatedadminapi.AccountsImportAuthJSONRequestObject) (generatedadminapi.AccountsImportAuthJSONResponseObject, error) {
	return nil, errors.New("adminapi.RequestLogsHandler.AccountsImportAuthJSON: not implemented")
}

func (h *RequestLogsHandler) AccountsExportAuthJSON(context.Context, generatedadminapi.AccountsExportAuthJSONRequestObject) (generatedadminapi.AccountsExportAuthJSONResponseObject, error) {
	return nil, errors.New("adminapi.RequestLogsHandler.AccountsExportAuthJSON: not implemented")
}

func (h *RequestLogsHandler) OauthBrowserManualCallback(context.Context, generatedadminapi.OauthBrowserManualCallbackRequestObject) (generatedadminapi.OauthBrowserManualCallbackResponseObject, error) {
	return nil, errors.New("adminapi.RequestLogsHandler.OauthBrowserManualCallback: not implemented")
}

func (h *RequestLogsHandler) OauthBrowserStart(context.Context, generatedadminapi.OauthBrowserStartRequestObject) (generatedadminapi.OauthBrowserStartResponseObject, error) {
	return nil, errors.New("adminapi.RequestLogsHandler.OauthBrowserStart: not implemented")
}

func (h *RequestLogsHandler) OauthCancel(context.Context, generatedadminapi.OauthCancelRequestObject) (generatedadminapi.OauthCancelResponseObject, error) {
	return nil, errors.New("adminapi.RequestLogsHandler.OauthCancel: not implemented")
}

func (h *RequestLogsHandler) OauthDeviceStart(context.Context, generatedadminapi.OauthDeviceStartRequestObject) (generatedadminapi.OauthDeviceStartResponseObject, error) {
	return nil, errors.New("adminapi.RequestLogsHandler.OauthDeviceStart: not implemented")
}

func (h *RequestLogsHandler) OauthFlowStatus(context.Context, generatedadminapi.OauthFlowStatusRequestObject) (generatedadminapi.OauthFlowStatusResponseObject, error) {
	return nil, errors.New("adminapi.RequestLogsHandler.OauthFlowStatus: not implemented")
}

func (h *RequestLogsHandler) PlaygroundRun(context.Context, generatedadminapi.PlaygroundRunRequestObject) (generatedadminapi.PlaygroundRunResponseObject, error) {
	return nil, errors.New("adminapi.RequestLogsHandler.PlaygroundRun: not implemented")
}

func (h *RequestLogsHandler) DashboardGet(context.Context, generatedadminapi.DashboardGetRequestObject) (generatedadminapi.DashboardGetResponseObject, error) {
	return nil, errors.New("adminapi.RequestLogsHandler.DashboardGet: not implemented")
}

func (h *RequestLogsHandler) UsageGet(context.Context, generatedadminapi.UsageGetRequestObject) (generatedadminapi.UsageGetResponseObject, error) {
	return nil, errors.New("adminapi.RequestLogsHandler.UsageGet: not implemented")
}

func (h *RequestLogsHandler) SettingsGet(context.Context, generatedadminapi.SettingsGetRequestObject) (generatedadminapi.SettingsGetResponseObject, error) {
	return nil, errors.New("adminapi.RequestLogsHandler.SettingsGet: not implemented")
}

func (h *RequestLogsHandler) SettingsUpdate(context.Context, generatedadminapi.SettingsUpdateRequestObject) (generatedadminapi.SettingsUpdateResponseObject, error) {
	return nil, errors.New("adminapi.RequestLogsHandler.SettingsUpdate: not implemented")
}

func (h *RequestLogsHandler) AccountModelsList(ctx context.Context, request generatedadminapi.AccountModelsListRequestObject) (generatedadminapi.AccountModelsListResponseObject, error) {
	return generatedadminapi.AccountModelsList500JSONResponse{
		SystemErrorJSONResponse: generatedadminapi.SystemErrorJSONResponse{
			Code: generatedadminapi.N1901,
			Msg:  "internal_error",
			Data: map[string]any{},
		},
	}, nil
}

func (h *RequestLogsHandler) AccountModelAdd(ctx context.Context, request generatedadminapi.AccountModelAddRequestObject) (generatedadminapi.AccountModelAddResponseObject, error) {
	return generatedadminapi.AccountModelAdd500JSONResponse{
		SystemErrorJSONResponse: generatedadminapi.SystemErrorJSONResponse{
			Code: generatedadminapi.N1901,
			Msg:  "internal_error",
			Data: map[string]any{},
		},
	}, nil
}

func (h *RequestLogsHandler) AccountModelRefresh(ctx context.Context, request generatedadminapi.AccountModelRefreshRequestObject) (generatedadminapi.AccountModelRefreshResponseObject, error) {
	return generatedadminapi.AccountModelRefresh500JSONResponse{
		SystemErrorJSONResponse: generatedadminapi.SystemErrorJSONResponse{
			Code: generatedadminapi.N1901,
			Msg:  "internal_error",
			Data: map[string]any{},
		},
	}, nil
}

func (h *RequestLogsHandler) AccountModelRemove(ctx context.Context, request generatedadminapi.AccountModelRemoveRequestObject) (generatedadminapi.AccountModelRemoveResponseObject, error) {
	return generatedadminapi.AccountModelRemove500JSONResponse{
		SystemErrorJSONResponse: generatedadminapi.SystemErrorJSONResponse{
			Code: generatedadminapi.N1901,
			Msg:  "internal_error",
			Data: map[string]any{},
		},
	}, nil
}

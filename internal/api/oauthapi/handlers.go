package oauthapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/user/one-llm-router/internal/api/errcode"
	"github.com/user/one-llm-router/internal/domain"
	generatedadminapi "github.com/user/one-llm-router/internal/generated/adminapi"
	"github.com/user/one-llm-router/internal/oauth"
	"github.com/user/one-llm-router/internal/presentation"
	"github.com/user/one-llm-router/internal/requestid"
)

type coordinator interface {
	StartBrowser(ctx context.Context, provider string) (*oauth.Flow, error)
	StartDevice(ctx context.Context, provider string) (*oauth.Flow, error)
	CancelWithContext(ctx context.Context, flowID string) error
	BrowserCallbackSnapshot(flowID string) (oauth.BrowserCallbackSnapshot, bool)
	CurrentFlow() *oauth.Flow
	GetFlow() (oauth.FlowSnapshot, error)
	Now() time.Time
	ConsumeCode(ctx context.Context, flowID, code, state string, rail oauth.Rail) (*domain.UpstreamAccount, error)
	CancelFromProviderError(ctx context.Context, flowID string, rail oauth.Rail, providerError string) error
	WinnerRail(flowID string) (oauth.Rail, bool)
}

// Handler implements the shipped browser-flow handlers plus explicit
// placeholders for later OAuth tasks on the same generated strict-handler
// surface.
type Handler struct {
	coord  coordinator
	logger *slog.Logger
}

var _ generatedadminapi.StrictServerInterface = (*Handler)(nil)

func NewHandler(coord coordinator) *Handler {
	return NewHandlerWithDeps(coord, nil)
}

func NewHandlerWithLogger(coord coordinator, logger *slog.Logger) *Handler {
	return NewHandlerWithDeps(coord, logger)
}

func NewHandlerWithDeps(coord coordinator, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		coord:  coord,
		logger: logger,
	}
}

func (h *Handler) OauthBrowserStart(ctx context.Context, request generatedadminapi.OauthBrowserStartRequestObject) (generatedadminapi.OauthBrowserStartResponseObject, error) {
	if h == nil || h.coord == nil {
		return oauthBrowserStartSystemError(errors.New("oauthapi.Handler.OauthBrowserStart: coordinator is nil"))
	}
	if request.Body == nil || request.Body.Provider != generatedadminapi.OAuthStartRequestProviderOpenai {
		return oauthBrowserStartInvalidProvider()
	}

	flow, err := h.coord.StartBrowser(ctx, string(request.Body.Provider))
	switch {
	case err == nil:
		return oauthBrowserStartSuccess(flow)
	case errors.Is(err, oauth.ErrFlowInProgress):
		info, ok := oauth.FlowInProgressInfo(err)
		if !ok {
			return oauthBrowserStartSystemError(fmt.Errorf("oauthapi.Handler.OauthBrowserStart: ErrFlowInProgress missing metadata: %w", err))
		}
		return oauthBrowserStartInProgress(info)
	default:
		return oauthBrowserStartSystemError(fmt.Errorf("oauthapi.Handler.OauthBrowserStart: start browser flow: %w", err))
	}
}

func (h *Handler) OauthBrowserManualCallback(ctx context.Context, request generatedadminapi.OauthBrowserManualCallbackRequestObject) (generatedadminapi.OauthBrowserManualCallbackResponseObject, error) {
	if h == nil || h.coord == nil {
		return manualCallbackSystemError(errors.New("oauthapi.Handler.OauthBrowserManualCallback: coordinator is nil"))
	}

	rawURL := ""
	if request.Body != nil {
		rawURL = request.Body.CallbackUrl
	}

	parsed, invalidReason, ok := parseManualCallbackURL(rawURL)
	if !ok {
		return manualCallbackInvalidURL(invalidReason)
	}

	query := parsed.Query()
	state := query.Get("state")
	providerError := query.Get("error")
	code := query.Get("code")
	if code == "" && providerError == "" {
		return manualCallbackInvalidURL(generatedadminapi.MissingCodeAndError)
	}

	flow, ok := h.coord.BrowserCallbackSnapshot("")
	if !ok {
		return manualCallbackNoFlow()
	}
	if !oauth.CompareStatesConstantTime(flow.State, state) {
		h.logRailRejected(ctx, flow.FlowID, "oauth_state_mismatch")
		return manualCallbackStateMismatch()
	}
	if h.coord.Now().After(flow.ExpiresAt) {
		return manualCallbackFlowExpired()
	}

	if providerError != "" {
		if flow.Consumed {
			return h.manualCallbackConsumed(flow.FlowID)
		}
		if providerError == "access_denied" {
			err := h.coord.CancelFromProviderError(ctx, flow.FlowID, oauth.RailManualPaste, providerError)
			switch {
			case err == nil:
				return manualCallbackCancelled()
			case errors.Is(err, oauth.ErrAlreadyConsumed):
				return h.manualCallbackConsumed(flow.FlowID)
			case errors.Is(err, oauth.ErrFlowExpired):
				return manualCallbackFlowExpired()
			case errors.Is(err, oauth.ErrFlowNotFound):
				return manualCallbackNoFlow()
			default:
				return manualCallbackSystemError(fmt.Errorf("oauthapi.Handler.OauthBrowserManualCallback: cancel from provider error: %w", err))
			}
		}

		h.logRailRejected(ctx, flow.FlowID, providerError)
		return manualCallbackUpstreamError(providerError, stringPointer(query.Get("error_description")), nil)
	}

	account, err := h.coord.ConsumeCode(ctx, flow.FlowID, code, state, oauth.RailManualPaste)
	switch {
	case err == nil:
		return manualCallbackSuccess(account)
	case errors.Is(err, oauth.ErrStateMismatch):
		return manualCallbackStateMismatch()
	case errors.Is(err, oauth.ErrFlowNotFound):
		return manualCallbackNoFlow()
	case errors.Is(err, oauth.ErrFlowExpired):
		return manualCallbackFlowExpired()
	case errors.Is(err, oauth.ErrAlreadyConsumed):
		return h.manualCallbackConsumed(flow.FlowID)
	default:
		if exchange, ok := exchangeErrorDetails(err); ok {
			if exchange.code == "invalid_grant" {
				return manualCallbackInvalidGrant(stringPointer(exchange.message))
			}
			return manualCallbackUpstreamError(exchange.code, stringPointer(exchange.message), intPointerIfPositive(exchange.httpStatus))
		}
		return manualCallbackSystemError(fmt.Errorf("oauthapi.Handler.OauthBrowserManualCallback: consume code: %w", err))
	}
}

func (h *Handler) OauthCancel(ctx context.Context, request generatedadminapi.OauthCancelRequestObject) (generatedadminapi.OauthCancelResponseObject, error) {
	if h == nil || h.coord == nil {
		return oauthCancelSystemError(errors.New("oauthapi.Handler.OauthCancel: coordinator is nil"))
	}

	requestedFlowID := ""
	if request.Body != nil {
		requestedFlowID = request.Body.FlowId
	}

	current := h.coord.CurrentFlow()
	if current == nil {
		return oauthCancelIdle()
	}
	if requestedFlowID != current.ID {
		return oauthCancelMismatch(current.ID)
	}
	if err := h.coord.CancelWithContext(ctx, current.ID); err != nil && !errors.Is(err, oauth.ErrFlowNotFound) {
		return oauthCancelSystemError(fmt.Errorf("oauthapi.Handler.OauthCancel: cancel flow: %w", err))
	}
	return oauthCancelIdle()
}

func (h *Handler) OauthDeviceStart(ctx context.Context, request generatedadminapi.OauthDeviceStartRequestObject) (generatedadminapi.OauthDeviceStartResponseObject, error) {
	if h == nil || h.coord == nil {
		return oauthDeviceStartSystemError(errors.New("oauthapi.Handler.OauthDeviceStart: coordinator is nil"))
	}
	if request.Body == nil || request.Body.Provider != generatedadminapi.OAuthStartRequestProviderOpenai {
		return oauthDeviceStartInvalidProvider()
	}

	flow, err := h.coord.StartDevice(ctx, string(request.Body.Provider))
	switch {
	case err == nil:
		return oauthDeviceStartSuccess(flow)
	case errors.Is(err, oauth.ErrFlowInProgress):
		info, ok := oauth.FlowInProgressInfo(err)
		if !ok {
			return oauthDeviceStartSystemError(fmt.Errorf("oauthapi.Handler.OauthDeviceStart: ErrFlowInProgress missing metadata: %w", err))
		}
		return oauthDeviceStartInProgress(info)
	default:
		if exchange, ok := exchangeErrorDetails(err); ok {
			if exchange.httpStatus == http.StatusNotFound || exchange.code == "device_auth_unavailable" {
				return oauthDeviceStartUnavailable()
			}
			return oauthDeviceStartUpstreamError(exchange.code, stringPointer(exchange.message), intPointerIfPositive(exchange.httpStatus))
		}
		return oauthDeviceStartSystemError(fmt.Errorf("oauthapi.Handler.OauthDeviceStart: start device flow: %w", err))
	}
}

func (h *Handler) OauthFlowStatus(_ context.Context, _ generatedadminapi.OauthFlowStatusRequestObject) (generatedadminapi.OauthFlowStatusResponseObject, error) {
	if h == nil || h.coord == nil {
		return oauthFlowStatusSystemError(errors.New("oauthapi.Handler.OauthFlowStatus: coordinator is nil"))
	}

	snapshot, err := h.coord.GetFlow()
	if err != nil {
		return oauthFlowStatusSystemError(fmt.Errorf("oauthapi.Handler.OauthFlowStatus: get flow: %w", err))
	}
	return oauthFlowStatusResponse(snapshot)
}

func (h *Handler) AccountsImportAuthJSON(context.Context, generatedadminapi.AccountsImportAuthJSONRequestObject) (generatedadminapi.AccountsImportAuthJSONResponseObject, error) {
	return nil, errors.New("oauthapi.Handler.AccountsImportAuthJSON: not implemented")
}

func (h *Handler) AccountsExportAuthJSON(context.Context, generatedadminapi.AccountsExportAuthJSONRequestObject) (generatedadminapi.AccountsExportAuthJSONResponseObject, error) {
	return nil, errors.New("oauthapi.Handler.AccountsExportAuthJSON: not implemented")
}

func (h *Handler) PlaygroundRun(context.Context, generatedadminapi.PlaygroundRunRequestObject) (generatedadminapi.PlaygroundRunResponseObject, error) {
	return nil, errors.New("oauthapi.Handler.PlaygroundRun: not implemented")
}

func (h *Handler) DashboardGet(context.Context, generatedadminapi.DashboardGetRequestObject) (generatedadminapi.DashboardGetResponseObject, error) {
	return nil, errors.New("oauthapi.Handler.DashboardGet: not implemented")
}

func (h *Handler) UsageGet(context.Context, generatedadminapi.UsageGetRequestObject) (generatedadminapi.UsageGetResponseObject, error) {
	return nil, errors.New("oauthapi.Handler.UsageGet: not implemented")
}

func (h *Handler) RequestsList(context.Context, generatedadminapi.RequestsListRequestObject) (generatedadminapi.RequestsListResponseObject, error) {
	return nil, errors.New("oauthapi.Handler.RequestsList: not implemented")
}

func (h *Handler) RequestsOptions(context.Context, generatedadminapi.RequestsOptionsRequestObject) (generatedadminapi.RequestsOptionsResponseObject, error) {
	return nil, errors.New("oauthapi.Handler.RequestsOptions: not implemented")
}

func (h *Handler) RequestsGet(context.Context, generatedadminapi.RequestsGetRequestObject) (generatedadminapi.RequestsGetResponseObject, error) {
	return nil, errors.New("oauthapi.Handler.RequestsGet: not implemented")
}

func (h *Handler) SettingsGet(context.Context, generatedadminapi.SettingsGetRequestObject) (generatedadminapi.SettingsGetResponseObject, error) {
	return nil, errors.New("oauthapi.Handler.SettingsGet: not implemented")
}

func (h *Handler) SettingsUpdate(context.Context, generatedadminapi.SettingsUpdateRequestObject) (generatedadminapi.SettingsUpdateResponseObject, error) {
	return nil, errors.New("oauthapi.Handler.SettingsUpdate: not implemented")
}

func oauthBrowserStartSuccess(flow *oauth.Flow) (generatedadminapi.OauthBrowserStartResponseObject, error) {
	if flow == nil {
		return nil, errors.New("oauthBrowserStartSuccess: flow is nil")
	}

	env := generatedadminapi.BrowserStartEnvelope{
		Code: generatedadminapi.BrowserStartEnvelopeCodeN0,
		Msg:  generatedadminapi.BrowserStartEnvelopeMsgOk,
	}
	env.Data.AuthorizeUrl = flow.AuthorizeURL()
	env.Data.CallbackUrl = flow.CallbackURL()
	env.Data.ExpiresAt = flow.ExpiresAt
	env.Data.FlowId = flow.ID
	env.Data.ListenerBound = flow.ListenerBound
	env.Data.Method = generatedadminapi.BrowserStartEnvelopeDataMethodBrowser

	var body generatedadminapi.BrowserStartResponseBody
	if err := body.FromBrowserStartEnvelope(env); err != nil {
		return nil, fmt.Errorf("oauthBrowserStartSuccess: encode response: %w", err)
	}
	return generatedadminapi.OauthBrowserStart200JSONResponse(body), nil
}

func oauthBrowserStartInProgress(info oauth.FlowAlreadyInProgressInfo) (generatedadminapi.OauthBrowserStartResponseObject, error) {
	env := generatedadminapi.FlowInProgressEnvelope{
		Code: generatedadminapi.N3001,
		Msg:  generatedadminapi.OauthFlowInProgress,
	}
	env.Data.CreatedAt = info.CreatedAt
	env.Data.ExpiresAt = info.ExpiresAt
	env.Data.FlowId = info.FlowID
	env.Data.Method = generatedadminapi.OAuthMethod(info.Method)

	var body generatedadminapi.BrowserStartResponseBody
	if err := body.FromFlowInProgressEnvelope(env); err != nil {
		return nil, fmt.Errorf("oauthBrowserStartInProgress: encode response: %w", err)
	}
	return generatedadminapi.OauthBrowserStart200JSONResponse(body), nil
}

func oauthBrowserStartInvalidProvider() (generatedadminapi.OauthBrowserStartResponseObject, error) {
	env := generatedadminapi.InvalidOAuthProviderEnvelope{
		Code: generatedadminapi.N3002,
		Msg:  generatedadminapi.InvalidOauthProvider,
	}
	env.Data.Field = generatedadminapi.Provider

	var body generatedadminapi.BrowserStartResponseBody
	if err := body.FromInvalidOAuthProviderEnvelope(env); err != nil {
		return nil, fmt.Errorf("oauthBrowserStartInvalidProvider: encode response: %w", err)
	}
	return generatedadminapi.OauthBrowserStart200JSONResponse(body), nil
}

func oauthBrowserStartSystemError(err error) (generatedadminapi.OauthBrowserStartResponseObject, error) {
	return generatedadminapi.OauthBrowserStart500JSONResponse{
		SystemErrorJSONResponse: oauthSystemErrorEnvelope(err),
	}, nil
}

func manualCallbackSystemError(err error) (generatedadminapi.OauthBrowserManualCallbackResponseObject, error) {
	return generatedadminapi.OauthBrowserManualCallback500JSONResponse{
		SystemErrorJSONResponse: oauthSystemErrorEnvelope(err),
	}, nil
}

func oauthCancelSystemError(err error) (generatedadminapi.OauthCancelResponseObject, error) {
	return generatedadminapi.OauthCancel500JSONResponse{
		SystemErrorJSONResponse: oauthSystemErrorEnvelope(err),
	}, nil
}

func oauthDeviceStartSystemError(err error) (generatedadminapi.OauthDeviceStartResponseObject, error) {
	return generatedadminapi.OauthDeviceStart500JSONResponse{
		SystemErrorJSONResponse: oauthSystemErrorEnvelope(err),
	}, nil
}

func oauthFlowStatusSystemError(err error) (generatedadminapi.OauthFlowStatusResponseObject, error) {
	return generatedadminapi.OauthFlowStatus500JSONResponse{
		SystemErrorJSONResponse: oauthSystemErrorEnvelope(err),
	}, nil
}

func oauthSystemErrorEnvelope(err error) generatedadminapi.SystemErrorJSONResponse {
	code := oauthSystemErrorCode(err)
	return generatedadminapi.SystemErrorJSONResponse{
		Code: oauthSystemErrorEnum(code),
		Data: map[string]any{},
		Msg:  errcode.Symbol(code),
	}
}

func oauthSystemErrorCode(err error) int {
	var storeErr *oauth.StoreError
	if errors.As(err, &storeErr) {
		return errcode.OAuthStoreFailed
	}
	return errcode.OAuthInternalError
}

func oauthSystemErrorEnum(code int) generatedadminapi.EnvelopeSystemErrorCode {
	switch code {
	case errcode.OAuthInternalError:
		return generatedadminapi.N3900
	case errcode.OAuthStoreFailed:
		return generatedadminapi.N3901
	default:
		panic(fmt.Sprintf("oauthSystemErrorEnum: unsupported code %d", code))
	}
}

func oauthFlowStatusResponse(snapshot oauth.FlowSnapshot) (generatedadminapi.OauthFlowStatusResponseObject, error) {
	env := generatedadminapi.FlowStatusEnvelope{
		Code: generatedadminapi.FlowStatusEnvelopeCodeN0,
		Msg:  generatedadminapi.FlowStatusEnvelopeMsgOk,
	}

	switch snapshot.Status {
	case oauth.FlowStatusIdle:
		if err := env.Data.FromFlowStatusIdle(generatedadminapi.FlowStatusIdle{
			Status: generatedadminapi.FlowStatusIdleStatusIdle,
		}); err != nil {
			return nil, fmt.Errorf("oauthFlowStatusResponse: encode idle: %w", err)
		}
	case oauth.FlowStatusPending:
		if snapshot.Method == oauth.FlowDevice {
			body := generatedadminapi.FlowStatusPendingDevice{
				CreatedAt:       snapshot.CreatedAt,
				ExpiresAt:       snapshot.ExpiresAt,
				FlowId:          generatedadminapi.FlowID(snapshot.FlowID),
				Method:          generatedadminapi.FlowStatusPendingDeviceMethodDevice,
				Status:          generatedadminapi.FlowStatusPendingDeviceStatusPending,
				UserCode:        snapshot.UserCode,
				VerificationUrl: snapshot.VerificationURL,
			}
			if err := env.Data.FromFlowStatusPendingDevice(body); err != nil {
				return nil, fmt.Errorf("oauthFlowStatusResponse: encode pending device: %w", err)
			}
			break
		}

		body := generatedadminapi.FlowStatusPendingBrowser{
			CreatedAt:     snapshot.CreatedAt,
			ExpiresAt:     snapshot.ExpiresAt,
			FlowId:        generatedadminapi.FlowID(snapshot.FlowID),
			ListenerBound: snapshot.ListenerBound,
			Method:        generatedadminapi.FlowStatusPendingBrowserMethodBrowser,
			Status:        generatedadminapi.FlowStatusPendingBrowserStatusPending,
		}
		if err := env.Data.FromFlowStatusPendingBrowser(body); err != nil {
			return nil, fmt.Errorf("oauthFlowStatusResponse: encode pending browser: %w", err)
		}
	case oauth.FlowStatusSuccess:
		item, err := accountListItemFromDomain(snapshot.Account)
		if err != nil {
			return nil, fmt.Errorf("oauthFlowStatusResponse: map account: %w", err)
		}
		body := generatedadminapi.FlowStatusSuccess{
			Account: item,
			FlowId:  generatedadminapi.FlowID(snapshot.FlowID),
			Method:  generatedadminapi.OAuthMethod(snapshot.Method),
			Status:  generatedadminapi.FlowStatusSuccessStatusSuccess,
		}
		if snapshot.Method == oauth.FlowBrowser {
			rail := railNameFromOAuth(snapshot.Rail)
			body.Rail = &rail
		}
		if err := env.Data.FromFlowStatusSuccess(body); err != nil {
			return nil, fmt.Errorf("oauthFlowStatusResponse: encode success: %w", err)
		}
	case oauth.FlowStatusError:
		if snapshot.Error == nil {
			return nil, errors.New("oauthFlowStatusResponse: error snapshot missing error payload")
		}
		body := generatedadminapi.FlowStatusError{
			FlowId: generatedadminapi.FlowID(snapshot.FlowID),
			Method: generatedadminapi.OAuthMethod(snapshot.Method),
			Status: generatedadminapi.FlowStatusErrorStatusError,
		}
		body.Error.Code = snapshot.Error.Code
		body.Error.Message = snapshot.Error.Message
		if err := env.Data.FromFlowStatusError(body); err != nil {
			return nil, fmt.Errorf("oauthFlowStatusResponse: encode error: %w", err)
		}
	default:
		return nil, fmt.Errorf("oauthFlowStatusResponse: unsupported flow status %q", snapshot.Status)
	}

	return generatedadminapi.OauthFlowStatus200JSONResponse(env), nil
}

func oauthDeviceStartSuccess(flow *oauth.Flow) (generatedadminapi.OauthDeviceStartResponseObject, error) {
	if flow == nil {
		return nil, errors.New("oauthDeviceStartSuccess: flow is nil")
	}

	env := generatedadminapi.DeviceStartEnvelope{
		Code: generatedadminapi.DeviceStartEnvelopeCodeN0,
		Msg:  generatedadminapi.DeviceStartEnvelopeMsgOk,
	}
	env.Data.FlowId = generatedadminapi.FlowID(flow.ID)
	env.Data.UserCode = flow.UserCode
	env.Data.VerificationUrl = flow.VerificationURL
	env.Data.IntervalSeconds = int(flow.PollInterval / time.Second)
	env.Data.ExpiresAt = flow.ExpiresAt
	env.Data.Method = generatedadminapi.DeviceStartEnvelopeDataMethodDevice

	var body generatedadminapi.DeviceStartResponseBody
	if err := body.FromDeviceStartEnvelope(env); err != nil {
		return nil, fmt.Errorf("oauthDeviceStartSuccess: encode response: %w", err)
	}
	return generatedadminapi.OauthDeviceStart200JSONResponse(body), nil
}

func oauthDeviceStartInProgress(info oauth.FlowAlreadyInProgressInfo) (generatedadminapi.OauthDeviceStartResponseObject, error) {
	env := generatedadminapi.FlowInProgressEnvelope{
		Code: generatedadminapi.N3001,
		Msg:  generatedadminapi.OauthFlowInProgress,
	}
	env.Data.CreatedAt = info.CreatedAt
	env.Data.ExpiresAt = info.ExpiresAt
	env.Data.FlowId = info.FlowID
	env.Data.Method = generatedadminapi.OAuthMethod(info.Method)

	var body generatedadminapi.DeviceStartResponseBody
	if err := body.FromFlowInProgressEnvelope(env); err != nil {
		return nil, fmt.Errorf("oauthDeviceStartInProgress: encode response: %w", err)
	}
	return generatedadminapi.OauthDeviceStart200JSONResponse(body), nil
}

func oauthDeviceStartInvalidProvider() (generatedadminapi.OauthDeviceStartResponseObject, error) {
	env := generatedadminapi.InvalidOAuthProviderEnvelope{
		Code: generatedadminapi.N3002,
		Msg:  generatedadminapi.InvalidOauthProvider,
	}
	env.Data.Field = generatedadminapi.Provider

	var body generatedadminapi.DeviceStartResponseBody
	if err := body.FromInvalidOAuthProviderEnvelope(env); err != nil {
		return nil, fmt.Errorf("oauthDeviceStartInvalidProvider: encode response: %w", err)
	}
	return generatedadminapi.OauthDeviceStart200JSONResponse(body), nil
}

func oauthDeviceStartUnavailable() (generatedadminapi.OauthDeviceStartResponseObject, error) {
	env := generatedadminapi.DeviceAuthUnavailableEnvelope{
		Code: generatedadminapi.N3015,
		Msg:  generatedadminapi.DeviceAuthUnavailable,
	}
	env.Data.Provider = generatedadminapi.DeviceAuthUnavailableEnvelopeDataProviderOpenai

	var body generatedadminapi.DeviceStartResponseBody
	if err := body.FromDeviceAuthUnavailableEnvelope(env); err != nil {
		return nil, fmt.Errorf("oauthDeviceStartUnavailable: encode response: %w", err)
	}
	return generatedadminapi.OauthDeviceStart200JSONResponse(body), nil
}

func oauthDeviceStartUpstreamError(providerError string, providerMessage *string, httpStatus *int) (generatedadminapi.OauthDeviceStartResponseObject, error) {
	env := generatedadminapi.OAuthUpstreamErrorEnvelope{
		Code: generatedadminapi.N3016,
		Msg:  generatedadminapi.OauthUpstreamError,
	}
	env.Data.ProviderError = providerError
	env.Data.ProviderMessage = providerMessage
	env.Data.HttpStatus = httpStatus

	var body generatedadminapi.DeviceStartResponseBody
	if err := body.FromOAuthUpstreamErrorEnvelope(env); err != nil {
		return nil, fmt.Errorf("oauthDeviceStartUpstreamError: encode response: %w", err)
	}
	return generatedadminapi.OauthDeviceStart200JSONResponse(body), nil
}

func oauthCancelIdle() (generatedadminapi.OauthCancelResponseObject, error) {
	env := generatedadminapi.CancelSuccessEnvelope{
		Code: generatedadminapi.CancelSuccessEnvelopeCodeN0,
		Msg:  generatedadminapi.CancelSuccessEnvelopeMsgOk,
	}
	env.Data.Status = generatedadminapi.CancelSuccessEnvelopeDataStatusIdle

	var body generatedadminapi.OAuthCancelResponseBody
	if err := body.FromCancelSuccessEnvelope(env); err != nil {
		return nil, fmt.Errorf("oauthCancelIdle: encode response: %w", err)
	}
	return generatedadminapi.OauthCancel200JSONResponse(body), nil
}

func oauthCancelMismatch(expectedFlowID string) (generatedadminapi.OauthCancelResponseObject, error) {
	env := generatedadminapi.FlowIDMismatchEnvelope{
		Code: generatedadminapi.N3008,
		Msg:  generatedadminapi.FlowIdMismatch,
	}
	env.Data.ExpectedFlowId = stringPointer(expectedFlowID)

	var body generatedadminapi.OAuthCancelResponseBody
	if err := body.FromFlowIDMismatchEnvelope(env); err != nil {
		return nil, fmt.Errorf("oauthCancelMismatch: encode response: %w", err)
	}
	return generatedadminapi.OauthCancel200JSONResponse(body), nil
}

type exchangeError interface {
	error
	Code() string
	Message() string
	HTTPStatus() int
}

type exchangeErrorInfo struct {
	code       string
	message    string
	httpStatus int
}

func exchangeErrorDetails(err error) (exchangeErrorInfo, bool) {
	var exchangeErr exchangeError
	if !errors.As(err, &exchangeErr) {
		return exchangeErrorInfo{}, false
	}
	return exchangeErrorInfo{
		code:       exchangeErr.Code(),
		message:    exchangeErr.Message(),
		httpStatus: exchangeErr.HTTPStatus(),
	}, true
}

func parseManualCallbackURL(raw string) (*url.URL, generatedadminapi.InvalidCallbackURLEnvelopeDataReason, bool) {
	parsed, err := oauth.ParseLoopbackCallbackURL(raw)
	if err != nil {
		return nil, generatedadminapi.UrlPrefixMismatch, false
	}
	return parsed, "", true
}

func (h *Handler) logRailRejected(ctx context.Context, flowID, errorCode string) {
	if h == nil || h.logger == nil || flowID == "" {
		return
	}
	h.logger.Info("oauth_rail_rejected",
		"request_id", requestid.FromContext(ctx),
		"flow_id", flowID,
		"method", oauth.FlowBrowser,
		"rail", oauth.RailManualPaste,
		"error_code", errorCode,
	)
}

func (h *Handler) currentWinnerRail(flowID string) (generatedadminapi.RailName, bool) {
	if h != nil && h.coord != nil {
		if winner, ok := h.coord.WinnerRail(flowID); ok {
			return railNameFromOAuth(winner), true
		}
	}
	return "", false
}

func (h *Handler) manualCallbackConsumed(flowID string) (generatedadminapi.OauthBrowserManualCallbackResponseObject, error) {
	if flowID == "" {
		return nil, errors.New("oauthapi.Handler.OauthBrowserManualCallback: consumed flow id is empty")
	}
	if winner, ok := h.currentWinnerRail(flowID); ok {
		return manualCallbackAlreadyConsumed(winner)
	}

	flow, ok := h.coord.BrowserCallbackSnapshot(flowID)
	if !ok {
		return nil, fmt.Errorf("oauthapi.Handler.OauthBrowserManualCallback: consumed flow %q disappeared before classification", flowID)
	}
	if flow.Error != nil && flow.Error.Code == "flow_expired" {
		return manualCallbackFlowExpired()
	}
	return nil, fmt.Errorf(
		"oauthapi.Handler.OauthBrowserManualCallback: consumed flow %q has no winner rail (status=%s error_code=%s)",
		flowID,
		flow.Status,
		flowErrorCode(flow.Error),
	)
}

func flowErrorCode(err *oauth.FlowTerminalError) string {
	if err == nil {
		return ""
	}
	return err.Code
}

func manualCallbackSuccess(account *domain.UpstreamAccount) (generatedadminapi.OauthBrowserManualCallbackResponseObject, error) {
	item, err := accountListItemFromDomain(account)
	if err != nil {
		return nil, fmt.Errorf("manualCallbackSuccess: map account: %w", err)
	}

	env := generatedadminapi.ManualCallbackSuccessEnvelope{
		Code: generatedadminapi.ManualCallbackSuccessEnvelopeCodeN0,
		Msg:  generatedadminapi.ManualCallbackSuccessEnvelopeMsgOk,
	}
	env.Data.Account = item
	env.Data.Rail = generatedadminapi.ManualCallbackSuccessEnvelopeDataRailManualPaste
	env.Data.Status = generatedadminapi.ManualCallbackSuccessEnvelopeDataStatusSuccess

	var body generatedadminapi.ManualCallbackResponseBody
	if err := body.FromManualCallbackSuccessEnvelope(env); err != nil {
		return nil, fmt.Errorf("manualCallbackSuccess: encode response: %w", err)
	}
	return generatedadminapi.OauthBrowserManualCallback200JSONResponse(body), nil
}

func manualCallbackCancelled() (generatedadminapi.OauthBrowserManualCallbackResponseObject, error) {
	env := generatedadminapi.ManualCallbackCancelEnvelope{
		Code: generatedadminapi.ManualCallbackCancelEnvelopeCodeN0,
		Msg:  generatedadminapi.ManualCallbackCancelEnvelopeMsgOk,
	}
	env.Data.Rail = generatedadminapi.ManualCallbackCancelEnvelopeDataRailManualPaste
	env.Data.Status = generatedadminapi.ManualCallbackCancelEnvelopeDataStatusCancelled

	var body generatedadminapi.ManualCallbackResponseBody
	if err := body.FromManualCallbackCancelEnvelope(env); err != nil {
		return nil, fmt.Errorf("manualCallbackCancelled: encode response: %w", err)
	}
	return generatedadminapi.OauthBrowserManualCallback200JSONResponse(body), nil
}

func manualCallbackInvalidURL(reason generatedadminapi.InvalidCallbackURLEnvelopeDataReason) (generatedadminapi.OauthBrowserManualCallbackResponseObject, error) {
	env := generatedadminapi.InvalidCallbackURLEnvelope{
		Code: generatedadminapi.N3007,
		Msg:  generatedadminapi.InvalidCallbackUrl,
	}
	env.Data.Reason = reason

	var body generatedadminapi.ManualCallbackResponseBody
	if err := body.FromInvalidCallbackURLEnvelope(env); err != nil {
		return nil, fmt.Errorf("manualCallbackInvalidURL: encode response: %w", err)
	}
	return generatedadminapi.OauthBrowserManualCallback200JSONResponse(body), nil
}

func manualCallbackStateMismatch() (generatedadminapi.OauthBrowserManualCallbackResponseObject, error) {
	env := generatedadminapi.OAuthStateMismatchEnvelope{
		Code: generatedadminapi.N3003,
		Msg:  generatedadminapi.OauthStateMismatch,
		Data: map[string]interface{}{},
	}

	var body generatedadminapi.ManualCallbackResponseBody
	if err := body.FromOAuthStateMismatchEnvelope(env); err != nil {
		return nil, fmt.Errorf("manualCallbackStateMismatch: encode response: %w", err)
	}
	return generatedadminapi.OauthBrowserManualCallback200JSONResponse(body), nil
}

func manualCallbackNoFlow() (generatedadminapi.OauthBrowserManualCallbackResponseObject, error) {
	env := generatedadminapi.NoFlowInProgressEnvelope{
		Code: generatedadminapi.N3004,
		Msg:  generatedadminapi.NoFlowInProgress,
		Data: map[string]interface{}{},
	}

	var body generatedadminapi.ManualCallbackResponseBody
	if err := body.FromNoFlowInProgressEnvelope(env); err != nil {
		return nil, fmt.Errorf("manualCallbackNoFlow: encode response: %w", err)
	}
	return generatedadminapi.OauthBrowserManualCallback200JSONResponse(body), nil
}

func manualCallbackAlreadyConsumed(winner generatedadminapi.RailName) (generatedadminapi.OauthBrowserManualCallbackResponseObject, error) {
	env := generatedadminapi.AlreadyConsumedEnvelope{
		Code: generatedadminapi.N3005,
		Msg:  generatedadminapi.AlreadyConsumed,
	}
	env.Data.RailWon = winner

	var body generatedadminapi.ManualCallbackResponseBody
	if err := body.FromAlreadyConsumedEnvelope(env); err != nil {
		return nil, fmt.Errorf("manualCallbackAlreadyConsumed: encode response: %w", err)
	}
	return generatedadminapi.OauthBrowserManualCallback200JSONResponse(body), nil
}

func manualCallbackFlowExpired() (generatedadminapi.OauthBrowserManualCallbackResponseObject, error) {
	env := generatedadminapi.FlowExpiredEnvelope{
		Code: generatedadminapi.N3006,
		Msg:  generatedadminapi.FlowExpired,
		Data: map[string]interface{}{},
	}

	var body generatedadminapi.ManualCallbackResponseBody
	if err := body.FromFlowExpiredEnvelope(env); err != nil {
		return nil, fmt.Errorf("manualCallbackFlowExpired: encode response: %w", err)
	}
	return generatedadminapi.OauthBrowserManualCallback200JSONResponse(body), nil
}

func manualCallbackInvalidGrant(message *string) (generatedadminapi.OauthBrowserManualCallbackResponseObject, error) {
	env := generatedadminapi.OAuthInvalidGrantEnvelope{
		Code: generatedadminapi.N3009,
		Msg:  generatedadminapi.OauthInvalidGrant,
	}
	env.Data.ProviderError = generatedadminapi.InvalidGrant
	env.Data.ProviderMessage = message

	var body generatedadminapi.ManualCallbackResponseBody
	if err := body.FromOAuthInvalidGrantEnvelope(env); err != nil {
		return nil, fmt.Errorf("manualCallbackInvalidGrant: encode response: %w", err)
	}
	return generatedadminapi.OauthBrowserManualCallback200JSONResponse(body), nil
}

func manualCallbackUpstreamError(providerError string, providerMessage *string, httpStatus *int) (generatedadminapi.OauthBrowserManualCallbackResponseObject, error) {
	env := generatedadminapi.OAuthUpstreamErrorEnvelope{
		Code: generatedadminapi.N3016,
		Msg:  generatedadminapi.OauthUpstreamError,
	}
	env.Data.ProviderError = providerError
	env.Data.ProviderMessage = providerMessage
	env.Data.HttpStatus = httpStatus

	var body generatedadminapi.ManualCallbackResponseBody
	if err := body.FromOAuthUpstreamErrorEnvelope(env); err != nil {
		return nil, fmt.Errorf("manualCallbackUpstreamError: encode response: %w", err)
	}
	return generatedadminapi.OauthBrowserManualCallback200JSONResponse(body), nil
}

func accountListItemFromDomain(account *domain.UpstreamAccount) (generatedadminapi.AccountListItem, error) {
	if account == nil {
		return generatedadminapi.AccountListItem{}, errors.New("accountListItemFromDomain: account is nil")
	}

	authMethod, err := accountAuthMethodToAPI(account.AuthMethod)
	if err != nil {
		return generatedadminapi.AccountListItem{}, err
	}

	createdAt := account.CreatedAt
	updatedAt := account.UpdatedAt
	item := generatedadminapi.AccountListItem{
		Id:         account.ID,
		Name:       account.Name,
		Provider:   account.Provider,
		AuthMethod: authMethod,
		Status:     generatedadminapi.AccountListItemStatus(account.Status),
		BaseUrl:    account.BaseURL,
		CreatedAt:  &createdAt,
		UpdatedAt:  &updatedAt,
	}

	if account.AuthMethod != domain.AuthMethodAPIKey {
		item.Email = account.Email
		item.PlanType = account.PlanType
		item.ChatgptAccountId = account.ChatGPTAccountID
		item.LastRefresh = account.LastRefresh
		item.AccessExpiresAt = account.AccessExpiresAt
		if account.PlanType != nil {
			item.PlanTypeLabel = stringPointer(presentation.PlanTypeLabel(*account.PlanType))
		}
	}
	return item, nil
}

func accountAuthMethodToAPI(method domain.AuthMethod) (generatedadminapi.AccountListItemAuthMethod, error) {
	switch method {
	case domain.AuthMethodAPIKey:
		return generatedadminapi.AccountListItemAuthMethodApiKey, nil
	case domain.AuthMethodOAuthBrowser:
		return generatedadminapi.AccountListItemAuthMethodOauthBrowser, nil
	case domain.AuthMethodOAuthDevice:
		return generatedadminapi.AccountListItemAuthMethodOauthDevice, nil
	case domain.AuthMethodOAuthImport:
		return generatedadminapi.AccountListItemAuthMethodOauthImport, nil
	default:
		return "", fmt.Errorf("accountAuthMethodToAPI: unsupported auth method %q", method)
	}
}

func railNameFromOAuth(rail oauth.Rail) generatedadminapi.RailName {
	switch rail {
	case oauth.RailManualPaste:
		return generatedadminapi.ManualPaste
	default:
		return generatedadminapi.Loopback
	}
}

func stringPointer(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

func intPointerIfPositive(v int) *int {
	if v <= 0 {
		return nil
	}
	return &v
}

func int64Pointer(v int64) *int64 {
	return &v
}

func boolPointer(v bool) *bool {
	return &v
}

func (h *Handler) AccountModelsList(ctx context.Context, request generatedadminapi.AccountModelsListRequestObject) (generatedadminapi.AccountModelsListResponseObject, error) {
	return generatedadminapi.AccountModelsList500JSONResponse{
		SystemErrorJSONResponse: generatedadminapi.SystemErrorJSONResponse{
			Code: generatedadminapi.N1901,
			Msg:  "internal_error",
			Data: map[string]any{},
		},
	}, nil
}

func (h *Handler) AccountModelAdd(ctx context.Context, request generatedadminapi.AccountModelAddRequestObject) (generatedadminapi.AccountModelAddResponseObject, error) {
	return generatedadminapi.AccountModelAdd500JSONResponse{
		SystemErrorJSONResponse: generatedadminapi.SystemErrorJSONResponse{
			Code: generatedadminapi.N1901,
			Msg:  "internal_error",
			Data: map[string]any{},
		},
	}, nil
}

func (h *Handler) AccountModelRefresh(ctx context.Context, request generatedadminapi.AccountModelRefreshRequestObject) (generatedadminapi.AccountModelRefreshResponseObject, error) {
	return generatedadminapi.AccountModelRefresh500JSONResponse{
		SystemErrorJSONResponse: generatedadminapi.SystemErrorJSONResponse{
			Code: generatedadminapi.N1901,
			Msg:  "internal_error",
			Data: map[string]any{},
		},
	}, nil
}

func (h *Handler) AccountModelRemove(ctx context.Context, request generatedadminapi.AccountModelRemoveRequestObject) (generatedadminapi.AccountModelRemoveResponseObject, error) {
	return generatedadminapi.AccountModelRemove500JSONResponse{
		SystemErrorJSONResponse: generatedadminapi.SystemErrorJSONResponse{
			Code: generatedadminapi.N1901,
			Msg:  "internal_error",
			Data: map[string]any{},
		},
	}, nil
}

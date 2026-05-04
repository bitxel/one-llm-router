package playgroundapi

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
	"github.com/user/one-llm-router/internal/presentation"
)

const Route = "/api/admin/playground/run"

type Runner interface {
	Run(ctx context.Context, request core.PlaygroundRunRequest) (core.PlaygroundRunResult, error)
}

type Handler struct {
	runner Runner
	logger *slog.Logger
}

var _ generatedadminapi.StrictServerInterface = (*Handler)(nil)

func NewHandler(runner Runner, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{runner: runner, logger: logger}
}

func RegisterHandler(mux *http.ServeMux, handler *Handler, chain func(http.Handler) http.Handler) {
	if chain == nil {
		chain = func(next http.Handler) http.Handler { return next }
	}
	innerMux := http.NewServeMux()
	root := generatedadminapi.HandlerFromMuxWithEnvelope(handler, innerMux)
	mux.Handle("POST "+Route, chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		root.ServeHTTP(w, r)
	})))
}

func (h *Handler) PlaygroundRun(ctx context.Context, request generatedadminapi.PlaygroundRunRequestObject) (generatedadminapi.PlaygroundRunResponseObject, error) {
	if h == nil || h.runner == nil {
		return playgroundSystemError(errors.New("playgroundapi.Handler.PlaygroundRun: runner is nil"))
	}
	req := core.PlaygroundRunRequest{}
	if request.Body != nil {
		var endpoint core.PlaygroundEndpoint
		if request.Body.Endpoint != nil {
			endpoint = core.PlaygroundEndpoint(*request.Body.Endpoint)
		}
		req = core.PlaygroundRunRequest{
			SelectionMode:      core.PlaygroundSelectionMode(request.Body.SelectionMode),
			Endpoint:           endpoint,
			AccountID:          request.Body.AccountId,
			SessionKey:         request.Body.SessionKey,
			Model:              request.Body.Model,
			Text:               request.Body.Text,
			MaxOutputTokens:    request.Body.MaxOutputTokens,
			IncludeRawResponse: request.Body.IncludeRawResponse != nil && *request.Body.IncludeRawResponse,
		}
	}

	result, err := h.runner.Run(ctx, req)
	if err != nil {
		return h.playgroundErrorResponse(err)
	}
	return playgroundSuccess(result)
}

func (h *Handler) playgroundErrorResponse(err error) (generatedadminapi.PlaygroundRunResponseObject, error) {
	var validationErr *core.PlaygroundValidationError
	if errors.As(err, &validationErr) {
		env := generatedadminapi.InvalidPlaygroundRequestEnvelope{
			Code: generatedadminapi.N4001,
			Msg:  generatedadminapi.InvalidPlaygroundRequest,
		}
		env.Data.Field = playgroundField(validationErr.Field)
		if validationErr.Reason != "" {
			env.Data.Reason = &validationErr.Reason
		}
		return playgroundBody(func(body *generatedadminapi.PlaygroundRunResponseBody) error {
			return body.FromInvalidPlaygroundRequestEnvelope(env)
		})
	}
	if errors.Is(err, core.ErrPlaygroundNoActiveAccount) {
		return playgroundBody(func(body *generatedadminapi.PlaygroundRunResponseBody) error {
			return body.FromPlaygroundNoActiveAccountEnvelope(generatedadminapi.PlaygroundNoActiveAccountEnvelope{
				Code: generatedadminapi.N4002,
				Data: map[string]interface{}{},
				Msg:  generatedadminapi.PlaygroundNoActiveAccount,
			})
		})
	}

	var unavailable *core.PlaygroundAccountUnavailableError
	if errors.As(err, &unavailable) {
		env := generatedadminapi.PlaygroundAccountUnavailableEnvelope{
			Code: generatedadminapi.N4003,
			Msg:  generatedadminapi.PlaygroundAccountUnavailable,
		}
		env.Data.RequestedAccountId = unavailable.AccountID
		env.Data.Reason = playgroundUnavailableReason(unavailable.Reason)
		return playgroundBody(func(body *generatedadminapi.PlaygroundRunResponseBody) error {
			return body.FromPlaygroundAccountUnavailableEnvelope(env)
		})
	}

	var upstreamErr *core.PlaygroundUpstreamError
	if errors.As(err, &upstreamErr) {
		env := generatedadminapi.PlaygroundUpstreamErrorEnvelope{
			Code: generatedadminapi.N4004,
			Msg:  generatedadminapi.PlaygroundUpstreamError,
		}
		env.Data.AccountId = upstreamErr.AccountID
		env.Data.Account = generatedAccountSummaryPtr(upstreamErr.Account)
		env.Data.UpstreamStatus = upstreamErr.UpstreamStatus
		env.Data.ProviderError = upstreamErr.ProviderError
		env.Data.ProviderMessage = upstreamErr.ProviderMessage
		return playgroundBody(func(body *generatedadminapi.PlaygroundRunResponseBody) error {
			return body.FromPlaygroundUpstreamErrorEnvelope(env)
		})
	}

	var timeoutErr *core.PlaygroundUpstreamTimeoutError
	if errors.As(err, &timeoutErr) {
		env := generatedadminapi.PlaygroundUpstreamTimeoutEnvelope{
			Code: generatedadminapi.N4005,
			Msg:  generatedadminapi.PlaygroundUpstreamTimeout,
		}
		env.Data.AccountId = timeoutErr.AccountID
		env.Data.Account = generatedAccountSummaryPtr(timeoutErr.Account)
		env.Data.WaitLimitMs = generatedadminapi.PlaygroundUpstreamTimeoutEnvelopeDataWaitLimitMs(timeoutErr.WaitLimitMS)
		return playgroundBody(func(body *generatedadminapi.PlaygroundRunResponseBody) error {
			return body.FromPlaygroundUpstreamTimeoutEnvelope(env)
		})
	}

	var malformedErr *core.PlaygroundResponseMalformedError
	if errors.As(err, &malformedErr) {
		env := generatedadminapi.PlaygroundResponseMalformedEnvelope{
			Code: generatedadminapi.N4006,
			Msg:  generatedadminapi.PlaygroundResponseMalformed,
		}
		env.Data.AccountId = malformedErr.AccountID
		env.Data.Account = generatedAccountSummaryPtr(malformedErr.Account)
		env.Data.Reason = playgroundMalformedReason(malformedErr.Reason)
		return playgroundBody(func(body *generatedadminapi.PlaygroundRunResponseBody) error {
			return body.FromPlaygroundResponseMalformedEnvelope(env)
		})
	}

	var tooLargeErr *core.PlaygroundResponseTooLargeError
	if errors.As(err, &tooLargeErr) {
		env := generatedadminapi.PlaygroundResponseTooLargeEnvelope{
			Code: generatedadminapi.N4007,
			Msg:  generatedadminapi.PlaygroundResponseTooLarge,
		}
		env.Data.AccountId = tooLargeErr.AccountID
		env.Data.Account = generatedAccountSummaryPtr(tooLargeErr.Account)
		env.Data.LimitBytes = generatedadminapi.PlaygroundResponseTooLargeEnvelopeDataLimitBytes(tooLargeErr.LimitBytes)
		return playgroundBody(func(body *generatedadminapi.PlaygroundRunResponseBody) error {
			return body.FromPlaygroundResponseTooLargeEnvelope(env)
		})
	}

	if h != nil && h.logger != nil {
		h.logger.Error("playground run failed", "err", sanitizeLogError(err))
	}
	return playgroundSystemError(fmt.Errorf("playground run: %w", err))
}

func playgroundSuccess(result core.PlaygroundRunResult) (generatedadminapi.PlaygroundRunResponseObject, error) {
	env := generatedadminapi.PlaygroundRunSuccessEnvelope{
		Code: generatedadminapi.PlaygroundRunSuccessEnvelopeCodeN0,
		Msg:  generatedadminapi.PlaygroundRunSuccessEnvelopeMsgOk,
		Data: generatedadminapi.PlaygroundRunSuccessData{
			Run: generatedadminapi.PlaygroundRun{
				SelectionMode: generatedadminapi.PlaygroundRunSelectionMode(result.Run.SelectionMode),
				Endpoint:      generatedadminapi.PlaygroundRunEndpoint(result.Run.Endpoint),
				SessionKey:    result.Run.SessionKey,
				Outcome:       generatedadminapi.PlaygroundRunOutcome(result.Run.Outcome),
				LatencyMs:     result.Run.LatencyMS,
			},
			Account:  generatedAccountSummary(result.Account),
			Upstream: generatedUpstreamSummary(result.Upstream),
			Output:   generatedOutput(result.Output),
			Usage:    generatedUsage(result.Usage),
		},
	}
	return playgroundBody(func(body *generatedadminapi.PlaygroundRunResponseBody) error {
		return body.FromPlaygroundRunSuccessEnvelope(env)
	})
}

func playgroundBody(fill func(*generatedadminapi.PlaygroundRunResponseBody) error) (generatedadminapi.PlaygroundRunResponseObject, error) {
	var body generatedadminapi.PlaygroundRunResponseBody
	if err := fill(&body); err != nil {
		return nil, err
	}
	return generatedadminapi.PlaygroundRun200JSONResponse(body), nil
}

func playgroundSystemError(err error) (generatedadminapi.PlaygroundRunResponseObject, error) {
	return generatedadminapi.PlaygroundRun500JSONResponse{
		SystemErrorJSONResponse: generatedadminapi.SystemErrorJSONResponse{
			Code: generatedadminapi.N4900,
			Data: map[string]interface{}{},
			Msg:  errcode.Symbol(errcode.PlaygroundInternalError),
		},
	}, nil
}

func generatedAccountSummaryPtr(account *core.PlaygroundAccountSummary) *generatedadminapi.PlaygroundAccountSummary {
	if account == nil {
		return nil
	}
	out := generatedAccountSummary(*account)
	return &out
}

func generatedAccountSummary(account core.PlaygroundAccountSummary) generatedadminapi.PlaygroundAccountSummary {
	out := generatedadminapi.PlaygroundAccountSummary{
		Id:         account.ID,
		Name:       account.Name,
		Provider:   account.Provider,
		AuthMethod: generatedadminapi.PlaygroundAccountSummaryAuthMethod(account.AuthMethod),
		Status:     generatedadminapi.PlaygroundAccountSummaryStatus(account.Status),
		Email:      account.Email,
		PlanType:   account.PlanType,
	}
	if account.PlanTypeLabel != nil {
		out.PlanTypeLabel = account.PlanTypeLabel
	} else if account.PlanType != nil {
		label := presentation.PlanTypeLabel(*account.PlanType)
		out.PlanTypeLabel = &label
	}
	return out
}

func generatedUpstreamSummary(upstream core.PlaygroundUpstreamSummary) generatedadminapi.PlaygroundUpstreamSummary {
	return generatedadminapi.PlaygroundUpstreamSummary{
		StatusCode:   upstream.StatusCode,
		ResponseMode: generatedadminapi.PlaygroundUpstreamSummaryResponseMode(upstream.ResponseMode),
	}
}

func generatedOutput(output core.PlaygroundOutput) generatedadminapi.PlaygroundOutput {
	out := generatedadminapi.PlaygroundOutput{
		Text:                 output.Text,
		TextAvailable:        output.TextAvailable,
		RawResponseAvailable: output.RawResponseAvailable,
	}
	if output.RawResponseAvailable && output.RawResponse != nil {
		raw := map[string]interface{}(output.RawResponse)
		out.RawResponse = &raw
	}
	if !output.RawResponseAvailable && output.RawResponseOmittedReason != nil {
		reason := generatedadminapi.PlaygroundOutputRawResponseOmittedReason(*output.RawResponseOmittedReason)
		out.RawResponseOmittedReason = &reason
	}
	return out
}

func sanitizeLogError(err error) string {
	if err == nil {
		return ""
	}
	return sanitizeLogValue(err.Error())
}

func sanitizeLogValue(value string) string {
	lower := strings.ToLower(value)
	if strings.Contains(lower, "bearer ") ||
		strings.Contains(lower, "sk-") ||
		strings.Contains(lower, "sk_") ||
		strings.Contains(value, "eyJ") {
		return "[redacted]"
	}
	return value
}

func generatedUsage(usage map[string]int) generatedadminapi.PlaygroundUsage {
	out := generatedadminapi.PlaygroundUsage{}
	for key, value := range usage {
		switch key {
		case "input":
			v := value
			out.Input = &v
		case "output":
			v := value
			out.Output = &v
		default:
			out.Set(key, value)
		}
	}
	return out
}

func playgroundField(field core.PlaygroundValidationField) generatedadminapi.InvalidPlaygroundRequestEnvelopeDataField {
	switch field {
	case core.PlaygroundFieldSelectionMode:
		return generatedadminapi.InvalidPlaygroundRequestEnvelopeDataFieldSelectionMode
	case core.PlaygroundFieldEndpoint:
		return generatedadminapi.InvalidPlaygroundRequestEnvelopeDataFieldEndpoint
	case core.PlaygroundFieldAccountID:
		return generatedadminapi.InvalidPlaygroundRequestEnvelopeDataFieldAccountId
	case core.PlaygroundFieldSessionKey:
		return generatedadminapi.InvalidPlaygroundRequestEnvelopeDataFieldSessionKey
	case core.PlaygroundFieldModel:
		return generatedadminapi.InvalidPlaygroundRequestEnvelopeDataFieldModel
	case core.PlaygroundFieldMaxOutputTokens:
		return generatedadminapi.InvalidPlaygroundRequestEnvelopeDataFieldMaxOutputTokens
	case core.PlaygroundFieldIncludeRawResponse:
		return generatedadminapi.InvalidPlaygroundRequestEnvelopeDataFieldIncludeRawResponse
	default:
		return generatedadminapi.InvalidPlaygroundRequestEnvelopeDataFieldText
	}
}

func playgroundUnavailableReason(reason core.PlaygroundAccountUnavailableReason) generatedadminapi.PlaygroundAccountUnavailableEnvelopeDataReason {
	return generatedadminapi.PlaygroundAccountUnavailableEnvelopeDataReason(reason)
}

func playgroundMalformedReason(reason core.PlaygroundMalformedReason) generatedadminapi.PlaygroundResponseMalformedEnvelopeDataReason {
	return generatedadminapi.PlaygroundResponseMalformedEnvelopeDataReason(reason)
}

func (h *Handler) AccountsImportAuthJSON(context.Context, generatedadminapi.AccountsImportAuthJSONRequestObject) (generatedadminapi.AccountsImportAuthJSONResponseObject, error) {
	return nil, errors.New("playgroundapi.Handler.AccountsImportAuthJSON: not implemented")
}

func (h *Handler) AccountsExportAuthJSON(context.Context, generatedadminapi.AccountsExportAuthJSONRequestObject) (generatedadminapi.AccountsExportAuthJSONResponseObject, error) {
	return nil, errors.New("playgroundapi.Handler.AccountsExportAuthJSON: not implemented")
}

func (h *Handler) OauthBrowserManualCallback(context.Context, generatedadminapi.OauthBrowserManualCallbackRequestObject) (generatedadminapi.OauthBrowserManualCallbackResponseObject, error) {
	return nil, errors.New("playgroundapi.Handler.OauthBrowserManualCallback: not implemented")
}

func (h *Handler) OauthBrowserStart(context.Context, generatedadminapi.OauthBrowserStartRequestObject) (generatedadminapi.OauthBrowserStartResponseObject, error) {
	return nil, errors.New("playgroundapi.Handler.OauthBrowserStart: not implemented")
}

func (h *Handler) OauthCancel(context.Context, generatedadminapi.OauthCancelRequestObject) (generatedadminapi.OauthCancelResponseObject, error) {
	return nil, errors.New("playgroundapi.Handler.OauthCancel: not implemented")
}

func (h *Handler) OauthDeviceStart(context.Context, generatedadminapi.OauthDeviceStartRequestObject) (generatedadminapi.OauthDeviceStartResponseObject, error) {
	return nil, errors.New("playgroundapi.Handler.OauthDeviceStart: not implemented")
}

func (h *Handler) OauthFlowStatus(context.Context, generatedadminapi.OauthFlowStatusRequestObject) (generatedadminapi.OauthFlowStatusResponseObject, error) {
	return nil, errors.New("playgroundapi.Handler.OauthFlowStatus: not implemented")
}

func (h *Handler) DashboardGet(context.Context, generatedadminapi.DashboardGetRequestObject) (generatedadminapi.DashboardGetResponseObject, error) {
	return nil, errors.New("playgroundapi.Handler.DashboardGet: not implemented")
}

func (h *Handler) UsageGet(context.Context, generatedadminapi.UsageGetRequestObject) (generatedadminapi.UsageGetResponseObject, error) {
	return nil, errors.New("playgroundapi.Handler.UsageGet: not implemented")
}

func (h *Handler) RequestsList(context.Context, generatedadminapi.RequestsListRequestObject) (generatedadminapi.RequestsListResponseObject, error) {
	return nil, errors.New("playgroundapi.Handler.RequestsList: not implemented")
}

func (h *Handler) RequestsOptions(context.Context, generatedadminapi.RequestsOptionsRequestObject) (generatedadminapi.RequestsOptionsResponseObject, error) {
	return nil, errors.New("playgroundapi.Handler.RequestsOptions: not implemented")
}

func (h *Handler) RequestsGet(context.Context, generatedadminapi.RequestsGetRequestObject) (generatedadminapi.RequestsGetResponseObject, error) {
	return nil, errors.New("playgroundapi.Handler.RequestsGet: not implemented")
}

func (h *Handler) SettingsGet(context.Context, generatedadminapi.SettingsGetRequestObject) (generatedadminapi.SettingsGetResponseObject, error) {
	return nil, errors.New("playgroundapi.Handler.SettingsGet: not implemented")
}

func (h *Handler) SettingsUpdate(context.Context, generatedadminapi.SettingsUpdateRequestObject) (generatedadminapi.SettingsUpdateResponseObject, error) {
	return nil, errors.New("playgroundapi.Handler.SettingsUpdate: not implemented")
}

var _ = domain.ResponseModeJSON

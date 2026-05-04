package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/user/one-llm-router/internal/clientip"
	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/requestid"
)

const (
	PlaygroundDefaultModel            = "gpt-5.4-mini"
	PlaygroundPromptLimitChars        = 16_000
	PlaygroundDefaultMaxOutputTokens  = 1_024
	PlaygroundMaxOutputTokens         = 4_096
	PlaygroundWaitLimit               = 30 * time.Second
	PlaygroundResponseReadLimitBytes  = 1 << 20
	PlaygroundProviderMessageMaxChars = 512
	PlaygroundRunPath                 = "/api/admin/playground/run"

	playgroundClientClosedStatus = 499
)

var ErrPlaygroundNoActiveAccount = errors.New("playground: no active eligible account")

type PlaygroundSelectionMode string

const (
	PlaygroundSelectionAuto    PlaygroundSelectionMode = "auto"
	PlaygroundSelectionAccount PlaygroundSelectionMode = "account"
)

type PlaygroundEndpoint string

const (
	PlaygroundEndpointResponses       PlaygroundEndpoint = "responses"
	PlaygroundEndpointChatCompletions PlaygroundEndpoint = "chat_completions"
)

type PlaygroundValidationField string

const (
	PlaygroundFieldSelectionMode      PlaygroundValidationField = "selection_mode"
	PlaygroundFieldEndpoint           PlaygroundValidationField = "endpoint"
	PlaygroundFieldAccountID          PlaygroundValidationField = "account_id"
	PlaygroundFieldSessionKey         PlaygroundValidationField = "session_key"
	PlaygroundFieldModel              PlaygroundValidationField = "model"
	PlaygroundFieldText               PlaygroundValidationField = "text"
	PlaygroundFieldMaxOutputTokens    PlaygroundValidationField = "max_output_tokens"
	PlaygroundFieldIncludeRawResponse PlaygroundValidationField = "include_raw_response"
)

type PlaygroundAccountUnavailableReason string

const (
	PlaygroundAccountMissing               PlaygroundAccountUnavailableReason = "missing"
	PlaygroundAccountDeleted               PlaygroundAccountUnavailableReason = "deleted"
	PlaygroundAccountDisabled              PlaygroundAccountUnavailableReason = "disabled"
	PlaygroundAccountIneligible            PlaygroundAccountUnavailableReason = "ineligible"
	PlaygroundAccountCredentialUnavailable PlaygroundAccountUnavailableReason = "credential_unavailable"
)

type PlaygroundMalformedReason string

const (
	PlaygroundMalformedInvalidJSON PlaygroundMalformedReason = "invalid_json"
	PlaygroundMalformedUnsafeJSON  PlaygroundMalformedReason = "unsafe_json"
)

type PlaygroundRawResponseOmittedReason string

const (
	PlaygroundRawNotRequested PlaygroundRawResponseOmittedReason = "not_requested"
	PlaygroundRawTooLarge     PlaygroundRawResponseOmittedReason = "too_large"
	PlaygroundRawMalformed    PlaygroundRawResponseOmittedReason = "malformed"
	PlaygroundRawUnsafe       PlaygroundRawResponseOmittedReason = "unsafe"
)

type PlaygroundOutcome string

const (
	PlaygroundOutcomeSuccess           PlaygroundOutcome = "success"
	PlaygroundOutcomeNoExtractableText PlaygroundOutcome = "no_extractable_text"
)

type PlaygroundRunRequest struct {
	SelectionMode      PlaygroundSelectionMode
	Endpoint           PlaygroundEndpoint
	AccountID          *int64
	SessionKey         *string
	Model              string
	Text               string
	MaxOutputTokens    *int
	IncludeRawResponse bool
}

func (r PlaygroundRunRequest) EffectiveMaxOutputTokens() int {
	if r.MaxOutputTokens == nil {
		return PlaygroundDefaultMaxOutputTokens
	}
	return *r.MaxOutputTokens
}

func (r PlaygroundRunRequest) EffectiveEndpoint() PlaygroundEndpoint {
	if r.Endpoint == "" {
		return PlaygroundEndpointResponses
	}
	return r.Endpoint
}

type PlaygroundAccountSummary struct {
	ID            int64
	Name          string
	Provider      string
	AuthMethod    domain.AuthMethod
	Status        string
	Email         *string
	PlanType      *string
	PlanTypeLabel *string
}

type PlaygroundRunMeta struct {
	SelectionMode PlaygroundSelectionMode
	Endpoint      PlaygroundEndpoint
	SessionKey    *string
	Outcome       PlaygroundOutcome
	LatencyMS     int64
}

type PlaygroundUpstreamSummary struct {
	StatusCode   *int
	ResponseMode string
}

type PlaygroundOutput struct {
	Text                     string
	TextAvailable            bool
	RawResponse              domain.JSONMap
	RawResponseAvailable     bool
	RawResponseOmittedReason *PlaygroundRawResponseOmittedReason
}

type PlaygroundRunResult struct {
	Run      PlaygroundRunMeta
	Account  PlaygroundAccountSummary
	Upstream PlaygroundUpstreamSummary
	Output   PlaygroundOutput
	Usage    map[string]int
}

type PlaygroundAccountStore interface {
	GetByID(ctx context.Context, id int64) (*domain.UpstreamAccount, error)
	ListActive(ctx context.Context) ([]domain.UpstreamAccount, error)
}

type PlaygroundUpstreamClient interface {
	RunPlayground(ctx context.Context, account domain.UpstreamAccount, token []byte, request PlaygroundRunRequest) (*PlaygroundUpstreamResult, error)
}

type PlaygroundRunRecorder interface {
	Record(ctx context.Context, rec domain.RequestRecord)
}

type PlaygroundUpstreamResult struct {
	StatusCode               int
	ResponseMode             string
	UpstreamEndpoint         string
	UpstreamRequestBody      []byte
	Text                     string
	TextAvailable            bool
	RawResponse              domain.JSONMap
	RawResponseAvailable     bool
	RawResponseOmittedReason *PlaygroundRawResponseOmittedReason
	Usage                    map[string]int
}

type PlaygroundValidationError struct {
	Field  PlaygroundValidationField
	Reason string
}

func (e *PlaygroundValidationError) Error() string {
	if e == nil {
		return "playground validation failed"
	}
	if e.Reason == "" {
		return fmt.Sprintf("playground validation failed: %s", e.Field)
	}
	return fmt.Sprintf("playground validation failed: %s: %s", e.Field, e.Reason)
}

type PlaygroundAccountUnavailableError struct {
	AccountID int64
	Reason    PlaygroundAccountUnavailableReason
}

func (e *PlaygroundAccountUnavailableError) Error() string {
	if e == nil {
		return "playground account unavailable"
	}
	return fmt.Sprintf("playground account unavailable: account_id=%d reason=%s", e.AccountID, e.Reason)
}

type PlaygroundUpstreamError struct {
	AccountID            int64
	Account              *PlaygroundAccountSummary
	UpstreamStatus       int
	UpstreamEndpoint     string
	UpstreamRequestBody  []byte
	UpstreamResponseBody []byte
	ProviderError        *string
	ProviderMessage      *string
}

func (e *PlaygroundUpstreamError) Error() string {
	if e == nil {
		return "playground upstream error"
	}
	return fmt.Sprintf("playground upstream error: account_id=%d status=%d", e.AccountID, e.UpstreamStatus)
}

type PlaygroundUpstreamConnectError struct {
	AccountID           int64
	Account             *PlaygroundAccountSummary
	UpstreamEndpoint    string
	UpstreamRequestBody []byte
	Cause               error
}

func (e *PlaygroundUpstreamConnectError) Error() string {
	if e == nil {
		return "playground upstream connect failed"
	}
	return fmt.Sprintf("playground upstream connect failed: account_id=%d", e.AccountID)
}

func (e *PlaygroundUpstreamConnectError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

type PlaygroundUpstreamTimeoutError struct {
	AccountID           int64
	Account             *PlaygroundAccountSummary
	WaitLimitMS         int
	UpstreamEndpoint    string
	UpstreamRequestBody []byte
}

func (e *PlaygroundUpstreamTimeoutError) Error() string {
	if e == nil {
		return "playground upstream timeout"
	}
	return fmt.Sprintf("playground upstream timeout: account_id=%d wait_limit_ms=%d", e.AccountID, e.WaitLimitMS)
}

type PlaygroundResponseMalformedError struct {
	AccountID            int64
	Account              *PlaygroundAccountSummary
	Reason               PlaygroundMalformedReason
	UpstreamEndpoint     string
	UpstreamRequestBody  []byte
	UpstreamResponseBody []byte
}

func (e *PlaygroundResponseMalformedError) Error() string {
	if e == nil {
		return "playground response malformed"
	}
	return fmt.Sprintf("playground response malformed: account_id=%d reason=%s", e.AccountID, e.Reason)
}

type PlaygroundResponseTooLargeError struct {
	AccountID           int64
	Account             *PlaygroundAccountSummary
	LimitBytes          int
	UpstreamEndpoint    string
	UpstreamRequestBody []byte
}

func (e *PlaygroundResponseTooLargeError) Error() string {
	if e == nil {
		return "playground response too large"
	}
	return fmt.Sprintf("playground response too large: account_id=%d limit_bytes=%d", e.AccountID, e.LimitBytes)
}

type PlaygroundService struct {
	accounts PlaygroundAccountStore
	selector *AccountSelector
	upstream PlaygroundUpstreamClient
	recorder PlaygroundRunRecorder
	bodyLog  func() (clientReqLog, upstreamReqLog, upstreamRespLog bool)
	now      func() time.Time
}

func NewPlaygroundService(accounts PlaygroundAccountStore, selector *AccountSelector, upstream PlaygroundUpstreamClient) *PlaygroundService {
	return &PlaygroundService{
		accounts: accounts,
		selector: selector,
		upstream: upstream,
		now:      time.Now,
	}
}

func (s *PlaygroundService) SetClock(now func() time.Time) {
	if s == nil {
		return
	}
	if now == nil {
		s.now = time.Now
		return
	}
	s.now = now
}

func (s *PlaygroundService) SetRecorder(recorder PlaygroundRunRecorder) {
	if s == nil {
		return
	}
	s.recorder = recorder
}

func (s *PlaygroundService) SetBodyLogFunc(fn func() (clientReqLog, upstreamReqLog, upstreamRespLog bool)) {
	if s == nil {
		return
	}
	s.bodyLog = fn
}

func (s *PlaygroundService) Run(ctx context.Context, req PlaygroundRunRequest) (PlaygroundRunResult, error) {
	if s == nil || s.selector == nil {
		return PlaygroundRunResult{}, errors.New("playground service: selector is nil")
	}
	if s.upstream == nil {
		return PlaygroundRunResult{}, errors.New("playground service: upstream client is nil")
	}
	req, err := s.Validate(req)
	if err != nil {
		return PlaygroundRunResult{}, err
	}

	start := s.now()
	var (
		account domain.UpstreamAccount
		token   []byte
	)
	switch req.SelectionMode {
	case PlaygroundSelectionAuto:
		sessionKey := ""
		if req.SessionKey != nil {
			sessionKey = *req.SessionKey
		}
		account, token, _, err = s.selector.Select(ctx, sessionKey)
		if err != nil {
			if errors.Is(err, domain.ErrNoCapacity) {
				s.recordRun(ctx, playgroundRecordInput{
					request:   req,
					start:     start,
					status:    http.StatusOK,
					outcome:   domain.OutcomeNoAvailableAccount,
					errorCode: playgroundErrorSymbolNoActiveAccount,
				})
				return PlaygroundRunResult{}, ErrPlaygroundNoActiveAccount
			}
			s.recordRun(ctx, playgroundRecordInput{
				request:   req,
				start:     start,
				status:    http.StatusInternalServerError,
				outcome:   domain.OutcomeRouterError,
				errorCode: playgroundErrorSymbolInternal,
			})
			return PlaygroundRunResult{}, err
		}
	case PlaygroundSelectionAccount:
		account, token, _, err = s.selector.SelectByID(ctx, *req.AccountID)
		if err != nil {
			mappedErr := mapAccountSelectionError(err)
			var unavailable *PlaygroundAccountUnavailableError
			if errors.As(mappedErr, &unavailable) {
				input := playgroundRecordInput{
					request:   req,
					start:     start,
					status:    http.StatusOK,
					outcome:   domain.OutcomeAccountUnavailable,
					errorCode: playgroundErrorSymbolAccountUnavailable,
				}
				if unavailable.Reason != PlaygroundAccountMissing {
					input.accountID = &unavailable.AccountID
				}
				s.recordRun(ctx, input)
				return PlaygroundRunResult{}, mappedErr
			}
			s.recordRun(ctx, playgroundRecordInput{
				request:   req,
				start:     start,
				status:    http.StatusInternalServerError,
				outcome:   domain.OutcomeRouterError,
				errorCode: playgroundErrorSymbolInternal,
			})
			return PlaygroundRunResult{}, mappedErr
		}
	}

	upstream, err := s.upstream.RunPlayground(ctx, account, token, req)
	if err != nil {
		decoratedErr := decoratePlaygroundError(accountSummary(account), err)
		s.recordRun(ctx, recordInputForPlaygroundError(req, start, account.ID, decoratedErr))
		return PlaygroundRunResult{}, decoratedErr
	}
	if upstream == nil {
		s.recordRun(ctx, playgroundRecordInput{
			request:   req,
			start:     start,
			accountID: &account.ID,
			status:    http.StatusInternalServerError,
			outcome:   domain.OutcomeRouterError,
			errorCode: playgroundErrorSymbolInternal,
		})
		return PlaygroundRunResult{}, errors.New("playground service: upstream result is nil")
	}

	latency := s.now().Sub(start).Milliseconds()
	if latency < 0 {
		latency = 0
	}
	outcome := PlaygroundOutcomeNoExtractableText
	if upstream.TextAvailable {
		outcome = PlaygroundOutcomeSuccess
	}
	statusCode := upstream.StatusCode
	output := PlaygroundOutput{
		Text:                     upstream.Text,
		TextAvailable:            upstream.TextAvailable,
		RawResponse:              sanitizeJSONMap(upstream.RawResponse),
		RawResponseAvailable:     upstream.RawResponseAvailable,
		RawResponseOmittedReason: upstream.RawResponseOmittedReason,
	}
	recordOutcome := domain.OutcomeNoExtractableText
	if upstream.TextAvailable {
		recordOutcome = domain.OutcomeSuccess
	}
	s.recordRun(ctx, playgroundRecordInput{
		request:             req,
		start:               start,
		accountID:           &account.ID,
		account:             &account,
		status:              statusCode,
		outcome:             recordOutcome,
		responseMode:        upstream.ResponseMode,
		upstreamEndpoint:    upstream.UpstreamEndpoint,
		upstreamRequestBody: upstream.UpstreamRequestBody,
		usage:               upstream.Usage,
		rawResponse:         output.RawResponse,
		omitted:             output.RawResponseOmittedReason,
	})

	return PlaygroundRunResult{
		Run: PlaygroundRunMeta{
			SelectionMode: req.SelectionMode,
			Endpoint:      req.Endpoint,
			SessionKey:    req.SessionKey,
			Outcome:       outcome,
			LatencyMS:     latency,
		},
		Account: accountSummary(account),
		Upstream: PlaygroundUpstreamSummary{
			StatusCode:   &statusCode,
			ResponseMode: upstream.ResponseMode,
		},
		Output: output,
		Usage:  upstream.Usage,
	}, nil
}

func (s *PlaygroundService) Validate(req PlaygroundRunRequest) (PlaygroundRunRequest, error) {
	req.Model = strings.TrimSpace(req.Model)
	req.Text = strings.TrimSpace(req.Text)
	if req.SessionKey != nil {
		sessionKey := strings.TrimSpace(*req.SessionKey)
		req.SessionKey = &sessionKey
	}
	req.Endpoint = req.EffectiveEndpoint()

	switch req.SelectionMode {
	case PlaygroundSelectionAuto:
		if req.AccountID != nil {
			return PlaygroundRunRequest{}, playgroundValidation(PlaygroundFieldAccountID, "must be omitted for automatic selection")
		}
	case PlaygroundSelectionAccount:
		if req.AccountID == nil || *req.AccountID <= 0 {
			return PlaygroundRunRequest{}, playgroundValidation(PlaygroundFieldAccountID, "positive account_id is required")
		}
	default:
		return PlaygroundRunRequest{}, playgroundValidation(PlaygroundFieldSelectionMode, "must be auto or account")
	}

	switch req.Endpoint {
	case PlaygroundEndpointResponses, PlaygroundEndpointChatCompletions:
	default:
		return PlaygroundRunRequest{}, playgroundValidation(PlaygroundFieldEndpoint, "must be responses or chat_completions")
	}

	if req.SessionKey != nil {
		count := utf8.RuneCountInString(*req.SessionKey)
		if count < 1 || count > 128 {
			return PlaygroundRunRequest{}, playgroundValidation(PlaygroundFieldSessionKey, "must be 1..128 characters")
		}
	}
	if count := utf8.RuneCountInString(req.Model); count < 1 || count > 128 {
		return PlaygroundRunRequest{}, playgroundValidation(PlaygroundFieldModel, "must be 1..128 characters")
	}
	if count := utf8.RuneCountInString(req.Text); count < 1 || count > PlaygroundPromptLimitChars {
		return PlaygroundRunRequest{}, playgroundValidation(PlaygroundFieldText, "must be 1..16000 characters")
	}
	if req.MaxOutputTokens != nil && (*req.MaxOutputTokens < 1 || *req.MaxOutputTokens > PlaygroundMaxOutputTokens) {
		return PlaygroundRunRequest{}, playgroundValidation(PlaygroundFieldMaxOutputTokens, "must be 1..4096")
	}

	return req, nil
}

func playgroundValidation(field PlaygroundValidationField, reason string) error {
	return &PlaygroundValidationError{Field: field, Reason: reason}
}

func mapAccountSelectionError(err error) error {
	var selectionErr *AccountSelectionError
	if !errors.As(err, &selectionErr) {
		return err
	}
	return &PlaygroundAccountUnavailableError{
		AccountID: selectionErr.AccountID,
		Reason:    playgroundReasonFromAccountSelection(selectionErr.Reason),
	}
}

func playgroundReasonFromAccountSelection(reason AccountUnavailableReason) PlaygroundAccountUnavailableReason {
	switch reason {
	case AccountUnavailableMissing:
		return PlaygroundAccountMissing
	case AccountUnavailableDeleted:
		return PlaygroundAccountDeleted
	case AccountUnavailableDisabled:
		return PlaygroundAccountDisabled
	case AccountUnavailableCredentialUnavailable:
		return PlaygroundAccountCredentialUnavailable
	default:
		return PlaygroundAccountIneligible
	}
}

const (
	playgroundErrorSymbolNoActiveAccount    = "playground_no_active_account"
	playgroundErrorSymbolAccountUnavailable = "playground_account_unavailable"
	playgroundErrorSymbolUpstreamError      = "playground_upstream_error"
	playgroundErrorSymbolUpstreamTimeout    = "playground_upstream_timeout"
	playgroundErrorSymbolMalformed          = "playground_response_malformed"
	playgroundErrorSymbolTooLarge           = "playground_response_too_large"
	playgroundErrorSymbolInternal           = "playground_internal_error"
)

type playgroundRecordInput struct {
	request              PlaygroundRunRequest
	start                time.Time
	accountID            *int64
	account              *domain.UpstreamAccount
	status               int
	outcome              string
	errorCode            string
	responseMode         string
	upstreamRequestBody  []byte
	upstreamResponseBody []byte
	upstreamEndpoint     string
	usage                map[string]int
	rawResponse          domain.JSONMap
	omitted              *PlaygroundRawResponseOmittedReason
}

func recordInputForPlaygroundError(req PlaygroundRunRequest, start time.Time, accountID int64, err error) playgroundRecordInput {
	input := playgroundRecordInput{
		request:      req,
		start:        start,
		accountID:    &accountID,
		status:       http.StatusInternalServerError,
		outcome:      domain.OutcomeRouterError,
		errorCode:    playgroundErrorSymbolInternal,
		responseMode: domain.ResponseModeJSON,
	}
	if errors.Is(err, context.Canceled) {
		input.status = playgroundClientClosedStatus
		input.outcome = domain.OutcomeCancelled
		input.errorCode = ""
		return input
	}

	var upstreamErr *PlaygroundUpstreamError
	if errors.As(err, &upstreamErr) {
		input.status = upstreamErr.UpstreamStatus
		input.outcome = domain.OutcomeUpstreamError
		input.errorCode = playgroundErrorSymbolUpstreamError
		input.upstreamRequestBody = upstreamErr.UpstreamRequestBody
		input.upstreamResponseBody = upstreamErr.UpstreamResponseBody
		input.upstreamEndpoint = upstreamErr.UpstreamEndpoint
		if upstreamErr.AccountID != 0 {
			input.accountID = &upstreamErr.AccountID
		}
		return input
	}

	var connectErr *PlaygroundUpstreamConnectError
	if errors.As(err, &connectErr) {
		input.status = http.StatusInternalServerError
		input.outcome = domain.OutcomeRouterError
		input.errorCode = playgroundErrorSymbolInternal
		input.upstreamRequestBody = connectErr.UpstreamRequestBody
		input.upstreamEndpoint = connectErr.UpstreamEndpoint
		if connectErr.AccountID != 0 {
			input.accountID = &connectErr.AccountID
		}
		return input
	}

	var timeoutErr *PlaygroundUpstreamTimeoutError
	if errors.As(err, &timeoutErr) {
		input.status = http.StatusOK
		input.outcome = domain.OutcomeUpstreamTimeout
		input.errorCode = playgroundErrorSymbolUpstreamTimeout
		input.upstreamRequestBody = timeoutErr.UpstreamRequestBody
		input.upstreamEndpoint = timeoutErr.UpstreamEndpoint
		if timeoutErr.AccountID != 0 {
			input.accountID = &timeoutErr.AccountID
		}
		return input
	}

	var malformedErr *PlaygroundResponseMalformedError
	if errors.As(err, &malformedErr) {
		input.status = http.StatusOK
		input.outcome = domain.OutcomeRouterError
		input.errorCode = playgroundErrorSymbolMalformed
		input.upstreamRequestBody = malformedErr.UpstreamRequestBody
		input.upstreamResponseBody = malformedErr.UpstreamResponseBody
		input.upstreamEndpoint = malformedErr.UpstreamEndpoint
		if malformedErr.AccountID != 0 {
			input.accountID = &malformedErr.AccountID
		}
		return input
	}

	var tooLargeErr *PlaygroundResponseTooLargeError
	if errors.As(err, &tooLargeErr) {
		input.status = http.StatusOK
		input.outcome = domain.OutcomeRouterError
		input.errorCode = playgroundErrorSymbolTooLarge
		input.upstreamRequestBody = tooLargeErr.UpstreamRequestBody
		input.upstreamEndpoint = tooLargeErr.UpstreamEndpoint
		if tooLargeErr.AccountID != 0 {
			input.accountID = &tooLargeErr.AccountID
		}
		return input
	}

	return input
}

func (s *PlaygroundService) recordRun(ctx context.Context, input playgroundRecordInput) {
	if s == nil || s.recorder == nil {
		return
	}
	requestID := requestid.FromContext(ctx)
	if requestID == "" {
		requestID = domain.NewRequestID()
	}
	latency := int(s.now().Sub(input.start).Milliseconds())
	if latency < 0 {
		latency = 0
	}
	responseMode := input.responseMode
	if responseMode == "" {
		responseMode = domain.ResponseModeJSON
	}
	model := input.request.Model
	modelParams := playgroundModelParams(input.request, input.account, input.omitted)
	tokenUsage := playgroundUsageMap(input.usage)
	clientReqLog, upstreamReqLog, upstreamRespLog := s.shouldLogBody()

	var clientRequestBody *string
	if clientReqLog {
		clientRequestBody = playgroundRequestBody(input.request)
	}
	var upstreamRequestBody *string
	if upstreamReqLog && len(input.upstreamRequestBody) > 0 {
		upstreamRequestBody = playgroundCapturedBody(input.upstreamRequestBody)
	}
	var upstreamResponseBody *string
	if upstreamRespLog && len(input.upstreamResponseBody) > 0 {
		upstreamResponseBody = playgroundCapturedBody(input.upstreamResponseBody)
	} else if upstreamRespLog && input.rawResponse != nil {
		upstreamResponseBody = playgroundResponseBody(input.rawResponse)
	}
	var errorCode *string
	if input.errorCode != "" {
		errorCode = &input.errorCode
	}

	rec := domain.RequestRecord{
		RequestID:            requestID,
		ClientIP:             clientip.FromContext(ctx),
		UpstreamAccountID:    input.accountID,
		SessionKey:           input.request.SessionKey,
		Method:               http.MethodPost,
		Path:                 PlaygroundRunPath,
		StatusCode:           input.status,
		LatencyMs:            latency,
		Outcome:              input.outcome,
		ErrorCode:            errorCode,
		Model:                &model,
		ModelParams:          modelParams,
		RouterMetadata:       playgroundRouterMetadata(input.upstreamEndpoint),
		ResponseMode:         responseMode,
		TokenUsage:           tokenUsage,
		ClientRequestBody:    clientRequestBody,
		UpstreamRequestBody:  upstreamRequestBody,
		UpstreamResponseBody: upstreamResponseBody,
	}
	s.recorder.Record(ctx, rec)
}

func playgroundRouterMetadata(upstreamEndpoint string) domain.JSONMap {
	return domain.JSONMap{
		"upstream_endpoint": upstreamEndpoint,
	}
}

func (s *PlaygroundService) shouldLogBody() (clientReqLog, upstreamReqLog, upstreamRespLog bool) {
	if s == nil || s.bodyLog == nil {
		return false, false, false
	}
	return s.bodyLog()
}

func playgroundModelParams(req PlaygroundRunRequest, account *domain.UpstreamAccount, omitted *PlaygroundRawResponseOmittedReason) domain.JSONMap {
	params := domain.JSONMap{
		"selection_mode":       string(req.SelectionMode),
		"endpoint":             string(req.EffectiveEndpoint()),
		"max_output_tokens":    req.EffectiveMaxOutputTokens(),
		"include_raw_response": req.IncludeRawResponse,
	}
	if req.AccountID != nil {
		params["requested_account_id"] = *req.AccountID
	}
	if account != nil {
		params["provider"] = account.Provider
		params["auth_method"] = string(account.AuthMethod)
	}
	if omitted != nil {
		params["raw_response_omitted_reason"] = string(*omitted)
	}
	return params
}

func playgroundUsageMap(usage map[string]int) domain.JSONMap {
	if len(usage) == 0 {
		return nil
	}
	out := make(domain.JSONMap, len(usage))
	for key, value := range usage {
		out[key] = value
	}
	return out
}

func playgroundRequestBody(req PlaygroundRunRequest) *string {
	body := domain.JSONMap{
		"selection_mode":       string(req.SelectionMode),
		"endpoint":             string(req.EffectiveEndpoint()),
		"model":                sanitizeString(req.Model),
		"text":                 sanitizeString(req.Text),
		"max_output_tokens":    req.EffectiveMaxOutputTokens(),
		"include_raw_response": req.IncludeRawResponse,
	}
	if req.AccountID != nil {
		body["account_id"] = *req.AccountID
	}
	if req.SessionKey != nil {
		body["session_key"] = sanitizeString(*req.SessionKey)
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil
	}
	out := string(encoded)
	return &out
}

func playgroundResponseBody(raw domain.JSONMap) *string {
	encoded, err := json.Marshal(sanitizeJSONMap(raw))
	if err != nil {
		return nil
	}
	return playgroundCapturedBody(encoded)
}

func playgroundCapturedBody(body []byte) *string {
	out := RedactCapturedBody(body)
	return &out
}

func accountSummary(account domain.UpstreamAccount) PlaygroundAccountSummary {
	return PlaygroundAccountSummary{
		ID:         account.ID,
		Name:       account.Name,
		Provider:   account.Provider,
		AuthMethod: account.AuthMethod,
		Status:     account.Status,
		Email:      account.Email,
		PlanType:   account.PlanType,
	}
}

func decoratePlaygroundError(account PlaygroundAccountSummary, err error) error {
	var upstreamErr *PlaygroundUpstreamError
	if errors.As(err, &upstreamErr) {
		clone := *upstreamErr
		if clone.AccountID == 0 {
			clone.AccountID = account.ID
		}
		if clone.Account == nil {
			clone.Account = &account
		}
		clone.ProviderError = sanitizeOptionalString(clone.ProviderError, 128)
		clone.ProviderMessage = sanitizeOptionalString(clone.ProviderMessage, PlaygroundProviderMessageMaxChars)
		return &clone
	}

	var connectErr *PlaygroundUpstreamConnectError
	if errors.As(err, &connectErr) {
		clone := *connectErr
		if clone.AccountID == 0 {
			clone.AccountID = account.ID
		}
		if clone.Account == nil {
			clone.Account = &account
		}
		return &clone
	}

	var timeoutErr *PlaygroundUpstreamTimeoutError
	if errors.As(err, &timeoutErr) {
		clone := *timeoutErr
		if clone.AccountID == 0 {
			clone.AccountID = account.ID
		}
		if clone.Account == nil {
			clone.Account = &account
		}
		if clone.WaitLimitMS == 0 {
			clone.WaitLimitMS = int(PlaygroundWaitLimit / time.Millisecond)
		}
		return &clone
	}

	var malformedErr *PlaygroundResponseMalformedError
	if errors.As(err, &malformedErr) {
		clone := *malformedErr
		if clone.AccountID == 0 {
			clone.AccountID = account.ID
		}
		if clone.Account == nil {
			clone.Account = &account
		}
		if clone.Reason == "" {
			clone.Reason = PlaygroundMalformedInvalidJSON
		}
		return &clone
	}

	var tooLargeErr *PlaygroundResponseTooLargeError
	if errors.As(err, &tooLargeErr) {
		clone := *tooLargeErr
		if clone.AccountID == 0 {
			clone.AccountID = account.ID
		}
		if clone.Account == nil {
			clone.Account = &account
		}
		if clone.LimitBytes == 0 {
			clone.LimitBytes = PlaygroundResponseReadLimitBytes
		}
		return &clone
	}

	return err
}

func sanitizeJSONMap(src domain.JSONMap) domain.JSONMap {
	return RedactJSONMap(src)
}

func sanitizeOptionalString(value *string, maxChars int) *string {
	if value == nil {
		return nil
	}
	sanitized := sanitizeString(strings.TrimSpace(*value))
	sanitized = clipRunes(sanitized, maxChars)
	return &sanitized
}

func sanitizeString(value string) string {
	if IsTokenLikeString(value) {
		return "[redacted]"
	}
	return value
}

func clipRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

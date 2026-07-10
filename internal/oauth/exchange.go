package oauth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/user/one-llm-router/internal/core"
	"github.com/user/one-llm-router/internal/requestid"
)

const (
	oauthHTTPTimeout          = 10 * time.Second
	oauthResponseBodyLimit    = 64 << 10
	formContentTypeURLEncoded = "application/x-www-form-urlencoded"
	jsonContentType           = "application/json"
	defaultDevicePollInterval = 5 * time.Second
)

type TokenExchangeError struct {
	code       string
	message    string
	httpStatus int
	cause      error
}

func (e *TokenExchangeError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.httpStatus > 0 {
		return fmt.Sprintf("oauth token exchange failed: code=%s status=%d message=%s", e.code, e.httpStatus, e.message)
	}
	return fmt.Sprintf("oauth token exchange failed: code=%s message=%s", e.code, e.message)
}

func (e *TokenExchangeError) Code() string { return e.code }

func (e *TokenExchangeError) Message() string { return e.message }

func (e *TokenExchangeError) HTTPStatus() int { return e.httpStatus }

func (e *TokenExchangeError) Retryable() bool { return e.httpStatus >= http.StatusInternalServerError }

func (e *TokenExchangeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

type tokenExchangeResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	IDToken          string `json:"id_token"`
	ExpiresInSeconds int64  `json:"expires_in"`

	Error            oauthErrorValue `json:"error"`
	ErrorDescription string          `json:"error_description"`
}

type deviceCodeResponse struct {
	DeviceAuthID    string          `json:"device_auth_id"`
	UserCode        string          `json:"user_code"`
	VerificationURI string          `json:"verification_uri"`
	Interval        json.RawMessage `json:"interval"`
	ExpiresAt       string          `json:"expires_at"`

	Error            oauthErrorValue `json:"error"`
	ErrorDescription string          `json:"error_description"`
	Status           string          `json:"status"`
}

type devicePollResponse struct {
	AuthorizationCode string `json:"authorization_code"`
	CodeVerifier      string `json:"code_verifier"`

	Error            oauthErrorValue `json:"error"`
	ErrorDescription string          `json:"error_description"`
	Status           string          `json:"status"`
}

type oauthErrorValue struct {
	Code    string
	Message string
}

func (e *oauthErrorValue) UnmarshalJSON(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}

	var code string
	if err := json.Unmarshal(raw, &code); err == nil {
		e.Code = code
		return nil
	}

	var payload struct {
		Code             string `json:"code"`
		Message          string `json:"message"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return err
	}
	e.Code = payload.Code
	e.Message = firstNonEmpty(payload.Message, payload.ErrorDescription)
	return nil
}

func (p *openAIProvider) ExchangeCode(ctx context.Context, code, verifier string) (Tokens, error) {
	if code == "" {
		return Tokens{}, errors.New("oauth.ExchangeCode: code is empty")
	}
	if verifier == "" {
		return Tokens{}, errors.New("oauth.ExchangeCode: verifier is empty")
	}

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("code_verifier", verifier)
	form.Set("redirect_uri", p.redirectURIOrDefault())
	form.Set("client_id", openAIClientID)
	return p.exchangeTokens(ctx, "authorization_code", form, "")
}

func (p *openAIProvider) Refresh(ctx context.Context, refreshToken []byte) (Tokens, error) {
	if len(refreshToken) == 0 {
		return Tokens{}, errors.New("oauth.Refresh: refreshToken is empty")
	}

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", string(refreshToken))
	form.Set("client_id", openAIClientID)
	return p.exchangeTokens(ctx, "refresh_token", form, oauthTokenDebugFingerprint(refreshToken))
}

func (p *openAIProvider) RequestDeviceCode(ctx context.Context) (DeviceCode, error) {
	body, err := json.Marshal(map[string]string{
		"client_id": openAIClientID,
	})
	if err != nil {
		return DeviceCode{}, fmt.Errorf("oauth.RequestDeviceCode: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.deviceCodeEndpoint(), bytes.NewReader(body))
	if err != nil {
		return DeviceCode{}, fmt.Errorf("oauth.RequestDeviceCode: create request: %w", err)
	}
	req.Header.Set("Content-Type", jsonContentType)

	resp, err := p.httpClientOrDefault().Do(req)
	if err != nil {
		return DeviceCode{}, &TokenExchangeError{
			code:    "request_failed",
			message: err.Error(),
			cause:   err,
		}
	}
	defer func() { _ = resp.Body.Close() }()

	payload, err := decodeDeviceCodeResponse(resp)
	if err != nil {
		return DeviceCode{}, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return DeviceCode{}, classifyDeviceCodeError(resp.StatusCode, payload)
	}

	deviceCode, err := p.deviceCodeFromResponse(payload)
	if err != nil {
		return DeviceCode{}, &TokenExchangeError{
			code:       "invalid_response",
			message:    err.Error(),
			httpStatus: resp.StatusCode,
		}
	}
	return deviceCode, nil
}

func (p *openAIProvider) PollDeviceCode(ctx context.Context, deviceAuthID, userCode string) (Tokens, error) {
	if deviceAuthID == "" {
		return Tokens{}, errors.New("oauth.PollDeviceCode: deviceAuthID is empty")
	}
	if userCode == "" {
		return Tokens{}, errors.New("oauth.PollDeviceCode: userCode is empty")
	}

	body, err := json.Marshal(map[string]string{
		"device_auth_id": deviceAuthID,
		"user_code":      userCode,
	})
	if err != nil {
		return Tokens{}, fmt.Errorf("oauth.PollDeviceCode: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.deviceTokenEndpoint(), bytes.NewReader(body))
	if err != nil {
		return Tokens{}, fmt.Errorf("oauth.PollDeviceCode: create request: %w", err)
	}
	req.Header.Set("Content-Type", jsonContentType)

	resp, err := p.httpClientOrDefault().Do(req)
	if err != nil {
		return Tokens{}, &TokenExchangeError{
			code:    "request_failed",
			message: err.Error(),
			cause:   err,
		}
	}
	defer func() { _ = resp.Body.Close() }()

	payload, err := decodeDevicePollResponse(resp)
	if err != nil {
		return Tokens{}, err
	}
	if pendingErr := classifyPendingDevicePoll(payload); pendingErr != nil {
		return Tokens{}, pendingErr
	}
	if code := semanticDevicePollCode(payload); code != "" {
		return Tokens{}, classifyDevicePollError(resp.StatusCode, payload)
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
		return Tokens{}, &TokenExchangeError{
			code:       fmt.Sprintf("http_%d", resp.StatusCode),
			message:    http.StatusText(resp.StatusCode),
			httpStatus: resp.StatusCode,
		}
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return Tokens{}, classifyDevicePollError(resp.StatusCode, payload)
	}
	if payload.AuthorizationCode == "" {
		return Tokens{}, &TokenExchangeError{
			code:       "invalid_response",
			message:    "missing authorization_code",
			httpStatus: resp.StatusCode,
		}
	}
	if payload.CodeVerifier == "" {
		return Tokens{}, &TokenExchangeError{
			code:       "invalid_response",
			message:    "missing code_verifier",
			httpStatus: resp.StatusCode,
		}
	}

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", payload.AuthorizationCode)
	form.Set("redirect_uri", p.deviceRedirectURI())
	form.Set("client_id", openAIClientID)
	form.Set("code_verifier", payload.CodeVerifier)
	return p.exchangeTokens(ctx, "authorization_code", form, "")
}

func (p *openAIProvider) exchangeTokens(ctx context.Context, grantType string, form url.Values, refreshTokenFingerprint string) (Tokens, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.tokenEndpoint(), strings.NewReader(form.Encode()))
	if err != nil {
		return Tokens{}, fmt.Errorf("oauth.exchangeTokens: create request: %w", err)
	}
	req.Header.Set("Content-Type", formContentTypeURLEncoded)

	resp, err := p.httpClientOrDefault().Do(req)
	if err != nil {
		return Tokens{}, &TokenExchangeError{
			code:    "request_failed",
			message: err.Error(),
			cause:   err,
		}
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := readLimitedResponseBody(resp.Body)
	if err != nil {
		exchangeErr := &TokenExchangeError{
			code:       "invalid_response",
			message:    err.Error(),
			httpStatus: resp.StatusCode,
		}
		return Tokens{}, exchangeErr
	}
	p.logTokenExchangeDebug(ctx, grantType, refreshTokenFingerprint, resp, body)

	payload, err := decodeTokenExchangeResponse(body)
	if err != nil {
		exchangeErr := &TokenExchangeError{
			code:       "invalid_response",
			message:    err.Error(),
			httpStatus: resp.StatusCode,
		}
		return Tokens{}, exchangeErr
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		exchangeErr := classifyTokenExchangeError(resp.StatusCode, payload)
		return Tokens{}, exchangeErr
	}

	tokens, err := p.tokensFromResponse(payload)
	if err != nil {
		exchangeErr := &TokenExchangeError{
			code:       "invalid_response",
			message:    err.Error(),
			httpStatus: resp.StatusCode,
		}
		return Tokens{}, exchangeErr
	}

	return tokens, nil
}

func decodeDeviceCodeResponse(resp *http.Response) (deviceCodeResponse, error) {
	body, err := readLimitedResponseBody(resp.Body)
	if err != nil {
		return deviceCodeResponse{}, &TokenExchangeError{
			code:       "invalid_response",
			message:    err.Error(),
			httpStatus: resp.StatusCode,
		}
	}

	var payload deviceCodeResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return deviceCodeResponse{}, &TokenExchangeError{
			code:       "invalid_response",
			message:    fmt.Sprintf("decode upstream oauth JSON: %v", err),
			httpStatus: resp.StatusCode,
		}
	}
	return payload, nil
}

func decodeDevicePollResponse(resp *http.Response) (devicePollResponse, error) {
	body, err := readLimitedResponseBody(resp.Body)
	if err != nil {
		return devicePollResponse{}, &TokenExchangeError{
			code:       "invalid_response",
			message:    err.Error(),
			httpStatus: resp.StatusCode,
		}
	}

	var payload devicePollResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return devicePollResponse{}, &TokenExchangeError{
			code:       "invalid_response",
			message:    fmt.Sprintf("decode upstream oauth JSON: %v", err),
			httpStatus: resp.StatusCode,
		}
	}
	return payload, nil
}

func (p *openAIProvider) deviceCodeFromResponse(payload deviceCodeResponse) (DeviceCode, error) {
	if payload.DeviceAuthID == "" {
		return DeviceCode{}, errors.New("missing device_auth_id")
	}
	if payload.UserCode == "" {
		return DeviceCode{}, errors.New("missing user_code")
	}

	interval, present, err := parseFlexibleSeconds(payload.Interval)
	if err != nil {
		return DeviceCode{}, fmt.Errorf("invalid interval: %w", err)
	}
	if !present || interval == 0 {
		interval = defaultDevicePollInterval
	} else if interval < 0 {
		return DeviceCode{}, errors.New("interval must be non-negative")
	}

	expiresIn, err := p.deviceCodeTTL(payload.ExpiresAt)
	if err != nil {
		return DeviceCode{}, err
	}
	verificationURL := payload.VerificationURI
	if verificationURL == "" {
		verificationURL = openAIDeviceVerificationURL
		if p != nil {
			verificationURL = p.deviceVerificationEndpoint()
		}
	}

	return DeviceCode{
		DeviceAuthID:    payload.DeviceAuthID,
		UserCode:        payload.UserCode,
		VerificationURL: verificationURL,
		Interval:        interval,
		ExpiresIn:       expiresIn,
	}, nil
}

func (p *openAIProvider) deviceCodeTTL(rawExpiresAt string) (time.Duration, error) {
	rawExpiresAt = strings.TrimSpace(rawExpiresAt)
	if rawExpiresAt == "" {
		return 0, errors.New("missing expires_at")
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, rawExpiresAt)
	if err != nil {
		return 0, fmt.Errorf("invalid expires_at: %w", err)
	}
	expiresIn := expiresAt.Sub(p.nowOrDefault().UTC())
	if expiresIn <= 0 {
		return 0, errors.New("expires_at must be in the future")
	}
	return expiresIn, nil
}

func classifyDeviceCodeError(httpStatus int, payload deviceCodeResponse) *TokenExchangeError {
	if httpStatus == http.StatusNotFound {
		message := payload.ErrorDescription
		if message == "" {
			message = http.StatusText(httpStatus)
		}
		return &TokenExchangeError{
			code:       "device_auth_unavailable",
			message:    message,
			httpStatus: httpStatus,
		}
	}
	if payload.Error.Code != "" || payload.Error.Message != "" || payload.ErrorDescription != "" {
		code := payload.Error.Code
		message := firstNonEmpty(payload.ErrorDescription, payload.Error.Message)
		return &TokenExchangeError{
			code:       defaultOAuthErrorCode(code, httpStatus),
			message:    defaultOAuthErrorMessage(message, code, httpStatus),
			httpStatus: httpStatus,
		}
	}
	return &TokenExchangeError{
		code:       defaultOAuthErrorCode("", httpStatus),
		message:    defaultOAuthErrorMessage("", "", httpStatus),
		httpStatus: httpStatus,
	}
}

func classifyDevicePollError(httpStatus int, payload devicePollResponse) *TokenExchangeError {
	code := semanticDevicePollCode(payload)
	message := firstNonEmpty(payload.ErrorDescription, payload.Error.Message)
	if message == "" {
		message = strings.TrimSpace(payload.Status)
	}
	return &TokenExchangeError{
		code:       defaultOAuthErrorCode(code, httpStatus),
		message:    defaultOAuthErrorMessage(message, code, httpStatus),
		httpStatus: httpStatus,
	}
}

func classifyPendingDevicePoll(payload devicePollResponse) *TokenExchangeError {
	errorCode := payload.Error.Code
	errorMessage := firstNonEmpty(payload.ErrorDescription, payload.Error.Message)
	switch {
	case isPendingDeviceStatus(errorCode), isPendingDeviceStatus(payload.Status):
		return &TokenExchangeError{
			code:    "authorization_pending",
			message: defaultDevicePollMessage(errorMessage, errorCode, payload.Status),
		}
	case strings.EqualFold(strings.TrimSpace(errorCode), "slow_down"), strings.EqualFold(strings.TrimSpace(payload.Status), "slow_down"):
		return &TokenExchangeError{
			code:    "slow_down",
			message: defaultDevicePollMessage(errorMessage, errorCode, payload.Status),
		}
	}
	return nil
}

func isPendingDeviceStatus(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "pending", "authorization_pending", "deviceauth_authorization_unknown", "deviceauth_authorization_pending":
		return true
	default:
		return false
	}
}

func semanticDevicePollCode(payload devicePollResponse) string {
	if code := strings.TrimSpace(payload.Error.Code); code != "" {
		return code
	}
	switch status := strings.TrimSpace(payload.Status); strings.ToLower(status) {
	case "", "ok", "success":
		return ""
	default:
		return status
	}
}

func parseFlexibleSeconds(raw json.RawMessage) (time.Duration, bool, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false, nil
	}

	var number int64
	if err := json.Unmarshal(raw, &number); err == nil {
		return time.Duration(number) * time.Second, true, nil
	}

	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, false, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, false, nil
	}
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0, false, err
	}
	return time.Duration(value) * time.Second, true, nil
}

func defaultOAuthErrorCode(code string, httpStatus int) string {
	if code != "" {
		return code
	}
	if httpStatus > 0 {
		return fmt.Sprintf("http_%d", httpStatus)
	}
	return "invalid_response"
}

func defaultOAuthErrorMessage(message, code string, httpStatus int) string {
	if message != "" {
		return message
	}
	if code != "" {
		return code
	}
	if httpStatus > 0 {
		return http.StatusText(httpStatus)
	}
	return "invalid_response"
}

func defaultDevicePollMessage(message, code, status string) string {
	if message != "" {
		return message
	}
	if code != "" {
		return code
	}
	if status != "" {
		return status
	}
	return "authorization_pending"
}

func (p *openAIProvider) tokensFromResponse(payload tokenExchangeResponse) (Tokens, error) {
	switch {
	case payload.AccessToken == "":
		return Tokens{}, errors.New("missing access_token")
	case payload.RefreshToken == "":
		return Tokens{}, errors.New("missing refresh_token")
	case payload.IDToken == "":
		return Tokens{}, errors.New("missing id_token")
	case payload.ExpiresInSeconds < 0:
		return Tokens{}, errors.New("invalid expires_in")
	}

	return Tokens{
		AccessToken:  []byte(payload.AccessToken),
		RefreshToken: []byte(payload.RefreshToken),
		IDToken:      []byte(payload.IDToken),
		ExpiresIn:    time.Duration(payload.ExpiresInSeconds) * time.Second,
		LastRefresh:  p.nowOrDefault().UTC(),
	}, nil
}

func classifyTokenExchangeError(httpStatus int, payload tokenExchangeResponse) *TokenExchangeError {
	code := payload.Error.Code
	message := firstNonEmpty(payload.ErrorDescription, payload.Error.Message)
	if code == "" {
		code = "invalid_response"
	}
	if message == "" {
		message = code
	}
	return &TokenExchangeError{
		code:       code,
		message:    message,
		httpStatus: httpStatus,
	}
}

func decodeTokenExchangeResponse(body []byte) (tokenExchangeResponse, error) {
	var payload tokenExchangeResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return tokenExchangeResponse{}, fmt.Errorf("decode upstream oauth JSON: %w", err)
	}
	return payload, nil
}

func (p *openAIProvider) logTokenExchangeDebug(ctx context.Context, grantType, refreshTokenFingerprint string, resp *http.Response, body []byte) {
	logger := p.logger
	if logger == nil {
		logger = slog.Default()
	}
	if !logger.Enabled(ctx, slog.LevelDebug) {
		return
	}

	attrs := []any{
		"component", "oauth",
		"request_id", requestid.FromContext(ctx),
		"grant_type", grantType,
		"http_status", resp.StatusCode,
		"response_content_type", resp.Header.Get("Content-Type"),
		"response_body_bytes", len(body),
		"response_body", core.RedactCapturedBody(body),
	}
	if refreshTokenFingerprint != "" {
		attrs = append(attrs, "refresh_token_fingerprint", refreshTokenFingerprint)
	}
	logger.Debug("oauth_token_exchange_debug", attrs...)
}

func oauthTokenDebugFingerprint(token []byte) string {
	if len(token) == 0 {
		return ""
	}
	sum := sha256.Sum256(token)
	return fmt.Sprintf("sha256:%s len:%d", hex.EncodeToString(sum[:8]), len(token))
}

func readLimitedResponseBody(r io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, oauthResponseBodyLimit+1))
	if err != nil {
		return nil, fmt.Errorf("read upstream oauth body: %w", err)
	}
	if len(body) > oauthResponseBodyLimit {
		return nil, fmt.Errorf("upstream oauth body exceeds %d bytes", oauthResponseBodyLimit)
	}
	return body, nil
}

func (p *openAIProvider) tokenEndpoint() string {
	if p != nil && p.tokenURL != "" {
		return p.tokenURL
	}
	return openAITokenURL
}

func (p *openAIProvider) deviceCodeEndpoint() string {
	if p != nil && p.deviceCodeURL != "" {
		return p.deviceCodeURL
	}
	return openAIDeviceCodeURL
}

func (p *openAIProvider) deviceTokenEndpoint() string {
	if p != nil && p.deviceTokenURL != "" {
		return p.deviceTokenURL
	}
	return openAIDeviceTokenURL
}

func (p *openAIProvider) httpClientOrDefault() *http.Client {
	if p != nil && p.httpClient != nil {
		return p.httpClient
	}
	return &http.Client{Timeout: oauthHTTPTimeout}
}

func (p *openAIProvider) nowOrDefault() time.Time {
	if p != nil && p.now != nil {
		return p.now()
	}
	return time.Now()
}

package api

import (
	"encoding/json"
	"net/http"
)

const (
	ErrCodeNoAvailableAccount  = "no_available_account"
	ErrCodeInternalError       = "internal_error"
	ErrCodeInvalidRequest      = "invalid_request"
	ErrCodeUnsupportedEndpoint = "unsupported_endpoint"
	ErrCodeBlockedEndpoint     = "blocked_endpoint"
	ErrCodeUpstreamConnFailed  = "upstream_connect_failed"
	ErrCodeUpstreamRespInvalid = "upstream_response_invalid"
	ErrCodeUpstreamTimeout     = "upstream_timeout"
)

type RouterErrorEnvelope struct {
	Error RouterError `json:"error"`
}

type RouterError struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func WriteRouterError(w http.ResponseWriter, statusCode int, code, message, requestID string) {
	WriteNativeError(w, statusCode, "router_error", code, message, requestID)
}

func WriteNativeError(w http.ResponseWriter, statusCode int, errorType, code, message, requestID string) {
	envelope := RouterErrorEnvelope{
		Error: RouterError{
			Type:    errorType,
			Code:    code,
			Message: message,
		},
	}

	// Charset suffix kept consistent with internal/api/envelope.go's
	// WriteOK/WriteBizErr/WriteSysErr so clients never see "mixed"
	// Content-Type values between the 001 native error shape and the
	// 002 router envelope. See contracts/setup-api.md / admin-api.md.
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if requestID != "" && w.Header().Get("X-Request-Id") == "" {
		w.Header().Set("X-Request-Id", requestID)
	}
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(envelope)
}

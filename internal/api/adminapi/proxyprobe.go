package adminapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/user/one-llm-router/internal/api"
	"github.com/user/one-llm-router/internal/api/errcode"
	"github.com/user/one-llm-router/internal/api/httpio"
	"github.com/user/one-llm-router/internal/proxydial"
)

// ProxyTestHandler serves POST /api/admin/settings/proxy/test
// (Feature 009) — a connectivity probe for the proxy URL typed into
// the settings page. The candidate URL rides in the request body and
// MAY carry credentials, so the response never echoes it: success is
// the plain 0 envelope, failures carry only a coarse stable reason
// (timeout / connection_refused / ...), and the diagnostic log line
// uses the masked form.
//
// The probe dials the OpenAI upstream through a FRESH Transport built
// from the candidate URL; the process-wide proxydial slot is
// deliberately untouched because the value is not yet saved.
type ProxyTestHandler struct {
	// testConnectivity is the injectable probe (default
	// proxydial.TestConnectivity); unit tests swap in fakes so no
	// socket is opened.
	testConnectivity func(ctx context.Context, rawProxyURL string, timeout time.Duration) error
	logger           *slog.Logger
}

// NewProxyTestHandler wires the handler. logger defaults to slog.Default.
func NewProxyTestHandler(logger *slog.Logger) *ProxyTestHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &ProxyTestHandler{
		testConnectivity: proxydial.TestConnectivity,
		logger:           logger,
	}
}

// Test implements the endpoint. Business outcomes ride the envelope at
// HTTP 200: 0 reachable, 9001 invalid_proxy_url, 9003 proxy_test_failed.
func (h *ProxyTestHandler) Test(w http.ResponseWriter, r *http.Request) {
	reqID := api.RequestIDFromContext(r.Context())

	raw, ok := httpio.DecodeJSON[map[string]json.RawMessage](w, r, reqID, maxSettingsProxyBodyBytes)
	if !ok {
		return
	}
	value, exists := raw["proxy_url"]
	if !exists || len(raw) != 1 {
		httpio.WriteMalformedBody(w, reqID)
		return
	}
	if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		httpio.WriteMalformedBody(w, reqID)
		return
	}
	var proxyURL string
	if err := json.Unmarshal(value, &proxyURL); err != nil {
		api.WriteBizErr(w, reqID, errcode.InvalidProxyURL,
			errcode.Symbol(errcode.InvalidProxyURL), map[string]any{
				"field":  "proxy_url",
				"detail": "proxy_url must be a string (http/https/socks5/socks5h)",
			})
		return
	}
	if strings.TrimSpace(proxyURL) == "" {
		api.WriteBizErr(w, reqID, errcode.InvalidProxyURL,
			errcode.Symbol(errcode.InvalidProxyURL), map[string]any{
				"field":  "proxy_url",
				"detail": "proxy_url must not be empty",
			})
		return
	}

	if err := h.testConnectivity(r.Context(), proxyURL, proxydial.ConnectivityTestTimeout); err != nil {
		if errors.Is(err, proxydial.ErrInvalidProxyURL) {
			api.WriteBizErr(w, reqID, errcode.InvalidProxyURL,
				errcode.Symbol(errcode.InvalidProxyURL), map[string]any{
					"field":  "proxy_url",
					"detail": err.Error(),
				})
			return
		}
		reason := "network_error"
		var connErr *proxydial.ConnectivityError
		if errors.As(err, &connErr) {
			reason = connErr.Reason
		}
		h.logger.Warn("proxy connectivity test failed",
			"request_id", reqID,
			"proxy_url", proxydial.MaskedURL(proxyURL),
			"reason", reason,
			"error", err,
		)
		api.WriteBizErr(w, reqID, errcode.ProxyTestFailed,
			errcode.Symbol(errcode.ProxyTestFailed), map[string]any{
				"detail": reason,
			})
		return
	}

	h.logger.Info("proxy connectivity test ok",
		"request_id", reqID,
		"proxy_url", proxydial.MaskedURL(proxyURL),
	)
	api.WriteOK(w, reqID, map[string]any{"reachable": true})
}

package adminapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/user/one-llm-router/internal/api"
	"github.com/user/one-llm-router/internal/api/errcode"
	"github.com/user/one-llm-router/internal/api/httpio"
	"github.com/user/one-llm-router/internal/domain"
)

// ProxyToggleHandler serves POST /api/admin/accounts/{id}/proxy/set
// (Feature 009) — the per-account opt-in for the global outbound proxy.
//
// The toggle is NOT a credential, so unlike POST .../update it works on
// every auth_method (api_key + the three OAuth variants); the store
// method bypasses UpdateDetails on purpose. Business errors ride the
// standard envelope at HTTP 200: 1001 for missing accounts, 1004 for
// deleted rows (mirroring the enable/disable wrap mapping), 9002 when
// the operator flips use_proxy=true while no global proxy is
// configured. That last check only reads the live config — the
// settings path gains NO store dependency by design (spec 009 D8:
// referential-integrity closure cut in favour of the runtime
// fail-fast defence).
type ProxyToggleHandler struct {
	accounts ProxyToggleStore
	reader   ConfigReader
	logger   *slog.Logger
}

// ProxyToggleStore is the narrow store surface the toggle needs.
// GetByID returns deleted rows too (status=deleted) so the handler can
// map them to 1004 exactly like enable/disable do through the wrap.
type ProxyToggleStore interface {
	GetByID(ctx context.Context, id int64) (*domain.UpstreamAccount, error)
	UpdateUseProxy(ctx context.Context, id int64, useProxy bool) error
}

// NewProxyToggleHandler wires the handler. reader is the same live
// ConfigReader the settings handler uses; nil reader degrades to
// "no proxy configured" which fails closed (use_proxy=true is then
// always rejected) — the safe direction for transitional boots.
func NewProxyToggleHandler(accounts ProxyToggleStore, reader ConfigReader, logger *slog.Logger) *ProxyToggleHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &ProxyToggleHandler{accounts: accounts, reader: reader, logger: logger}
}

// SetUseProxy implements the endpoint. Idempotent by definition: the
// value is an absolute assignment, not a state transition, so setting
// the same value twice succeeds.
func (h *ProxyToggleHandler) SetUseProxy(w http.ResponseWriter, r *http.Request) {
	reqID := api.RequestIDFromContext(r.Context())

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		api.WriteBizErr(w, reqID, errcode.AccountNotFound,
			errcode.Symbol(errcode.AccountNotFound), map[string]any{})
		return
	}

	raw, ok := httpio.DecodeJSON[map[string]json.RawMessage](w, r, reqID, maxSettingsProxyBodyBytes)
	if !ok {
		return
	}
	value, exists := raw["use_proxy"]
	if !exists || len(raw) != 1 {
		httpio.WriteMalformedBody(w, reqID)
		return
	}
	var useProxy *bool
	if err := json.Unmarshal(value, &useProxy); err != nil || useProxy == nil {
		httpio.WriteMalformedBody(w, reqID)
		return
	}

	// Account existence is checked BEFORE the global-proxy requirement
	// so a wrong id always reports 1001 regardless of use_proxy (spec
	// 009 US-2: resource errors take precedence over configuration
	// errors).
	acct, err := h.accounts.GetByID(r.Context(), id)
	if err != nil && !errors.Is(err, domain.ErrAccountNotFound) {
		h.logger.Error("admin proxy toggle account lookup failed",
			"request_id", reqID, "account_id", id, "error", err)
		api.WriteSysErr(w, reqID, errcode.InternalError,
			errcode.Symbol(errcode.InternalError))
		return
	}
	if errors.Is(err, domain.ErrAccountNotFound) || acct == nil {
		api.WriteBizErr(w, reqID, errcode.AccountNotFound,
			errcode.Symbol(errcode.AccountNotFound), map[string]any{})
		return
	}
	if acct.Status == domain.AccountStatusDeleted {
		api.WriteBizErr(w, reqID, errcode.AccountAlreadyInState,
			errcode.Symbol(errcode.AccountAlreadyInState), map[string]any{})
		return
	}

	if *useProxy && !h.globalProxyConfigured() {
		api.WriteBizErr(w, reqID, errcode.ProxyURLRequired,
			errcode.Symbol(errcode.ProxyURLRequired), map[string]any{
				"field": "use_proxy",
				"detail": "network.proxy_url is not configured; " +
					"set it in the settings page first or keep use_proxy=false",
			})
		return
	}

	if err := h.accounts.UpdateUseProxy(r.Context(), id, *useProxy); err != nil {
		if errors.Is(err, domain.ErrAccountNotFound) {
			api.WriteBizErr(w, reqID, errcode.AccountNotFound,
				errcode.Symbol(errcode.AccountNotFound), map[string]any{})
			return
		}
		h.logger.Error("admin proxy toggle write failed",
			"request_id", reqID, "account_id", id, "error", err)
		api.WriteSysErr(w, reqID, errcode.InternalError,
			errcode.Symbol(errcode.InternalError))
		return
	}

	h.logger.Info("account use_proxy updated",
		"request_id", reqID,
		"account_id", id,
		"use_proxy", *useProxy,
	)
	api.WriteOK(w, reqID, map[string]any{
		"id":        id,
		"use_proxy": *useProxy,
	})
}

// globalProxyConfigured reads the live config slot. An unpublished
// slot (should not happen in steady state) counts as "not configured"
// so the toggle never promises a proxy the egress layer cannot see.
func (h *ProxyToggleHandler) globalProxyConfigured() bool {
	if h.reader == nil {
		return false
	}
	cfg := h.reader.Load()
	if cfg == nil {
		return false
	}
	return strings.TrimSpace(cfg.Network.ProxyURL) != ""
}

// maxSettingsProxyBodyBytes — the body is one bool; 1 KiB is generous
// and matches the adminapi habit of an explicit, documented cap.
const maxSettingsProxyBodyBytes = 1 << 10

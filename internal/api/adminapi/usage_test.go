package adminapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/user/one-llm-router/internal/core"
)

func TestUsageHandler_ReturnsAdminEnvelope(t *testing.T) {
	t.Parallel()

	handler := NewUsageHandler(usageReaderStub{
		summary: core.AdminUsageSummary{
			RequestCount:      3,
			TotalTokens:       25,
			CachedInputTokens: 5,
			Codex: core.AdminUsageCodexStatus{
				PlanType: "chatgpt-plus",
			},
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	RegisterUsageHandler(mux, handler, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, usageRoute, nil)

	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Header().Get("Content-Type"), "application/json")

	var body struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			RequestCount      int     `json:"request_count"`
			TotalTokens       int     `json:"total_tokens"`
			CachedInputTokens int     `json:"cached_input_tokens"`
			TotalCostUSD      float64 `json:"total_cost_usd"`
			Limits            []any   `json:"limits"`
			Codex             struct {
				PlanType             string `json:"plan_type"`
				RateLimit            any    `json:"rate_limit"`
				Credits              any    `json:"credits"`
				AdditionalRateLimits []any  `json:"additional_rate_limits"`
			} `json:"codex"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, 0, body.Code)
	assert.Equal(t, "ok", body.Msg)
	assert.Equal(t, 3, body.Data.RequestCount)
	assert.Equal(t, 25, body.Data.TotalTokens)
	assert.Equal(t, 5, body.Data.CachedInputTokens)
	assert.Equal(t, 0.0, body.Data.TotalCostUSD)
	assert.Empty(t, body.Data.Limits)
	assert.Equal(t, "chatgpt-plus", body.Data.Codex.PlanType)
	assert.Nil(t, body.Data.Codex.RateLimit)
	assert.Nil(t, body.Data.Codex.Credits)
	assert.Empty(t, body.Data.Codex.AdditionalRateLimits)
}

func TestUsageHandler_SystemErrorEnvelope(t *testing.T) {
	t.Parallel()

	handler := NewUsageHandler(usageReaderStub{err: errors.New("store offline")}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	RegisterUsageHandler(mux, handler, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, usageRoute, nil)

	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	var body struct {
		Code int            `json:"code"`
		Msg  string         `json:"msg"`
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, 6900, body.Code)
	assert.Equal(t, "usage_internal_error", body.Msg)
	assert.Empty(t, body.Data)
}

type usageReaderStub struct {
	summary core.AdminUsageSummary
	err     error
}

func (s usageReaderStub) AdminUsage(context.Context) (core.AdminUsageSummary, error) {
	if s.err != nil {
		return core.AdminUsageSummary{}, s.err
	}
	return s.summary, nil
}

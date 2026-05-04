package adminapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/one-llm-router/internal/api/errcode"
)

func TestRegisterRoutes(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux, &SettingsHandler{}, &WrappedHandler{})

	rows := []struct {
		method  string
		target  string
		pattern string
	}{
		{http.MethodGet, "/api/admin/settings", "GET /api/admin/settings"},
		{http.MethodPost, "/api/admin/settings/update", "POST /api/admin/settings/update"},
		{http.MethodGet, "/api/admin/health", "GET /api/admin/health"},
		{http.MethodPost, "/api/admin/accounts", "POST /api/admin/accounts"},
		{http.MethodGet, "/api/admin/accounts", "GET /api/admin/accounts"},
		{http.MethodGet, "/api/admin/accounts/7", "GET /api/admin/accounts/{id}"},
		{http.MethodPost, "/api/admin/accounts/7/enable", "POST /api/admin/accounts/{id}/enable"},
		{http.MethodPost, "/api/admin/accounts/7/disable", "POST /api/admin/accounts/{id}/disable"},
		{http.MethodPost, "/api/admin/accounts/7/delete", "POST /api/admin/accounts/{id}/delete"},
		{http.MethodGet, "/api/admin/requests", "GET /api/admin/requests"},
		{http.MethodGet, "/api/admin/sessions/resolve", "GET /api/admin/sessions/resolve"},
	}

	for _, row := range rows {
		req := httptest.NewRequest(row.method, row.target, nil)
		_, pattern := mux.Handler(req)
		assert.Equal(t, row.pattern, pattern)
	}
}

func TestWrappedRouteErrorMaps(t *testing.T) {
	assert.Equal(t, errcode.InvalidRequestFilter, first(defaultRequestsErrMap(http.StatusBadRequest, nil)))
	assert.Equal(t, errcode.InternalError, first(defaultRequestsErrMap(http.StatusInternalServerError, nil)))
	assert.Equal(t, errcode.InvalidRequestFilter, first(defaultSessionErrMap(http.StatusBadRequest, nil)))
	assert.Equal(t, errcode.NoAvailableAccount, first(defaultSessionErrMap(http.StatusServiceUnavailable, nil)))
	assert.Equal(t, errcode.InternalError, first(defaultSessionErrMap(http.StatusInternalServerError, nil)))
}

func TestWrappedHandlerWrap(t *testing.T) {
	ww := NewWrappedHandler(nil, nil, nil)

	t.Run("business errors preserve detail object", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/admin/requests", nil)
		ww.wrap(rec, req, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"bad filter"}`))
		}, defaultRequestsErrMap)

		var body struct {
			Code int            `json:"code"`
			Msg  string         `json:"msg"`
			Data map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, errcode.InvalidRequestFilter, body.Code)
		assert.Equal(t, "invalid_request_filter", body.Msg)
		assert.Equal(t, "bad filter", body.Data["detail"])
	})

	t.Run("non json 2xx bodies become system errors", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/admin/health", nil)
		ww.wrap(rec, req, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`not-json`))
		}, defaultRequestsErrMap)

		var body struct {
			Code int `json:"code"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Equal(t, errcode.InternalError, body.Code)
	})
}

func first(code int, _ string) int { return code }

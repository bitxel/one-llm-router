package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteRouterError(t *testing.T) {
	w := httptest.NewRecorder()
	WriteRouterError(w, http.StatusServiceUnavailable, ErrCodeNoAvailableAccount, "no accounts", "req_abc123")

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, "application/json; charset=utf-8", w.Header().Get("Content-Type"))
	assert.Equal(t, "req_abc123", w.Header().Get("X-Request-Id"))
	assert.NotContains(t, w.Body.String(), "request_id")

	var envelope RouterErrorEnvelope
	err := json.Unmarshal(w.Body.Bytes(), &envelope)
	require.NoError(t, err)

	assert.Equal(t, "router_error", envelope.Error.Type)
	assert.Equal(t, ErrCodeNoAvailableAccount, envelope.Error.Code)
	assert.Equal(t, "no accounts", envelope.Error.Message)
}

package core

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/user/one-llm-router/internal/domain"
)

func TestRedactCapturedBody_JSON(t *testing.T) {
	body := []byte(`{"input":"hello sk-live-secret-123456","max_output_tokens":128,"usage":{"input_tokens":3,"output_tokens":1},"nested":{"access_token":"secret-access-token"},"items":[{"Authorization":"Bearer token-secret-value"}]}`)

	got := RedactCapturedBody(body)

	assert.NotContains(t, got, "sk-live-secret")
	assert.NotContains(t, got, "secret-access-token")
	assert.NotContains(t, got, "Bearer token-secret-value")
	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(got), &decoded))
	assert.Equal(t, "hello [redacted]", decoded["input"])
	assert.Equal(t, float64(128), decoded["max_output_tokens"])
	assert.Equal(t, float64(3), decoded["usage"].(map[string]any)["input_tokens"])
	assert.Equal(t, float64(1), decoded["usage"].(map[string]any)["output_tokens"])
	assert.Equal(t, redactedValue, decoded["nested"].(map[string]any)["access_token"])
	assert.Equal(t, redactedValue, decoded["items"].([]any)[0].(map[string]any)["Authorization"])
}

func TestRedactCapturedBody_Text(t *testing.T) {
	got := RedactCapturedBody([]byte(`token=plain-secret-token Authorization: "Bearer bearer-secret-token" api_key=sk-live-secret-123456`))

	assert.NotContains(t, got, "plain-secret-token")
	assert.NotContains(t, got, "bearer-secret-token")
	assert.NotContains(t, got, "sk-live-secret")
	assert.Contains(t, got, "token=[redacted]")
	assert.Contains(t, got, "Authorization: [redacted]")
	assert.Contains(t, got, "api_key=[redacted]")
}

func TestRedactCapturedBody_TextContainingJSONFragments(t *testing.T) {
	got := RedactCapturedBody([]byte(strings.Join([]string{
		`event: response.delta`,
		`data: {"access_token":"secret-access-token","message":"Bearer bearer-secret-token"}`,
		`data: {\"refresh_token\":\"secret-refresh-token\"}`,
	}, "\n")))

	assert.NotContains(t, got, "secret-access-token")
	assert.NotContains(t, got, "bearer-secret-token")
	assert.NotContains(t, got, "secret-refresh-token")
	assert.Contains(t, got, `"access_token":"[redacted]"`)
	assert.Contains(t, got, `\"refresh_token\":\"[redacted]\"`)
}

func TestRedactJSONMap(t *testing.T) {
	got := RedactJSONMap(domain.JSONMap{
		"safe":         "value",
		"refreshToken": "secret-refresh",
		"nested": map[string]any{
			"message": "Bearer bearer-secret-token",
		},
	})

	assert.Equal(t, "value", got["safe"])
	assert.Equal(t, redactedValue, got["refreshToken"])
	assert.Equal(t, redactedValue, got["nested"].(map[string]any)["message"])
}

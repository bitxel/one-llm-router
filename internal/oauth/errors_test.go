package oauth

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSentinels(t *testing.T) {
	t.Run("flow in progress", func(t *testing.T) {
		err := ErrFlowInProgress
		assert.NotNil(t, err)
		assert.ErrorIs(t, fmt.Errorf("wrap: %w", err), ErrFlowInProgress)
		assert.False(t, errors.Is(ErrFlowInProgress, ErrFlowNotFound))
		assert.False(t, errors.Is(ErrFlowInProgress, ErrStateMismatch))
		assert.False(t, errors.Is(ErrFlowInProgress, ErrAlreadyConsumed))
		assert.False(t, errors.Is(ErrFlowInProgress, ErrFlowExpired))
	})

	t.Run("flow not found", func(t *testing.T) {
		err := ErrFlowNotFound
		assert.NotNil(t, err)
		assert.ErrorIs(t, fmt.Errorf("wrap: %w", err), ErrFlowNotFound)
		assert.False(t, errors.Is(ErrFlowNotFound, ErrStateMismatch))
		assert.False(t, errors.Is(ErrFlowNotFound, ErrAlreadyConsumed))
		assert.False(t, errors.Is(ErrFlowNotFound, ErrFlowExpired))
	})

	t.Run("state mismatch", func(t *testing.T) {
		err := ErrStateMismatch
		assert.NotNil(t, err)
		assert.ErrorIs(t, fmt.Errorf("wrap: %w", err), ErrStateMismatch)
		assert.False(t, errors.Is(ErrStateMismatch, ErrAlreadyConsumed))
		assert.False(t, errors.Is(ErrStateMismatch, ErrFlowExpired))
	})

	t.Run("already consumed", func(t *testing.T) {
		err := ErrAlreadyConsumed
		assert.NotNil(t, err)
		assert.ErrorIs(t, fmt.Errorf("wrap: %w", err), ErrAlreadyConsumed)
		assert.False(t, errors.Is(ErrAlreadyConsumed, ErrFlowExpired))
	})

	t.Run("flow expired", func(t *testing.T) {
		err := ErrFlowExpired
		assert.NotNil(t, err)
		assert.ErrorIs(t, fmt.Errorf("wrap: %w", err), ErrFlowExpired)
	})
}

func TestErrorWrappers_FormatAndUnwrapConsistently(t *testing.T) {
	root := errors.New("boom")

	var nilStoreErr *StoreError
	assert.Equal(t, "oauth store failed", nilStoreErr.Error())
	assert.Nil(t, nilStoreErr.Unwrap())

	storeErr := &StoreError{Op: "persist", Err: root}
	assert.Equal(t, "oauth store failed: persist: boom", storeErr.Error())
	assert.ErrorIs(t, storeErr, root)

	var nilInvalid *InvalidAuthJSONError
	assert.Equal(t, "oauth: invalid auth_json", nilInvalid.Error())

	invalid := &InvalidAuthJSONError{Reason: "missing_required_fields", MissingFields: []string{"tokens.id_token"}}
	assert.Contains(t, invalid.Error(), "missing_required_fields")
	assert.Contains(t, invalid.Error(), "tokens.id_token")

	var nilMalformed *malformedIDTokenError
	assert.Equal(t, ErrMalformedIDToken.Error(), nilMalformed.Error())
	assert.Nil(t, nilMalformed.Unwrap())

	malformed := &malformedIDTokenError{detail: "decode payload", cause: root}
	assert.Contains(t, malformed.Error(), "decode payload")
	assert.ErrorIs(t, malformed, ErrMalformedIDToken)
	assert.ErrorIs(t, malformed, root)
}

func TestOpenAIProviderConfigValidation(t *testing.T) {
	_, err := NewOpenAIProvider(OpenAIProviderConfig{
		AuthorizeURL: "https://auth.example.test/oauth/authorize",
	})
	require.EqualError(t, err, "oauth.NewOpenAIProvider: partial endpoint override config is not allowed; missing token_url, device_code_url, device_token_url")

	_, err = NewOpenAIProvider(OpenAIProviderConfig{
		AuthorizeURL:   "/oauth/authorize",
		TokenURL:       "https://auth.example.test/oauth/token",
		DeviceCodeURL:  "https://auth.example.test/device/code",
		DeviceTokenURL: "https://auth.example.test/device/token",
	})
	require.EqualError(t, err, "oauth.NewOpenAIProvider: authorize_url must be an absolute URL")

	_, err = NewOpenAIProvider(OpenAIProviderConfig{
		RedirectURI: "http://localhost:1456/auth/callback",
	})
	require.EqualError(t, err, `oauth.NewOpenAIProvider: redirect_uri must be "http://localhost:1455/auth/callback"`)

	provider, err := NewOpenAIProvider(OpenAIProviderConfig{
		AuthorizeURL:   "https://auth.example.test/oauth/authorize",
		TokenURL:       "https://auth.example.test/oauth/token",
		DeviceCodeURL:  "https://auth.example.test/device/code",
		DeviceTokenURL: "https://auth.example.test/device/token",
		RedirectURI:    "http://localhost:1455/auth/callback",
	})
	require.NoError(t, err)
	require.NotNil(t, provider)
}

func TestDefaultOAuthErrorHelpers(t *testing.T) {
	assert.Equal(t, "invalid_grant", defaultOAuthErrorCode("invalid_grant", http.StatusBadRequest))
	assert.Equal(t, "http_502", defaultOAuthErrorCode("", http.StatusBadGateway))
	assert.Equal(t, "invalid_response", defaultOAuthErrorCode("", 0))

	assert.Equal(t, "message", defaultOAuthErrorMessage("message", "invalid_grant", http.StatusBadRequest))
	assert.Equal(t, "invalid_grant", defaultOAuthErrorMessage("", "invalid_grant", http.StatusBadRequest))
	assert.Equal(t, http.StatusText(http.StatusBadGateway), defaultOAuthErrorMessage("", "", http.StatusBadGateway))
	assert.Equal(t, "invalid_response", defaultOAuthErrorMessage("", "", 0))
}

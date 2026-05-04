package oauth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestExtractClaims(t *testing.T) {
	t.Run("url style openai auth claims", func(t *testing.T) {
		token := makeJWT(t, map[string]any{
			"email": "alice@example.com",
			"https://api.openai.com/auth": map[string]any{
				"chatgpt_plan_type":  "plus",
				"chatgpt_account_id": "org_X",
			},
		})

		claims, err := ExtractClaims([]byte(token))
		if !assert.NoError(t, err) {
			return
		}
		assert.Equal(t, "alice@example.com", claims.Email)
		assert.Equal(t, "plus", claims.PlanType)
		assert.Equal(t, "org_X", claims.ChatGPTAccountID)
	})

	t.Run("url style legacy plan_type fallback", func(t *testing.T) {
		token := makeJWT(t, map[string]any{
			"email": "legacy-url@example.com",
			"https://api.openai.com/auth": map[string]any{
				"plan_type":          "chatgpt-plus",
				"chatgpt_account_id": "org_legacy_url",
			},
		})

		claims, err := ExtractClaims([]byte(token))
		if !assert.NoError(t, err) {
			return
		}
		assert.Equal(t, "legacy-url@example.com", claims.Email)
		assert.Equal(t, "chatgpt-plus", claims.PlanType)
		assert.Equal(t, "org_legacy_url", claims.ChatGPTAccountID)
	})

	t.Run("legacy auth fallback", func(t *testing.T) {
		token := makeJWT(t, map[string]any{
			"email": "bob@example.com",
			"auth": map[string]any{
				"chatgpt_plan_type":  "team",
				"chatgpt_account_id": "org_Y",
			},
		})

		claims, err := ExtractClaims([]byte(token))
		if !assert.NoError(t, err) {
			return
		}
		assert.Equal(t, "bob@example.com", claims.Email)
		assert.Equal(t, "team", claims.PlanType)
		assert.Equal(t, "org_Y", claims.ChatGPTAccountID)
	})

	t.Run("url style claims win over legacy fallback", func(t *testing.T) {
		token := makeJWT(t, map[string]any{
			"email": "dual@example.com",
			"https://api.openai.com/auth": map[string]any{
				"chatgpt_plan_type":  "plus",
				"plan_type":          "chatgpt-enterprise",
				"chatgpt_account_id": "org_preferred",
			},
			"auth": map[string]any{
				"chatgpt_plan_type":  "team",
				"chatgpt_account_id": "org_legacy",
			},
		})

		claims, err := ExtractClaims([]byte(token))
		if !assert.NoError(t, err) {
			return
		}
		assert.Equal(t, "plus", claims.PlanType)
		assert.Equal(t, "org_preferred", claims.ChatGPTAccountID)
	})

	t.Run("missing metadata is tolerated", func(t *testing.T) {
		token := makeJWT(t, map[string]any{
			"email": "carol@example.com",
		})

		claims, err := ExtractClaims([]byte(token))
		if !assert.NoError(t, err) {
			return
		}
		assert.Equal(t, "carol@example.com", claims.Email)
		assert.Empty(t, claims.PlanType)
		assert.Empty(t, claims.ChatGPTAccountID)
		assert.Nil(t, claims.ExpiresAt)
	})

	t.Run("exp claim is decoded when present", func(t *testing.T) {
		token := makeJWT(t, map[string]any{
			"email": "exp@example.com",
			"exp":   float64(1_776_193_200),
		})

		claims, err := ExtractClaims([]byte(token))
		if !assert.NoError(t, err) {
			return
		}
		if assert.NotNil(t, claims.ExpiresAt) {
			assert.Equal(t, time.Unix(1_776_193_200, 0).UTC(), *claims.ExpiresAt)
		}
	})

	t.Run("non-integer exp falls back to nil", func(t *testing.T) {
		token := makeJWT(t, map[string]any{
			"email": "exp-float@example.com",
			"exp":   12.5,
		})

		claims, err := ExtractClaims([]byte(token))
		if !assert.NoError(t, err) {
			return
		}
		assert.Nil(t, claims.ExpiresAt)
	})

	t.Run("two segment token is malformed", func(t *testing.T) {
		_, err := ExtractClaims([]byte("a.b"))
		if assert.Error(t, err) {
			assert.True(t, errors.Is(err, ErrMalformedIDToken))
		}
	})

	t.Run("invalid base64 payload is malformed", func(t *testing.T) {
		_, err := ExtractClaims([]byte("header.not*base64.signature"))
		if assert.Error(t, err) {
			assert.True(t, errors.Is(err, ErrMalformedIDToken))
			var decodeErr base64.CorruptInputError
			assert.True(t, errors.As(err, &decodeErr))
		}
	})

	t.Run("invalid json payload is malformed", func(t *testing.T) {
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
		payload := base64.RawURLEncoding.EncodeToString([]byte(`{"email":`))
		_, err := ExtractClaims([]byte(header + "." + payload + ".sig"))
		if assert.Error(t, err) {
			assert.True(t, errors.Is(err, ErrMalformedIDToken))
			var syntaxErr *json.SyntaxError
			assert.True(t, errors.As(err, &syntaxErr))
		}
	})
}

func makeJWT(t *testing.T, payload map[string]any) string {
	t.Helper()

	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	payloadPart := base64.RawURLEncoding.EncodeToString(body)
	signature := base64.RawURLEncoding.EncodeToString([]byte("sig"))
	return header + "." + payloadPart + "." + signature
}

package core

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/user/one-llm-router/internal/domain"
)

const redactedValue = "[redacted]"

var (
	bearerTokenPattern         = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9+/=_.-]{8,}`)
	apiKeyPattern              = regexp.MustCompile(`\bsk[-_][A-Za-z0-9_-]{8,}`)
	jwtPattern                 = regexp.MustCompile(`\beyJ[A-Za-z0-9._-]{40,}`)
	keyValuePattern            = regexp.MustCompile(`(?i)\b(access_token|refresh_token|id_token|api_key|authorization|token|secret|password|credential)(\s*[:=]\s*)("[^"]+"|[^\s&,;}]+)`)
	quotedJSONKeyValuePattern  = regexp.MustCompile(`(?i)("(?:access_token|refresh_token|id_token|api_key|authorization|token|secret|password|credential)"\s*:\s*")([^"]+)(")`)
	escapedJSONKeyValuePattern = regexp.MustCompile(`(?i)(\\"(?:access_token|refresh_token|id_token|api_key|authorization|token|secret|password|credential)\\"\s*:\s*\\")([^"\\]+)(\\")`)
)

// RedactCapturedBody sanitizes request/response bodies before they are written
// to request_records. Valid JSON is redacted structurally so secret-like keys
// are removed recursively; malformed JSON or plain text is scrubbed with token
// patterns.
func RedactCapturedBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var value any
	if err := json.Unmarshal(body, &value); err == nil {
		encoded, marshalErr := json.Marshal(RedactJSONValue(value))
		if marshalErr == nil {
			return string(encoded)
		}
	}
	return RedactString(string(body))
}

func RedactJSONMap(src domain.JSONMap) domain.JSONMap {
	if src == nil {
		return nil
	}
	sanitized, ok := RedactJSONValue(map[string]any(src)).(map[string]any)
	if !ok {
		return nil
	}
	return domain.JSONMap(sanitized)
}

func RedactJSONValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			if IsSecretLikeKey(key) {
				out[key] = redactedValue
				continue
			}
			out[key] = RedactJSONValue(item)
		}
		return out
	case domain.JSONMap:
		return RedactJSONValue(map[string]any(v))
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = RedactJSONValue(item)
		}
		return out
	case string:
		return RedactString(v)
	default:
		return value
	}
}

func IsSecretLikeKey(key string) bool {
	lower := strings.ToLower(key)
	for _, token := range []string{"authorization", "api_key", "bearer", "secret", "password", "credential"} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	for _, tokenKey := range []string{
		"token",
		"access_token",
		"accesstoken",
		"refresh_token",
		"refreshtoken",
		"id_token",
		"idtoken",
	} {
		if lower == tokenKey {
			return true
		}
	}
	return false
}

func RedactString(value string) string {
	value = quotedJSONKeyValuePattern.ReplaceAllString(value, `${1}`+redactedValue+`${3}`)
	value = escapedJSONKeyValuePattern.ReplaceAllString(value, `${1}`+redactedValue+`${3}`)
	value = bearerTokenPattern.ReplaceAllString(value, redactedValue)
	value = apiKeyPattern.ReplaceAllString(value, redactedValue)
	value = jwtPattern.ReplaceAllString(value, redactedValue)
	value = keyValuePattern.ReplaceAllString(value, `${1}${2}`+redactedValue)
	return value
}

func IsTokenLikeString(value string) bool {
	return RedactString(value) != value
}

package oauth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Claims struct {
	Email            string
	PlanType         string
	ChatGPTAccountID string
	ExpiresAt        *time.Time
}

type malformedIDTokenError struct {
	detail string
	cause  error
}

func (e *malformedIDTokenError) Error() string {
	if e == nil {
		return ErrMalformedIDToken.Error()
	}
	if e.cause == nil {
		return fmt.Sprintf("%s: %s", ErrMalformedIDToken, e.detail)
	}
	return fmt.Sprintf("%s: %s: %v", ErrMalformedIDToken, e.detail, e.cause)
}

func (e *malformedIDTokenError) Is(target error) bool {
	return target == ErrMalformedIDToken
}

func (e *malformedIDTokenError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func newMalformedIDTokenError(detail string, cause error) error {
	return &malformedIDTokenError{detail: detail, cause: cause}
}

func ExtractClaims(idToken []byte) (Claims, error) {
	parts := strings.Split(string(idToken), ".")
	if len(parts) != 3 {
		return Claims{}, newMalformedIDTokenError(
			fmt.Sprintf("expected 3 segments, got %d", len(parts)),
			nil,
		)
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, newMalformedIDTokenError("decode payload", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return Claims{}, newMalformedIDTokenError("decode claims JSON", err)
	}

	return Claims{
		Email: stringClaim(raw["email"]),
		PlanType: firstNonEmpty(
			nestedStringClaim(raw, "https://api.openai.com/auth", "chatgpt_plan_type"),
			nestedStringClaim(raw, "https://api.openai.com/auth", "plan_type"),
			nestedStringClaim(raw, "auth", "chatgpt_plan_type"),
			nestedStringClaim(raw, "auth", "plan_type"),
		),
		ChatGPTAccountID: firstNonEmpty(
			nestedStringClaim(raw, "https://api.openai.com/auth", "chatgpt_account_id"),
			nestedStringClaim(raw, "auth", "chatgpt_account_id"),
		),
		ExpiresAt: unixTimeClaim(raw["exp"]),
	}, nil
}

func nestedStringClaim(raw map[string]any, outer, inner string) string {
	value, ok := raw[outer]
	if !ok {
		return ""
	}

	nested, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	return stringClaim(nested[inner])
}

func stringClaim(value any) string {
	s, _ := value.(string)
	return s
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func unixTimeClaim(value any) *time.Time {
	switch v := value.(type) {
	case float64:
		if v != float64(int64(v)) {
			return nil
		}
		t := time.Unix(int64(v), 0).UTC()
		return &t
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return nil
		}
		t := time.Unix(n, 0).UTC()
		return &t
	default:
		return nil
	}
}

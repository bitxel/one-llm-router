package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/user/one-llm-router/internal/domain"
)

var ErrInvalidAuthJSONStructure = errors.New("oauth: invalid auth_json structure")

type InvalidAuthJSONError struct {
	Reason        string
	MissingFields []string
}

func (e *InvalidAuthJSONError) Error() string {
	if e == nil {
		return "oauth: invalid auth_json"
	}
	return fmt.Sprintf("oauth: invalid auth_json (%s): missing_fields=%v", e.Reason, e.MissingFields)
}

type AuthJSONImportStore interface {
	InsertUpstreamAccount(ctx context.Context, account *domain.UpstreamAccount) (int64, error)
}

type AuthJSONImportService struct {
	store AuthJSONImportStore
	now   func() time.Time
}

type AuthJSONImportResult struct {
	Account         *domain.UpstreamAccount
	FallbackReasons []string
}

type authJSONImportPayload struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
	AccountID    string
	LastRefresh  importStringField
}

type importStringField struct {
	Value   string
	Present bool
	Valid   bool
}

type derivedTime struct {
	value  time.Time
	reason string
}

func NewAuthJSONImportService(store AuthJSONImportStore, now func() time.Time) *AuthJSONImportService {
	if now == nil {
		now = time.Now
	}
	return &AuthJSONImportService{store: store, now: now}
}

func (s *AuthJSONImportService) Import(ctx context.Context, raw []byte) (*AuthJSONImportResult, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("oauth.AuthJSONImportService.Import: store is nil")
	}

	payload, missingFields, structureErr := parseImportAuthJSONPayload(raw)
	if structureErr {
		return nil, ErrInvalidAuthJSONStructure
	}
	if len(missingFields) > 0 {
		return nil, &InvalidAuthJSONError{
			Reason:        "missing_required_fields",
			MissingFields: missingFields,
		}
	}

	claims, err := ExtractClaims([]byte(payload.IDToken))
	if err != nil {
		if errors.Is(err, ErrMalformedIDToken) {
			return nil, &InvalidAuthJSONError{
				Reason:        "malformed_id_token",
				MissingFields: []string{"tokens.id_token"},
			}
		}
		return nil, fmt.Errorf("extract auth.json claims: %w", err)
	}

	chatgptAccountID := firstNonEmpty(claims.ChatGPTAccountID, payload.AccountID)
	accountName := firstNonEmpty(claims.Email, chatgptAccountID)
	if accountName == "" {
		return nil, &InvalidAuthJSONError{
			Reason:        "missing_identity_claims",
			MissingFields: []string{"email", "chatgpt_account_id", "tokens.account_id"},
		}
	}

	now := s.now().UTC()
	lastRefresh := deriveImportLastRefresh(payload.LastRefresh, now)
	accessExpiresAt := deriveImportAccessExpiresAt(claims.ExpiresAt, now)

	account := &domain.UpstreamAccount{
		Name:             accountName,
		Provider:         domain.ProviderOpenAI,
		Status:           domain.AccountStatusActive,
		AuthMethod:       domain.AuthMethodOAuthImport,
		AccessToken:      []byte(payload.AccessToken),
		RefreshToken:     []byte(payload.RefreshToken),
		IDToken:          []byte(payload.IDToken),
		LastRefresh:      &lastRefresh.value,
		AccessExpiresAt:  &accessExpiresAt.value,
		Email:            stringPtr(claims.Email),
		PlanType:         stringPtr(claims.PlanType),
		ChatGPTAccountID: stringPtr(chatgptAccountID),
	}
	if _, err := s.store.InsertUpstreamAccount(ctx, account); err != nil {
		return nil, fmt.Errorf("insert imported auth.json account: %w", err)
	}

	var fallbackReasons []string
	if lastRefresh.reason != "" {
		fallbackReasons = append(fallbackReasons, lastRefresh.reason)
	}
	if accessExpiresAt.reason != "" {
		fallbackReasons = append(fallbackReasons, accessExpiresAt.reason)
	}

	return &AuthJSONImportResult{
		Account:         account,
		FallbackReasons: fallbackReasons,
	}, nil
}

func parseImportAuthJSONPayload(raw []byte) (authJSONImportPayload, []string, bool) {
	var payload authJSONImportPayload
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return payload, nil, true
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &top); err != nil {
		return payload, nil, true
	}

	payload.LastRefresh = optionalStringField(top, "last_refresh")

	tokensRaw, ok := top["tokens"]
	if !ok {
		return payload, []string{"tokens.access_token", "tokens.refresh_token", "tokens.id_token"}, false
	}
	if !isJSONObject(tokensRaw) {
		return payload, nil, true
	}

	var tokens map[string]json.RawMessage
	if err := json.Unmarshal(tokensRaw, &tokens); err != nil {
		return payload, nil, true
	}

	var missingFields []string
	if value, ok := requiredNonEmptyString(tokens, "access_token"); ok {
		payload.AccessToken = value
	} else {
		missingFields = append(missingFields, "tokens.access_token")
	}
	if value, ok := requiredNonEmptyString(tokens, "refresh_token"); ok {
		payload.RefreshToken = value
	} else {
		missingFields = append(missingFields, "tokens.refresh_token")
	}
	if value, ok := requiredNonEmptyString(tokens, "id_token"); ok {
		payload.IDToken = value
	} else {
		missingFields = append(missingFields, "tokens.id_token")
	}
	accountID := optionalStringField(tokens, "account_id")
	if accountID.Valid {
		payload.AccountID = accountID.Value
	}

	return payload, missingFields, false
}

func deriveImportLastRefresh(field importStringField, now time.Time) derivedTime {
	if !field.Present {
		return derivedTime{value: now, reason: "payload_last_refresh_absent"}
	}
	if !field.Valid {
		return derivedTime{value: now, reason: "payload_last_refresh_unparseable"}
	}

	parsed, err := time.Parse(time.RFC3339, field.Value)
	if err != nil {
		return derivedTime{value: now, reason: "payload_last_refresh_unparseable"}
	}
	parsed = parsed.UTC()
	if parsed.After(now) {
		return derivedTime{value: now, reason: "payload_last_refresh_future"}
	}
	return derivedTime{value: parsed}
}

func deriveImportAccessExpiresAt(expiresAt *time.Time, now time.Time) derivedTime {
	if expiresAt == nil {
		return derivedTime{value: now.Add(5 * time.Minute), reason: "id_token_exp_absent"}
	}
	return derivedTime{value: expiresAt.UTC()}
}

func requiredNonEmptyString(fields map[string]json.RawMessage, key string) (string, bool) {
	field := optionalStringField(fields, key)
	if !field.Valid {
		return "", false
	}
	value := strings.TrimSpace(field.Value)
	if value == "" {
		return "", false
	}
	return value, true
}

func optionalStringField(fields map[string]json.RawMessage, key string) importStringField {
	raw, ok := fields[key]
	if !ok {
		return importStringField{}
	}

	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return importStringField{Present: true}
	}
	return importStringField{
		Value:   value,
		Present: true,
		Valid:   true,
	}
}

func isJSONObject(raw []byte) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '{'
}

func stringPtr(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

package core

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlaygroundService_Validate(t *testing.T) {
	svc := NewPlaygroundService(nil, nil, nil)

	t.Run("valid auto request trims fields and keeps optional max output", func(t *testing.T) {
		maxTokens := 128
		sessionKey := " sticky "
		got, err := svc.Validate(PlaygroundRunRequest{
			SelectionMode:   PlaygroundSelectionAuto,
			Endpoint:        PlaygroundEndpointChatCompletions,
			SessionKey:      &sessionKey,
			Model:           " gpt-5.4-mini ",
			Text:            " hello ",
			MaxOutputTokens: &maxTokens,
		})
		require.NoError(t, err)
		assert.Equal(t, PlaygroundEndpointChatCompletions, got.Endpoint)
		assert.Equal(t, "gpt-5.4-mini", got.Model)
		assert.Equal(t, "hello", got.Text)
		require.NotNil(t, got.SessionKey)
		assert.Equal(t, "sticky", *got.SessionKey)
		assert.Equal(t, 128, got.EffectiveMaxOutputTokens())
	})

	t.Run("valid account request requires positive account id", func(t *testing.T) {
		accountID := int64(42)
		got, err := svc.Validate(PlaygroundRunRequest{
			SelectionMode: PlaygroundSelectionAccount,
			AccountID:     &accountID,
			Model:         "gpt-5.4-mini",
			Text:          "hello",
		})
		require.NoError(t, err)
		assert.Equal(t, int64(42), *got.AccountID)
		assert.Equal(t, PlaygroundEndpointResponses, got.EffectiveEndpoint())
		assert.Equal(t, PlaygroundDefaultMaxOutputTokens, got.EffectiveMaxOutputTokens())
	})

	t.Run("text boundary counts unicode characters", func(t *testing.T) {
		for name, text := range map[string]string{
			"ascii":   strings.Repeat("a", PlaygroundPromptLimitChars),
			"unicode": strings.Repeat("界", PlaygroundPromptLimitChars),
		} {
			t.Run(name, func(t *testing.T) {
				_, err := svc.Validate(PlaygroundRunRequest{
					SelectionMode: PlaygroundSelectionAuto,
					Model:         "gpt-5.4-mini",
					Text:          text,
				})
				require.NoError(t, err)
			})
		}
	})

	tests := []struct {
		name  string
		req   PlaygroundRunRequest
		field PlaygroundValidationField
	}{
		{
			name:  "invalid selection mode",
			req:   validPlaygroundRequest(func(r *PlaygroundRunRequest) { r.SelectionMode = "manual" }),
			field: PlaygroundFieldSelectionMode,
		},
		{
			name:  "invalid endpoint",
			req:   validPlaygroundRequest(func(r *PlaygroundRunRequest) { r.Endpoint = "completions" }),
			field: PlaygroundFieldEndpoint,
		},
		{
			name:  "account mode missing id",
			req:   validPlaygroundRequest(func(r *PlaygroundRunRequest) { r.SelectionMode = PlaygroundSelectionAccount }),
			field: PlaygroundFieldAccountID,
		},
		{
			name: "account mode non-positive id",
			req: validPlaygroundRequest(func(r *PlaygroundRunRequest) {
				id := int64(0)
				r.SelectionMode = PlaygroundSelectionAccount
				r.AccountID = &id
			}),
			field: PlaygroundFieldAccountID,
		},
		{
			name: "auto mode rejects account id",
			req: validPlaygroundRequest(func(r *PlaygroundRunRequest) {
				id := int64(42)
				r.AccountID = &id
			}),
			field: PlaygroundFieldAccountID,
		},
		{
			name:  "empty model",
			req:   validPlaygroundRequest(func(r *PlaygroundRunRequest) { r.Model = "   " }),
			field: PlaygroundFieldModel,
		},
		{
			name:  "model too long",
			req:   validPlaygroundRequest(func(r *PlaygroundRunRequest) { r.Model = strings.Repeat("m", 129) }),
			field: PlaygroundFieldModel,
		},
		{
			name:  "empty text",
			req:   validPlaygroundRequest(func(r *PlaygroundRunRequest) { r.Text = " \n\t " }),
			field: PlaygroundFieldText,
		},
		{
			name:  "text too long",
			req:   validPlaygroundRequest(func(r *PlaygroundRunRequest) { r.Text = strings.Repeat("a", PlaygroundPromptLimitChars+1) }),
			field: PlaygroundFieldText,
		},
		{
			name: "empty session key",
			req: validPlaygroundRequest(func(r *PlaygroundRunRequest) {
				v := " "
				r.SessionKey = &v
			}),
			field: PlaygroundFieldSessionKey,
		},
		{
			name: "session key too long",
			req: validPlaygroundRequest(func(r *PlaygroundRunRequest) {
				v := strings.Repeat("s", 129)
				r.SessionKey = &v
			}),
			field: PlaygroundFieldSessionKey,
		},
		{
			name: "max output too small",
			req: validPlaygroundRequest(func(r *PlaygroundRunRequest) {
				v := 0
				r.MaxOutputTokens = &v
			}),
			field: PlaygroundFieldMaxOutputTokens,
		},
		{
			name: "max output too large",
			req: validPlaygroundRequest(func(r *PlaygroundRunRequest) {
				v := PlaygroundMaxOutputTokens + 1
				r.MaxOutputTokens = &v
			}),
			field: PlaygroundFieldMaxOutputTokens,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Validate(tc.req)
			require.Error(t, err)
			var validationErr *PlaygroundValidationError
			require.ErrorAs(t, err, &validationErr)
			assert.Equal(t, tc.field, validationErr.Field)
		})
	}
}

func TestPlaygroundService_NilReceiverGuards(t *testing.T) {
	var svc *PlaygroundService

	require.NotPanics(t, func() {
		svc.SetClock(nil)
		svc.SetRecorder(nil)
		svc.SetBodyLogFunc(nil)
	})

	_, err := svc.Run(context.Background(), validPlaygroundRequest())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "selector is nil")
}

func validPlaygroundRequest(mutators ...func(*PlaygroundRunRequest)) PlaygroundRunRequest {
	req := PlaygroundRunRequest{
		SelectionMode: PlaygroundSelectionAuto,
		Model:         "gpt-5.4-mini",
		Text:          "hello",
	}
	for _, mutate := range mutators {
		mutate(&req)
	}
	return req
}

package core

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/one-llm-router/internal/api/errcode"
	"github.com/user/one-llm-router/internal/clientip"
	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/requestid"
)

func TestPlaygroundService_Recording(t *testing.T) {
	t.Run("success records safe metadata with body logging disabled", func(t *testing.T) {
		sessionKey := "sticky"
		maxOutput := 128
		recorder := &recordingPlaygroundRecorder{}
		svc := playgroundServiceWithUpstream(t, &fakePlaygroundUpstream{
			result: &PlaygroundUpstreamResult{
				StatusCode:       200,
				ResponseMode:     domain.ResponseModeJSON,
				UpstreamEndpoint: "/codex/responses",
				Text:             "pong",
				TextAvailable:    true,
				Usage:            map[string]int{"input": 3, "output": 2},
				RawResponse:      domain.JSONMap{"id": "resp_123"},
			},
		})
		svc.SetRecorder(recorder)
		svc.SetBodyLogFunc(func() (bool, bool, bool) { return false, false, false })

		ctx := requestid.WithContext(context.Background(), "req-playground-success")
		ctx = clientip.WithContext(ctx, "203.0.113.88")
		_, err := svc.Run(ctx, PlaygroundRunRequest{
			SelectionMode:   PlaygroundSelectionAuto,
			SessionKey:      &sessionKey,
			Model:           "gpt-5.4-mini",
			Text:            "ping",
			MaxOutputTokens: &maxOutput,
		})

		require.NoError(t, err)
		require.Len(t, recorder.records, 1)
		rec := recorder.records[0]
		require.NotNil(t, rec.UpstreamAccountID)
		assert.Equal(t, int64(7), *rec.UpstreamAccountID)
		require.NotNil(t, rec.SessionKey)
		assert.Equal(t, "sticky", *rec.SessionKey)
		assert.Equal(t, "req-playground-success", rec.RequestID)
		assert.Equal(t, "203.0.113.88", rec.ClientIP)
		assert.Equal(t, "POST", rec.Method)
		assert.Equal(t, PlaygroundRunPath, rec.Path)
		assert.Equal(t, 200, rec.StatusCode)
		assert.Equal(t, domain.OutcomeSuccess, rec.Outcome)
		require.NotNil(t, rec.Model)
		assert.Equal(t, "gpt-5.4-mini", *rec.Model)
		assert.Equal(t, "auto", rec.ModelParams["selection_mode"])
		assert.Equal(t, "responses", rec.ModelParams["endpoint"])
		assert.Equal(t, 128, rec.ModelParams["max_output_tokens"])
		assert.Equal(t, false, rec.ModelParams["include_raw_response"])
		assert.Equal(t, domain.JSONMap{"upstream_endpoint": "/codex/responses"}, rec.RouterMetadata)
		assert.Equal(t, 3, rec.TokenUsage["input"])
		assert.Equal(t, 2, rec.TokenUsage["output"])
		assert.Nil(t, rec.ClientRequestBody)
		assert.Nil(t, rec.UpstreamRequestBody)
		assert.Nil(t, rec.UpstreamResponseBody)
	})

	t.Run("body logging captures sanitized prompt and provider JSON only when enabled", func(t *testing.T) {
		recorder := &recordingPlaygroundRecorder{}
		svc := playgroundServiceWithUpstream(t, &fakePlaygroundUpstream{
			result: &PlaygroundUpstreamResult{
				StatusCode:          200,
				ResponseMode:        domain.ResponseModeJSON,
				UpstreamRequestBody: []byte(`{"model":"gpt-5.4-mini","input":"prompt with sk-secret-token","stream":false}`),
				Text:                "ok",
				TextAvailable:       true,
				RawResponse: domain.JSONMap{
					"id":           "resp_secret",
					"access_token": "secret-access",
					"nested": map[string]any{
						"message": "Bearer sk-secret-token",
					},
				},
				RawResponseAvailable: true,
			},
		})
		svc.SetRecorder(recorder)
		svc.SetBodyLogFunc(func() (bool, bool, bool) { return true, true, true })

		_, err := svc.Run(requestid.WithContext(context.Background(), "req-playground-body"), PlaygroundRunRequest{
			SelectionMode: PlaygroundSelectionAuto,
			Model:         "gpt-5.4-mini",
			Text:          "prompt with sk-secret-token",
		})

		require.NoError(t, err)
		require.Len(t, recorder.records, 1)
		rec := recorder.records[0]
		require.NotNil(t, rec.ClientRequestBody)
		assert.Contains(t, *rec.ClientRequestBody, `"text":"[redacted]"`)
		assert.NotContains(t, *rec.ClientRequestBody, "sk-secret-token")
		require.NotNil(t, rec.UpstreamRequestBody)
		assert.Contains(t, *rec.UpstreamRequestBody, `"input":"prompt with [redacted]"`)
		assert.NotContains(t, *rec.UpstreamRequestBody, "sk-secret-token")
		require.NotNil(t, rec.UpstreamResponseBody)
		assert.Contains(t, *rec.UpstreamResponseBody, `"access_token":"[redacted]"`)
		assert.NotContains(t, *rec.UpstreamResponseBody, "secret-access")
		assert.NotContains(t, *rec.UpstreamResponseBody, "sk-secret-token")
	})

	t.Run("business failures record registered outcome and error code without upstream body", func(t *testing.T) {
		recorder := &recordingPlaygroundRecorder{}
		repo := &mockAccountRepo{}
		upstream := &fakePlaygroundUpstream{}
		svc := NewPlaygroundService(repo, NewAccountSelector(repo, nil), upstream)
		svc.SetRecorder(recorder)

		_, err := svc.Run(requestid.WithContext(context.Background(), "req-playground-empty"), PlaygroundRunRequest{
			SelectionMode: PlaygroundSelectionAuto,
			Model:         "gpt-5.4-mini",
			Text:          "ping",
		})

		require.ErrorIs(t, err, ErrPlaygroundNoActiveAccount)
		require.Len(t, recorder.records, 1)
		rec := recorder.records[0]
		assert.Nil(t, rec.UpstreamAccountID)
		assert.Equal(t, domain.OutcomeNoAvailableAccount, rec.Outcome)
		require.NotNil(t, rec.ErrorCode)
		assert.Equal(t, errcode.Symbol(errcode.PlaygroundNoActiveAccount), *rec.ErrorCode)
		assert.Equal(t, 0, upstream.calls)
	})

	t.Run("client cancellation records cancelled outcome without credential bytes", func(t *testing.T) {
		recorder := &recordingPlaygroundRecorder{}
		ctx, cancel := context.WithCancel(requestid.WithContext(context.Background(), "req-playground-cancel"))
		cancel()
		svc := playgroundServiceWithUpstream(t, &fakePlaygroundUpstream{
			err: context.Canceled,
		})
		svc.SetRecorder(recorder)
		svc.SetBodyLogFunc(func() (bool, bool, bool) { return true, true, true })

		_, err := svc.Run(ctx, PlaygroundRunRequest{
			SelectionMode: PlaygroundSelectionAuto,
			Model:         "gpt-5.4-mini",
			Text:          "ping",
		})

		require.Error(t, err)
		assert.True(t, errors.Is(err, context.Canceled))
		require.Len(t, recorder.records, 1)
		rec := recorder.records[0]
		assert.Equal(t, domain.OutcomeCancelled, rec.Outcome)
		assert.Equal(t, playgroundClientClosedStatus, rec.StatusCode)
		require.NotNil(t, rec.UpstreamAccountID)
		assert.Equal(t, int64(7), *rec.UpstreamAccountID)
		assert.NotContains(t, recordText(rec), "sk-test")
	})

	t.Run("upstream errors record sanitized upstream request and response bodies", func(t *testing.T) {
		recorder := &recordingPlaygroundRecorder{}
		svc := playgroundServiceWithUpstream(t, &fakePlaygroundUpstream{
			err: &PlaygroundUpstreamError{
				UpstreamStatus:       429,
				UpstreamEndpoint:     "/codex/responses",
				UpstreamRequestBody:  []byte(`{"model":"gpt-5.4-mini","input":"sk-upstream-secret-123456"}`),
				UpstreamResponseBody: []byte(`{"error":{"message":"Bearer provider-secret-token","access_token":"secret-access-token"}}`),
			},
		})
		svc.SetRecorder(recorder)
		svc.SetBodyLogFunc(func() (bool, bool, bool) { return true, true, true })

		_, err := svc.Run(requestid.WithContext(context.Background(), "req-playground-upstream-error"), PlaygroundRunRequest{
			SelectionMode: PlaygroundSelectionAuto,
			Model:         "gpt-5.4-mini",
			Text:          "hello",
		})

		require.Error(t, err)
		require.Len(t, recorder.records, 1)
		rec := recorder.records[0]
		assert.Equal(t, domain.OutcomeUpstreamError, rec.Outcome)
		assert.Equal(t, domain.JSONMap{"upstream_endpoint": "/codex/responses"}, rec.RouterMetadata)
		require.NotNil(t, rec.UpstreamRequestBody)
		require.NotNil(t, rec.UpstreamResponseBody)
		assert.NotContains(t, *rec.UpstreamRequestBody, "sk-upstream-secret")
		assert.NotContains(t, *rec.UpstreamResponseBody, "provider-secret-token")
		assert.NotContains(t, *rec.UpstreamResponseBody, "secret-access-token")
		assert.Contains(t, *rec.UpstreamRequestBody, "[redacted]")
		assert.Contains(t, *rec.UpstreamResponseBody, "[redacted]")
	})

	t.Run("connect failures keep attempted upstream request body", func(t *testing.T) {
		recorder := &recordingPlaygroundRecorder{}
		svc := playgroundServiceWithUpstream(t, &fakePlaygroundUpstream{
			err: &PlaygroundUpstreamConnectError{
				UpstreamEndpoint:    "/v1/responses",
				UpstreamRequestBody: []byte(`{"model":"gpt-5.4-mini","input":"sk-connect-secret-123456"}`),
			},
		})
		svc.SetRecorder(recorder)
		svc.SetBodyLogFunc(func() (bool, bool, bool) { return true, true, true })

		_, err := svc.Run(requestid.WithContext(context.Background(), "req-playground-connect"), PlaygroundRunRequest{
			SelectionMode: PlaygroundSelectionAuto,
			Model:         "gpt-5.4-mini",
			Text:          "hello",
		})

		require.Error(t, err)
		require.Len(t, recorder.records, 1)
		rec := recorder.records[0]
		assert.Equal(t, domain.OutcomeRouterError, rec.Outcome)
		assert.Equal(t, domain.JSONMap{"upstream_endpoint": "/v1/responses"}, rec.RouterMetadata)
		require.NotNil(t, rec.UpstreamRequestBody)
		assert.NotContains(t, *rec.UpstreamRequestBody, "sk-connect-secret")
		assert.Contains(t, *rec.UpstreamRequestBody, "[redacted]")
	})
}

type recordingPlaygroundRecorder struct {
	records []domain.RequestRecord
}

func (r *recordingPlaygroundRecorder) Record(_ context.Context, rec domain.RequestRecord) {
	r.records = append(r.records, rec)
}

func recordText(rec domain.RequestRecord) string {
	var b strings.Builder
	if rec.ClientRequestBody != nil {
		b.WriteString(*rec.ClientRequestBody)
	}
	if rec.UpstreamRequestBody != nil {
		b.WriteString(*rec.UpstreamRequestBody)
	}
	if rec.UpstreamResponseBody != nil {
		b.WriteString(*rec.UpstreamResponseBody)
	}
	return b.String()
}

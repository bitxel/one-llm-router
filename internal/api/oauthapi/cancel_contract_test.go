package oauthapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/user/one-llm-router/internal/api/errcode"

	"github.com/user/one-llm-router/internal/api/testutil"
	generatedadminapi "github.com/user/one-llm-router/internal/generated/adminapi"
	"github.com/user/one-llm-router/internal/oauth"
)

func TestCancelContract(t *testing.T) {
	t.Run("matching flow id cancels to idle", func(t *testing.T) {
		h := newCancelHarness(t)
		flow := h.newPendingFlow("fl_cancel_happy_12345678901234567890")
		require.NoError(t, h.coord.TryStartFlow(flow))

		rec := h.post(t, `{"flow_id":"fl_cancel_happy_12345678901234567890"}`)

		data := testutil.AssertEnvelopeDataShape(t, rec, 0)
		assert.Equal(t, map[string]any{"status": "idle"}, data)

		body := decodeCancelResponse(t, rec)
		env, err := body.AsCancelSuccessEnvelope()
		require.NoError(t, err)
		assert.Equal(t, generatedadminapi.CancelSuccessEnvelopeDataStatusIdle, env.Data.Status)
		assert.Nil(t, h.coord.CurrentFlow())
	})

	t.Run("mismatched flow id returns expected flow id", func(t *testing.T) {
		h := newCancelHarness(t)
		flow := h.newPendingFlow("fl_cancel_expected_12345678901234567890")
		require.NoError(t, h.coord.TryStartFlow(flow))

		rec := h.post(t, `{"flow_id":"fl_cancel_wrong_1234567890123456789012"}`)

		data := testutil.AssertEnvelopeDataShape(t, rec, 3008)
		assert.Equal(t, map[string]any{
			"expected_flow_id": "fl_cancel_expected_12345678901234567890",
		}, data)

		body := decodeCancelResponse(t, rec)
		env, err := body.AsFlowIDMismatchEnvelope()
		require.NoError(t, err)
		require.NotNil(t, env.Data.ExpectedFlowId)
		assert.Equal(t, "fl_cancel_expected_12345678901234567890", *env.Data.ExpectedFlowId)
		require.NotNil(t, h.coord.CurrentFlow())
	})

	t.Run("idle router returns idle success", func(t *testing.T) {
		h := newCancelHarness(t)

		rec := h.post(t, `{"flow_id":"fl_cancel_idle_1234567890123456789012"}`)

		data := testutil.AssertEnvelopeDataShape(t, rec, 0)
		assert.Equal(t, map[string]any{"status": "idle"}, data)

		body := decodeCancelResponse(t, rec)
		env, err := body.AsCancelSuccessEnvelope()
		require.NoError(t, err)
		assert.Equal(t, generatedadminapi.CancelSuccessEnvelopeDataStatusIdle, env.Data.Status)
		assert.Nil(t, h.coord.CurrentFlow())
	})

	t.Run("consumed live flow is treated as idle", func(t *testing.T) {
		flow := &oauth.Flow{
			ID:     "fl_cancel_consumed_123456789012345678",
			Method: oauth.FlowBrowser,
		}
		flow.Consumed.Store(true)
		var cancelCalls int
		server := newOAuthAPIServerForTest(&stubCoordinator{
			currentFlowFn: func() *oauth.Flow {
				return flow
			},
			cancelWithContextFn: func(context.Context, string) error {
				cancelCalls++
				return nil
			},
		})

		rec := requestCancel(server, `{"flow_id":"fl_cancel_consumed_123456789012345678"}`)

		data := testutil.AssertEnvelopeDataShape(t, rec, 0)
		assert.Equal(t, map[string]any{"status": "idle"}, data)

		body := decodeCancelResponse(t, rec)
		env, err := body.AsCancelSuccessEnvelope()
		require.NoError(t, err)
		assert.Equal(t, generatedadminapi.CancelSuccessEnvelopeDataStatusIdle, env.Data.Status)
		assert.Equal(t, 1, cancelCalls)
	})

	t.Run("unexpected cancel failure is oauth internal error", func(t *testing.T) {
		now := time.Date(2026, 4, 21, 19, 30, 0, 0, time.UTC)
		flow := &oauth.Flow{
			ID:        "fl_cancel_system_error_1234567890",
			Method:    oauth.FlowBrowser,
			Status:    oauth.FlowStatusPending,
			State:     "s_cancel_system_error",
			CreatedAt: now,
			ExpiresAt: now.Add(5 * time.Minute),
		}
		server := newOAuthAPIServerForTest(&stubCoordinator{
			currentFlowFn: func() *oauth.Flow {
				return flow
			},
			cancelWithContextFn: func(context.Context, string) error {
				return errors.New("cancel exploded")
			},
		})

		rec := requestCancel(server, `{"flow_id":"fl_cancel_system_error_1234567890"}`)

		testutil.AssertEnvelopeDataShape(t, rec, errcode.OAuthInternalError)
	})
}

type cancelHarness struct {
	coord  *oauth.Coordinator
	server http.Handler
}

func newCancelHarness(t *testing.T) *cancelHarness {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	coord := oauth.NewCoordinatorWithClock(
		oauth.NewFakeClock(time.Date(2026, 4, 21, 19, 0, 0, 0, time.UTC)),
		nil,
		logger,
	)
	server := generatedadminapi.HandlerFromMuxWithEnvelope(NewHandlerWithLogger(coord, logger), http.NewServeMux())

	t.Cleanup(func() {
		if flow := coord.CurrentFlow(); flow != nil {
			coord.ReleaseFlow(flow.ID)
		}
	})

	return &cancelHarness{
		coord:  coord,
		server: server,
	}
}

func (h *cancelHarness) newPendingFlow(flowID string) *oauth.Flow {
	now := h.coord.Now()
	return &oauth.Flow{
		ID:        flowID,
		Method:    oauth.FlowBrowser,
		Status:    oauth.FlowStatusPending,
		State:     "s_cancel",
		CreatedAt: now,
		ExpiresAt: now.Add(5 * time.Minute),
	}
}

func (h *cancelHarness) post(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()

	rec := requestCancel(h.server, body)
	require.Equal(t, http.StatusOK, rec.Code)
	return rec
}

func requestCancel(server http.Handler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/admin/oauth/cancel", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	return rec
}

func decodeCancelResponse(t *testing.T, rec *httptest.ResponseRecorder) generatedadminapi.OAuthCancelResponseBody {
	t.Helper()

	var body generatedadminapi.OAuthCancelResponseBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body
}

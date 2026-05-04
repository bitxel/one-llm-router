package oauth

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/one-llm-router/internal/domain"
)

func TestGetFlow(t *testing.T) {
	t.Run("pending browser returns live snapshot", func(t *testing.T) {
		now := time.Date(2026, 4, 21, 16, 0, 0, 0, time.UTC)
		coord := NewCoordinatorWithClock(NewFakeClock(now), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		flow := &Flow{
			ID:            "fl_pending_browser_status_1234567890",
			Method:        FlowBrowser,
			Status:        FlowStatusPending,
			ListenerBound: true,
			CreatedAt:     now,
			ExpiresAt:     now.Add(5 * time.Minute),
		}

		require.NoError(t, coord.TryStartFlow(flow))

		snapshot, err := coord.GetFlow()
		require.NoError(t, err)
		assert.Equal(t, FlowStatusPending, snapshot.Status)
		assert.Equal(t, FlowBrowser, snapshot.Method)
		assert.Equal(t, flow.ID, snapshot.FlowID)
		assert.True(t, snapshot.ListenerBound)
	})

	t.Run("success is one shot but released metadata remains for late consumers", func(t *testing.T) {
		now := time.Date(2026, 4, 21, 16, 30, 0, 0, time.UTC)
		coord := NewCoordinatorWithClock(NewFakeClock(now), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		flow := &Flow{
			ID:         "fl_success_status_1234567890",
			Method:     FlowBrowser,
			Status:     FlowStatusSuccess,
			ConsumedBy: RailLoopback,
			CreatedAt:  now,
			ExpiresAt:  now.Add(5 * time.Minute),
			terminalAccount: &domain.UpstreamAccount{
				ID:         77,
				Name:       "op@example.com",
				Provider:   domain.ProviderOpenAI,
				AuthMethod: domain.AuthMethodOAuthBrowser,
				Status:     domain.AccountStatusActive,
			},
		}

		require.NoError(t, coord.TryStartFlow(flow))
		coord.ReleaseFlow(flow.ID)

		first, err := coord.GetFlow()
		require.NoError(t, err)
		require.NotNil(t, first.Account)
		assert.Equal(t, FlowStatusSuccess, first.Status)
		assert.Equal(t, RailLoopback, first.Rail)
		assert.Equal(t, int64(77), first.Account.ID)
		assert.NotNil(t, coord.loadReleasedFlow(flow.ID))

		second, err := coord.GetFlow()
		require.NoError(t, err)
		assert.Equal(t, FlowStatusIdle, second.Status)

		winner, ok := coord.WinnerRail(flow.ID)
		assert.True(t, ok)
		assert.Equal(t, RailLoopback, winner)
	})

	t.Run("error is one shot", func(t *testing.T) {
		now := time.Date(2026, 4, 21, 17, 0, 0, 0, time.UTC)
		coord := NewCoordinatorWithClock(NewFakeClock(now), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		flow := &Flow{
			ID:        "fl_error_status_1234567890",
			Method:    FlowBrowser,
			Status:    FlowStatusError,
			CreatedAt: now,
			ExpiresAt: now.Add(5 * time.Minute),
			terminalError: &FlowTerminalError{
				Code:    accessDeniedCode,
				Message: accessDeniedMessage,
			},
		}

		require.NoError(t, coord.TryStartFlow(flow))
		coord.ReleaseFlow(flow.ID)

		first, err := coord.GetFlow()
		require.NoError(t, err)
		require.NotNil(t, first.Error)
		assert.Equal(t, FlowStatusError, first.Status)
		assert.Equal(t, accessDeniedCode, first.Error.Code)
		assert.Equal(t, accessDeniedMessage, first.Error.Message)

		second, err := coord.GetFlow()
		require.NoError(t, err)
		assert.Equal(t, FlowStatusIdle, second.Status)
	})

	t.Run("device terminal error snapshot uses provider-facing expired token code", func(t *testing.T) {
		now := time.Date(2026, 4, 21, 17, 15, 0, 0, time.UTC)
		coord := NewCoordinatorWithClock(NewFakeClock(now), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		flow := &Flow{
			ID:              "fl_device_error_status_1234567890",
			Method:          FlowDevice,
			Status:          FlowStatusError,
			CreatedAt:       now,
			ExpiresAt:       now.Add(15 * time.Minute),
			UserCode:        "ABCD-1234",
			VerificationURL: openAIDeviceVerificationURL,
			terminalError: &FlowTerminalError{
				Code:    expiredTokenCode,
				Message: expiredTokenMessage,
			},
		}

		require.NoError(t, coord.TryStartFlow(flow))
		coord.ReleaseFlow(flow.ID)

		snapshot, err := coord.GetFlow()
		require.NoError(t, err)
		require.NotNil(t, snapshot.Error)
		assert.Equal(t, FlowDevice, snapshot.Method)
		assert.Equal(t, expiredTokenCode, snapshot.Error.Code)
		assert.Equal(t, expiredTokenMessage, snapshot.Error.Message)
	})

	t.Run("malformed terminal success fails fast", func(t *testing.T) {
		now := time.Date(2026, 4, 21, 17, 30, 0, 0, time.UTC)
		coord := NewCoordinatorWithClock(NewFakeClock(now), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		flow := &Flow{
			ID:         "fl_broken_status_1234567890",
			Method:     FlowBrowser,
			Status:     FlowStatusSuccess,
			ConsumedBy: RailLoopback,
			CreatedAt:  now,
			ExpiresAt:  now.Add(5 * time.Minute),
		}

		require.NoError(t, coord.TryStartFlow(flow))
		coord.ReleaseFlow(flow.ID)

		_, err := coord.GetFlow()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "success account is nil")
	})
}

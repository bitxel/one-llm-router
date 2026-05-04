package oauthapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/one-llm-router/internal/domain"
	generatedadminapi "github.com/user/one-llm-router/internal/generated/adminapi"
	"github.com/user/one-llm-router/internal/oauth"
)

func TestHandlerStubsAndHelpers(t *testing.T) {
	t.Run("unimplemented endpoints stay explicit", func(t *testing.T) {
		h := &Handler{}
		var err error

		_, err = h.AccountsImportAuthJSON(context.Background(), generatedadminapi.AccountsImportAuthJSONRequestObject{})
		require.EqualError(t, err, "oauthapi.Handler.AccountsImportAuthJSON: not implemented")

		_, err = h.AccountsExportAuthJSON(context.Background(), generatedadminapi.AccountsExportAuthJSONRequestObject{})
		require.EqualError(t, err, "oauthapi.Handler.AccountsExportAuthJSON: not implemented")
	})

	t.Run("oauth flow status requires coordinator", func(t *testing.T) {
		h := &Handler{}
		resp, err := h.OauthFlowStatus(context.Background(), generatedadminapi.OauthFlowStatusRequestObject{})
		require.NoError(t, err)
		_, ok := resp.(generatedadminapi.OauthFlowStatus500JSONResponse)
		assert.True(t, ok)
	})

	t.Run("oauth system error classifier maps store failures to 3901 only", func(t *testing.T) {
		assert.Equal(t, 3900, oauthSystemErrorCode(errors.New("plain")))
		assert.Equal(t, 3901, oauthSystemErrorCode(&oauth.StoreError{Op: "insert account", Err: errors.New("disk full")}))
	})

	t.Run("account auth method mapping covers all supported values", func(t *testing.T) {
		cases := []struct {
			name   string
			input  domain.AuthMethod
			want   generatedadminapi.AccountListItemAuthMethod
			hasErr bool
		}{
			{name: "api key", input: domain.AuthMethodAPIKey, want: generatedadminapi.AccountListItemAuthMethodApiKey},
			{name: "oauth browser", input: domain.AuthMethodOAuthBrowser, want: generatedadminapi.AccountListItemAuthMethodOauthBrowser},
			{name: "oauth device", input: domain.AuthMethodOAuthDevice, want: generatedadminapi.AccountListItemAuthMethodOauthDevice},
			{name: "oauth import", input: domain.AuthMethodOAuthImport, want: generatedadminapi.AccountListItemAuthMethodOauthImport},
			{name: "unsupported", input: domain.AuthMethod("weird"), hasErr: true},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got, err := accountAuthMethodToAPI(tc.input)
				if tc.hasErr {
					require.Error(t, err)
					return
				}
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("simple helper mappings are stable", func(t *testing.T) {
		assert.Equal(t, generatedadminapi.ManualPaste, railNameFromOAuth(oauth.RailManualPaste))
		assert.Equal(t, generatedadminapi.Loopback, railNameFromOAuth(oauth.RailLoopback))
		assert.Equal(t, generatedadminapi.Loopback, railNameFromOAuth(oauth.RailUnknown))

		require.NotNil(t, stringPointer("x"))
		assert.Nil(t, stringPointer(""))

		require.NotNil(t, intPointerIfPositive(1))
		assert.Nil(t, intPointerIfPositive(0))
		assert.Nil(t, intPointerIfPositive(-1))
	})

	t.Run("exchange error helper rejects plain errors", func(t *testing.T) {
		_, ok := exchangeErrorDetails(errors.New("plain"))
		assert.False(t, ok)
	})

	t.Run("manual callback url parser covers allowed and rejected ports", func(t *testing.T) {
		for _, raw := range []string{
			"http://localhost:1455/auth/callback?code=abc",
		} {
			_, reason, ok := parseManualCallbackURL(raw)
			assert.True(t, ok)
			assert.Equal(t, generatedadminapi.InvalidCallbackURLEnvelopeDataReason(""), reason)
		}

		for _, raw := range []string{
			"http://localhost:1454/auth/callback?code=abc",
			"http://localhost:1456/auth/callback?code=abc",
			"http://localhost:1457/auth/callback?code=abc",
			"http://localhost:1458/auth/callback?code=abc",
			"http://localhost:1459/auth/callback?code=abc",
			"http://localhost:9999/auth/callback?code=abc",
			"://bad-url",
		} {
			_, reason, ok := parseManualCallbackURL(raw)
			assert.False(t, ok)
			assert.Equal(t, generatedadminapi.UrlPrefixMismatch, reason)
		}
	})

	t.Run("manual callback consumed without winner rail fails fast", func(t *testing.T) {
		now := time.Date(2026, 4, 21, 21, 0, 0, 0, time.UTC)
		coord := oauth.NewCoordinatorWithClock(oauth.NewFakeClock(now), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		flow := &oauth.Flow{
			ID:        "fl_consumed_without_winner_1234567890",
			Method:    oauth.FlowBrowser,
			State:     "s_consumed",
			Status:    oauth.FlowStatusError,
			CreatedAt: now,
			ExpiresAt: now.Add(5 * time.Minute),
		}
		flow.Consumed.Store(true)
		require.NoError(t, coord.TryStartFlow(flow))

		h := NewHandler(coord)
		_, err := h.manualCallbackConsumed(flow.ID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "has no winner rail")
	})

}

func newOAuthAPIServerForTest(coord coordinator) http.Handler {
	return generatedadminapi.HandlerFromMuxWithEnvelope(NewHandler(coord), http.NewServeMux())
}

type stubCoordinator struct {
	startBrowserFn            func(context.Context, string) (*oauth.Flow, error)
	startDeviceFn             func(context.Context, string) (*oauth.Flow, error)
	cancelWithContextFn       func(context.Context, string) error
	browserCallbackSnapshotFn func(string) (oauth.BrowserCallbackSnapshot, bool)
	currentFlowFn             func() *oauth.Flow
	getFlowFn                 func() (oauth.FlowSnapshot, error)
	nowFn                     func() time.Time
	consumeCodeFn             func(context.Context, string, string, string, oauth.Rail) (*domain.UpstreamAccount, error)
	cancelFromProviderErrFn   func(context.Context, string, oauth.Rail, string) error
	winnerRailFn              func(string) (oauth.Rail, bool)
}

func (s *stubCoordinator) StartBrowser(ctx context.Context, provider string) (*oauth.Flow, error) {
	if s != nil && s.startBrowserFn != nil {
		return s.startBrowserFn(ctx, provider)
	}
	return nil, nil
}

func (s *stubCoordinator) StartDevice(ctx context.Context, provider string) (*oauth.Flow, error) {
	if s != nil && s.startDeviceFn != nil {
		return s.startDeviceFn(ctx, provider)
	}
	return nil, nil
}

func (s *stubCoordinator) CancelWithContext(ctx context.Context, flowID string) error {
	if s != nil && s.cancelWithContextFn != nil {
		return s.cancelWithContextFn(ctx, flowID)
	}
	return nil
}

func (s *stubCoordinator) BrowserCallbackSnapshot(flowID string) (oauth.BrowserCallbackSnapshot, bool) {
	if s != nil && s.browserCallbackSnapshotFn != nil {
		return s.browserCallbackSnapshotFn(flowID)
	}
	return oauth.BrowserCallbackSnapshot{}, false
}

func (s *stubCoordinator) CurrentFlow() *oauth.Flow {
	if s != nil && s.currentFlowFn != nil {
		return s.currentFlowFn()
	}
	return nil
}

func (s *stubCoordinator) GetFlow() (oauth.FlowSnapshot, error) {
	if s != nil && s.getFlowFn != nil {
		return s.getFlowFn()
	}
	return oauth.FlowSnapshot{}, nil
}

func (s *stubCoordinator) Now() time.Time {
	if s != nil && s.nowFn != nil {
		return s.nowFn()
	}
	return time.Time{}
}

func (s *stubCoordinator) ConsumeCode(ctx context.Context, flowID, code, state string, rail oauth.Rail) (*domain.UpstreamAccount, error) {
	if s != nil && s.consumeCodeFn != nil {
		return s.consumeCodeFn(ctx, flowID, code, state, rail)
	}
	return nil, nil
}

func (s *stubCoordinator) CancelFromProviderError(ctx context.Context, flowID string, rail oauth.Rail, providerError string) error {
	if s != nil && s.cancelFromProviderErrFn != nil {
		return s.cancelFromProviderErrFn(ctx, flowID, rail, providerError)
	}
	return nil
}

func (s *stubCoordinator) WinnerRail(flowID string) (oauth.Rail, bool) {
	if s != nil && s.winnerRailFn != nil {
		return s.winnerRailFn(flowID)
	}
	return oauth.RailUnknown, false
}

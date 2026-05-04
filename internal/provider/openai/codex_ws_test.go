package openai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/user/one-llm-router/internal/domain"
)

func TestResolveCodexTurnState(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "turn-existing", ResolveCodexTurnState(" turn-existing "))

	generated := ResolveCodexTurnState("")
	require.NotEmpty(t, generated)
	assert.Equal(t, strings.TrimSpace(generated), generated)

	second := ResolveCodexTurnState(" ")
	require.NotEmpty(t, second)
	assert.NotEqual(t, generated, second)
}

func TestDialCodexWebSocketAddsRequiredHeadersAndRelays(t *testing.T) {
	t.Parallel()

	headerCh := make(chan http.Header, 1)
	errCh := make(chan error, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/codex/responses" {
			errCh <- fmt.Errorf("unexpected upstream path %q", r.URL.Path)
			return
		}
		headerCh <- r.Header.Clone()

		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			errCh <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		messageType, payload, err := conn.Read(ctx)
		if err != nil {
			errCh <- fmt.Errorf("read upstream frame: %w", err)
			return
		}
		if string(payload) != "from-router" {
			errCh <- fmt.Errorf("unexpected upstream payload %q", payload)
			return
		}
		errCh <- conn.Write(ctx, messageType, []byte("from-upstream"))
	}))
	t.Cleanup(upstream.Close)

	accountID := "chatgpt-account-1"
	account := domain.UpstreamAccount{
		AuthMethod:       domain.AuthMethodOAuthBrowser,
		ChatGPTAccountID: &accountID,
	}
	original := httptest.NewRequest(http.MethodGet, "/v1/responses?model=gpt-5", nil)
	original.Header.Set("Authorization", "Bearer client-token")
	original.Header.Set("OpenAI-Beta", "existing_beta=1")
	original.Header.Set("Cookie", "router_session=secret")
	original.Header.Set("chatgpt-account-id", "client-controlled-account")
	original.Header.Set("X-Forwarded-For", "203.0.113.10")

	client := NewClient(5 * time.Second)
	client.SetCodexBackendBaseURLForTest(upstream.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, resp, err := client.DialCodexWebSocket(ctx, account, "oauth-access-token", original, GatewayRoute{
		OAuthUpstreamPath: "/codex/responses",
		BodyPolicy:        GatewayBodyPolicyWebSocketNoBody,
	}, "turn-123")
	if resp != nil && resp.Body != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	require.NoError(t, err)
	require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
	defer func() { _ = conn.CloseNow() }()

	require.NoError(t, conn.Write(ctx, websocket.MessageText, []byte("from-router")))
	messageType, payload, err := conn.Read(ctx)
	require.NoError(t, err)
	assert.Equal(t, websocket.MessageText, messageType)
	assert.Equal(t, "from-upstream", string(payload))
	require.NoError(t, <-errCh)

	headers := <-headerCh
	assert.Equal(t, "Bearer oauth-access-token", headers.Get("Authorization"))
	assert.Equal(t, CodexCLIUserAgent, headers.Get("User-Agent"))
	assert.Equal(t, "chatgpt-account-1", headers.Get("chatgpt-account-id"))
	assert.Equal(t, "turn-123", headers.Get("x-codex-turn-state"))
	assert.Contains(t, headers.Values("OpenAI-Beta"), "existing_beta=1")
	assert.Contains(t, strings.Join(headers.Values("OpenAI-Beta"), ","), responsesWebSocketBetaToken)
	assert.Empty(t, headers.Get("Cookie"))
	assert.Empty(t, headers.Get("X-Forwarded-For"))
}

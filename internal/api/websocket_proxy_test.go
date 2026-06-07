package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/user/one-llm-router/internal/core"
	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/provider/openai"
	"github.com/user/one-llm-router/internal/store"
)

func TestProxyWebSocketRelaysFramesAndRecordsMetadata(t *testing.T) {
	t.Parallel()

	headerCh := make(chan http.Header, 1)
	errCh := make(chan error, 1)
	upstream := newWebSocketEchoUpstream(t, headerCh, errCh)
	h := newOAuthProxyHarness(t, upstream.URL)

	proxyServer := httptest.NewServer(h.handler)
	t.Cleanup(proxyServer.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, wsURL(proxyServer.URL, "/v1/responses"), &websocket.DialOptions{
		HTTPHeader: http.Header{"X-Forwarded-For": []string{"198.51.100.99"}},
	})
	if resp != nil && resp.Body != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	require.NoError(t, err)
	require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
	defer func() { _ = conn.CloseNow() }()

	turnState := resp.Header.Get("x-codex-turn-state")
	require.NotEmpty(t, turnState)

	require.NoError(t, conn.Write(ctx, websocket.MessageText, []byte("client payload secret frame")))
	messageType, payload, err := conn.Read(ctx)
	require.NoError(t, err)
	assert.Equal(t, websocket.MessageText, messageType)
	assert.Equal(t, "upstream payload", string(payload))
	require.NoError(t, <-errCh)

	headers := <-headerCh
	assert.Equal(t, "Bearer oauth-access", headers.Get("Authorization"))
	assert.Equal(t, openai.CodexCLIUserAgent, headers.Get("User-Agent"))
	assert.Equal(t, "acct-oauth-access", headers.Get("chatgpt-account-id"))
	assert.Equal(t, turnState, headers.Get("x-codex-turn-state"))
	assert.Contains(t, strings.Join(headers.Values("OpenAI-Beta"), ","), "responses_websockets=2026-02-06")

	records := requestRecordsEventually(t, h.store)
	require.Len(t, records, 1)
	record := records[0]
	assert.Equal(t, "WS", record.Method)
	assert.NotEmpty(t, record.ClientIP)
	assert.NotNil(t, net.ParseIP(record.ClientIP))
	assert.NotEqual(t, "198.51.100.99", record.ClientIP)
	assert.Equal(t, "/v1/responses", record.Path)
	assert.Equal(t, domain.ResponseModeWebSocket, record.ResponseMode)
	assert.Equal(t, domain.OutcomeSuccess, record.Outcome)
	require.NotNil(t, record.UpstreamAccountID)
	assert.Nil(t, record.ClientRequestBody)
	assert.Nil(t, record.UpstreamRequestBody)
	assert.Nil(t, record.UpstreamResponseBody)
	assertRecordBridgeMetadata(t, record, openai.BridgeMetadata{
		OpID:             openai.OpOpenAIResponsesWebSocket,
		BridgeID:         openai.BridgeOpenAIResponsesWebSocketToCodex,
		ClientContract:   openai.ContractOpenAIV1ResponsesWS,
		UpstreamContract: openai.ContractChatGPTBackendAPICodexResponsesWS,
		CredentialClass:  openai.CredentialClassOAuth,
	})
	assertRecordBridgeUpstreamEndpoint(t, record, "/codex/responses")
	assert.NotContains(t, fmt.Sprintf("%v", record.ModelParams), "client payload secret frame")
}

func TestProxyWebSocketReusesInboundTurnStateAndSupportsBackendAPIPath(t *testing.T) {
	t.Parallel()

	headerCh := make(chan http.Header, 1)
	errCh := make(chan error, 1)
	upstream := newWebSocketEchoUpstream(t, headerCh, errCh)
	h := newOAuthProxyHarness(t, upstream.URL)

	proxyServer := httptest.NewServer(h.handler)
	t.Cleanup(proxyServer.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, wsURL(proxyServer.URL, "/backend-api/codex/responses"), &websocket.DialOptions{
		HTTPHeader: http.Header{"x-codex-turn-state": []string{"turn-reuse"}},
	})
	if resp != nil && resp.Body != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	require.NoError(t, err)
	require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
	defer func() { _ = conn.CloseNow() }()

	assert.Equal(t, "turn-reuse", resp.Header.Get("x-codex-turn-state"))
	require.NoError(t, conn.Write(ctx, websocket.MessageText, []byte("client payload secret frame")))
	_, payload, err := conn.Read(ctx)
	require.NoError(t, err)
	assert.Equal(t, "upstream payload", string(payload))
	require.NoError(t, <-errCh)
	assert.Equal(t, "turn-reuse", (<-headerCh).Get("x-codex-turn-state"))

	records := requestRecordsEventually(t, h.store)
	require.Len(t, records, 1)
	assert.Equal(t, "WS", records[0].Method)
	assert.Equal(t, "/backend-api/codex/responses", records[0].Path)
	assert.Equal(t, domain.ResponseModeWebSocket, records[0].ResponseMode)
	assertRecordBridgeMetadata(t, records[0], openai.BridgeMetadata{
		OpID:             openai.OpCodexNativeResponsesWebSocket,
		BridgeID:         openai.BridgeCodexNativeResponsesWebSocketDirect,
		ClientContract:   openai.ContractChatGPTBackendAPICodexResponsesWS,
		UpstreamContract: openai.ContractChatGPTBackendAPICodexResponsesWS,
		CredentialClass:  openai.CredentialClassOAuth,
	})
	assertRecordBridgeUpstreamEndpoint(t, records[0], "/codex/responses")
}

func TestProxyWebSocketIsOAuthOnly(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"/v1/responses", "/backend-api/codex/responses"} {
		path := path
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			var upstreamHits atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				upstreamHits.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			t.Cleanup(upstream.Close)

			h := newProxyHarness(t, 5*time.Second)
			h.client.SetCodexBackendBaseURLForTest(upstream.URL)
			require.NoError(t, h.repo.Create(context.Background(), &domain.UpstreamAccount{
				Name:         "api-key",
				Provider:     domain.ProviderOpenAI,
				APIKey:       "sk-test",
				BaseURL:      &upstream.URL,
				Status:       domain.AccountStatusActive,
				Capabilities: []string{"op.openai.responses", "op.openai.chat_completions"},
			}))

			proxyServer := httptest.NewServer(h.handler)
			t.Cleanup(proxyServer.Close)

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			conn, resp, err := websocket.Dial(ctx, wsURL(proxyServer.URL, path), nil)
			if resp != nil && resp.Body != nil {
				defer func() { _ = resp.Body.Close() }()
			}
			if conn != nil {
				defer func() { _ = conn.CloseNow() }()
			}
			require.Error(t, err)
			require.NotNil(t, resp)
			assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
			var envelope RouterErrorEnvelope
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&envelope))
			assert.Equal(t, "router_error", envelope.Error.Type)
			assert.Equal(t, ErrCodeNoAvailableAccount, envelope.Error.Code)
			assert.Equal(t, int32(0), upstreamHits.Load())

			records := requestRecordsEventually(t, h.store)
			require.Len(t, records, 1)
			assert.Equal(t, http.MethodGet, records[0].Method)
			assert.Equal(t, path, records[0].Path)
			assert.Nil(t, records[0].UpstreamAccountID)
			assert.Equal(t, http.StatusServiceUnavailable, records[0].StatusCode)
			assert.Equal(t, domain.OutcomeNoAvailableAccount, records[0].Outcome)
			assert.Equal(t, domain.ResponseModeJSON, records[0].ResponseMode)
			require.NotNil(t, records[0].ErrorCode)
			assert.Equal(t, ErrCodeNoAvailableAccount, *records[0].ErrorCode)
		})
	}
}

func TestProxyWebSocketMalformedHandshakeDoesNotReachUpstream(t *testing.T) {
	t.Parallel()

	var upstreamHits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamHits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(upstream.Close)

	h := newOAuthProxyHarness(t, upstream.URL)
	proxyServer := httptest.NewServer(h.handler)
	t.Cleanup(proxyServer.Close)

	req, err := http.NewRequest(http.MethodGet, proxyServer.URL+"/v1/responses", nil)
	require.NoError(t, err)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")

	resp, err := proxyServer.Client().Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, int32(0), upstreamHits.Load())

	records := requestRecordsEventually(t, h.store)
	require.Len(t, records, 1)
	assert.Nil(t, records[0].UpstreamAccountID)
	assert.Equal(t, domain.ResponseModeJSON, records[0].ResponseMode)
	require.NotNil(t, records[0].ErrorCode)
	assert.Equal(t, ErrCodeInvalidRequest, *records[0].ErrorCode)
}

func TestProxyWebSocketClientAbortClosesUpstream(t *testing.T) {
	t.Parallel()

	upstreamClosed := make(chan error, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			upstreamClosed <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _, err = conn.Read(ctx)
		upstreamClosed <- err
	}))
	t.Cleanup(upstream.Close)

	h := newOAuthProxyHarness(t, upstream.URL)
	proxyServer := httptest.NewServer(h.handler)
	t.Cleanup(proxyServer.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, wsURL(proxyServer.URL, "/v1/responses"), nil)
	if resp != nil && resp.Body != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	require.NoError(t, err)
	require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
	require.NoError(t, conn.Close(websocket.StatusGoingAway, "client abort"))

	select {
	case err := <-upstreamClosed:
		require.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("upstream websocket was not closed after client abort")
	}
}

func newWebSocketEchoUpstream(t *testing.T, headerCh chan<- http.Header, errCh chan<- error) *httptest.Server {
	t.Helper()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headerCh <- r.Header.Clone()
		if r.URL.Path != "/codex/responses" {
			errCh <- fmt.Errorf("unexpected upstream path %q", r.URL.Path)
			return
		}

		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			errCh <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		messageType, payload, err := conn.Read(ctx)
		if err != nil {
			errCh <- fmt.Errorf("read upstream frame: %w", err)
			return
		}
		if string(payload) != "client payload secret frame" {
			errCh <- fmt.Errorf("unexpected upstream payload %q", payload)
			return
		}
		if err := conn.Write(ctx, messageType, []byte("upstream payload")); err != nil {
			errCh <- err
			return
		}
		errCh <- conn.Close(websocket.StatusNormalClosure, "")
	}))
	t.Cleanup(upstream.Close)
	return upstream
}

func wsURL(httpURL string, path string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http") + path
}

func requestRecordsEventually(t *testing.T, s *store.Store) []domain.RequestRecord {
	t.Helper()

	recordRepo := store.NewRequestRecordRepo(s.Engine())
	var records []domain.RequestRecord
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		got, err := recordRepo.Query(context.Background(), core.QueryParams{Limit: 10})
		require.NoError(c, err)
		require.NotEmpty(c, got)
		records = got
	}, time.Second, 10*time.Millisecond)
	return records
}

func assertRecordBridgeMetadata(t assert.TestingT, record domain.RequestRecord, want openai.BridgeMetadata) {
	if helper, ok := t.(interface{ Helper() }); ok {
		helper.Helper()
	}

	if !assert.NotNil(t, record.RouterMetadata) {
		return
	}
	raw, ok := record.RouterMetadata[openai.RouterMetadataBridgeKey]
	if !assert.True(t, ok, "bridge metadata missing from router_metadata") {
		return
	}
	metadata, ok := raw.(map[string]any)
	if !assert.True(t, ok, "bridge metadata must be an object: %#v", raw) {
		return
	}
	assert.Equal(t, string(want.OpID), metadata["op_id"])
	assert.Equal(t, string(want.BridgeID), metadata["bridge_id"])
	assert.Equal(t, string(want.ClientContract), metadata["client_contract"])
	assert.Equal(t, string(want.UpstreamContract), metadata["upstream_contract"])
	assert.Equal(t, string(want.CredentialClass), metadata["credential_class"])

	text := strings.ToLower(fmt.Sprintf("%v", metadata))
	for _, forbidden := range []string{
		"authorization",
		"cookie",
		"bearer ",
		"sk-",
		"oauth-access",
		"access_token",
		"refresh_token",
		"id_token",
		"request_body",
		"response_body",
		"client payload secret frame",
	} {
		assert.NotContains(t, text, forbidden)
	}
}

func assertRecordBridgeUpstreamEndpoint(t assert.TestingT, record domain.RequestRecord, want string) {
	if helper, ok := t.(interface{ Helper() }); ok {
		helper.Helper()
	}

	if !assert.NotNil(t, record.RouterMetadata) {
		return
	}
	raw, ok := record.RouterMetadata[openai.RouterMetadataBridgeKey]
	if !assert.True(t, ok, "bridge metadata missing from router_metadata") {
		return
	}
	metadata, ok := raw.(map[string]any)
	if !assert.True(t, ok, "bridge metadata must be an object: %#v", raw) {
		return
	}
	assert.Equal(t, want, metadata[openai.RouterMetadataBridgeUpstreamEndpointKey])
}

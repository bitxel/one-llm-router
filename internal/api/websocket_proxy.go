package api

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/user/one-llm-router/internal/clientip"
	"github.com/user/one-llm-router/internal/core"
	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/provider"
	"github.com/user/one-llm-router/internal/provider/openai"
)

type webSocketRelayResult struct {
	direction string
	err       error
}

func (h *ProxyHandler) serveWebSocket(
	w http.ResponseWriter,
	requestID string,
	start time.Time,
	r *http.Request,
	route gatewayRoute,
	sessionKey string,
) {
	if !webSocketHandshakeIsValid(r) {
		h.writeError(w, requestID, start, r, nil,
			http.StatusBadRequest, ErrCodeInvalidRequest, "invalid websocket handshake",
			domain.OutcomeRouterError, proxyErrorBodyCapture{})
		return
	}

	account, accessToken, _, err := h.selector.SelectEligible(r.Context(), sessionKey, proxyAccountEligible(route))
	if err != nil {
		if errors.Is(err, domain.ErrNoCapacity) {
			h.writeError(w, requestID, start, r, nil,
				http.StatusServiceUnavailable, ErrCodeNoAvailableAccount, "no available upstream account",
				domain.OutcomeNoAvailableAccount, proxyErrorBodyCapture{})
			return
		}
		if errors.Is(err, core.ErrPreForward) {
			h.writeError(w, requestID, start, r, &account,
				http.StatusBadGateway, ErrCodeInternalError, "upstream credentials unavailable",
				domain.OutcomeRouterError, proxyErrorBodyCapture{})
			return
		}
		h.writeError(w, requestID, start, r, nil,
			http.StatusInternalServerError, ErrCodeInternalError, "account selection failed",
			domain.OutcomeRouterError, proxyErrorBodyCapture{})
		return
	}

	credentialClass := credentialClassForAccount(account)
	bridge, ok := h.selectGatewayBridge(route, credentialClass)
	if !ok {
		h.writeError(w, requestID, start, r, &account,
			http.StatusServiceUnavailable, ErrCodeNoAvailableAccount, "no eligible upstream bridge",
			domain.OutcomeNoAvailableAccount, proxyErrorBodyCapture{})
		return
	}
	bridgeMetadata := gatewayBridgeMetadata(bridge, credentialClass)
	bridgeRouterMetadata := provider.MergeBridgeRouterMetadata(nil, bridgeMetadata)
	r.Header.Set("X-Request-Id", requestID)

	turnState := openai.ResolveCodexTurnState(r.Header.Get("x-codex-turn-state"))
	clientReq, err := bridge.DecodeClientRequest(r.Context(), decodeInputFromGatewayRoute(route, r, nil, requestID))
	if err != nil {
		h.writeError(w, requestID, start, r, &account,
			http.StatusBadRequest, ErrCodeInvalidRequest, "invalid request body",
			domain.OutcomeRouterError, proxyErrorBodyCapture{routerMetadata: bridgeRouterMetadata})
		return
	}
	upstreamBaseURL := h.client.CodexBackendBaseURL()
	upstreamReq, _, err := bridge.BuildUpstreamRequest(r.Context(), provider.BuildInput{
		ClientRequest:   clientReq,
		Credential:      credentialClass,
		CredentialValue: string(accessToken),
		AccountMetadata: accountBridgeMetadata(account),
		UpstreamBaseURL: upstreamBaseURL,
	})
	if err != nil {
		h.writeError(w, requestID, start, r, &account,
			http.StatusBadRequest, ErrCodeInvalidRequest, "invalid request body",
			domain.OutcomeRouterError, proxyErrorBodyCapture{routerMetadata: bridgeRouterMetadata})
		return
	}
	bridgeMetadata.UpstreamEndpoint = upstreamReq.Path
	bridgeRouterMetadata = provider.MergeBridgeRouterMetadata(nil, bridgeMetadata)
	upstream, upstreamResp, err := h.client.DialBridgeWebSocket(r.Context(), upstreamReq, turnState)
	if upstreamResp != nil && upstreamResp.Body != nil {
		defer func() { _ = upstreamResp.Body.Close() }()
	}
	if err != nil {
		if errors.Is(err, openai.ErrUpstreamTimeout) {
			h.writeError(w, requestID, start, r, &account,
				http.StatusGatewayTimeout, ErrCodeUpstreamTimeout, "upstream response timeout",
				domain.OutcomeRouterError, proxyErrorBodyCapture{routerMetadata: bridgeRouterMetadata})
			return
		}
		h.writeError(w, requestID, start, r, &account,
			http.StatusBadGateway, ErrCodeUpstreamConnFailed, "cannot connect to upstream",
			domain.OutcomeRouterError, proxyErrorBodyCapture{routerMetadata: bridgeRouterMetadata})
		return
	}
	defer func() { _ = upstream.CloseNow() }()

	w.Header().Set("x-codex-turn-state", turnState)
	downstream, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		h.recordWebSocket(r, requestID, start, &account, http.StatusBadRequest, domain.OutcomeRouterError, ErrCodeInvalidRequest, sessionKey, bridgeMetadata)
		return
	}
	defer func() { _ = downstream.CloseNow() }()
	downstream.SetReadLimit(h.maxRequestBody)
	upstream.SetReadLimit(h.maxRequestBody)

	outcome, errCode := h.relayWebSockets(r.Context(), downstream, upstream)
	h.recordWebSocket(r, requestID, start, &account, http.StatusSwitchingProtocols, outcome, errCode, sessionKey, bridgeMetadata)
}

func webSocketHandshakeIsValid(r *http.Request) bool {
	if r == nil || r.Method != http.MethodGet || !isGatewayWebSocketUpgrade(r) {
		return false
	}
	if r.Header.Get("Sec-WebSocket-Version") != "13" {
		return false
	}
	key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	if key == "" {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(key)
	return err == nil && len(decoded) == 16
}

func (h *ProxyHandler) relayWebSockets(parent context.Context, downstream, upstream *websocket.Conn) (string, string) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	results := make(chan webSocketRelayResult, 2)
	go func() {
		results <- webSocketRelayResult{
			direction: "downstream_to_upstream",
			err:       copyWebSocketMessages(ctx, upstream, downstream),
		}
	}()
	go func() {
		results <- webSocketRelayResult{
			direction: "upstream_to_downstream",
			err:       copyWebSocketMessages(ctx, downstream, upstream),
		}
	}()

	first := <-results
	cancel()
	closeWebSocketPeer(downstream, first.err)
	closeWebSocketPeer(upstream, first.err)

	select {
	case <-results:
	case <-time.After(time.Second):
		h.logger.Warn("websocket relay peer did not stop promptly", "direction", first.direction)
	}

	if relayErrorIsNormal(first.err) {
		return domain.OutcomeSuccess, ""
	}
	if first.direction == "downstream_to_upstream" {
		return domain.OutcomeCancelled, ""
	}
	return domain.OutcomeRouterError, ErrCodeUpstreamConnFailed
}

func copyWebSocketMessages(ctx context.Context, dst, src *websocket.Conn) error {
	for {
		messageType, reader, err := src.Reader(ctx)
		if err != nil {
			return err
		}
		writer, err := dst.Writer(ctx, messageType)
		if err != nil {
			_, _ = io.Copy(io.Discard, reader)
			return err
		}
		if _, err := io.Copy(writer, reader); err != nil {
			_ = writer.Close()
			return err
		}
		if err := writer.Close(); err != nil {
			return err
		}
	}
}

func closeWebSocketPeer(conn *websocket.Conn, cause error) {
	if conn == nil {
		return
	}
	status := websocket.CloseStatus(cause)
	switch status {
	case websocket.StatusNormalClosure, websocket.StatusGoingAway, websocket.StatusPolicyViolation, websocket.StatusMessageTooBig, websocket.StatusBadGateway:
		_ = conn.Close(status, "")
	case websocket.StatusNoStatusRcvd, websocket.StatusAbnormalClosure:
		_ = conn.Close(websocket.StatusBadGateway, "")
	default:
		_ = conn.Close(websocket.StatusBadGateway, "")
	}
	_ = conn.CloseNow()
}

func relayErrorIsNormal(err error) bool {
	if err == nil {
		return true
	}
	switch websocket.CloseStatus(err) {
	case websocket.StatusNormalClosure, websocket.StatusGoingAway:
		return true
	default:
		return false
	}
}

func (h *ProxyHandler) recordWebSocket(
	r *http.Request,
	requestID string,
	start time.Time,
	account *domain.UpstreamAccount,
	statusCode int,
	outcome string,
	errCode string,
	sessionKey string,
	bridgeMetadata provider.BridgeMetadata,
) {
	var accountID *int64
	if account != nil {
		accountID = &account.ID
	}
	var sessionKeyPtr *string
	if sessionKey != "" {
		sessionKeyPtr = &sessionKey
	}
	var errCodePtr *string
	if errCode != "" {
		errCodePtr = &errCode
	}

	h.recorder.Record(r.Context(), domain.RequestRecord{
		RequestID:         requestID,
		ClientIP:          clientip.FromRequest(r),
		UpstreamAccountID: accountID,
		SessionKey:        sessionKeyPtr,
		Method:            "WS",
		Path:              r.URL.Path,
		StatusCode:        statusCode,
		LatencyMs:         int(time.Since(start).Milliseconds()),
		Outcome:           outcome,
		ErrorCode:         errCodePtr,
		RouterMetadata:    provider.MergeBridgeRouterMetadata(nil, bridgeMetadata),
		ResponseMode:      domain.ResponseModeWebSocket,
	})
}

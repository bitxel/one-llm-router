package api

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/user/one-llm-router/internal/clientip"
	"github.com/user/one-llm-router/internal/core"
	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/provider"
	"github.com/user/one-llm-router/internal/provider/openai"
)

var hopByHopHeaders = map[string]bool{
	"connection":          true,
	"keep-alive":          true,
	"proxy-authenticate":  true,
	"proxy-authorization": true,
	"te":                  true,
	"trailer":             true,
	"trailers":            true,
	"transfer-encoding":   true,
	"upgrade":             true,
}

func isHopByHopHeader(key string) bool {
	return hopByHopHeaders[strings.ToLower(key)]
}

func dynamicHopByHopHeaders(headers http.Header) map[string]struct{} {
	out := map[string]struct{}{}
	for _, raw := range headers.Values("Connection") {
		for _, token := range strings.Split(raw, ",") {
			if t := strings.TrimSpace(strings.ToLower(token)); t != "" {
				out[t] = struct{}{}
			}
		}
	}
	return out
}

type ProxyHandler struct {
	selector       *core.AccountSelector
	recorder       *core.RequestRecorder
	client         *openai.Client
	bridgeRegistry *provider.BridgeRegistry
	bodyLog        bool
	bodyLogFn      func() (clientReqLog, upstreamReqLog, upstreamRespLog bool)
	maxRequestBody int64
	logger         *slog.Logger
}

type proxyErrorBodyCapture struct {
	clientRequestBody    []byte
	upstreamRequestBody  []byte
	upstreamResponseBody []byte
	modelParams          domain.JSONMap
	routerMetadata       domain.JSONMap
}

// shouldLogBody returns whether request/response body capture is
// currently enabled, split by direction. Checks the live-config
// override installed via SetBodyLogFunc (002 hot-reload) before
// falling back to the single static flag captured at handler-build
// time (001 behaviour, which captures both or neither).
func (h *ProxyHandler) shouldLogBody() (clientReqLog, upstreamReqLog, upstreamRespLog bool) {
	if h == nil {
		return false, false, false
	}
	if h.bodyLogFn != nil {
		return h.bodyLogFn()
	}
	return h.bodyLog, h.bodyLog, h.bodyLog
}

// SetBodyLogFunc installs a live-config getter that returns
// (log_client_request_body, log_upstream_request_body,
// log_upstream_response_body). Used by 002's settings
// hot-reload path (T-400): the proxy consults the function on every
// request so POST /api/admin/settings/update takes effect without a
// restart. Passing nil reverts to the constructor-time static flag.
func (h *ProxyHandler) SetBodyLogFunc(fn func() (clientReqLog, upstreamReqLog, upstreamRespLog bool)) {
	if h == nil {
		return
	}
	h.bodyLogFn = fn
}

const defaultMaxRequestBody = 32 << 20
const defaultMaxUpstreamResponseBody = 64 << 20

func NewProxyHandler(
	selector *core.AccountSelector,
	recorder *core.RequestRecorder,
	client *openai.Client,
	bodyLoggingEnabled bool,
	maxRequestBodyBytes int64,
	logger *slog.Logger,
) *ProxyHandler {
	if maxRequestBodyBytes <= 0 {
		maxRequestBodyBytes = defaultMaxRequestBody
	}
	return &ProxyHandler{
		selector:       selector,
		recorder:       recorder,
		client:         client,
		bridgeRegistry: defaultGatewayBridgeRegistry,
		bodyLog:        bodyLoggingEnabled,
		maxRequestBody: maxRequestBodyBytes,
		logger:         logger,
	}
}

func (h *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	// Prefer the ID installed by RequestIDMiddleware so proxy logs, admin
	// logs, and the ID echoed back to the client all agree. Fall back to a
	// freshly generated ID when the middleware is absent (e.g. unit tests
	// exercising the handler directly).
	requestID := RequestIDFromContext(r.Context())
	if requestID == "" {
		requestID = domain.NewRequestID()
	}

	sessionKey := ExtractSessionKey(r)
	route := classifyGatewayRoute(r.Method, r.URL.Path, isGatewayWebSocketUpgrade(r))
	if route.Kind == gatewayRouteKindUnsupported || route.Kind == gatewayRouteKindBlocked {
		h.writeError(w, requestID, start, r, nil,
			route.StatusCode, route.ErrorCode, gatewayRouteErrorMessage(route),
			domain.OutcomeRouterError, proxyErrorBodyCapture{})
		return
	}
	if route.ResponseMode == domain.ResponseModeWebSocket {
		h.serveWebSocket(w, requestID, start, r, route, sessionKey)
		return
	}
	if isModelsUnionRoute(route, r) {
		h.serveModelsUnion(w, requestID, start, r, route)
		return
	}

	var reqBodyBytes []byte
	if r.Body != nil {
		switch route.BodyPolicy {
		case gatewayBodyPolicyJSONCaptureAllowed:
			var err error
			reqBodyBytes, err = io.ReadAll(io.LimitReader(r.Body, h.maxRequestBody+1))
			if err != nil {
				h.writeError(w, requestID, start, r, nil,
					http.StatusBadGateway, ErrCodeInternalError, "failed to read request body",
					domain.OutcomeRouterError, proxyErrorBodyCapture{})
				return
			}
			if int64(len(reqBodyBytes)) > h.maxRequestBody {
				h.writeError(w, requestID, start, r, nil,
					http.StatusRequestEntityTooLarge, ErrCodeInvalidRequest, "request body too large",
					domain.OutcomeRouterError, proxyErrorBodyCapture{})
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(reqBodyBytes))
		case gatewayBodyPolicyCaptureDisabled:
			if r.ContentLength > h.maxRequestBody {
				h.writeError(w, requestID, start, r, nil,
					http.StatusRequestEntityTooLarge, ErrCodeInvalidRequest, "request body too large",
					domain.OutcomeRouterError, proxyErrorBodyCapture{})
				return
			}
			r.Body = newBoundedRequestBody(r.Body, h.maxRequestBody)
		}
	}

	account, accessToken, _, err := h.selector.SelectEligible(r.Context(), sessionKey, proxyAccountEligible(route))
	if err != nil {
		if errors.Is(err, domain.ErrNoCapacity) {
			h.writeError(w, requestID, start, r, nil,
				http.StatusServiceUnavailable, ErrCodeNoAvailableAccount,
				"No active upstream accounts available",
				domain.OutcomeNoAvailableAccount, proxyErrorBodyCapture{clientRequestBody: reqBodyBytes})
			return
		}
		if errors.Is(err, core.ErrPreForward) {
			h.writeError(w, requestID, start, r, &account,
				http.StatusBadGateway, ErrCodeInternalError, "upstream credentials unavailable",
				domain.OutcomeRouterError, proxyErrorBodyCapture{clientRequestBody: reqBodyBytes})
			return
		}
		h.writeError(w, requestID, start, r, nil,
			http.StatusInternalServerError, ErrCodeInternalError, "account selection failed",
			domain.OutcomeRouterError, proxyErrorBodyCapture{clientRequestBody: reqBodyBytes})
		return
	}

	credentialClass := credentialClassForAccount(account)
	bridge, ok := h.selectGatewayBridge(route, credentialClass)
	if !ok {
		h.writeError(w, requestID, start, r, &account,
			http.StatusServiceUnavailable, ErrCodeNoAvailableAccount,
			"No active upstream accounts available",
			domain.OutcomeNoAvailableAccount, proxyErrorBodyCapture{clientRequestBody: reqBodyBytes})
		return
	}
	bridgeMetadata := gatewayBridgeMetadata(bridge, credentialClass)
	bridgeRouterMetadata := provider.MergeBridgeRouterMetadata(nil, bridgeMetadata)
	// Propagate request ID to upstream so OpenAI/Codex side can correlate a
	// request with our router-side trace. Header filtering still controls
	// operation-specific exceptions such as multipart transcribe.
	r.Header.Set("X-Request-Id", requestID)
	clientReq, err := bridge.DecodeClientRequest(r.Context(), decodeInputFromGatewayRoute(route, r, reqBodyBytes, requestID))
	if err != nil {
		h.writeError(w, requestID, start, r, &account,
			http.StatusBadRequest, ErrCodeInvalidRequest, "invalid request body",
			domain.OutcomeRouterError, proxyErrorBodyCapture{clientRequestBody: reqBodyBytes, routerMetadata: bridgeRouterMetadata})
		return
	}
	upstreamBaseURL := account.EffectiveBaseURL()
	if account.IsOAuth() {
		upstreamBaseURL = h.client.CodexBackendBaseURL()
	}
	upstreamReq, responseAdapter, err := bridge.BuildUpstreamRequest(r.Context(), provider.BuildInput{
		ClientRequest:   clientReq,
		Credential:      credentialClass,
		CredentialValue: string(accessToken),
		AccountMetadata: accountBridgeMetadata(account),
		UpstreamBaseURL: upstreamBaseURL,
	})
	if err != nil {
		h.writeError(w, requestID, start, r, &account,
			http.StatusBadRequest, ErrCodeInvalidRequest, "invalid request body",
			domain.OutcomeRouterError, proxyErrorBodyCapture{clientRequestBody: reqBodyBytes, routerMetadata: bridgeRouterMetadata})
		return
	}
	bridgeMetadata.UpstreamEndpoint = upstreamReq.Path
	bridgeRouterMetadata = provider.MergeBridgeRouterMetadata(nil, bridgeMetadata)

	h.logger.Info("request routed",
		"request_id", requestID,
		"account_id", account.ID,
		"session_key", sessionKey,
		"method", r.Method,
		"path", r.URL.Path,
	)
	upstreamResp, capture, err := h.client.ForwardBridgeRequestWithCapture(r.Context(), upstreamReq, responseAdapter)
	if err != nil {
		errorCapture := proxyErrorBodyCapture{
			clientRequestBody:    reqBodyBytes,
			upstreamRequestBody:  capture.UpstreamRequestBody,
			upstreamResponseBody: capture.UpstreamResponseBody,
			routerMetadata:       bridgeRouterMetadata,
		}
		if errors.Is(err, openai.ErrUpstreamTimeout) {
			h.writeError(w, requestID, start, r, &account,
				http.StatusGatewayTimeout, ErrCodeUpstreamTimeout, "upstream response timeout",
				domain.OutcomeRouterError, errorCapture)
			return
		}
		if errors.Is(err, openai.ErrRequestBodyTooLarge) {
			h.writeError(w, requestID, start, r, &account,
				http.StatusRequestEntityTooLarge, ErrCodeInvalidRequest, "request body too large",
				domain.OutcomeRouterError, errorCapture)
			return
		}
		if errors.Is(err, openai.ErrInvalidUpstreamRequest) {
			h.writeError(w, requestID, start, r, &account,
				http.StatusBadRequest, ErrCodeInvalidRequest, "invalid request body",
				domain.OutcomeRouterError, errorCapture)
			return
		}
		if errors.Is(err, openai.ErrUpstreamResponseInvalid) {
			h.writeError(w, requestID, start, r, &account,
				http.StatusBadGateway, ErrCodeUpstreamRespInvalid, "invalid upstream response",
				domain.OutcomeRouterError, errorCapture)
			return
		}
		h.writeError(w, requestID, start, r, &account,
			http.StatusBadGateway, ErrCodeUpstreamConnFailed, "cannot connect to upstream",
			domain.OutcomeRouterError, errorCapture)
		return
	}
	defer func() { _ = upstreamResp.Body.Close() }()

	dynamicResponseDrops := dynamicHopByHopHeaders(upstreamResp.Header)
	for key, values := range upstreamResp.Header {
		lower := strings.ToLower(key)
		if isHopByHopHeader(key) {
			continue
		}
		if _, drop := dynamicResponseDrops[lower]; drop {
			continue
		}
		if lower == "set-cookie" {
			continue
		}
		// The router owns X-Request-Id on the outbound side — it is
		// set by RequestIDMiddleware / the envelope writer using the
		// id we assigned at request entry. Surfacing the upstream's
		// own value here would break tracing (router logs point at
		// our id, client sees a different one) and can leak an
		// upstream account identifier. Drop it and let the
		// middleware-installed value stand. Same applies to the
		// legacy un-prefixed "Request-Id" form.
		if http.CanonicalHeaderKey(key) == "X-Request-Id" ||
			http.CanonicalHeaderKey(key) == "Request-Id" {
			continue
		}
		for _, v := range values {
			w.Header().Add(key, v)
		}
	}

	responseBody := io.Reader(upstreamResp.Body)
	responseMode := openai.DetectResponseMode(upstreamResp.Header.Get("Content-Type"))
	if responseMode == domain.ResponseModeJSON && shouldSniffResponseForSSE(r, reqBodyBytes, upstreamResp.StatusCode) {
		buffered := bufio.NewReader(upstreamResp.Body)
		responseBody = buffered
		if bufferedReaderStartsWithSSE(buffered) {
			responseMode = domain.ResponseModeSSE
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Del("Content-Length")
		}
	}

	var tokenUsage domain.JSONMap
	var upstreamRespBodyStr *string
	var errCode *string
	var ttftMs *int
	outcome := domain.OutcomeSuccess
	recordStatusCode := upstreamResp.StatusCode

	if upstreamResp.StatusCode >= 400 {
		outcome = domain.OutcomeUpstreamError
	}

	clientReqBodyLog, upstreamReqBodyLog, upstreamRespBodyLog := h.shouldLogBody()
	switch responseMode {
	case domain.ResponseModeSSE:
		w.WriteHeader(upstreamResp.StatusCode)
		sseResult := ForwardSSE(w, responseBody, upstreamRespBodyLog)
		tokenUsage = sseResult.TokenUsage
		ttftMs = sseResult.TTFTMs
		if sseResult.Err != nil {
			h.logger.Warn("SSE stream read error", "request_id", requestID, "error", sseResult.Err)
			outcome, errCode = classifySSEForwardFailure(r.Context(), outcome, sseResult.Err)
		} else if tokenUsage == nil && capture.TokenUsage != nil {
			tokenUsage = capture.TokenUsage()
		}
		if upstreamRespBodyLog {
			switch {
			case len(capture.UpstreamResponseBody) > 0:
				s := capturedBodyString(capture.UpstreamResponseBody)
				upstreamRespBodyStr = &s
			case capture.UpstreamResponseBodySnapshot != nil:
				if body := capture.UpstreamResponseBodySnapshot(); len(body) > 0 {
					s := capturedBodyString(body)
					upstreamRespBodyStr = &s
				}
			case sseResult.UpstreamResponseBody != "":
				s := capturedBodyString([]byte(sseResult.UpstreamResponseBody))
				upstreamRespBodyStr = &s
			}
		}

	default:
		body, readErr := readBounded(responseBody, defaultMaxUpstreamResponseBody, "upstream response too large")
		if readErr != nil {
			h.logger.Error("failed to read upstream response", "request_id", requestID, "error", readErr)
			outcome = domain.OutcomeRouterError
			recordStatusCode = http.StatusBadGateway
			code := ErrCodeUpstreamRespInvalid
			errCode = &code

			for key := range w.Header() {
				w.Header().Del(key)
			}
			WriteRouterError(w, http.StatusBadGateway, ErrCodeUpstreamRespInvalid,
				"failed to read upstream response", requestID)
		} else {
			switch outcome {
			case domain.OutcomeSuccess:
				tokenUsage = openai.ExtractUsageFromJSON(body)
			case domain.OutcomeUpstreamError:
				errCode = openai.ExtractErrorCode(body)
			}

			w.WriteHeader(upstreamResp.StatusCode)
			_, _ = w.Write(body)

			recordedBody := body
			preserveCollectedJSON := false
			if len(capture.UpstreamResponseBody) > 0 {
				recordedBody = capture.UpstreamResponseBody
				preserveCollectedJSON = true
			}
			if upstreamRespBodyLog && len(recordedBody) > 0 {
				s := capturedBodyString(recordedBody)
				if preserveCollectedJSON {
					s = core.RedactCapturedBody(recordedBody)
				}
				upstreamRespBodyStr = &s
			}
		}
	}

	var model *string
	var modelParams domain.JSONMap
	if len(reqBodyBytes) > 0 {
		model = openai.ExtractModel(reqBodyBytes)
		modelParams = openai.ExtractModelParams(reqBodyBytes)
	}
	if capture.ModelParams != nil {
		modelParams = mergeModelParams(modelParams, capture.ModelParams())
	}

	var sessKeyPtr *string
	if sessionKey != "" {
		sessKeyPtr = &sessionKey
	}

	var clientReqBodyPtr *string
	if clientReqBodyLog && len(reqBodyBytes) > 0 {
		s := capturedBodyString(reqBodyBytes)
		clientReqBodyPtr = &s
	}
	var upstreamReqBodyPtr *string
	if upstreamReqBodyLog && len(capture.UpstreamRequestBody) > 0 {
		s := capturedBodyString(capture.UpstreamRequestBody)
		upstreamReqBodyPtr = &s
	}

	accountID := &account.ID

	rec := domain.RequestRecord{
		RequestID:            requestID,
		ClientIP:             clientip.FromRequest(r),
		UpstreamAccountID:    accountID,
		SessionKey:           sessKeyPtr,
		Method:               r.Method,
		Path:                 r.URL.Path,
		StatusCode:           recordStatusCode,
		LatencyMs:            int(time.Since(start).Milliseconds()),
		TTFTMs:               ttftMs,
		Outcome:              outcome,
		ErrorCode:            errCode,
		Model:                model,
		ModelParams:          modelParams,
		RouterMetadata:       bridgeRouterMetadata,
		ResponseMode:         responseMode,
		TokenUsage:           tokenUsage,
		ClientRequestBody:    clientReqBodyPtr,
		UpstreamRequestBody:  upstreamReqBodyPtr,
		UpstreamResponseBody: upstreamRespBodyStr,
	}
	h.recorder.Record(r.Context(), rec)
}

func mergeModelParams(base domain.JSONMap, extra domain.JSONMap) domain.JSONMap {
	if len(extra) == 0 {
		return base
	}
	if base == nil {
		base = domain.JSONMap{}
	}
	for key, value := range extra {
		base[key] = value
	}
	return base
}

func classifySSEForwardFailure(ctx context.Context, currentOutcome string, err error) (string, *string) {
	if (ctx != nil && ctx.Err() != nil) || errors.Is(err, context.Canceled) {
		if currentOutcome == domain.OutcomeSuccess {
			return domain.OutcomeCancelled, nil
		}
		return currentOutcome, nil
	}
	if currentOutcome == domain.OutcomeSuccess {
		currentOutcome = domain.OutcomeRouterError
	}
	code := ErrCodeUpstreamRespInvalid
	return currentOutcome, &code
}

func gatewayBridgeMetadata(bridge provider.OperationBridge, credential provider.CredentialClass) provider.BridgeMetadata {
	if bridge == nil {
		return provider.BridgeMetadata{}
	}
	return provider.BridgeMetadata{
		OpID:             bridge.OpID(),
		BridgeID:         bridge.ID(),
		ClientContract:   bridge.ClientContract(),
		UpstreamContract: bridge.UpstreamContract(),
		CredentialClass:  credential,
	}
}

func accountBridgeMetadata(account domain.UpstreamAccount) domain.JSONMap {
	metadata := domain.JSONMap{}
	if account.ChatGPTAccountID != nil && *account.ChatGPTAccountID != "" {
		metadata["chatgpt_account_id"] = *account.ChatGPTAccountID
	}
	return metadata
}

func decodeInputFromGatewayRoute(route gatewayRoute, r *http.Request, body []byte, requestID string) provider.DecodeInput {
	return provider.DecodeInput{
		OpID:          route.OpID,
		Method:        r.Method,
		Path:          r.URL.Path,
		Pattern:       route.Pattern,
		RawQuery:      r.URL.RawQuery,
		RawBody:       body,
		Body:          r.Body,
		Headers:       r.Header.Clone(),
		ContentLength: r.ContentLength,
		WebSocket:     route.ResponseMode == domain.ResponseModeWebSocket,
		ResponseMode:  route.ResponseMode,
		BodyPolicy:    string(openAIBodyPolicy(route.BodyPolicy)),
		RequestID:     requestID,
	}
}

func shouldSniffResponseForSSE(r *http.Request, requestBody []byte, statusCode int) bool {
	if r == nil || statusCode >= http.StatusBadRequest {
		return false
	}
	return r.URL.Path == "/v1/responses" && openai.RequestWantsStreaming(requestBody)
}

func bufferedReaderStartsWithSSE(r *bufio.Reader) bool {
	first, err := r.Peek(1)
	if err != nil || len(first) == 0 {
		return false
	}
	switch first[0] {
	case 'e', 'd', ':':
		return true
	default:
		return false
	}
}

type boundedRequestBody struct {
	body      io.ReadCloser
	remaining int64
}

func newBoundedRequestBody(body io.ReadCloser, limit int64) io.ReadCloser {
	return &boundedRequestBody{body: body, remaining: limit}
}

func (b *boundedRequestBody) Read(p []byte) (int, error) {
	if b.remaining <= 0 {
		var probe [1]byte
		n, err := b.body.Read(probe[:])
		if n > 0 || err == nil {
			return 0, openai.ErrRequestBodyTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > b.remaining {
		p = p[:int(b.remaining)]
	}
	n, err := b.body.Read(p)
	b.remaining -= int64(n)
	return n, err
}

func (b *boundedRequestBody) Close() error {
	return b.body.Close()
}

func readBounded(r io.Reader, limit int64, limitMessage string) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return data, err
	}
	if int64(len(data)) > limit {
		return data, errors.New(limitMessage)
	}
	return data, nil
}

const capturedBodyTruncatedMarker = "\n[truncated]"

func capturedBodyString(body []byte) string {
	value := core.RedactCapturedBody(body)
	if len(value) <= bodyCaptureMaxBytes {
		return value
	}
	limit := bodyCaptureMaxBytes - len(capturedBodyTruncatedMarker)
	if limit <= 0 {
		return capturedBodyTruncatedMarker
	}
	cut := 0
	for idx := range value {
		if idx > limit {
			break
		}
		cut = idx
	}
	if cut == 0 {
		cut = limit
	}
	return value[:cut] + capturedBodyTruncatedMarker
}

func gatewayRouteErrorMessage(route gatewayRoute) string {
	switch route.Kind {
	case gatewayRouteKindBlocked:
		return "endpoint is blocked by router policy"
	default:
		return "endpoint is not supported by this router"
	}
}

func openAIBodyPolicy(policy gatewayBodyPolicy) openai.GatewayBodyPolicy {
	switch policy {
	case gatewayBodyPolicyCaptureDisabled:
		return openai.GatewayBodyPolicyCaptureDisabled
	case gatewayBodyPolicyWebSocketNoBody:
		return openai.GatewayBodyPolicyWebSocketNoBody
	default:
		return openai.GatewayBodyPolicyJSONCaptureAllowed
	}
}

func proxyAccountEligible(route gatewayRoute) func(domain.UpstreamAccount) bool {
	return func(account domain.UpstreamAccount) bool {
		if account.IsOAuth() {
			return route.OAuth.Eligible
		}
		return route.APIKey.Eligible
	}
}

func (h *ProxyHandler) writeError(
	w http.ResponseWriter,
	requestID string,
	start time.Time,
	r *http.Request,
	account *domain.UpstreamAccount,
	statusCode int,
	errCode, message, outcome string,
	capture proxyErrorBodyCapture,
) {
	WriteRouterError(w, statusCode, errCode, message, requestID)

	var accountID *int64
	if account != nil {
		accountID = &account.ID
	}

	var sessionKey *string
	if sk := ExtractSessionKey(r); sk != "" {
		sessionKey = &sk
	}

	var model *string
	var modelParams domain.JSONMap
	if len(capture.clientRequestBody) > 0 {
		model = openai.ExtractModel(capture.clientRequestBody)
		modelParams = openai.ExtractModelParams(capture.clientRequestBody)
	}
	modelParams = mergeModelParams(modelParams, capture.modelParams)
	routerMetadata := capture.routerMetadata

	clientReqBodyLog, upstreamReqBodyLog, upstreamRespBodyLog := h.shouldLogBody()
	var clientReqBodyPtr *string
	if clientReqBodyLog && len(capture.clientRequestBody) > 0 {
		s := capturedBodyString(capture.clientRequestBody)
		clientReqBodyPtr = &s
	}
	var upstreamReqBodyPtr *string
	if upstreamReqBodyLog && len(capture.upstreamRequestBody) > 0 {
		s := capturedBodyString(capture.upstreamRequestBody)
		upstreamReqBodyPtr = &s
	}
	var upstreamRespBodyPtr *string
	if upstreamRespBodyLog && len(capture.upstreamResponseBody) > 0 {
		s := capturedBodyString(capture.upstreamResponseBody)
		upstreamRespBodyPtr = &s
	}

	rec := domain.RequestRecord{
		RequestID:            requestID,
		ClientIP:             clientip.FromRequest(r),
		UpstreamAccountID:    accountID,
		SessionKey:           sessionKey,
		Method:               r.Method,
		Path:                 r.URL.Path,
		StatusCode:           statusCode,
		LatencyMs:            int(time.Since(start).Milliseconds()),
		Outcome:              outcome,
		ErrorCode:            &errCode,
		Model:                model,
		ModelParams:          modelParams,
		RouterMetadata:       routerMetadata,
		ResponseMode:         domain.ResponseModeJSON,
		ClientRequestBody:    clientReqBodyPtr,
		UpstreamRequestBody:  upstreamReqBodyPtr,
		UpstreamResponseBody: upstreamRespBodyPtr,
	}
	h.recorder.Record(r.Context(), rec)

	h.logger.Warn("router error",
		"request_id", requestID,
		"error_code", errCode,
		"message", message,
		"path", r.URL.Path,
	)
}

package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/user/one-llm-router/internal/clientip"
	"github.com/user/one-llm-router/internal/core"
	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/provider"
	"github.com/user/one-llm-router/internal/provider/openai"
)

const routerMetadataModelsUnionKey = "models_union"

type openAIModelsListResponse struct {
	Object string           `json:"object"`
	Data   []map[string]any `json:"data"`
}

func isModelsUnionRoute(route gatewayRoute, r *http.Request) bool {
	return r != nil &&
		r.Method == http.MethodGet &&
		r.URL.Path == "/v1/models" &&
		route.Kind == gatewayRouteKindSupported &&
		route.OpID == openai.OpOpenAIModelsList &&
		route.ResponseMode == domain.ResponseModeJSON
}

func (h *ProxyHandler) serveModelsUnion(w http.ResponseWriter, requestID string, start time.Time, r *http.Request, route gatewayRoute) {
	prepared, err := h.selector.ListEligiblePrepared(r.Context(), proxyAccountEligible(route))
	if err != nil {
		if errors.Is(err, domain.ErrNoCapacity) {
			h.writeError(w, requestID, start, r, nil,
				http.StatusServiceUnavailable, ErrCodeNoAvailableAccount,
				"No active upstream accounts available",
				domain.OutcomeNoAvailableAccount, proxyErrorBodyCapture{})
			return
		}
		if errors.Is(err, core.ErrPreForward) {
			h.writeError(w, requestID, start, r, nil,
				http.StatusBadGateway, ErrCodeInternalError, "upstream credentials unavailable",
				domain.OutcomeRouterError, proxyErrorBodyCapture{})
			return
		}
		h.writeError(w, requestID, start, r, nil,
			http.StatusInternalServerError, ErrCodeInternalError, "account selection failed",
			domain.OutcomeRouterError, proxyErrorBodyCapture{})
		return
	}

	merged := make([]map[string]any, 0)
	seen := map[string]struct{}{}
	metadata := modelsUnionMetadata(prepared)
	var upstreamReqBodies [][]byte
	var upstreamRespBodies [][]byte

	for _, item := range prepared {
		account := item.Account
		credentialClass := credentialClassForAccount(account)

		bridge, ok := h.selectGatewayBridge(route, credentialClass)
		if !ok {
			h.writeError(w, requestID, start, r, &account,
				http.StatusServiceUnavailable, ErrCodeNoAvailableAccount,
				"No active upstream accounts available",
				domain.OutcomeNoAvailableAccount, proxyErrorBodyCapture{routerMetadata: metadata})
			return
		}

		headers := r.Header.Clone()
		headers.Set("X-Request-Id", requestID)
		headers.Set("Accept-Encoding", "identity")
		clientReq, err := bridge.DecodeClientRequest(r.Context(), provider.DecodeInput{
			OpID:          route.OpID,
			Method:        r.Method,
			Path:          r.URL.Path,
			Pattern:       route.Pattern,
			RawQuery:      r.URL.RawQuery,
			Headers:       headers,
			ContentLength: r.ContentLength,
			ResponseMode:  route.ResponseMode,
			BodyPolicy:    string(openAIBodyPolicy(route.BodyPolicy)),
			RequestID:     requestID,
		})
		if err != nil {
			h.writeError(w, requestID, start, r, &account,
				http.StatusBadRequest, ErrCodeInvalidRequest, "invalid request body",
				domain.OutcomeRouterError, proxyErrorBodyCapture{routerMetadata: metadata})
			return
		}

		upstreamBaseURL := account.EffectiveBaseURL()
		if account.IsOAuth() {
			upstreamBaseURL = h.client.CodexBackendBaseURL()
		}
		upstreamReq, responseAdapter, err := bridge.BuildUpstreamRequest(r.Context(), provider.BuildInput{
			ClientRequest:   clientReq,
			Credential:      credentialClass,
			CredentialValue: string(item.Token),
			AccountMetadata: accountBridgeMetadata(account),
			UpstreamBaseURL: upstreamBaseURL,
		})
		if err != nil {
			h.writeError(w, requestID, start, r, &account,
				http.StatusBadRequest, ErrCodeInvalidRequest, "invalid request body",
				domain.OutcomeRouterError, proxyErrorBodyCapture{routerMetadata: metadata})
			return
		}

		upstreamResp, capture, err := h.client.ForwardBridgeRequestWithCapture(r.Context(), upstreamReq, responseAdapter)
		if len(capture.UpstreamRequestBody) > 0 {
			upstreamReqBodies = append(upstreamReqBodies, capture.UpstreamRequestBody)
		}
		if err != nil {
			errorCapture := proxyErrorBodyCapture{
				upstreamRequestBody:  joinBodies(upstreamReqBodies),
				upstreamResponseBody: capture.UpstreamResponseBody,
				routerMetadata:       metadata,
			}
			if errors.Is(err, openai.ErrUpstreamTimeout) {
				h.writeError(w, requestID, start, r, &account,
					http.StatusGatewayTimeout, ErrCodeUpstreamTimeout, "upstream response timeout",
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

		body, readErr := readBounded(upstreamResp.Body, defaultMaxUpstreamResponseBody, "upstream response too large")
		_ = upstreamResp.Body.Close()
		if readErr != nil {
			h.writeError(w, requestID, start, r, &account,
				http.StatusBadGateway, ErrCodeUpstreamRespInvalid, "failed to read upstream response",
				domain.OutcomeRouterError, proxyErrorBodyCapture{
					upstreamRequestBody: joinBodies(upstreamReqBodies),
					routerMetadata:      metadata,
				})
			return
		}
		upstreamRespBodies = append(upstreamRespBodies, body)

		if upstreamResp.StatusCode >= http.StatusBadRequest {
			copyProxyResponseHeaders(w.Header(), upstreamResp.Header)
			w.WriteHeader(upstreamResp.StatusCode)
			_, _ = w.Write(body)
			errCode := openai.ExtractErrorCode(body)
			h.recordModelsUnion(r, requestID, start, &account, upstreamResp.StatusCode, domain.OutcomeUpstreamError, errCode,
				metadata, joinBodies(upstreamReqBodies), joinBodies(upstreamRespBodies))
			return
		}

		models, err := decodeOpenAIModelsList(body)
		if err != nil {
			h.writeError(w, requestID, start, r, &account,
				http.StatusBadGateway, ErrCodeUpstreamRespInvalid, "invalid upstream response",
				domain.OutcomeRouterError, proxyErrorBodyCapture{
					upstreamRequestBody:  joinBodies(upstreamReqBodies),
					upstreamResponseBody: joinBodies(upstreamRespBodies),
					routerMetadata:       metadata,
				})
			return
		}
		for _, model := range models {
			id, _ := model["id"].(string)
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			merged = append(merged, model)
		}
	}

	response, err := json.Marshal(openAIModelsListResponse{
		Object: "list",
		Data:   merged,
	})
	if err != nil {
		h.writeError(w, requestID, start, r, nil,
			http.StatusInternalServerError, ErrCodeInternalError, "failed to encode response",
			domain.OutcomeRouterError, proxyErrorBodyCapture{routerMetadata: metadata})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(response)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(response)
	h.recordModelsUnion(r, requestID, start, nil, http.StatusOK, domain.OutcomeSuccess, nil,
		metadata, joinBodies(upstreamReqBodies), joinBodies(upstreamRespBodies))
}

func decodeOpenAIModelsList(body []byte) ([]map[string]any, error) {
	var payload openAIModelsListResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode models list: %w", err)
	}
	if payload.Object != "list" {
		return nil, fmt.Errorf("models list object must be list, got %q", payload.Object)
	}
	if payload.Data == nil {
		return nil, errors.New("models list data missing")
	}

	out := make([]map[string]any, 0, len(payload.Data))
	for _, raw := range payload.Data {
		id, ok := raw["id"].(string)
		if !ok || strings.TrimSpace(id) == "" {
			return nil, errors.New("models list contains model without id")
		}
		model := map[string]any{
			"id":     id,
			"object": "model",
		}
		if created, ok := numericModelValue(raw["created"]); ok {
			model["created"] = created
		}
		if ownedBy, ok := raw["owned_by"].(string); ok && strings.TrimSpace(ownedBy) != "" {
			model["owned_by"] = ownedBy
		}
		for key, value := range raw {
			if key == "id" || key == "object" || key == "created" || key == "owned_by" {
				continue
			}
			model[key] = value
		}
		out = append(out, model)
	}
	return out, nil
}

func numericModelValue(value any) (any, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case int:
		return v, true
	case int64:
		return v, true
	case json.Number:
		return v, true
	default:
		return nil, false
	}
}

func modelsUnionMetadata(prepared []core.PreparedAccount) domain.JSONMap {
	classes := make([]string, 0, len(prepared))
	for _, item := range prepared {
		classes = append(classes, string(credentialClassForAccount(item.Account)))
	}
	return domain.JSONMap{
		routerMetadataModelsUnionKey: domain.JSONMap{
			"account_count":       len(prepared),
			"credential_classes":  classes,
			"source":              "active_eligible_accounts",
			"partial_success":     false,
			"model_routing_bound": false,
		},
	}
}

func (h *ProxyHandler) recordModelsUnion(
	r *http.Request,
	requestID string,
	start time.Time,
	account *domain.UpstreamAccount,
	statusCode int,
	outcome string,
	errCode *string,
	routerMetadata domain.JSONMap,
	upstreamReqBody []byte,
	upstreamRespBody []byte,
) {
	_, upstreamReqBodyLog, upstreamRespBodyLog := h.shouldLogBody()
	var upstreamReqBodyPtr *string
	if upstreamReqBodyLog && len(upstreamReqBody) > 0 {
		s := capturedBodyString(upstreamReqBody)
		upstreamReqBodyPtr = &s
	}
	var upstreamRespBodyPtr *string
	if upstreamRespBodyLog && len(upstreamRespBody) > 0 {
		s := capturedBodyString(upstreamRespBody)
		upstreamRespBodyPtr = &s
	}
	var sessionKey *string
	if sk := ExtractSessionKey(r); sk != "" {
		sessionKey = &sk
	}
	var accountID *int64
	if account != nil {
		accountID = &account.ID
	}
	rec := domain.RequestRecord{
		RequestID:            requestID,
		ClientIP:             clientip.FromRequest(r),
		SessionKey:           sessionKey,
		UpstreamAccountID:    accountID,
		Method:               r.Method,
		Path:                 r.URL.Path,
		StatusCode:           statusCode,
		LatencyMs:            int(time.Since(start).Milliseconds()),
		Outcome:              outcome,
		ErrorCode:            errCode,
		RouterMetadata:       routerMetadata,
		ResponseMode:         domain.ResponseModeJSON,
		UpstreamRequestBody:  upstreamReqBodyPtr,
		UpstreamResponseBody: upstreamRespBodyPtr,
	}
	h.recorder.Record(r.Context(), rec)
}

func copyProxyResponseHeaders(dst, src http.Header) {
	dynamicResponseDrops := dynamicHopByHopHeaders(src)
	for key, values := range src {
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
		if http.CanonicalHeaderKey(key) == "X-Request-Id" ||
			http.CanonicalHeaderKey(key) == "Request-Id" {
			continue
		}
		for _, v := range values {
			dst.Add(key, v)
		}
	}
}

func joinBodies(bodies [][]byte) []byte {
	switch len(bodies) {
	case 0:
		return nil
	case 1:
		return append([]byte(nil), bodies[0]...)
	}
	return bytes.Join(bodies, []byte("\n"))
}

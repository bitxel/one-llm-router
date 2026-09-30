package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/user/one-llm-router/internal/clientip"
	"github.com/user/one-llm-router/internal/domain"
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

// serveModelsUnion serves GET /v1/models from the local account_models
// cache. It never dials upstream; model discovery is explicit via the
// account models refresh endpoint (and account-creation async refresh).
func (h *ProxyHandler) serveModelsUnion(w http.ResponseWriter, requestID string, start time.Time, r *http.Request, route gatewayRoute) {
	if h.accountModelRepo == nil {
		h.writeError(w, requestID, start, r, nil,
			http.StatusInternalServerError, ErrCodeInternalError, "model cache unavailable",
			domain.OutcomeRouterError, proxyErrorBodyCapture{})
		return
	}

	accounts, err := h.selector.ListEligibleAccounts(r.Context(), proxyAccountEligible(route, nil))
	if err != nil {
		// ErrNoCapacity and other selection failures share the same shape.
		if errors.Is(err, domain.ErrNoCapacity) {
			h.writeError(w, requestID, start, r, nil,
				http.StatusServiceUnavailable, ErrCodeNoAvailableAccount,
				"No active upstream accounts available",
				domain.OutcomeNoAvailableAccount, proxyErrorBodyCapture{})
			return
		}
		h.writeError(w, requestID, start, r, nil,
			http.StatusInternalServerError, ErrCodeInternalError, "account selection failed",
			domain.OutcomeRouterError, proxyErrorBodyCapture{})
		return
	}

	accountIDs := make([]int64, 0, len(accounts))
	for _, acct := range accounts {
		accountIDs = append(accountIDs, acct.ID)
	}

	rows, err := h.accountModelRepo.ListByAccounts(r.Context(), accountIDs)
	if err != nil {
		h.writeError(w, requestID, start, r, nil,
			http.StatusInternalServerError, ErrCodeInternalError, "failed to load account models",
			domain.OutcomeRouterError, proxyErrorBodyCapture{})
		return
	}

	merged, degraded := mergeAccountModelRows(rows)
	metadata := modelsUnionCacheMetadata(accounts, len(merged), degraded)
	if degraded > 0 {
		h.logger.Warn("models_union_degraded_metadata",
			"request_id", requestID,
			"degraded_metadata_count", degraded,
		)
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
		metadata, nil, nil)
}

// mergeAccountModelRows builds a deduplicated OpenAI-style model list from
// account_models rows. First non-null metadata (stable order: model_id,
// account_id) wins for a given model_id. Corrupt metadata degrades to a
// minimal {"id","object"} object and increments degraded.
func mergeAccountModelRows(rows []domain.AccountModel) ([]map[string]any, int) {
	byID := make(map[string]map[string]any, len(rows))
	validMetadata := make(map[string]bool, len(rows))
	corruptMetadata := make(map[string]bool)
	order := make([]string, 0, len(rows))

	for _, row := range rows {
		if row.ModelID == "" {
			continue
		}
		if _, exists := byID[row.ModelID]; !exists {
			byID[row.ModelID] = map[string]any{"id": row.ModelID, "object": "model"}
			order = append(order, row.ModelID)
		}
		if row.Metadata == nil || validMetadata[row.ModelID] {
			continue
		}
		model, ok := decodeModelMetadata(row.Metadata, row.ModelID)
		if !ok {
			corruptMetadata[row.ModelID] = true
			continue
		}
		byID[row.ModelID] = model
		validMetadata[row.ModelID] = true
	}

	out := make([]map[string]any, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	degraded := 0
	for id := range corruptMetadata {
		if !validMetadata[id] {
			degraded++
		}
	}
	return out, degraded
}

func decodeModelMetadata(raw *string, fallbackID string) (map[string]any, bool) {
	if raw == nil || *raw == "" {
		return nil, false
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(*raw), &obj); err != nil || obj == nil {
		return nil, false
	}
	id, _ := obj["id"].(string)
	if strings.TrimSpace(id) == "" {
		if fallbackID == "" {
			return nil, false
		}
		obj["id"] = fallbackID
	}
	if o, _ := obj["object"].(string); strings.TrimSpace(o) == "" {
		obj["object"] = "model"
	}
	return obj, true
}

func modelsUnionCacheMetadata(accounts []domain.UpstreamAccount, modelCount, degraded int) domain.JSONMap {
	classes := make([]string, 0, len(accounts))
	for _, acct := range accounts {
		classes = append(classes, string(credentialClassForAccount(acct)))
	}
	return domain.JSONMap{
		routerMetadataModelsUnionKey: domain.JSONMap{
			"source":                  "account_models_cache",
			"account_count":           len(accounts),
			"credential_classes":      classes,
			"model_count":             modelCount,
			"model_routing_bound":     true,
			"degraded_metadata_count": degraded,
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

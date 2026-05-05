package openai

import (
	"encoding/json"
	"strings"

	"github.com/user/one-llm-router/internal/domain"
)

func ExtractUsageFromEvent(eventData []byte) domain.JSONMap {
	if usage := extractChatUsageFromEvent(eventData); usage != nil {
		return usage
	}

	var evt struct {
		Type     string        `json:"type"`
		Usage    *usagePayload `json:"usage"`
		Response *struct {
			Usage *usagePayload `json:"usage"`
		} `json:"response"`
	}

	if err := json.Unmarshal(eventData, &evt); err != nil {
		return nil
	}

	if evt.Type != "response.completed" {
		return nil
	}
	usagePayload := evt.Usage
	if usagePayload == nil && evt.Response != nil {
		usagePayload = evt.Response.Usage
	}
	return usageMap(usagePayload)
}

func extractChatUsageFromEvent(eventData []byte) domain.JSONMap {
	var evt struct {
		Object string `json:"object"`
		Usage  *struct {
			PromptTokens     *int `json:"prompt_tokens"`
			CompletionTokens *int `json:"completion_tokens"`
			PromptDetails    *struct {
				CachedTokens *int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			CompletionDetails *struct {
				ReasoningTokens *int `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(eventData, &evt); err != nil {
		return nil
	}
	if evt.Object != "chat.completion.chunk" || evt.Usage == nil {
		return nil
	}
	usage := domain.JSONMap{}
	if evt.Usage.PromptTokens != nil {
		usage["input"] = *evt.Usage.PromptTokens
	}
	if evt.Usage.CompletionTokens != nil {
		usage["output"] = *evt.Usage.CompletionTokens
	}
	if evt.Usage.PromptDetails != nil && evt.Usage.PromptDetails.CachedTokens != nil {
		usage["cached_input"] = *evt.Usage.PromptDetails.CachedTokens
	}
	if evt.Usage.CompletionDetails != nil && evt.Usage.CompletionDetails.ReasoningTokens != nil {
		usage["reasoning"] = *evt.Usage.CompletionDetails.ReasoningTokens
	}
	if len(usage) == 0 {
		return nil
	}
	return usage
}

type usagePayload struct {
	InputTokens        *int `json:"input_tokens"`
	OutputTokens       *int `json:"output_tokens"`
	InputTokensDetails *struct {
		CachedTokens *int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	OutputTokensDetails *struct {
		ReasoningTokens *int `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

func usageMap(payload *usagePayload) domain.JSONMap {
	if payload == nil {
		return nil
	}
	usage := domain.JSONMap{}
	if payload.InputTokens != nil {
		usage["input"] = *payload.InputTokens
	}
	if payload.OutputTokens != nil {
		usage["output"] = *payload.OutputTokens
	}
	if payload.InputTokensDetails != nil && payload.InputTokensDetails.CachedTokens != nil {
		usage["cached_input"] = *payload.InputTokensDetails.CachedTokens
	}
	if payload.OutputTokensDetails != nil && payload.OutputTokensDetails.ReasoningTokens != nil {
		usage["reasoning"] = *payload.OutputTokensDetails.ReasoningTokens
	}

	if len(usage) == 0 {
		return nil
	}
	return usage
}

func ExtractUsageFromJSON(body []byte) domain.JSONMap {
	if usage := extractChatUsageFromJSON(body); usage != nil {
		return usage
	}

	var resp struct {
		Usage *struct {
			InputTokens        *int `json:"input_tokens"`
			OutputTokens       *int `json:"output_tokens"`
			InputTokensDetails *struct {
				CachedTokens *int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
			OutputTokensDetails *struct {
				ReasoningTokens *int `json:"reasoning_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(body, &resp); err != nil || resp.Usage == nil {
		return nil
	}

	usage := domain.JSONMap{}
	if resp.Usage.InputTokens != nil {
		usage["input"] = *resp.Usage.InputTokens
	}
	if resp.Usage.OutputTokens != nil {
		usage["output"] = *resp.Usage.OutputTokens
	}
	if resp.Usage.InputTokensDetails != nil && resp.Usage.InputTokensDetails.CachedTokens != nil {
		usage["cached_input"] = *resp.Usage.InputTokensDetails.CachedTokens
	}
	if resp.Usage.OutputTokensDetails != nil && resp.Usage.OutputTokensDetails.ReasoningTokens != nil {
		usage["reasoning"] = *resp.Usage.OutputTokensDetails.ReasoningTokens
	}

	if len(usage) == 0 {
		return nil
	}
	return usage
}

func extractChatUsageFromJSON(body []byte) domain.JSONMap {
	var resp struct {
		Usage *struct {
			PromptTokens     *int `json:"prompt_tokens"`
			CompletionTokens *int `json:"completion_tokens"`
			PromptDetails    *struct {
				CachedTokens *int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			CompletionDetails *struct {
				ReasoningTokens *int `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &resp); err != nil || resp.Usage == nil {
		return nil
	}
	usage := domain.JSONMap{}
	if resp.Usage.PromptTokens != nil {
		usage["input"] = *resp.Usage.PromptTokens
	}
	if resp.Usage.CompletionTokens != nil {
		usage["output"] = *resp.Usage.CompletionTokens
	}
	if resp.Usage.PromptDetails != nil && resp.Usage.PromptDetails.CachedTokens != nil {
		usage["cached_input"] = *resp.Usage.PromptDetails.CachedTokens
	}
	if resp.Usage.CompletionDetails != nil && resp.Usage.CompletionDetails.ReasoningTokens != nil {
		usage["reasoning"] = *resp.Usage.CompletionDetails.ReasoningTokens
	}
	if len(usage) == 0 {
		return nil
	}
	return usage
}

func ExtractModel(requestBody []byte) *string {
	var req struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(requestBody, &req); err != nil || req.Model == "" {
		return nil
	}
	return &req.Model
}

func ExtractModelParams(requestBody []byte) domain.JSONMap {
	var req map[string]any
	if err := json.Unmarshal(requestBody, &req); err != nil {
		return nil
	}

	params := domain.JSONMap{}
	if reasoningEffort, ok := req["reasoning_effort"]; ok {
		params["reasoning_effort"] = reasoningEffort
	} else if reasoning, ok := req["reasoning"].(map[string]any); ok {
		if effort, ok := reasoning["effort"]; ok {
			params["reasoning_effort"] = effort
		}
	}
	if serviceTier, ok := req["service_tier"]; ok {
		params["service_tier"] = serviceTier
	}
	if len(params) == 0 {
		return nil
	}
	return params
}

func ExtractErrorCode(body []byte) *string {
	if body == nil {
		return nil
	}
	var resp struct {
		Error *struct {
			Code string `json:"code"`
			Type string `json:"type"`
		} `json:"error"`
		Response *struct {
			Error *struct {
				Code string `json:"code"`
				Type string `json:"type"`
			} `json:"error"`
		} `json:"response"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil
	}
	if resp.Error == nil && resp.Response != nil {
		resp.Error = resp.Response.Error
	}
	if resp.Error == nil {
		return nil
	}
	code := resp.Error.Code
	if code == "" {
		code = resp.Error.Type
	}
	if code == "" {
		return nil
	}
	return &code
}

func DetectResponseMode(contentType string) string {
	if strings.Contains(contentType, "text/event-stream") {
		return domain.ResponseModeSSE
	}
	return domain.ResponseModeJSON
}

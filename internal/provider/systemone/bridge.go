// Package systemone implements native System One forwarding, independent of vendor.
package systemone

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/provider"
)

const OpCreate provider.OpID = "op.systemone.create"
const Contract provider.Contract = "systemone.v1"

var ErrUnsupportedMedia = errors.New("systemone requires uncompressed application/json")

// ValidateHeaders rejects unsupported transports before reading a potentially large body.
func ValidateHeaders(headers http.Header, rawQuery string) error {
	media, params, err := mime.ParseMediaType(headers.Get("Content-Type"))
	encoding := strings.TrimSpace(headers.Get("Content-Encoding"))
	if err != nil || media != "application/json" || (params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) || (encoding != "" && !strings.EqualFold(encoding, "identity")) {
		return ErrUnsupportedMedia
	}
	if rawQuery != "" {
		return errors.New("systemone query parameters are unsupported")
	}
	return nil
}

// ValidateRequest inspects routing fields without reserializing state or questions.
func ValidateRequest(body []byte) error {
	if !utf8.Valid(body) {
		return errors.New("invalid UTF-8")
	}
	var req map[string]json.RawMessage
	if err := json.Unmarshal(body, &req); err != nil {
		return fmt.Errorf("decode systemone request: %w", err)
	}
	var model string
	if err := json.Unmarshal(req["model"], &model); err != nil || strings.TrimSpace(model) == "" {
		return errors.New("model must be a nonempty string")
	}
	state := strings.TrimSpace(string(req["state"]))
	if state == "" || (state[0] != '"' && state[0] != '{' && state[0] != '[') {
		return errors.New("state must be a string, object or array")
	}
	for _, key := range []string{"assets", "images", "stream"} {
		if _, ok := req[key]; ok {
			return fmt.Errorf("unsupported systemone field: %s", key)
		}
	}
	var questions map[string]json.RawMessage
	if err := json.Unmarshal(req["questions"], &questions); err != nil || len(questions) == 0 {
		return errors.New("questions must be a nonempty object")
	}
	for _, raw := range questions {
		var q struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &q); err != nil {
			return errors.New("question must be an object")
		}
		switch q.Type {
		case "noul", "choice", "score":
		default:
			return errors.New("unsupported question type")
		}
	}
	return nil
}

// ValidateResponse returns only observed usage; malformed success is never repaired.
func ValidateResponse(body []byte) (domain.JSONMap, error) {
	if !utf8.Valid(body) {
		return nil, errors.New("invalid response UTF-8")
	}
	var response struct {
		Model   string                     `json:"model"`
		Answers map[string]json.RawMessage `json:"answers"`
		Usage   *struct {
			Input             *int64          `json:"input_tokens"`
			Output            *int64          `json:"output_tokens"`
			InputDetails      json.RawMessage `json:"input_tokens_details"`
			OutputDetails     json.RawMessage `json:"output_tokens_details"`
			Prompt            json.RawMessage `json:"prompt_tokens"`
			Completion        json.RawMessage `json:"completion_tokens"`
			PromptDetails     json.RawMessage `json:"prompt_tokens_details"`
			CompletionDetails json.RawMessage `json:"completion_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("decode systemone response: %w", err)
	}
	if strings.TrimSpace(response.Model) == "" || len(response.Answers) == 0 || response.Usage == nil || response.Usage.Input == nil || response.Usage.Output == nil || *response.Usage.Input < 0 || *response.Usage.Output < 0 {
		return nil, errors.New("invalid systemone response contract")
	}
	for _, answer := range response.Answers {
		var a struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(answer, &a); err != nil {
			return nil, errors.New("invalid systemone answer")
		}
		switch a.Type {
		case "noul", "choice", "score":
		default:
			return nil, errors.New("invalid systemone answer type")
		}
	}
	// Match the existing generic usage extractor's Chat-first preference,
	// without decoding the complete response again.
	u := response.Usage
	normalize := func(input, output, inputDetails, outputDetails json.RawMessage) (domain.JSONMap, bool) {
		var in, out *int
		var cached *struct {
			Tokens *int `json:"cached_tokens"`
		}
		var reasoning *struct {
			Tokens *int `json:"reasoning_tokens"`
		}
		fields := []struct {
			raw    json.RawMessage
			target any
		}{
			{input, &in}, {output, &out}, {inputDetails, &cached}, {outputDetails, &reasoning},
		}
		for _, field := range fields {
			if len(field.raw) > 0 && json.Unmarshal(field.raw, field.target) != nil {
				return nil, false
			}
		}
		usage := domain.JSONMap{}
		if in != nil {
			usage["input"] = *in
		}
		if out != nil {
			usage["output"] = *out
		}
		if cached != nil && cached.Tokens != nil {
			usage["cached_input"] = *cached.Tokens
		}
		if reasoning != nil && reasoning.Tokens != nil {
			usage["reasoning"] = *reasoning.Tokens
		}
		return usage, true
	}
	if usage, ok := normalize(u.Prompt, u.Completion, u.PromptDetails, u.CompletionDetails); ok && len(usage) > 0 {
		return usage, nil
	}
	usage, ok := normalize(nil, nil, u.InputDetails, u.OutputDetails)
	if !ok {
		return nil, nil
	}
	usage["input"], usage["output"] = *u.Input, *u.Output
	return usage, nil
}

// Bridge maps the native protocol onto any compatible account Base URL.
type Bridge struct{}

func (Bridge) ID() provider.BridgeID                     { return "bridge.systemone.direct" }
func (Bridge) OpID() provider.OpID                       { return OpCreate }
func (Bridge) CredentialClass() provider.CredentialClass { return provider.CredentialClassAPIKey }
func (Bridge) ClientContract() provider.Contract         { return Contract }
func (Bridge) UpstreamContract() provider.Contract       { return Contract }
func (Bridge) Kind() string                              { return provider.ResponseAdapterPassthrough }

func (b Bridge) DecodeClientRequest(_ context.Context, in provider.DecodeInput) (provider.ClientRequest, error) {
	if err := ValidateHeaders(in.Headers, in.RawQuery); err != nil {
		return provider.ClientRequest{}, err
	}
	return b.DecodeRequestBody(in)
}

// DecodeRequestBody validates and decodes the body after ValidateHeaders has
// succeeded. The proxy uses it before account selection, retaining this result.
func (Bridge) DecodeRequestBody(in provider.DecodeInput) (provider.ClientRequest, error) {
	if err := ValidateRequest(in.RawBody); err != nil {
		return provider.ClientRequest{}, err
	}
	return provider.ClientRequest{ContractValue: Contract, Method: http.MethodPost, Path: in.Path, Pattern: in.Pattern, RawBody: in.RawBody, ContentLength: int64(len(in.RawBody)), Headers: in.Headers.Clone(), RequestID: in.RequestID, Metadata: in.Metadata}, nil
}
func (b Bridge) BuildUpstreamRequest(_ context.Context, in provider.BuildInput) (provider.UpstreamRequest, provider.ClientResponseAdapter, error) {
	if in.Credential != provider.CredentialClassAPIKey || in.ClientRequest.Contract() != Contract || in.CredentialValue == "" {
		return provider.UpstreamRequest{}, nil, errors.New("invalid systemone credential or contract")
	}
	if err := domain.ValidateBaseURL(in.UpstreamBaseURL); err != nil {
		return provider.UpstreamRequest{}, nil, err
	}
	base, err := url.Parse(in.UpstreamBaseURL)
	if err != nil || base.User != nil {
		return provider.UpstreamRequest{}, nil, errors.New("invalid systemone base URL")
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+in.CredentialValue)
	headers.Set("Content-Type", "application/json")
	headers.Set("Accept", "application/json")
	headers.Set("Accept-Encoding", "identity")
	headers.Set("X-Request-Id", in.ClientRequest.RequestID)
	return provider.UpstreamRequest{ContractValue: Contract, OpID: OpCreate, BridgeID: b.ID(), Method: http.MethodPost, URL: strings.TrimRight(in.UpstreamBaseURL, "/") + "/systemone", Path: "/systemone", Pattern: in.ClientRequest.Pattern, RawBody: in.ClientRequest.RawBody, ContentLength: int64(len(in.ClientRequest.RawBody)), Headers: headers, Metadata: in.ClientRequest.Metadata, UseProxy: in.UseProxy, AccountID: in.AccountID}, b, nil
}

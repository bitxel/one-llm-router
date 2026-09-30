package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/proxydial"
)

var ErrModelRefreshStoreFailed = errors.New("core: model refresh store failed")

type ModelRefresherRepo interface {
	ReplaceUpstreamModels(ctx context.Context, accountID int64, models []domain.AccountModelDraft) (added int, keptManual int, err error)
}

type ModelRefresher struct {
	// directClient dials without any proxy; proxiedClient routes
	// through the global outbound proxy (Feature 009). The choice is
	// per-account via UseProxy — same fail-fast rule as the forwarder.
	directClient       *http.Client
	proxiedClient      *http.Client
	codexBackend       string
	codexClientVersion string
	logger             *slog.Logger
}

// NewModelRefresher takes the direct and proxied clients separately so
// the app wiring can share its transports; proxied may be nil in tests
// that never opt accounts into the proxy.
func NewModelRefresher(directClient, proxiedClient *http.Client, codexBackend, codexClientVersion string, logger *slog.Logger) *ModelRefresher {
	if logger == nil {
		logger = slog.Default()
	}
	return &ModelRefresher{
		directClient:       directClient,
		proxiedClient:      proxiedClient,
		codexBackend:       codexBackend,
		codexClientVersion: codexClientVersion,
		logger:             logger,
	}
}

// TriggerAsync is the fire-and-forget hook every account-creation
// path runs after persisting a new row (admin create, OAuth
// browser/device flows, auth.json import, wizard-seed compensation).
// Nil-safe so callers can hold an optional refresher, skipped for
// non-active accounts, and failures are only logged — a creation
// response never depends on the refresh succeeding. The manual
// refresh endpoint keeps calling Refresh directly because it reports
// errors synchronously.
func (r *ModelRefresher) TriggerAsync(acct *domain.UpstreamAccount, repo ModelRefresherRepo) {
	if r == nil || repo == nil || acct == nil || acct.Status != domain.AccountStatusActive {
		return
	}
	go func() {
		if _, _, _, err := r.Refresh(context.Background(), acct, repo); err != nil {
			r.logger.Warn("account_models_refresh_failed",
				"account_id", acct.ID,
				"error", err,
			)
		}
	}()
}

func (r *ModelRefresher) Refresh(ctx context.Context, acct *domain.UpstreamAccount, modelRepo ModelRefresherRepo) (int, int, int, error) {
	acctID := acct.ID
	r.logger.Info("model_refresh_started",
		"account_id", acctID,
		"auth_method", acct.AuthMethod,
	)

	models, err := r.fetchUpstreamModels(ctx, acct)
	if err != nil {
		r.logger.Warn("model_refresh_upstream_failed",
			"account_id", acctID,
			"error", err,
		)
		return 0, 0, 0, err
	}
	if len(models) == 0 {
		// Empty upstream list does not clear the local allow-list (fail-safe).
		r.logger.Info("model_refresh_no_models",
			"account_id", acctID,
		)
		return 0, 0, 0, nil
	}

	added, keptManual, err := modelRepo.ReplaceUpstreamModels(ctx, acctID, models)
	if err != nil {
		r.logger.Warn("model_refresh_store_failed",
			"account_id", acctID,
			"error", err,
		)
		return 0, 0, 0, fmt.Errorf("%w: %w", ErrModelRefreshStoreFailed, err)
	}

	r.logger.Info("model_refresh_complete",
		"account_id", acctID,
		"added", added,
		"kept_manual", keptManual,
		"total_fetched", len(models),
	)
	return added, keptManual, len(models), nil
}

func (r *ModelRefresher) fetchUpstreamModels(ctx context.Context, acct *domain.UpstreamAccount) ([]domain.AccountModelDraft, error) {
	endpoint := r.modelListEndpoint(acct)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	var token string
	if acct.IsOAuth() {
		token = string(acct.AccessToken)
	} else {
		token = acct.APIKey
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := r.directClient
	if acct.UseProxy {
		if _, err := proxydial.Decide(true); err != nil {
			r.logger.Warn("outbound proxy required but not configured",
				"account_id", acct.ID,
				"hint", proxydial.ProxyRequiredHint)
			return nil, fmt.Errorf("upstream request failed: %w", proxydial.ErrProxyRequired)
		}
		if r.proxiedClient == nil {
			return nil, errors.New("model refresher proxied client is nil")
		}
		client = r.proxiedClient
	}
	if client == nil {
		return nil, errors.New("model refresher client is nil")
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upstream request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read upstream response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream returned status %d: %s", resp.StatusCode, string(body[:min(len(body), 256)]))
	}

	models, err := ParseModels(body)
	if err != nil {
		return nil, fmt.Errorf("parse models: %w", err)
	}
	seen := make(map[string]struct{}, len(models))
	deduped := make([]domain.AccountModelDraft, 0, len(models))
	for _, m := range models {
		if m.ID == "" {
			continue
		}
		if _, ok := seen[m.ID]; ok {
			continue
		}
		seen[m.ID] = struct{}{}
		deduped = append(deduped, m)
	}
	return deduped, nil
}

func (r *ModelRefresher) modelListEndpoint(acct *domain.UpstreamAccount) string {
	if acct.IsOAuth() {
		codexBackend := r.codexBackend
		if codexBackend == "" {
			codexBackend = domain.ChatGPTBackendBaseURL
		}
		return strings.TrimSuffix(codexBackend, "/") + "/codex/models?client_version=" + r.codexClientVersion
	}
	return strings.TrimSuffix(acct.EffectiveBaseURL(), "/v1") + "/v1/models"
}

// ParseModels decodes an upstream models list into drafts (ID + raw object
// metadata). Codex format uses top-level "models" with "slug"; OpenAI uses
// "object":"list" with "data". Returns an error when neither shape matches.
func ParseModels(body []byte) ([]domain.AccountModelDraft, error) {
	var probe struct {
		Models []json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(body, &probe); err == nil && probe.Models != nil {
		var codexPayload struct {
			Models []json.RawMessage `json:"models"`
		}
		if err := json.Unmarshal(body, &codexPayload); err != nil {
			return nil, fmt.Errorf("decode codex models: %w", err)
		}
		out := make([]domain.AccountModelDraft, 0, len(codexPayload.Models))
		for _, raw := range codexPayload.Models {
			var m struct {
				Slug string `json:"slug"`
			}
			if err := json.Unmarshal(raw, &m); err != nil {
				return nil, fmt.Errorf("decode codex model entry: %w", err)
			}
			if m.Slug == "" {
				continue
			}
			// Normalize slug to id so OpenAI-shaped responses stay consistent.
			normalized, err := normalizeModelObject(raw, m.Slug)
			if err != nil {
				return nil, err
			}
			out = append(out, domain.AccountModelDraft{ID: m.Slug, Metadata: normalized})
		}
		return out, nil
	}
	var openAIPayload struct {
		Object string            `json:"object"`
		Data   []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &openAIPayload); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if openAIPayload.Object != "" && openAIPayload.Object != "list" {
		return nil, fmt.Errorf("unexpected object type: %s", openAIPayload.Object)
	}
	out := make([]domain.AccountModelDraft, 0, len(openAIPayload.Data))
	for _, raw := range openAIPayload.Data {
		var m struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("decode openai model entry: %w", err)
		}
		if m.ID == "" {
			continue
		}
		normalized, err := normalizeModelObject(raw, m.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, domain.AccountModelDraft{ID: m.ID, Metadata: normalized})
	}
	return out, nil
}

// normalizeModelObject returns the raw model object as JSON with at least
// "id" and "object" set for OpenAI list compatibility.
func normalizeModelObject(raw json.RawMessage, id string) ([]byte, error) {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("decode model object: %w", err)
	}
	if obj == nil {
		obj = map[string]any{}
	}
	if _, ok := obj["id"].(string); !ok || obj["id"] == "" {
		obj["id"] = id
	}
	if _, ok := obj["object"].(string); !ok || obj["object"] == "" {
		obj["object"] = "model"
	}
	// Codex uses slug; ensure id is present alongside slug for consumers.
	if _, ok := obj["slug"].(string); !ok {
		if id != "" {
			obj["slug"] = id
		}
	}
	return json.Marshal(obj)
}

// ParseModelIDs extracts model IDs from an upstream models list body.
func ParseModelIDs(body []byte) ([]string, error) {
	models, err := ParseModels(body)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(models))
	for _, m := range models {
		ids = append(ids, m.ID)
	}
	return ids, nil
}

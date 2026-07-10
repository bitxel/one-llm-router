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
)

var ErrModelRefreshStoreFailed = errors.New("core: model refresh store failed")

type ModelRefresherRepo interface {
	ReplaceUpstreamModels(ctx context.Context, accountID int64, modelIDs []string) (added int, keptManual int, err error)
}

type ModelRefresher struct {
	client             *http.Client
	codexBackend       string
	codexClientVersion string
	logger             *slog.Logger
}

func NewModelRefresher(client *http.Client, codexBackend, codexClientVersion string, logger *slog.Logger) *ModelRefresher {
	if logger == nil {
		logger = slog.Default()
	}
	return &ModelRefresher{
		client:             client,
		codexBackend:       codexBackend,
		codexClientVersion: codexClientVersion,
		logger:             logger,
	}
}

func (r *ModelRefresher) Refresh(ctx context.Context, acct *domain.UpstreamAccount, modelRepo ModelRefresherRepo) (int, int, int, error) {
	acctID := acct.ID
	r.logger.Info("model_refresh_started",
		"account_id", acctID,
		"auth_method", acct.AuthMethod,
	)

	modelIDs, err := r.fetchUpstreamModels(ctx, acct)
	if err != nil {
		r.logger.Warn("model_refresh_upstream_failed",
			"account_id", acctID,
			"error", err,
		)
		return 0, 0, 0, err
	}
	if len(modelIDs) == 0 {
		r.logger.Info("model_refresh_no_models",
			"account_id", acctID,
		)
		return 0, 0, 0, nil
	}

	added, keptManual, err := modelRepo.ReplaceUpstreamModels(ctx, acctID, modelIDs)
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
		"total_fetched", len(modelIDs),
	)
	return added, keptManual, len(modelIDs), nil
}

func (r *ModelRefresher) fetchUpstreamModels(ctx context.Context, acct *domain.UpstreamAccount) ([]string, error) {
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

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upstream request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read upstream response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream returned status %d: %s", resp.StatusCode, string(body[:min(len(body), 256)]))
	}

	modelIDs, err := ParseModelIDs(body)
	if err != nil {
		return nil, fmt.Errorf("parse models: %w", err)
	}
	seen := make(map[string]struct{}, len(modelIDs))
	deduped := make([]string, 0, len(modelIDs))
	for _, id := range modelIDs {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		deduped = append(deduped, id)
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

func ParseModelIDs(body []byte) ([]string, error) {
	var probe struct {
		Models []json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(body, &probe); err == nil && probe.Models != nil {
		var codexPayload struct {
			Models []struct {
				Slug string `json:"slug"`
			} `json:"models"`
		}
		if err := json.Unmarshal(body, &codexPayload); err != nil {
			return nil, fmt.Errorf("decode codex models: %w", err)
		}
		ids := make([]string, 0, len(codexPayload.Models))
		for _, m := range codexPayload.Models {
			if m.Slug != "" {
				ids = append(ids, m.Slug)
			}
		}
		return ids, nil
	}
	var openAIPayload struct {
		Object string `json:"object"`
		Data   []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &openAIPayload); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if openAIPayload.Object != "" && openAIPayload.Object != "list" {
		return nil, fmt.Errorf("unexpected object type: %s", openAIPayload.Object)
	}
	ids := make([]string, 0, len(openAIPayload.Data))
	for _, m := range openAIPayload.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	return ids, nil
}

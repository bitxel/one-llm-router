package core

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/user/one-llm-router/internal/domain"
)

type GatewayUsageService struct {
	records  RequestRecordRepository
	accounts AccountRepository
}

type RequestUsageSummary struct {
	RequestCount      int
	TotalTokens       int
	CachedInputTokens int
}

type AdminUsageSummary struct {
	RequestCount      int
	TotalTokens       int
	CachedInputTokens int
	TotalCostUSD      float64
	Limits            []map[string]any
	Codex             AdminUsageCodexStatus
}

type AdminUsageCodexStatus struct {
	PlanType             string
	RateLimit            map[string]any
	Credits              map[string]any
	AdditionalRateLimits []map[string]any
}

func NewGatewayUsageService(records RequestRecordRepository, accounts AccountRepository) *GatewayUsageService {
	return &GatewayUsageService{
		records:  records,
		accounts: accounts,
	}
}

func (s *GatewayUsageService) AdminUsage(ctx context.Context) (AdminUsageSummary, error) {
	if s == nil || s.records == nil {
		return AdminUsageSummary{}, fmt.Errorf("gateway usage: request record repository is not configured")
	}
	if s.accounts == nil {
		return AdminUsageSummary{}, fmt.Errorf("gateway usage: account repository is not configured")
	}

	summary, err := s.records.UsageSummary(ctx)
	if err != nil {
		return AdminUsageSummary{}, fmt.Errorf("gateway usage: summarize request records: %w", err)
	}

	accounts, err := s.accounts.List(ctx, []string{domain.AccountStatusActive})
	if err != nil {
		return AdminUsageSummary{}, fmt.Errorf("gateway usage: list active accounts: %w", err)
	}

	return AdminUsageSummary{
		RequestCount:      summary.RequestCount,
		TotalTokens:       summary.TotalTokens,
		CachedInputTokens: summary.CachedInputTokens,
		Limits:            []map[string]any{},
		Codex: AdminUsageCodexStatus{
			PlanType:             codexPlanType(accounts),
			AdditionalRateLimits: []map[string]any{},
		},
	}, nil
}

func codexPlanType(accounts []domain.UpstreamAccount) string {
	plans := map[string]struct{}{}
	hasOAuth := false
	hasUnknown := false

	for _, account := range accounts {
		if account.Status != domain.AccountStatusActive || !account.IsOAuth() {
			continue
		}
		hasOAuth = true
		if account.PlanType == nil || strings.TrimSpace(*account.PlanType) == "" {
			hasUnknown = true
			continue
		}
		plans[strings.TrimSpace(*account.PlanType)] = struct{}{}
	}

	if !hasOAuth {
		return "guest"
	}
	if len(plans) == 0 {
		return "unknown"
	}
	if len(plans) == 1 && !hasUnknown {
		for plan := range plans {
			return plan
		}
	}
	return "mixed"
}

func usageInt(values domain.JSONMap, keys ...string) int {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			return nonNegativeJSONInt(value)
		}
	}
	return 0
}

func nonNegativeJSONInt(value any) int {
	var f float64
	switch v := value.(type) {
	case int:
		if v < 0 {
			return 0
		}
		return v
	case int64:
		f = float64(v)
	case float64:
		f = v
	case float32:
		f = float64(v)
	case json.Number:
		parsed, err := v.Float64()
		if err != nil {
			return 0
		}
		f = parsed
	case string:
		parsed, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return 0
		}
		f = parsed
	default:
		return 0
	}
	if f <= 0 || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	if f > float64(math.MaxInt) {
		return math.MaxInt
	}
	return int(f)
}

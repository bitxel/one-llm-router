package core

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/proxydial"
)

type noopModelRepo struct{}

func (noopModelRepo) ReplaceUpstreamModels(context.Context, int64, []domain.AccountModelDraft) (int, int, error) {
	return 0, 0, nil
}

// TestModelRefresher_StaleOptInFailsFast covers the 009 defence: an
// account opted into a proxy that no longer exists fails loudly and
// never silently dials direct (spec 009 §3.3 / US-4).
func TestModelRefresher_StaleOptInFailsFast(t *testing.T) {
	t.Cleanup(func() { _ = proxydial.Configure("") }) // slot must be empty

	refresher := NewModelRefresher(&http.Client{Timeout: time.Second}, nil, "", "", discardLogger())
	acct := &domain.UpstreamAccount{
		ID:         7,
		Name:       "stale-optin",
		Provider:   domain.ProviderOpenAI,
		APIKey:     "sk-x",
		Status:     domain.AccountStatusActive,
		AuthMethod: domain.AuthMethodAPIKey,
		UseProxy:   true,
	}

	_, _, _, err := refresher.Refresh(context.Background(), acct, noopModelRepo{})
	if !errors.Is(err, proxydial.ErrProxyRequired) {
		t.Fatalf("Refresh err = %v, want ErrProxyRequired chain (fail-fast, no silent direct)", err)
	}
}

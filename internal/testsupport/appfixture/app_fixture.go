// Package appfixture provides a steady-state app harness for admin API tests.
package appfixture

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/user/one-llm-router/internal/app"
	"github.com/user/one-llm-router/internal/config"
	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/store"
)

type Fixture struct {
	App     *app.App
	DBPath  string
	Repo    *store.AccountRepo
	Logger  *slog.Logger
	cfgPath string
}

func NewSteadyStateApp(t *testing.T) *Fixture {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "router.db")
	cfgPath := filepath.Join(dir, "config.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{
		Version: config.SupportedVersion,
		DB: config.DBConfig{
			Driver: "sqlite3",
			URL:    dbPath,
		},
		Runtime: config.DefaultRuntimeConfig(),
		Plugins: config.DefaultPluginsConfig(),
	}
	if err := config.WriteAtomic(cfgPath, cfg); err != nil {
		t.Fatalf("WriteAtomic(config): %v", err)
	}

	built, err := app.BuildApp(context.Background(), cfg, nil, app.Deps{
		ConfigPath: cfgPath,
		Env:        config.MapEnv(map[string]string{}),
		Logger:     logger,
	})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	t.Cleanup(func() {
		_ = built.Stop(context.Background())
	})

	return &Fixture{
		App:     built,
		DBPath:  dbPath,
		Repo:    store.NewAccountRepo(built.Store().Engine()),
		Logger:  logger,
		cfgPath: cfgPath,
	}
}

func (f *Fixture) SeedAPIKeyAccount(t *testing.T, name string) *domain.UpstreamAccount {
	t.Helper()

	account := &domain.UpstreamAccount{
		Name:       name,
		Provider:   domain.ProviderOpenAI,
		APIKey:     "sk_" + name + "_fixture_0123456789abcdef",
		Status:     domain.AccountStatusActive,
		AuthMethod: domain.AuthMethodAPIKey,
	}
	if err := f.Repo.Create(context.Background(), account); err != nil {
		t.Fatalf("SeedAPIKeyAccount(%s): %v", name, err)
	}
	return account
}

func (f *Fixture) SeedOAuthAccount(t *testing.T, name string, method domain.AuthMethod) *domain.UpstreamAccount {
	t.Helper()

	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	account := &domain.UpstreamAccount{
		Name:             name,
		Provider:         domain.ProviderOpenAI,
		Status:           domain.AccountStatusActive,
		AuthMethod:       method,
		AccessToken:      []byte("fixture_access_token_0123456789abcdef"),
		RefreshToken:     []byte("fixture_refresh_token_0123456789abcdef"),
		IDToken:          []byte("fixture_id_token_0123456789abcdef"),
		LastRefresh:      &now,
		AccessExpiresAt:  ptrTime(now.Add(2 * time.Hour)),
		Email:            ptrString(name + "@example.com"),
		PlanType:         ptrString("chatgpt-plus"),
		ChatGPTAccountID: ptrString("acct_" + name),
	}
	if _, err := f.Repo.InsertUpstreamAccount(context.Background(), account); err != nil {
		t.Fatalf("SeedOAuthAccount(%s): %v", name, err)
	}
	return account
}

func ptrString(v string) *string { return &v }

func ptrTime(v time.Time) *time.Time { return &v }

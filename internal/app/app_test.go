package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/user/one-llm-router/internal/api"
	"github.com/user/one-llm-router/internal/api/errcode"
	"github.com/user/one-llm-router/internal/app/buildinfo"
	"github.com/user/one-llm-router/internal/config"
	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/oauth"
	"github.com/user/one-llm-router/internal/provider/openai"
	"github.com/user/one-llm-router/internal/setup"
	"github.com/user/one-llm-router/internal/store"
)

// filepathStat is a tiny shim around os.Stat kept here so the test
// file's import surface stays deliberately minimal — callers read as
// "does this path exist?" rather than getting distracted by FileInfo.
func filepathStat(path string) (os.FileInfo, error) { return os.Stat(path) }

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func makeAppJWT(t *testing.T, payload map[string]any) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	payloadPart := base64.RawURLEncoding.EncodeToString(body)
	signature := base64.RawURLEncoding.EncodeToString([]byte("sig"))
	return header + "." + payloadPart + "." + signature
}

// writeConfig helps tests materialize a config.json without coupling
// to the wizard commit flow.
func writeConfig(t *testing.T, dir, dbURL string) string {
	t.Helper()
	path := filepath.Join(dir, "config.json")
	cfg := &config.Config{
		Version: config.SupportedVersion,
		DB: config.DBConfig{
			Driver: "sqlite3",
			URL:    dbURL,
		},
		Runtime: config.DefaultRuntimeConfig(),
		Plugins: config.DefaultPluginsConfig(),
	}
	if err := config.WriteAtomic(path, cfg); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	return path
}

func TestBuildApp_NilConfig_SetupPendingMode(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	a, err := BuildApp(context.Background(), nil, nil, Deps{
		ConfigPath: cfgPath,
		Env:        config.MapEnv(map[string]string{}),
		Logger:     silentLogger(),
	})
	if err != nil {
		t.Fatalf("BuildApp err = %v, want nil", err)
	}
	if a == nil {
		t.Fatal("BuildApp returned nil App")
	}
	if a.Store() != nil {
		t.Error("Store() should be nil in setup-pending mode")
	}
	if len(a.Plugins()) != 0 {
		t.Errorf("Plugins() = %d, want 0 in setup-pending", len(a.Plugins()))
	}

	// Health endpoint should still serve a degraded envelope.
	req := httptest.NewRequest(http.MethodGet, "/api/admin/health", nil)
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	status := envelopeStatus(t, rec.Body.Bytes())
	if status != "degraded" {
		t.Errorf("status = %q, want degraded", status)
	}
	var payload struct {
		Data struct {
			System map[string]string `json:"system"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json: %v body=%s", err, rec.Body.String())
	}
	if payload.Data.System["router_version"] != buildinfo.Version {
		t.Errorf("system.router_version = %q, want %q", payload.Data.System["router_version"], buildinfo.Version)
	}
	if payload.Data.System["router_git_sha"] != buildinfo.GitSHA {
		t.Errorf("system.router_git_sha = %q, want %q", payload.Data.System["router_git_sha"], buildinfo.GitSHA)
	}
	if payload.Data.System["router_built_at"] != buildinfo.BuiltAt {
		t.Errorf("system.router_built_at = %q, want %q", payload.Data.System["router_built_at"], buildinfo.BuiltAt)
	}
}

// T-028: Stop(nil) is safe; documents nil-receiver contract.
func TestApp_Stop_NilReceiver_NoPanic(t *testing.T) {
	t.Parallel()
	var a *App
	if err := a.Stop(context.Background()); err != nil {
		t.Fatalf("Stop(nil) err = %v, want nil", err)
	}
}

func TestBuildApp_SteadyState_RunsMigrationsAndHealthy(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "router.db")
	cfgPath := writeConfig(t, dir, dbFile)

	cfg := mustLoad(t, cfgPath)
	a, err := BuildApp(context.Background(), cfg, nil, Deps{
		ConfigPath: cfgPath,
		Env:        config.MapEnv(map[string]string{}),
		Logger:     silentLogger(),
	})
	if err != nil {
		t.Fatalf("BuildApp err = %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(context.Background()) })

	if a.Store() == nil {
		t.Fatal("Store() is nil after steady-state BuildApp")
	}

	// Migrations must have run (version >= 1).
	mig := a.Store()
	if mig == nil {
		t.Fatal("Store() nil")
	}

	// Health now requires at least one active upstream account to
	// report "healthy" (US-1 Edge-3 signal — zero active accounts
	// degrades health so the admin home can surface a "no healthy
	// accounts" banner). Seed one row via raw SQL against the
	// already-migrated DB to stay off the account-service surface.
	raw := openRawSQLite(t, dbFile)
	if _, err := raw.Exec(
		`INSERT INTO upstream_accounts (name, provider, api_key, status) VALUES (?, ?, ?, ?)`,
		"steady-seed", "openai", "sk-seed", "active",
	); err != nil {
		t.Fatalf("seed active account: %v", err)
	}
	_ = raw.Close()

	// Health endpoint should report healthy.
	req := httptest.NewRequest(http.MethodGet, "/api/admin/health", nil)
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if got := envelopeStatus(t, rec.Body.Bytes()); got != "healthy" {
		t.Errorf("status = %q, want healthy", got)
	}
}

func TestBuildApp_SteadyState_HealthAccountsProjection(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "router.db")
	cfgPath := writeConfig(t, dir, dbFile)

	cfg := mustLoad(t, cfgPath)
	a, err := BuildApp(context.Background(), cfg, nil, Deps{
		ConfigPath: cfgPath,
		Env:        config.MapEnv(map[string]string{}),
		Logger:     silentLogger(),
	})
	if err != nil {
		t.Fatalf("BuildApp err = %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(context.Background()) })

	repo := store.NewAccountRepo(a.Store().Engine())
	active := &domain.UpstreamAccount{
		Name:       "health-active",
		Provider:   domain.ProviderOpenAI,
		APIKey:     "sk-health-active",
		Status:     domain.AccountStatusActive,
		AuthMethod: domain.AuthMethodAPIKey,
	}
	if err := repo.Create(context.Background(), active); err != nil {
		t.Fatalf("seed active account: %v", err)
	}
	disabled := &domain.UpstreamAccount{
		Name:       "health-disabled",
		Provider:   domain.ProviderOpenAI,
		APIKey:     "sk-health-disabled",
		Status:     domain.AccountStatusDisabled,
		AuthMethod: domain.AuthMethodAPIKey,
	}
	if err := repo.Create(context.Background(), disabled); err != nil {
		t.Fatalf("seed disabled account: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/health?account_id="+strconv.FormatInt(active.ID, 10), nil)
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var env struct {
		Code int `json:"code"`
		Data struct {
			Status           string         `json:"status"`
			ActiveAccounts   int64          `json:"active_accounts"`
			DisabledAccounts int64          `json:"disabled_accounts"`
			Accounts         map[string]any `json:"accounts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal health response: %v body=%s", err, rec.Body.String())
	}
	if env.Code != 0 {
		t.Fatalf("envelope code = %d, want 0 body=%s", env.Code, rec.Body.String())
	}
	if env.Data.Status != "healthy" {
		t.Fatalf("data.status = %q, want healthy body=%s", env.Data.Status, rec.Body.String())
	}
	if env.Data.ActiveAccounts != 1 {
		t.Fatalf("active_accounts = %d, want 1", env.Data.ActiveAccounts)
	}
	if env.Data.DisabledAccounts != 1 {
		t.Fatalf("disabled_accounts = %d, want 1", env.Data.DisabledAccounts)
	}
	if got, ok := env.Data.Accounts["active"].(float64); !ok || got != 1 {
		t.Fatalf("accounts.active = %#v, want 1", env.Data.Accounts["active"])
	}
	accountProbe, ok := env.Data.Accounts[strconv.FormatInt(active.ID, 10)].(map[string]any)
	if !ok {
		t.Fatalf("accounts[%d] = %#v, want object", active.ID, env.Data.Accounts[strconv.FormatInt(active.ID, 10)])
	}
	if got := accountProbe["status"]; got != string(domain.AccountStatusActive) {
		t.Fatalf("accounts[%d].status = %#v, want %q", active.ID, got, domain.AccountStatusActive)
	}
}

func TestBuildApp_PlaygroundRouteWiring(t *testing.T) {
	t.Run("steady state registers playground admin route", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		dbFile := filepath.Join(dir, "router.db")
		cfgPath := writeConfig(t, dir, dbFile)

		cfg := mustLoad(t, cfgPath)
		a, err := BuildApp(context.Background(), cfg, nil, Deps{
			ConfigPath: cfgPath,
			Env:        config.MapEnv(map[string]string{}),
			Logger:     silentLogger(),
		})
		if err != nil {
			t.Fatalf("BuildApp err = %v", err)
		}
		t.Cleanup(func() { _ = a.Stop(context.Background()) })

		req := httptest.NewRequest(http.MethodPost, "/api/admin/playground/run", strings.NewReader(`{"selection_mode":"auto","model":"gpt-5.4-mini","text":""}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		a.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 body=%s", rec.Code, rec.Body.String())
		}
		if got := envelopeCode(t, rec.Body.Bytes()); got != errcode.InvalidPlaygroundRequest {
			t.Fatalf("envelope code = %d, want %d body=%s", got, errcode.InvalidPlaygroundRequest, rec.Body.String())
		}
		if rec.Header().Get("X-Request-Id") == "" {
			t.Fatalf("X-Request-Id header is empty")
		}
		if strings.Contains(rec.Body.String(), "X-Request-Id") {
			t.Fatalf("request id leaked into response body: %s", rec.Body.String())
		}
	})

	t.Run("setup pending gate protects playground route", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.json")

		a, err := BuildApp(context.Background(), nil, nil, Deps{
			ConfigPath: cfgPath,
			Env:        config.MapEnv(map[string]string{}),
			Logger:     silentLogger(),
		})
		if err != nil {
			t.Fatalf("BuildApp err = %v", err)
		}

		req := httptest.NewRequest(http.MethodPost, "/api/admin/playground/run", strings.NewReader(`{"selection_mode":"auto","model":"gpt-5.4-mini","text":"ping"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		a.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 body=%s", rec.Code, rec.Body.String())
		}
		if got := envelopeCode(t, rec.Body.Bytes()); got != errcode.SetupRequired {
			t.Fatalf("envelope code = %d, want %d body=%s", got, errcode.SetupRequired, rec.Body.String())
		}
	})
}

// TestBuildApp_SteadyState_DegradedWhenNoActiveAccounts pins the
// spec US-1 Edge-3 signal the admin-home banner depends on: after
// wizard commit, if the seeded upstream key is later rejected /
// disabled, `active == 0` MUST surface as health `degraded` rather
// than `healthy` — otherwise the banner can never fire.
func TestBuildApp_SteadyState_DegradedWhenNoActiveAccounts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "router.db")
	cfgPath := writeConfig(t, dir, dbFile)

	cfg := mustLoad(t, cfgPath)
	a, err := BuildApp(context.Background(), cfg, nil, Deps{
		ConfigPath: cfgPath,
		Env:        config.MapEnv(map[string]string{}),
		Logger:     silentLogger(),
	})
	if err != nil {
		t.Fatalf("BuildApp err = %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(context.Background()) })

	req := httptest.NewRequest(http.MethodGet, "/api/admin/health", nil)
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := envelopeStatus(t, rec.Body.Bytes()); got != "degraded" {
		t.Errorf("status = %q, want degraded (no active accounts)", got)
	}
}

func TestApp003Wiring(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbFile := filepath.Join(dir, "router.db")
	cfgPath := writeConfig(t, dir, dbFile)
	cfg := mustLoad(t, cfgPath)

	a, err := BuildApp(context.Background(), cfg, nil, Deps{
		ConfigPath: cfgPath,
		Env:        config.MapEnv(map[string]string{}),
		Logger:     silentLogger(),
	})
	if err != nil {
		t.Fatalf("BuildApp err = %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(context.Background()) })

	if a.oauthCoordinator == nil {
		t.Fatal("oauthCoordinator = nil, want coordinator wired in steady-state app")
	}

	req := httptest.NewRequest(http.MethodPost, "/api/admin/oauth/browser/start", strings.NewReader(`{"provider":"openai"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var env struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			FlowID        string `json:"flow_id"`
			AuthorizeURL  string `json:"authorize_url"`
			CallbackURL   string `json:"callback_url"`
			ListenerBound bool   `json:"listener_bound"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal oauth browser start response: %v — body=%s", err, rec.Body.String())
	}
	if env.Code != 0 {
		t.Fatalf("envelope code = %d, want 0 — body=%s", env.Code, rec.Body.String())
	}
	if env.Data.FlowID == "" {
		t.Errorf("flow_id = empty, want non-empty")
	}
	if env.Data.AuthorizeURL == "" {
		t.Errorf("authorize_url = empty, want non-empty")
	}
	if env.Data.CallbackURL == "" {
		t.Errorf("callback_url = empty, want non-empty")
	}
}

func TestApp003Wiring_DeviceStartRouteRegistered(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbFile := filepath.Join(dir, "router.db")
	cfgPath := writeConfig(t, dir, dbFile)
	cfg := mustLoad(t, cfgPath)
	fake := oauth.NewFakeClock(time.Date(2026, 4, 22, 18, 0, 0, 0, time.UTC))
	provider := &appDeviceProvider{
		deviceCode: oauth.DeviceCode{
			DeviceAuthID:    "dev_app_route_123",
			UserCode:        "ABCD-1234",
			VerificationURL: "https://auth.openai.com/codex/device",
			Interval:        5 * time.Second,
			ExpiresIn:       15 * time.Minute,
		},
	}

	a, err := BuildApp(context.Background(), cfg, nil, Deps{
		ConfigPath:    cfgPath,
		Env:           config.MapEnv(map[string]string{}),
		Logger:        silentLogger(),
		OAuthProvider: provider,
		OAuthClock:    fake,
	})
	if err != nil {
		t.Fatalf("BuildApp err = %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(context.Background()) })

	req := httptest.NewRequest(http.MethodPost, "/api/admin/oauth/device/start", strings.NewReader(`{"provider":"openai"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}

	var env struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			FlowID          string `json:"flow_id"`
			UserCode        string `json:"user_code"`
			VerificationURL string `json:"verification_url"`
			IntervalSeconds int    `json:"interval_seconds"`
			Method          string `json:"method"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal oauth device start response: %v — body=%s", err, rec.Body.String())
	}
	if env.Code != 0 {
		t.Fatalf("envelope code = %d, want 0 — body=%s", env.Code, rec.Body.String())
	}
	if env.Data.FlowID == "" {
		t.Fatal("flow_id = empty, want non-empty")
	}
	if env.Data.UserCode != "ABCD-1234" {
		t.Fatalf("user_code = %q, want ABCD-1234", env.Data.UserCode)
	}
	if env.Data.VerificationURL == "" {
		t.Fatal("verification_url = empty, want non-empty")
	}
	if env.Data.IntervalSeconds != 5 {
		t.Fatalf("interval_seconds = %d, want 5", env.Data.IntervalSeconds)
	}
	if env.Data.Method != "device" {
		t.Fatalf("method = %q, want device", env.Data.Method)
	}
	if got := provider.requests.Load(); got != 1 {
		t.Fatalf("RequestDeviceCode calls = %d, want 1", got)
	}
}

func TestApp003Wiring_ImportAuthJSONRouteRegistered(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbFile := filepath.Join(dir, "router.db")
	cfgPath := writeConfig(t, dir, dbFile)
	cfg := mustLoad(t, cfgPath)

	a, err := BuildApp(context.Background(), cfg, nil, Deps{
		ConfigPath: cfgPath,
		Env:        config.MapEnv(map[string]string{}),
		Logger:     silentLogger(),
	})
	if err != nil {
		t.Fatalf("BuildApp err = %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(context.Background()) })

	token := makeAppJWT(t, map[string]any{
		"email": "imported@app.test",
		"exp":   float64(time.Date(2026, 4, 22, 21, 0, 0, 0, time.UTC).Unix()),
		"https://api.openai.com/auth": map[string]any{
			"plan_type": "chatgpt-plus",
		},
	})
	authJSON, err := json.Marshal(map[string]any{
		"tokens": map[string]any{
			"access_token":  "acc-app-import",
			"refresh_token": "ref-app-import",
			"id_token":      token,
		},
	})
	if err != nil {
		t.Fatalf("marshal auth.json: %v", err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("auth_json", "auth.json")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(authJSON); err != nil {
		t.Fatalf("part.Write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/admin/accounts/import-auth-json", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}

	var env struct {
		Code int            `json:"code"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal import auth.json response: %v — body=%s", err, rec.Body.String())
	}
	if env.Code != 0 {
		t.Fatalf("envelope code = %d, want 0 — body=%s", env.Code, rec.Body.String())
	}
	account, ok := env.Data["account"].(map[string]any)
	if !ok {
		t.Fatalf("data.account type = %T, want object", env.Data["account"])
	}
	if account["id"] == float64(0) {
		t.Fatal("account.id = 0, want non-zero")
	}
	if account["name"] != "imported@app.test" {
		t.Fatalf("account.name = %#v, want imported@app.test", account["name"])
	}
	if account["provider"] != "openai" {
		t.Fatalf("account.provider = %#v, want openai", account["provider"])
	}
	if account["auth_method"] != "oauth_import" {
		t.Fatalf("account.auth_method = %#v, want oauth_import", account["auth_method"])
	}
	if account["email"] != "imported@app.test" {
		t.Fatalf("account.email = %#v, want imported@app.test", account["email"])
	}
	if account["plan_type"] != "chatgpt-plus" {
		t.Fatalf("account.plan_type = %#v, want chatgpt-plus", account["plan_type"])
	}
	if account["plan_type_label"] != "ChatGPT Plus" {
		t.Fatalf("account.plan_type_label = %#v, want ChatGPT Plus", account["plan_type_label"])
	}
	if _, ok := account["access_expires_at"]; !ok {
		t.Fatal("account.access_expires_at missing, want present")
	}
	if _, leaked := account["access_token"]; leaked {
		t.Fatalf("account leaked access_token: %#v", account["access_token"])
	}
	if _, leaked := account["refresh_token"]; leaked {
		t.Fatalf("account leaked refresh_token: %#v", account["refresh_token"])
	}
	if _, leaked := account["id_token"]; leaked {
		t.Fatalf("account leaked id_token: %#v", account["id_token"])
	}
}

func TestApp003Wiring_ExportAuthJSONRouteRegistered(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbFile := filepath.Join(dir, "router.db")
	cfgPath := writeConfig(t, dir, dbFile)
	cfg := mustLoad(t, cfgPath)

	a, err := BuildApp(context.Background(), cfg, nil, Deps{
		ConfigPath: cfgPath,
		Env:        config.MapEnv(map[string]string{}),
		Logger:     silentLogger(),
	})
	if err != nil {
		t.Fatalf("BuildApp err = %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(context.Background()) })

	repo := store.NewAccountRepo(a.store.Engine())
	lastRefresh := time.Date(2026, 4, 22, 22, 0, 0, 0, time.UTC)
	expiresAt := lastRefresh.Add(90 * time.Minute)
	idToken := makeAppJWT(t, map[string]any{
		"email": "export@app.test",
	})
	account := &domain.UpstreamAccount{
		Name:            "export-route",
		Provider:        domain.ProviderOpenAI,
		Status:          domain.AccountStatusActive,
		AuthMethod:      domain.AuthMethodOAuthImport,
		AccessToken:     []byte("app-export-access"),
		RefreshToken:    []byte("app-export-refresh"),
		IDToken:         []byte(idToken),
		LastRefresh:     &lastRefresh,
		AccessExpiresAt: &expiresAt,
	}
	if _, err := repo.InsertUpstreamAccount(context.Background(), account); err != nil {
		t.Fatalf("InsertUpstreamAccount err = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/admin/accounts/1/export-auth-json", nil)
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="auth.json"` {
		t.Fatalf("Content-Disposition = %q, want attachment header", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store, private" {
		t.Fatalf("Cache-Control = %q, want no-store, private", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want application/json; charset=utf-8", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("X-Request-Id"); got == "" {
		t.Fatal("X-Request-Id empty, want non-empty request id")
	}

	var body struct {
		OpenAIAPIKey *string `json:"OPENAI_API_KEY"`
		Tokens       struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			IDToken      string `json:"id_token"`
		} `json:"tokens"`
		LastRefresh time.Time `json:"last_refresh"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal export auth.json response: %v — body=%s", err, rec.Body.String())
	}
	if body.OpenAIAPIKey != nil {
		t.Fatalf("OPENAI_API_KEY = %v, want nil", *body.OpenAIAPIKey)
	}
	if body.Tokens.AccessToken != "app-export-access" {
		t.Fatalf("access_token = %q, want app-export-access", body.Tokens.AccessToken)
	}
	if body.Tokens.RefreshToken != "app-export-refresh" {
		t.Fatalf("refresh_token = %q, want app-export-refresh", body.Tokens.RefreshToken)
	}
	if body.Tokens.IDToken != idToken {
		t.Fatalf("id_token = %q, want seeded token", body.Tokens.IDToken)
	}
	if !body.LastRefresh.UTC().Equal(lastRefresh.UTC()) {
		t.Fatalf("last_refresh = %s, want %s", body.LastRefresh.UTC(), lastRefresh.UTC())
	}
}

func TestApp003Wiring_ExportAuthJSONRouteRegistered_APIKeyBackstop(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbFile := filepath.Join(dir, "router.db")
	cfgPath := writeConfig(t, dir, dbFile)
	cfg := mustLoad(t, cfgPath)

	a, err := BuildApp(context.Background(), cfg, nil, Deps{
		ConfigPath: cfgPath,
		Env:        config.MapEnv(map[string]string{}),
		Logger:     silentLogger(),
	})
	if err != nil {
		t.Fatalf("BuildApp err = %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(context.Background()) })

	repo := store.NewAccountRepo(a.store.Engine())
	account := &domain.UpstreamAccount{
		Name:       "export-api-key",
		Provider:   domain.ProviderOpenAI,
		APIKey:     "sk-export-backstop",
		Status:     domain.AccountStatusActive,
		AuthMethod: domain.AuthMethodAPIKey,
	}
	if err := repo.Create(context.Background(), account); err != nil {
		t.Fatalf("Create err = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/admin/accounts/1/export-auth-json", nil)
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		Code int            `json:"code"`
		Msg  string         `json:"msg"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal envelope: %v — body=%s", err, rec.Body.String())
	}
	if env.Code != 3014 {
		t.Fatalf("code = %d, want 3014 body=%s", env.Code, rec.Body.String())
	}
	if env.Data["auth_method"] != "api_key" {
		t.Fatalf("data.auth_method = %#v, want api_key", env.Data["auth_method"])
	}
	if got := rec.Header().Get("Content-Disposition"); got != "" {
		t.Fatalf("Content-Disposition = %q, want empty on envelope branch", got)
	}
}

func TestApp003Wiring_ExportAuthJSONRouteRegistered_NotFound(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbFile := filepath.Join(dir, "router.db")
	cfgPath := writeConfig(t, dir, dbFile)
	cfg := mustLoad(t, cfgPath)

	a, err := BuildApp(context.Background(), cfg, nil, Deps{
		ConfigPath: cfgPath,
		Env:        config.MapEnv(map[string]string{}),
		Logger:     silentLogger(),
	})
	if err != nil {
		t.Fatalf("BuildApp err = %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(context.Background()) })

	req := httptest.NewRequest(http.MethodPost, "/api/admin/accounts/999/export-auth-json", nil)
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		Code int            `json:"code"`
		Msg  string         `json:"msg"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal envelope: %v — body=%s", err, rec.Body.String())
	}
	if env.Code != 1001 {
		t.Fatalf("code = %d, want 1001 body=%s", env.Code, rec.Body.String())
	}
	if len(env.Data) != 0 {
		t.Fatalf("data = %#v, want empty object", env.Data)
	}
	if got := rec.Header().Get("Content-Disposition"); got != "" {
		t.Fatalf("Content-Disposition = %q, want empty on envelope branch", got)
	}
}

func TestApp003Wiring_ExportAuthJSONRouteRegistered_CorruptedOAuthIs3902(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbFile := filepath.Join(dir, "router.db")
	cfgPath := writeConfig(t, dir, dbFile)
	cfg := mustLoad(t, cfgPath)

	a, err := BuildApp(context.Background(), cfg, nil, Deps{
		ConfigPath: cfgPath,
		Env:        config.MapEnv(map[string]string{}),
		Logger:     silentLogger(),
	})
	if err != nil {
		t.Fatalf("BuildApp err = %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(context.Background()) })

	repo := store.NewAccountRepo(a.store.Engine())
	lastRefresh := time.Date(2026, 4, 22, 23, 0, 0, 0, time.UTC)
	expiresAt := lastRefresh.Add(90 * time.Minute)
	idToken := makeAppJWT(t, map[string]any{"email": "corrupt@app.test"})
	account := &domain.UpstreamAccount{
		Name:            "export-corrupt",
		Provider:        domain.ProviderOpenAI,
		Status:          domain.AccountStatusActive,
		AuthMethod:      domain.AuthMethodOAuthImport,
		AccessToken:     []byte("corrupt-access"),
		RefreshToken:    []byte("corrupt-refresh"),
		IDToken:         []byte(idToken),
		LastRefresh:     &lastRefresh,
		AccessExpiresAt: &expiresAt,
	}
	if _, err := repo.InsertUpstreamAccount(context.Background(), account); err != nil {
		t.Fatalf("InsertUpstreamAccount err = %v", err)
	}
	if _, err := a.store.Engine().ID(account.ID).Cols("access_token").Update(&domain.UpstreamAccount{}); err != nil {
		t.Fatalf("corrupt export row err = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/admin/accounts/1/export-auth-json", nil)
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal envelope: %v — body=%s", err, rec.Body.String())
	}
	if env.Code != 3902 {
		t.Fatalf("code = %d, want 3902 body=%s", env.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Disposition"); got != "" {
		t.Fatalf("Content-Disposition = %q, want empty on system error branch", got)
	}
}

func TestApp003Wiring_StopCancelsPendingDevicePoller(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbFile := filepath.Join(dir, "router.db")
	cfgPath := writeConfig(t, dir, dbFile)
	cfg := mustLoad(t, cfgPath)
	fake := oauth.NewFakeClock(time.Date(2026, 4, 22, 18, 30, 0, 0, time.UTC))
	provider := &appDeviceProvider{
		deviceCode: oauth.DeviceCode{
			DeviceAuthID:    "dev_app_stop_123",
			UserCode:        "STOP-POLL",
			VerificationURL: "https://auth.openai.com/codex/device",
			Interval:        5 * time.Second,
			ExpiresIn:       15 * time.Minute,
		},
	}

	a, err := BuildApp(context.Background(), cfg, nil, Deps{
		ConfigPath:    cfgPath,
		Env:           config.MapEnv(map[string]string{}),
		Logger:        silentLogger(),
		OAuthProvider: provider,
		OAuthClock:    fake,
	})
	if err != nil {
		t.Fatalf("BuildApp err = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/admin/oauth/device/start", strings.NewReader(`{"provider":"openai"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}

	if err := a.Stop(context.Background()); err != nil {
		t.Fatalf("Stop err = %v", err)
	}
	fake.Step(30 * time.Second)

	if got := provider.pollCalls.Load(); got != 0 {
		t.Fatalf("PollDeviceCode calls = %d, want 0 after Stop", got)
	}
}

func TestApp003Wiring_OAuthResponsesStreamFalseReturnsCollectedJSON(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbFile := filepath.Join(dir, "router.db")
	cfgPath := writeConfig(t, dir, dbFile)
	cfg := mustLoad(t, cfgPath)

	seen := make(chan struct {
		auth             string
		chatGPTAccountID string
		userAgent        string
		path             string
		body             string
	}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen <- struct {
			auth             string
			chatGPTAccountID string
			userAgent        string
			path             string
			body             string
		}{
			auth:             r.Header.Get("Authorization"),
			chatGPTAccountID: r.Header.Get("chatgpt-account-id"),
			userAgent:        r.Header.Get("User-Agent"),
			path:             r.URL.Path,
			body:             string(body),
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(strings.Join([]string{
			`event: response.output_text.delta`,
			`data: {"type":"response.output_text.delta","delta":"OK"}`,
			``,
			`event: response.completed`,
			`data: {"type":"response.completed","response":{"id":"resp_123"}}`,
			``,
		}, "\n")))
	}))
	defer upstream.Close()

	a, err := BuildApp(context.Background(), cfg, nil, Deps{
		ConfigPath:          cfgPath,
		Env:                 config.MapEnv(map[string]string{}),
		Logger:              silentLogger(),
		CodexBackendBaseURL: upstream.URL,
	})
	if err != nil {
		t.Fatalf("BuildApp err = %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(context.Background()) })

	repo := store.NewAccountRepo(a.Store().Engine())
	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	acct := &domain.UpstreamAccount{
		Name:             "app-wiring-oauth",
		Provider:         domain.ProviderOpenAI,
		BaseURL:          stringPtr(upstream.URL),
		Status:           domain.AccountStatusActive,
		AuthMethod:       domain.AuthMethodOAuthBrowser,
		AccessToken:      []byte("wired-access-token"),
		RefreshToken:     []byte("wired-refresh-token"),
		IDToken:          mustTestJWT(t, map[string]any{"email": "wired@example.com"}),
		LastRefresh:      timePtr(now),
		AccessExpiresAt:  timePtr(now.Add(2 * time.Hour)),
		Email:            stringPtr("wired@example.com"),
		PlanType:         stringPtr("chatgpt-plus"),
		ChatGPTAccountID: stringPtr("acct-wired"),
	}
	if _, err := repo.InsertUpstreamAccount(context.Background(), acct); err != nil {
		t.Fatalf("InsertUpstreamAccount err = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-4o-mini","input":"ping","stream":false}`))
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if rec.Body.String() != `{"id":"resp_123","output_text":"OK"}` {
		t.Fatalf("body = %s, want collected JSON", rec.Body.String())
	}
	got := <-seen
	if got.auth != "Bearer wired-access-token" {
		t.Fatalf("Authorization = %q, want Bearer wired-access-token", got.auth)
	}
	if got.chatGPTAccountID != "acct-wired" {
		t.Fatalf("chatgpt-account-id = %q, want acct-wired", got.chatGPTAccountID)
	}
	if got.userAgent != openai.CodexCLIUserAgent {
		t.Fatalf("User-Agent = %q, want %q", got.userAgent, openai.CodexCLIUserAgent)
	}
	if got.path != "/codex/responses" {
		t.Fatalf("path = %q, want /codex/responses", got.path)
	}
	if !strings.Contains(got.body, `"stream":true`) {
		t.Fatalf("upstream body = %s, want forced stream true", got.body)
	}
}

func TestApp003Wiring_OAuthResponsesStreamTrueKeepsSSE(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbFile := filepath.Join(dir, "router.db")
	cfgPath := writeConfig(t, dir, dbFile)
	cfg := mustLoad(t, cfgPath)

	sseBody := strings.Join([]string{
		`event: response.completed`,
		`data: {"type":"response.completed","response":{"id":"resp_stream"}}`,
		``,
	}, "\n")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/codex/responses" {
			t.Errorf("path = %q, want /codex/responses", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sseBody))
	}))
	defer upstream.Close()

	a, err := BuildApp(context.Background(), cfg, nil, Deps{
		ConfigPath:          cfgPath,
		Env:                 config.MapEnv(map[string]string{}),
		Logger:              silentLogger(),
		CodexBackendBaseURL: upstream.URL,
	})
	if err != nil {
		t.Fatalf("BuildApp err = %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(context.Background()) })

	repo := store.NewAccountRepo(a.Store().Engine())
	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	acct := &domain.UpstreamAccount{
		Name:             "app-wiring-oauth-stream",
		Provider:         domain.ProviderOpenAI,
		BaseURL:          stringPtr(upstream.URL),
		Status:           domain.AccountStatusActive,
		AuthMethod:       domain.AuthMethodOAuthBrowser,
		AccessToken:      []byte("wired-access-token"),
		RefreshToken:     []byte("wired-refresh-token"),
		IDToken:          mustTestJWT(t, map[string]any{"email": "wired@example.com"}),
		LastRefresh:      timePtr(now),
		AccessExpiresAt:  timePtr(now.Add(2 * time.Hour)),
		Email:            stringPtr("wired@example.com"),
		PlanType:         stringPtr("chatgpt-plus"),
		ChatGPTAccountID: stringPtr("acct-wired"),
	}
	if _, err := repo.InsertUpstreamAccount(context.Background(), acct); err != nil {
		t.Fatalf("InsertUpstreamAccount err = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-4o-mini","input":"ping","stream":true}`))
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	if rec.Body.String() != sseBody {
		t.Fatalf("body = %q, want %q", rec.Body.String(), sseBody)
	}
}

func TestApp003Wiring_ProxyInvokesRefreshHookForOAuthRows(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbFile := filepath.Join(dir, "router.db")
	cfgPath := writeConfig(t, dir, dbFile)
	cfg := mustLoad(t, cfgPath)

	var upstreamHits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamHits.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"resp_123"}`))
	}))
	defer upstream.Close()

	a, err := BuildApp(context.Background(), cfg, nil, Deps{
		ConfigPath:          cfgPath,
		Env:                 config.MapEnv(map[string]string{}),
		Logger:              silentLogger(),
		CodexBackendBaseURL: upstream.URL,
	})
	if err != nil {
		t.Fatalf("BuildApp err = %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(context.Background()) })

	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	_, err = a.Store().Engine().Exec(
		`INSERT INTO upstream_accounts
			(name, provider, base_url, status, auth_method, access_token, created_at, updated_at)
		  VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"malformed-oauth-row",
		domain.ProviderOpenAI,
		upstream.URL,
		domain.AccountStatusActive,
		domain.AuthMethodOAuthBrowser,
		[]byte("stored-access-token"),
		now,
		now,
	)
	if err != nil {
		t.Fatalf("seed malformed oauth row: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-4o-mini"}`))
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 body=%s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("unmarshal router error: %v body=%s", err, rec.Body.String())
	}
	if envelope.Error.Code != "internal_error" || envelope.Error.Message != "upstream credentials unavailable" {
		t.Fatalf("router error = %+v, want internal_error/upstream credentials unavailable", envelope.Error)
	}
	if upstreamHits.Load() != 0 {
		t.Fatalf("upstream hits = %d, want 0", upstreamHits.Load())
	}
}

func TestBuildApp_MissingConfigPath_Rejected(t *testing.T) {
	t.Parallel()
	_, err := BuildApp(context.Background(), nil, nil, Deps{})
	if err == nil {
		t.Fatal("err = nil, want ConfigPath required")
	}
	// T-003: assert the sentinel message — any error (including
	// unrelated nil-pointer panics wrapped into an error) would
	// have passed a bare `err == nil` check, defeating the
	// regression intent.
	if !strings.Contains(err.Error(), "ConfigPath") {
		t.Errorf("err = %q, want to mention 'ConfigPath' so the contract stays verifiable", err.Error())
	}
}

// TestBuildApp_SetupPending_V1Returns503Native exercises decision D1
// end-to-end via BuildApp (not just the gate unit): a greenfield
// setup-pending boot must return HTTP 503 + 001 native error shape
// on /v1/*, never the envelope. This is T-028's "greenfield /v1
// blocked" assertion wired through the full middleware chain.
func TestBuildApp_SetupPending_V1Returns503Native(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	a, err := BuildApp(context.Background(), nil, nil, Deps{
		ConfigPath: cfgPath,
		Env:        config.MapEnv(map[string]string{}),
		Logger:     silentLogger(),
	})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 (D1 — /v1 never gets envelope during setup)", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, `"code":`) && !strings.Contains(body, `"error"`) {
		t.Errorf("body = %s, want 001 native {\"error\":...} shape not envelope", body)
	}
	if !strings.Contains(body, "setup_required") {
		t.Errorf("body = %s, want setup_required code", body)
	}
}

// TestBuildApp_HealthSharesLatchWithGate is the L-001 regression
// guard. Before the fix, /api/admin/health stat'd disk on every
// request while the gate latched open on first observation — giving
// operators a split-brain where health reported setup_state="pending"
// while the gate kept serving traffic. After the fix, health reads
// gate.ProbeState() so the two agree across an FR-007 post-latch
// deletion of config.json.
func TestBuildApp_HealthSharesLatchWithGate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "router.db")
	cfgPath := writeConfig(t, dir, dbFile)
	cfg := mustLoad(t, cfgPath)

	a, err := BuildApp(context.Background(), cfg, nil, Deps{
		ConfigPath: cfgPath,
		Env:        config.MapEnv(map[string]string{}),
		Logger:     silentLogger(),
	})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(context.Background()) })

	// Prime the latch.
	req := httptest.NewRequest(http.MethodGet, "/api/admin/health", nil)
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("initial health status = %d, want 200", rec.Code)
	}
	if got := envelopeSetupState(t, rec.Body.Bytes()); got != "done" {
		t.Fatalf("initial setup_state = %q, want done", got)
	}

	// Delete config.json post-latch. FR-007 requires both the gate
	// and /api/admin/health to keep reporting "done".
	if err := os.Remove(cfgPath); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/admin/health", nil)
	rec = httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, req)
	if got := envelopeSetupState(t, rec.Body.Bytes()); got != "done" {
		t.Errorf("post-deletion setup_state = %q, want done — L-001 split-brain regression", got)
	}

	// Gate must still pass /admin/* without redirecting.
	req = httptest.NewRequest(http.MethodGet, "/admin/settings", nil)
	rec = httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, req)
	if rec.Code == http.StatusFound {
		t.Error("gate redirected /admin/* after post-latch config deletion — FR-007 regression")
	}
}

func TestBuildApp_UnknownDriver_Errors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfg := &config.Config{
		Version: config.SupportedVersion,
		DB:      config.DBConfig{Driver: "oracle", URL: "x"},
		Runtime: config.DefaultRuntimeConfig(),
		Plugins: config.DefaultPluginsConfig(),
	}

	a, err := BuildApp(context.Background(), cfg, nil, Deps{
		ConfigPath: cfgPath,
		Logger:     silentLogger(),
	})
	if err == nil {
		t.Fatal("err = nil, want driver error")
	}
	if a != nil {
		t.Error("App should be nil on error")
	}
}

func TestBuildApp_SetupGate_RedirectsHTMLPaths(t *testing.T) {
	// Even with a valid config on disk, /admin/* HTML paths should NOT
	// redirect because setup is done. This verifies the gate integrates
	// correctly.
	t.Parallel()
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "router.db")
	cfgPath := writeConfig(t, dir, dbFile)
	cfg := mustLoad(t, cfgPath)

	a, err := BuildApp(context.Background(), cfg, nil, Deps{
		ConfigPath: cfgPath,
		Logger:     silentLogger(),
	})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(context.Background()) })

	req := httptest.NewRequest(http.MethodGet, "/admin/settings", nil)
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, req)

	// Setup done → gate latched open → mux takes over → 404 (no SPA
	// yet in 002 foundation scope).
	if rec.Code == http.StatusFound {
		t.Errorf("setup-done boot must NOT redirect /admin/*, got 302")
	}
}

func TestBuildApp_RootRedirectsBySetupState(t *testing.T) {
	t.Parallel()

	t.Run("setup pending redirects root to setup", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.json")

		a, err := BuildApp(context.Background(), nil, nil, Deps{
			ConfigPath: cfgPath,
			Logger:     silentLogger(),
		})
		if err != nil {
			t.Fatalf("BuildApp: %v", err)
		}
		t.Cleanup(func() { _ = a.Stop(context.Background()) })

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		a.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusFound {
			t.Errorf("status = %d, want 302", rec.Code)
		}
		if got := rec.Header().Get("Location"); got != "/setup/" {
			t.Errorf("Location = %q, want /setup/", got)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("Cache-Control = %q, want no-store", got)
		}
	})

	t.Run("setup done redirects root to admin", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		dbFile := filepath.Join(dir, "router.db")
		cfgPath := writeConfig(t, dir, dbFile)
		cfg := mustLoad(t, cfgPath)

		a, err := BuildApp(context.Background(), cfg, nil, Deps{
			ConfigPath: cfgPath,
			Logger:     silentLogger(),
		})
		if err != nil {
			t.Fatalf("BuildApp: %v", err)
		}
		t.Cleanup(func() { _ = a.Stop(context.Background()) })

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		a.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusFound {
			t.Errorf("status = %d, want 302", rec.Code)
		}
		if got := rec.Header().Get("Location"); got != "/admin/" {
			t.Errorf("Location = %q, want /admin/", got)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("Cache-Control = %q, want no-store", got)
		}
	})
}

func TestBuildApp_SetupPending_RedirectsAdmin(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	a, err := BuildApp(context.Background(), nil, nil, Deps{
		ConfigPath: cfgPath,
		Logger:     silentLogger(),
	})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/settings", nil)
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Errorf("status = %d, want 302 on setup-pending /admin/*", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/setup/" {
		t.Errorf("Location = %q, want /setup/", got)
	}
}

func TestBuildApp_Stop_Idempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := writeConfig(t, dir, filepath.Join(dir, "router.db"))
	cfg := mustLoad(t, cfgPath)

	a, err := BuildApp(context.Background(), cfg, nil, Deps{
		ConfigPath: cfgPath,
		Logger:     silentLogger(),
	})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}

	// Stop without Start should not crash.
	if err := a.Stop(context.Background()); err != nil {
		t.Errorf("Stop(no-start) err = %v, want nil", err)
	}
}

// TestBuildApp_Brownfield_MaterializesAndBoots verifies the FR-002
// auto-materialization path: a pre-existing DB with upstream_accounts
// rows AND env carrying ROUTER_DB_DRIVER/ROUTER_DB_URL (but no
// config.json on disk) must cause BuildApp to synthesize config.json
// and proceed to steady-state instead of entering setup-pending mode.
//
// This is the Fix-F1 regression guard — earlier BuildApp revisions
// short-circuited to setup-pending before tryBrownfield ever ran.
func TestBuildApp_Brownfield_MaterializesAndBoots(t *testing.T) {
	t.Parallel()
	// We use config.MapEnv instead of t.Setenv so this test is
	// parallel-safe: nothing here mutates the process-wide env and
	// the brownfield path is driven entirely by the Deps.Env hook.
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "brownfield.db")
	cfgPath := filepath.Join(dir, "config.json")

	// Pre-seed the DB: create schema via a throwaway BuildApp round
	// and insert one row so the brownfield count crosses the threshold.
	seedCfg := &config.Config{
		Version: config.SupportedVersion,
		DB:      config.DBConfig{Driver: "sqlite3", URL: dbFile},
		Runtime: config.DefaultRuntimeConfig(),
		Plugins: config.DefaultPluginsConfig(),
	}
	seed, err := BuildApp(context.Background(), seedCfg, nil, Deps{
		ConfigPath: filepath.Join(dir, "seed-config.json"),
		Logger:     silentLogger(),
	})
	if err != nil {
		t.Fatalf("seed BuildApp: %v", err)
	}
	raw := openRawSQLite(t, dbFile)
	if _, err := raw.Exec(
		`INSERT INTO upstream_accounts (name, provider, api_key) VALUES (?, ?, ?)`,
		"seed-account", "openai", "sk-seed",
	); err != nil {
		t.Fatalf("insert seed row: %v", err)
	}
	_ = raw.Close()
	if err := seed.Stop(context.Background()); err != nil {
		t.Fatalf("seed Stop: %v", err)
	}

	// Precondition: no config.json on disk before BuildApp runs.
	if _, err := filepathStat(cfgPath); err == nil {
		t.Fatalf("precondition: config.json must not exist at %s", cfgPath)
	}

	env := config.MapEnv(map[string]string{
		"ROUTER_DB_DRIVER": "sqlite3",
		"ROUTER_DB_URL":    dbFile,
	})
	// Record logs so we can assert migrate-up runs EXACTLY ONCE on
	// the brownfield boot path (T-006 regression guard for the I6
	// double-migrate fix). tryBrownfield opens a temporary store
	// and runs migrate up to unblock the account-count probe; the
	// steady-state branch must then SKIP its own runMigrations
	// call against the same DSN.
	logs := &bytes.Buffer{}
	recLogger := slog.New(slog.NewTextHandler(logs, nil))

	a, err := BuildApp(context.Background(), nil, nil, Deps{
		ConfigPath: cfgPath,
		Env:        env,
		Logger:     recLogger,
	})
	if err != nil {
		t.Fatalf("BuildApp brownfield err = %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(context.Background()) })

	// Steady-state boot expected: Store() wired, config.json materialized.
	if a.Store() == nil {
		t.Fatal("Store() nil after brownfield BuildApp — materialization skipped")
	}
	if count := strings.Count(logs.String(), "running schema migrations on boot"); count != 1 {
		t.Errorf("'running schema migrations on boot' count = %d, want 1 (I6/T-006: brownfield must not double-migrate)\nlogs:\n%s", count, logs.String())
	}
	if _, err := filepathStat(cfgPath); err != nil {
		t.Fatalf("config.json was not materialized at %s: %v", cfgPath, err)
	}
	reloaded, _, err := config.Load(context.Background(), cfgPath, env)
	if err != nil {
		t.Fatalf("reload materialized config: %v", err)
	}
	if reloaded.DB.Driver != "sqlite3" || reloaded.DB.URL != dbFile {
		t.Errorf("materialized DB = %+v, want driver=sqlite3 url=%s",
			reloaded.DB, dbFile)
	}

	// Gate must be latched open — /admin/* does NOT redirect.
	req := httptest.NewRequest(http.MethodGet, "/admin/settings", nil)
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, req)
	if rec.Code == http.StatusFound {
		t.Errorf("brownfield boot should not redirect /admin/*, got 302")
	}

	// Health reports healthy (not degraded).
	req = httptest.NewRequest(http.MethodGet, "/api/admin/health", nil)
	rec = httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, req)
	if got := envelopeStatus(t, rec.Body.Bytes()); got != "healthy" {
		t.Errorf("health status = %q, want healthy", got)
	}
}

// TestBuildApp_Brownfield_EmptyDBStaysPending verifies that env
// pointing at an empty (schema-only, zero rows) DB keeps the app in
// setup-pending mode so the wizard can run without clobbering what
// would be a blank install.
func TestBuildApp_Brownfield_EmptyDBStaysPending(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "empty.db")
	cfgPath := filepath.Join(dir, "config.json")

	env := config.MapEnv(map[string]string{
		"ROUTER_DB_DRIVER": "sqlite3",
		"ROUTER_DB_URL":    dbFile,
	})
	a, err := BuildApp(context.Background(), nil, nil, Deps{
		ConfigPath: cfgPath,
		Env:        env,
		Logger:     silentLogger(),
	})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(context.Background()) })

	if a.Store() != nil {
		t.Error("Store() non-nil — empty DB must stay setup-pending")
	}
	if _, err := filepathStat(cfgPath); err == nil {
		t.Error("config.json materialized against empty DB — FR-002 violation")
	}
}

func TestEngineCounter_ReturnsZeroOnFreshDB(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := writeConfig(t, dir, filepath.Join(dir, "router.db"))
	cfg := mustLoad(t, cfgPath)

	a, err := BuildApp(context.Background(), cfg, nil, Deps{
		ConfigPath: cfgPath,
		Logger:     silentLogger(),
	})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(context.Background()) })

	c := &engineCounter{eng: a.Store().Engine()}
	n, err := c.CountUpstreamAccounts(context.Background())
	if err != nil {
		t.Fatalf("CountUpstreamAccounts: %v", err)
	}
	if n != 0 {
		t.Errorf("count = %d, want 0 on fresh DB", n)
	}
}

// T-028 / data-model: engineCounter must fail fast when ctx is already canceled.
func TestEngineCounter_CountUpstreamAccounts_ContextCanceled(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "router.db")
	st, err := store.New("sqlite3", dbFile, defaultMaxConns, defaultMinConns)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ec := &engineCounter{eng: st.Engine()}
	_, err = ec.CountUpstreamAccounts(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// T-028: happy path for brownfield counter adapter.
func TestEngineCounter_CountUpstreamAccounts_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "router.db")
	st, err := store.New("sqlite3", dbFile, defaultMaxConns, defaultMinConns)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate("sqlite3"); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ec := &engineCounter{eng: st.Engine()}
	n, err := ec.CountUpstreamAccounts(context.Background())
	if err != nil {
		t.Fatalf("CountUpstreamAccounts: %v", err)
	}
	if n != 0 {
		t.Fatalf("count = %d, want 0 on fresh DB", n)
	}
}

// FR-010 / plan.md L-001: health surfaces setup_error when gate probe fails
// (config path is a directory — StateReader.IsDone error branch).
func TestMakeHealthHandler_ProbeError_SetsSetupError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Deliberately create a directory where config.json should be — triggers IsDone error path.
	badPath := filepath.Join(dir, "config.json")
	if err := os.MkdirAll(badPath, 0o755); err != nil {
		t.Fatalf("mkdir config path: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gate := setup.NewGate(badPath, nil, logger)
	h := makeHealthHandler(nil, nil, gate, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/health", nil)
	req = req.WithContext(api.WithRequestID(req.Context(), "req_probe_err"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var env map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("json: %v body=%s", err, rec.Body.String())
	}
	data, _ := env["data"].(map[string]any)
	if data == nil {
		t.Fatalf("missing data object: %s", rec.Body.String())
	}
	if _, ok := data["setup_error"]; !ok {
		t.Fatalf("expected setup_error in data, got keys=%v body=%s", keysOf(data), rec.Body.String())
	}
}

// T-028 steady-state: DB ping failure path on /api/admin/health.
func TestMakeHealthHandler_DBCheckFailed_Unhealthy(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "router.db")
	cfgPath := writeConfig(t, dir, dbFile)
	cfg := mustLoad(t, cfgPath)

	a, err := BuildApp(context.Background(), cfg, nil, Deps{
		ConfigPath: cfgPath,
		Logger:     silentLogger(),
	})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(context.Background()) })

	if _, err := a.Store().Engine().Exec("DROP TABLE IF EXISTS upstream_accounts"); err != nil {
		t.Fatalf("drop table: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gate := setup.NewGate(cfgPath, nil, logger)
	h := makeHealthHandler(a, a.Store(), gate, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/health", nil)
	req = req.WithContext(api.WithRequestID(req.Context(), "req_db_fail"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (envelope carries business state)", rec.Code)
	}
	var payload struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json: %v", err)
	}
	if payload.Data.Status != "unhealthy" {
		t.Fatalf("data.status = %q, want unhealthy", payload.Data.Status)
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

type appDeviceProvider struct {
	deviceCode oauth.DeviceCode
	requests   atomic.Int32
	pollCalls  atomic.Int32
}

func (*appDeviceProvider) BuildAuthorizeURL(string, string) (string, error) {
	return "", errors.New("appDeviceProvider.BuildAuthorizeURL: not implemented")
}

func (*appDeviceProvider) ExchangeCode(context.Context, string, string) (oauth.Tokens, error) {
	return oauth.Tokens{}, errors.New("appDeviceProvider.ExchangeCode: not implemented")
}

func (*appDeviceProvider) Refresh(context.Context, []byte) (oauth.Tokens, error) {
	return oauth.Tokens{}, errors.New("appDeviceProvider.Refresh: not implemented")
}

func (p *appDeviceProvider) RequestDeviceCode(context.Context) (oauth.DeviceCode, error) {
	p.requests.Add(1)
	return p.deviceCode, nil
}

func (p *appDeviceProvider) PollDeviceCode(context.Context, string, string) (oauth.Tokens, error) {
	p.pollCalls.Add(1)
	return oauth.Tokens{}, errors.New("appDeviceProvider.PollDeviceCode: should not be called")
}

type appBrowserProvider struct {
	redirectURI string
}

func (p *appBrowserProvider) BuildAuthorizeURL(state, verifier string) (string, error) {
	redirectURI := p.redirectURI
	if redirectURI == "" {
		redirectURI = "http://localhost:1455/auth/callback"
	}
	return "https://auth.example.test/oauth/authorize?state=" + state + "&verifier=" + verifier + "&redirect_uri=" + redirectURI, nil
}

func (*appBrowserProvider) ExchangeCode(context.Context, string, string) (oauth.Tokens, error) {
	return oauth.Tokens{}, errors.New("appBrowserProvider.ExchangeCode: not implemented")
}

func (*appBrowserProvider) Refresh(context.Context, []byte) (oauth.Tokens, error) {
	return oauth.Tokens{}, errors.New("appBrowserProvider.Refresh: not implemented")
}

func (*appBrowserProvider) RequestDeviceCode(context.Context) (oauth.DeviceCode, error) {
	return oauth.DeviceCode{}, errors.New("appBrowserProvider.RequestDeviceCode: not implemented")
}

func (*appBrowserProvider) PollDeviceCode(context.Context, string, string) (oauth.Tokens, error) {
	return oauth.Tokens{}, errors.New("appBrowserProvider.PollDeviceCode: not implemented")
}

func (p *appBrowserProvider) SetRedirectURI(redirectURI string) {
	p.redirectURI = redirectURI
}

func (p *appBrowserProvider) WithRedirectURI(redirectURI string) oauth.Provider {
	return &appBrowserProvider{redirectURI: redirectURI}
}

// helpers

func mustLoad(t *testing.T, path string) *config.Config {
	t.Helper()
	cfg, _, err := config.Load(context.Background(), path, config.MapEnv(map[string]string{}))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

func mustTestJWT(t *testing.T, payload map[string]any) []byte {
	t.Helper()

	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal jwt payload: %v", err)
	}
	body := base64.RawURLEncoding.EncodeToString(bodyBytes)
	return []byte(header + "." + body + ".sig")
}

func stringPtr(v string) *string {
	return &v
}

func timePtr(v time.Time) *time.Time {
	return &v
}

func envelopeStatus(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Code int `json:"code"`
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("unmarshal: %v — body=%s", err, string(body))
	}
	if env.Code != 0 {
		t.Errorf("envelope code = %d, want 0", env.Code)
	}
	return env.Data.Status
}

func envelopeCode(t *testing.T, body []byte) int {
	t.Helper()
	var env struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("unmarshal: %v — body=%s", err, string(body))
	}
	return env.Code
}

func envelopeSetupState(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Code int `json:"code"`
		Data struct {
			SetupState string `json:"setup_state"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("unmarshal: %v — body=%s", err, string(body))
	}
	if env.Code != 0 {
		t.Errorf("envelope code = %d, want 0", env.Code)
	}
	return env.Data.SetupState
}

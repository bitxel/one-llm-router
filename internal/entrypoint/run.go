// Package entrypoint contains the shared process boot path used by the
// production router binary and specialised test harness binaries.
package entrypoint

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/user/one-llm-router/internal/app"
	"github.com/user/one-llm-router/internal/app/buildinfo"
	"github.com/user/one-llm-router/internal/config"
	"github.com/user/one-llm-router/internal/oauth"
)

const (
	ExitOK    = 0
	ExitError = 1
	ExitUsage = 2
)

// DefaultConfigPath matches the migrate subcommand and specs/002-.../
// quickstart.md so an operator switching between `one-llm-router` and
// `one-llm-router migrate` never has to re-discover the path.
const DefaultConfigPath = "./config.json"

// EnvVarConfigPath lets containerised deployments bake the config
// location into a Dockerfile / systemd unit without touching the
// -config CLI flag. Precedence, matching data-model.md §Config
// discovery: explicit -config flag value > ROUTER_CONFIG_PATH env
// var > DefaultConfigPath.
const EnvVarConfigPath = "ROUTER_CONFIG_PATH"

// EnvVarListenAddr lets deployments set the bind address without
// touching the CLI, preserving the 001 operator contract.
const EnvVarListenAddr = "ROUTER_LISTEN_ADDR"

const (
	EnvVarOAuthAuthorizeURL   = "ROUTER_OAUTH_AUTHORIZE_URL"
	EnvVarOAuthTokenURL       = "ROUTER_OAUTH_TOKEN_URL"
	EnvVarOAuthDeviceCodeURL  = "ROUTER_OAUTH_DEVICE_CODE_URL"
	EnvVarOAuthDeviceTokenURL = "ROUTER_OAUTH_DEVICE_TOKEN_URL"
)

const (
	EnvVarLegacyCodexBackendBaseURL = "ROUTER_CODEX_BACKEND_BASE_URL"
	EnvVarEnableTestCodexBackend    = "ROUTER_ENABLE_TEST_CODEX_BACKEND"
	EnvVarTestCodexBackendBaseURL   = "ROUTER_TEST_CODEX_BACKEND_BASE_URL"
	EnvVarE2ECodexBackendBaseURL    = "ROUTER_E2E_CODEX_BACKEND_BASE_URL"
)

// DefaultListenAddr aligns with 001's ROUTER_LISTEN_ADDR fallback so
// existing Codex clients continue to reach the router on :8080 across
// the 002 upgrade.
const DefaultListenAddr = ":8080"

type Options struct {
	// ValidateEnv runs after logging is initialized and before config
	// loading / app construction. Production uses it to fail fast on
	// override env vars that must not be accepted by the router binary.
	ValidateEnv func(config.Env) error

	// CodexBackendBaseURL is a test-harness-only dependency injection
	// hook. The production cmd/one-llm-router entrypoint always leaves this
	// empty; specialised E2E binaries may pass a loopback mock URL.
	CodexBackendBaseURL string
}

// Run is the testable router process entry point. Returns a process
// exit code so callers can assert on it without shelling out to a
// binary.
func Run(ctx context.Context, args []string, stdout, stderr *os.File, opts Options) int {
	env := config.OSEnv()
	fs := flag.NewFlagSet("one-llm-router", flag.ContinueOnError)
	fs.SetOutput(stderr)

	cfgDefault := DefaultConfigPath
	if v, ok := env(EnvVarConfigPath); ok && v != "" {
		cfgDefault = v
	}
	listenDefault := DefaultListenAddr
	if v, ok := env(EnvVarListenAddr); ok && v != "" {
		listenDefault = v
	}
	var (
		configFlag  = fs.String("config", cfgDefault, "path to config.json (the wizard writes this file on commit); override via -config or ROUTER_CONFIG_PATH")
		listenFlag  = fs.String("listen", listenDefault, "TCP address the HTTP server binds (host:port); override via -listen or ROUTER_LISTEN_ADDR")
		versionFlag = fs.Bool("version", false, "print build metadata and exit")
	)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: one-llm-router [flags]        — start the HTTP server")
		fmt.Fprintln(stderr, "       one-llm-router migrate ...    — run migration subcommands (see `one-llm-router migrate -h`)")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Flags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}

	if *versionFlag {
		snap := buildinfo.Get()
		fmt.Fprintf(stdout, "one-llm-router version=%s git_sha=%s built_at=%s\n",
			snap.Version, snap.GitSHA, snap.BuiltAt)
		return ExitOK
	}

	levelVar := new(slog.LevelVar)
	levelVar.Set(slog.LevelInfo)
	logger := slog.New(slog.NewJSONHandler(stdout, &slog.HandlerOptions{Level: levelVar}))
	slog.SetDefault(logger)

	if opts.ValidateEnv != nil {
		if err := opts.ValidateEnv(env); err != nil {
			logger.Error("validate environment", "error", err)
			return ExitError
		}
	}

	cfgPath, err := filepath.Abs(*configFlag)
	if err != nil {
		logger.Error("resolve config path", "error", err)
		return ExitError
	}

	cfg, sources, err := config.Load(ctx, cfgPath, env)
	switch {
	case err == nil:
		logger.Info("loaded config.json",
			"db_driver", cfg.DB.Driver,
			"version", cfg.Version,
		)
		logger.Debug("config.json path", "path", cfgPath)
	case errors.Is(err, config.ErrNoConfig):
		logger.Info("config.json absent — entering setup-pending mode")
		logger.Debug("config.json path", "path", cfgPath)
		cfg, sources = nil, nil
	default:
		logger.Error("load config", "error", err)
		return ExitError
	}

	listenAddr := *listenFlag

	appCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	go func() {
		sig, ok := <-sigCh
		if !ok {
			return
		}
		logger.Info("received signal — draining", "signal", sig.String())
		cancel()
		if sig2, ok := <-sigCh; ok {
			logger.Warn("received second signal — forcing exit", "signal", sig2.String())
			os.Exit(ExitError)
		}
	}()

	oauthProvider, err := OAuthProviderFromEnv(env)
	if err != nil {
		logger.Error("build oauth provider from env", "error", err)
		return ExitError
	}

	a, err := app.BuildApp(appCtx, cfg, sources, app.Deps{
		ConfigPath:          cfgPath,
		Env:                 env,
		Logger:              logger,
		LogLevel:            levelVar,
		ListenAddr:          listenAddr,
		OAuthProvider:       oauthProvider,
		CodexBackendBaseURL: opts.CodexBackendBaseURL,
	})
	if err != nil {
		logger.Error("build app", "error", err)
		return ExitError
	}

	logger.Info("starting one-llm-router",
		"listen", listenAddr,
		"version", buildinfo.Version,
		"git_sha", buildinfo.GitSHA,
	)

	serveErr := a.Start(appCtx)
	switch {
	case serveErr == nil, errors.Is(serveErr, http.ErrServerClosed):
	default:
		logger.Error("server error", "error", serveErr)
	}

	drainCtx, drainCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer drainCancel()
	if err := a.Stop(drainCtx); err != nil {
		logger.Error("stop app", "error", err)
		return ExitError
	}

	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return ExitError
	}
	logger.Info("server stopped")
	return ExitOK
}

func OAuthProviderFromEnv(env config.Env) (oauth.Provider, error) {
	cfg := oauth.OpenAIProviderConfig{
		AuthorizeURL:   strings.TrimSpace(envValue(env, EnvVarOAuthAuthorizeURL)),
		TokenURL:       strings.TrimSpace(envValue(env, EnvVarOAuthTokenURL)),
		DeviceCodeURL:  strings.TrimSpace(envValue(env, EnvVarOAuthDeviceCodeURL)),
		DeviceTokenURL: strings.TrimSpace(envValue(env, EnvVarOAuthDeviceTokenURL)),
	}
	if cfg.AuthorizeURL == "" && cfg.TokenURL == "" && cfg.DeviceCodeURL == "" && cfg.DeviceTokenURL == "" {
		return nil, nil
	}
	return oauth.NewOpenAIProvider(cfg)
}

func RejectProductionCodexBackendOverrideEnv(env config.Env) error {
	for _, name := range []string{
		EnvVarLegacyCodexBackendBaseURL,
		EnvVarEnableTestCodexBackend,
		EnvVarTestCodexBackendBaseURL,
		EnvVarE2ECodexBackendBaseURL,
	} {
		if strings.TrimSpace(envValue(env, name)) != "" {
			return fmt.Errorf("%s is not supported in the production entrypoint", name)
		}
	}
	return nil
}

func E2ECodexBackendBaseURLFromEnv(env config.Env) (string, error) {
	baseURL := strings.TrimSpace(envValue(env, EnvVarE2ECodexBackendBaseURL))
	if baseURL == "" {
		return "", nil
	}
	if err := validateLoopbackHTTPURL(baseURL); err != nil {
		return "", fmt.Errorf("%s must be an http(s) loopback URL: %w", EnvVarE2ECodexBackendBaseURL, err)
	}
	return baseURL, nil
}

func envValue(env config.Env, key string) string {
	if env == nil {
		env = config.OSEnv()
	}
	v, _ := env(key)
	return v
}

func validateLoopbackHTTPURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("unsupported scheme %q", parsed.Scheme)
	}
	host := parsed.Hostname()
	if host == "" {
		return errors.New("missing host")
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("host %q is not an IP address or localhost", host)
	}
	if !ip.IsLoopback() {
		return fmt.Errorf("host %q is not loopback", host)
	}
	return nil
}

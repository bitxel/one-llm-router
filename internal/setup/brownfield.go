package setup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"time"

	"github.com/user/one-llm-router/internal/config"
)

// AccountCounter is the narrow dependency the brownfield path needs:
// one function that returns the number of `upstream_accounts` rows.
// Using an interface keeps tests hermetic (no real DB needed) and
// matches the store.Store wrapping that will be added in 002 BuildApp
// (T-028). The 001 codebase already has this count plumbed through
// domain.UpstreamAccountRepository; 002's BuildApp hands a thin
// adapter in.
type AccountCounter interface {
	CountUpstreamAccounts(ctx context.Context) (int64, error)
}

// EnvVarDBDriver / EnvVarDBURL are the env var names the brownfield
// auto-materialization gate probes to decide whether to synthesize
// config.json on boot. They are deliberately a separate surface from
// config.overrideTable — those control per-field env OVERRIDES of a
// loaded config; these control whether we SYNTHESIZE a config at all.
// Exported so cmd/one-llm-router, internal/app, and tests can share one
// spelling and avoid drift (CC-004).
const (
	EnvVarDBDriver = "ROUTER_DB_DRIVER"
	EnvVarDBURL    = "ROUTER_DB_URL"
)

// BootstrapIfBrownfield implements the FR-002 auto-materialization
// path (data-model.md §Brownfield auto-materialization):
//
//  1. If `cfgPath` already exists on disk — return (false, nil).
//     Steady-state boot; nothing to do.
//  2. If either ROUTER_DB_DRIVER or ROUTER_DB_URL is unset — return
//     (false, nil). Greenfield install; the wizard will run.
//  3. Query the supplied counter:
//     count == 0 — return (false, nil). Env points at a blank DB;
//     the operator still has to walk the wizard
//     (they might want to supply a DSN that points
//     at a different, populated DB later).
//     count  > 0 — synthesize a minimal `*Config` from env + the
//     compiled-in defaults (DefaultRuntimeConfig /
//     DefaultPluginsConfig) and write it via
//     config.WriteAtomic. Returns (true, nil) on
//     success.
//  4. On any I/O, stat, count, or write error — return (false, err)
//     with a wrapped diagnostic. Callers (BuildApp) MUST fail boot
//     on error so the operator sees the problem rather than silently
//     staying in wizard mode on a populated install.
//
// Parameters:
//   - ctx     — inherited from BuildApp; cancelled during graceful
//     shutdown. The count query MUST honour it.
//   - counter — anything that can answer `SELECT COUNT(*) FROM
//     upstream_accounts`. The 001 code exposes this via
//     domain.UpstreamAccountRepository.CountActive + Count-
//     Disabled; 002's BuildApp composes them.
//   - cfgPath — absolute path to `config.json` (same value passed to
//     the gate and the loader — ROUTER_CONFIG_PATH resolved).
//   - env     — environment lookup (config.OSEnv() in production;
//     config.MapEnv in tests). Nil falls back to OSEnv().
//
// The function is idempotent across boots: once it has materialized
// config.json, the first precondition (file present) short-circuits
// it on every subsequent boot.
//
// A successful materialization logs one structured INFO line (driver +
// accounts; config path is DEBUG-only per plan.md §362) plus a DEBUG
// line with the resolved path for operators who need it.
//
// logger is optional; passing nil falls back to slog.Default() so legacy
// callers keep compiling, but production wiring in BuildApp ALWAYS
// threads the app's structured logger so brownfield logs inherit the
// same handler/level/attributes as the rest of the boot chain (CC-003).
func BootstrapIfBrownfield(
	ctx context.Context,
	counter AccountCounter,
	cfgPath string,
	env config.Env,
	logger *slog.Logger,
) (bool, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if cfgPath == "" {
		return false, errors.New("brownfield: cfgPath is empty")
	}
	if counter == nil {
		return false, errors.New("brownfield: counter is nil")
	}
	if env == nil {
		env = config.OSEnv()
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}

	// 1. File present → skip.
	switch _, err := os.Stat(cfgPath); {
	case err == nil:
		return false, nil
	case errors.Is(err, fs.ErrNotExist):
		// fall through
	default:
		// plan.md §362: os.Stat returns *os.PathError whose Error()
		// embeds cfgPath; config.ScrubPath rewrites it before %w
		// wrapping so the path does not leak into INFO+ logs.
		return false, fmt.Errorf("brownfield: stat config.json: %w", config.ScrubPath(err))
	}

	// 2. Env-DB not set → skip.
	driver, driverOK := env(EnvVarDBDriver)
	url, urlOK := env(EnvVarDBURL)
	if !driverOK || driver == "" || !urlOK || url == "" {
		return false, nil
	}

	// 3. Ask the DB whether the install is populated.
	count, err := counter.CountUpstreamAccounts(ctx)
	if err != nil {
		return false, fmt.Errorf("brownfield: count upstream_accounts: %w", err)
	}
	if count <= 0 {
		return false, nil
	}

	// 4. Synthesize + write.
	now := time.Now().UTC()
	cfg := &config.Config{
		Version: config.SupportedVersion,
		DB: config.DBConfig{
			Driver: driver,
			URL:    url,
		},
		Runtime:   config.DefaultRuntimeConfig(),
		Plugins:   config.DefaultPluginsConfig(),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := config.WriteAtomic(cfgPath, cfg); err != nil {
		// plan.md §362: config.json path excluded from returned error.
		return false, fmt.Errorf("brownfield: write config.json: %w", err)
	}

	// plan.md §362 keeps config.json path out of INFO logs; operators
	// cross-reference with DEBUG logs when they need the full path.
	logger.Info("brownfield upgrade: materialized config.json",
		"driver", driver,
		"accounts", count,
	)
	logger.Debug("brownfield materialization path", "path", cfgPath)
	return true, nil
}

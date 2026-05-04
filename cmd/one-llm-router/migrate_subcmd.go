package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/user/one-llm-router/internal/config"
	"github.com/user/one-llm-router/internal/store"
)

// Exit codes for the migrate subcommand. Kept as named constants so
// callers (main.go, smoke tests) can reference them without magic
// numbers.
const (
	migrateExitOK    = 0
	migrateExitError = 1
	migrateExitUsage = 2
)

// migrateUsage is the canonical help string. Kept in one place so it
// stays in sync between main.go -h and `one-llm-router migrate -h`.
const migrateUsage = `Usage: one-llm-router migrate <subcommand> [flags]

Subcommands:
  up                 Apply all pending migrations. Idempotent.
  down               Roll back all applied migrations. Destructive.
  force <v>          Mark schema at version <v> and clear dirty flag.
                     Use after manual recovery to unstick migrations.
  version            Print "version=<N> dirty=<bool>" for scripting.
  status             Print human-readable schema status.

Flags:
  -config <path>     Path to config.json (default: ./config.json).
                     When absent AND ROUTER_DB_DRIVER + ROUTER_DB_URL
                     are set, the migrator falls back to env-only DB
                     config — useful on brownfield hosts.

Exit codes:
  0  success
  1  runtime error (DB unreachable, migration failure, ...)
  2  usage error (missing/invalid arguments)
`

// runMigrate is the testable entry point. It takes its own stdout
// writer so tests can capture output, and returns an exit code the
// caller (main) hands to os.Exit. No state is persisted between calls.
func runMigrate(ctx context.Context, out io.Writer, args []string) int {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.SetOutput(out)
	// Same precedence as `one-llm-router` itself: explicit -config flag >
	// ROUTER_CONFIG_PATH env var > ./config.json. Keeping the two
	// subcommands in sync avoids operator surprise when they switch
	// between `one-llm-router` and `one-llm-router migrate` on the same host.
	cfgDefault := defaultConfigPath
	if v, ok := os.LookupEnv(envVarConfigPath); ok && v != "" {
		cfgDefault = v
	}
	var configPath string
	fs.StringVar(&configPath, "config", cfgDefault, "path to config.json (ROUTER_CONFIG_PATH overrides the default when -config is omitted)")

	if err := fs.Parse(args); err != nil {
		// ContinueOnError: flag.Parse already printed a message via
		// fs.SetOutput. Just translate to usage exit code.
		return migrateExitUsage
	}
	remaining := fs.Args()
	if len(remaining) == 0 {
		fmt.Fprint(out, migrateUsage)
		return migrateExitUsage
	}

	sub := remaining[0]
	subArgs := remaining[1:]

	// Help / unknown-subcommand dispatch happens BEFORE DB resolution
	// so operators can discover the CLI on a fresh host without any
	// config.json or env vars. resolveDBConfig would otherwise fail
	// loudly and mask the help text.
	switch sub {
	case "-h", "--help", "help":
		fmt.Fprint(out, migrateUsage)
		return migrateExitOK
	case "up", "down", "force", "version", "status":
		// handled below
	default:
		fmt.Fprintf(out, "one-llm-router migrate: unknown subcommand %q\n\n%s", sub, migrateUsage)
		return migrateExitUsage
	}

	db, err := resolveDBConfig(ctx, configPath)
	if err != nil {
		fmt.Fprintf(out, "one-llm-router migrate: resolve db config: %v\n", err)
		return migrateExitError
	}

	mig, err := store.NewMigrator(ctx, db)
	if err != nil {
		fmt.Fprintf(out, "one-llm-router migrate: open migrator: %v\n", err)
		return migrateExitError
	}
	defer func() {
		if closeErr := mig.Close(); closeErr != nil {
			fmt.Fprintf(out, "one-llm-router migrate: close migrator: %v\n", closeErr)
		}
	}()

	switch sub {
	case "up":
		if err := mig.Up(ctx); err != nil {
			fmt.Fprintf(out, "one-llm-router migrate up: %v\n", err)
			return migrateExitError
		}
		fmt.Fprintln(out, "migrations applied")
		return migrateExitOK

	case "down":
		if err := mig.Down(ctx); err != nil {
			fmt.Fprintf(out, "one-llm-router migrate down: %v\n", err)
			return migrateExitError
		}
		fmt.Fprintln(out, "migrations rolled back")
		return migrateExitOK

	case "force":
		if len(subArgs) != 1 {
			fmt.Fprintln(out, "one-llm-router migrate force: requires exactly one <v> argument")
			return migrateExitUsage
		}
		v, err := strconv.Atoi(subArgs[0])
		if err != nil {
			fmt.Fprintf(out, "one-llm-router migrate force: invalid version %q: %v\n", subArgs[0], err)
			return migrateExitUsage
		}
		if err := mig.Force(ctx, v); err != nil {
			fmt.Fprintf(out, "one-llm-router migrate force: %v\n", err)
			return migrateExitError
		}
		fmt.Fprintf(out, "forced version to %d\n", v)
		return migrateExitOK

	case "version":
		v, dirty, err := mig.Version(ctx)
		if err != nil {
			fmt.Fprintf(out, "one-llm-router migrate version: %v\n", err)
			return migrateExitError
		}
		fmt.Fprintf(out, "version=%d dirty=%v\n", v, dirty)
		return migrateExitOK

	case "status":
		fmt.Fprintln(out, mig.Status(ctx))
		return migrateExitOK

	default:
		// Unreachable — the pre-DB dispatch above already filtered
		// unknown subcommands. Keep the branch as a defensive guard
		// in case the whitelist drifts from the runtime switch.
		fmt.Fprintf(out, "one-llm-router migrate: unhandled subcommand %q\n", sub)
		return migrateExitError
	}
}

// resolveDBConfig produces the DBConfig the migrator should use. It
// tries (in order):
//
//  1. config.Load on configPath — if success, return cfg.DB (env
//     overlay already applied by the loader).
//  2. On ErrNoConfig, fall back to env-only DB config when both
//     ROUTER_DB_DRIVER and ROUTER_DB_URL are set. This matches the
//     brownfield-boot path so the CLI works on hosts where the
//     wizard has not yet run but env is provisioned.
//  3. Otherwise, return a descriptive error pointing the operator at
//     both remediation paths.
//
// Any loader error other than ErrNoConfig is propagated untouched —
// operator mistakes (malformed JSON, bad perms) should NOT be masked
// by the env fallback.
func resolveDBConfig(ctx context.Context, configPath string) (*config.DBConfig, error) {
	cfg, _, err := config.Load(ctx, configPath, config.OSEnv())
	if err == nil {
		return &cfg.DB, nil
	}
	if !errors.Is(err, config.ErrNoConfig) {
		return nil, fmt.Errorf("load %s: %w", configPath, err)
	}

	// No config.json — try env fallback.
	driver, hasDriver := os.LookupEnv("ROUTER_DB_DRIVER")
	url, hasURL := os.LookupEnv("ROUTER_DB_URL")
	if !hasDriver || driver == "" || !hasURL || url == "" {
		return nil, fmt.Errorf("no config.json at %s AND ROUTER_DB_DRIVER/ROUTER_DB_URL not set — cannot determine DB target", configPath)
	}
	return &config.DBConfig{Driver: driver, URL: url}, nil
}

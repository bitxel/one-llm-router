// Package main is the router's process entrypoint. Responsibilities
// are deliberately minimal: dispatch the `migrate` subcommand when
// requested, then delegate normal HTTP-server boot to internal
// packages.
package main

import (
	"context"
	"os"

	"github.com/user/one-llm-router/internal/config"
	"github.com/user/one-llm-router/internal/entrypoint"
	"github.com/user/one-llm-router/internal/oauth"
)

const (
	exitOK    = entrypoint.ExitOK
	exitError = entrypoint.ExitError
	exitUsage = entrypoint.ExitUsage
)

const defaultConfigPath = entrypoint.DefaultConfigPath

const envVarConfigPath = entrypoint.EnvVarConfigPath

func main() {
	// Early subcommand dispatch: `one-llm-router migrate ...` MUST work on a
	// fresh host where config.json is absent and no env is set. That
	// is also the reason the dispatch happens BEFORE flag parsing.
	if len(os.Args) >= 2 && os.Args[1] == "migrate" {
		os.Exit(runMigrate(context.Background(), os.Stdout, os.Args[2:]))
	}

	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr *os.File) int {
	return entrypoint.Run(ctx, args, stdout, stderr, entrypoint.Options{
		ValidateEnv: entrypoint.RejectProductionCodexBackendOverrideEnv,
	})
}

func oauthProviderFromEnv() (oauth.Provider, error) {
	return entrypoint.OAuthProviderFromEnv(config.OSEnv())
}

func rejectProductionCodexBackendOverrideEnv() error {
	return entrypoint.RejectProductionCodexBackendOverrideEnv(config.OSEnv())
}

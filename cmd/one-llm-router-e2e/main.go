//go:build e2e

// Package main builds a Playwright-only router binary with loopback mock
// dependency injection. It is excluded from normal production builds.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/user/one-llm-router/internal/config"
	"github.com/user/one-llm-router/internal/entrypoint"
)

func main() {
	codexBackendBaseURL, err := entrypoint.E2ECodexBackendBaseURLFromEnv(config.OSEnv())
	if err != nil {
		fmt.Fprintf(os.Stderr, "one-llm-router-e2e: %v\n", err)
		os.Exit(entrypoint.ExitError)
	}

	os.Exit(entrypoint.Run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, entrypoint.Options{
		CodexBackendBaseURL: codexBackendBaseURL,
	}))
}

package entrypoint

import (
	"strings"
	"testing"

	"github.com/user/one-llm-router/internal/config"
)

func TestE2ECodexBackendBaseURLFromEnv_AllowsLoopbackHTTPURL(t *testing.T) {
	got, err := E2ECodexBackendBaseURLFromEnv(config.MapEnv(map[string]string{
		EnvVarE2ECodexBackendBaseURL: "http://127.0.0.1:4555/backend-api",
	}))
	if err != nil {
		t.Fatalf("E2ECodexBackendBaseURLFromEnv() error = %v, want nil", err)
	}
	if got != "http://127.0.0.1:4555/backend-api" {
		t.Fatalf("baseURL = %q", got)
	}
}

func TestE2ECodexBackendBaseURLFromEnv_RejectsNonLoopbackHTTPURL(t *testing.T) {
	_, err := E2ECodexBackendBaseURLFromEnv(config.MapEnv(map[string]string{
		EnvVarE2ECodexBackendBaseURL: "https://example.com/backend-api",
	}))
	if err == nil {
		t.Fatal("E2ECodexBackendBaseURLFromEnv() error = nil, want non-loopback rejection")
	}
	if !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("error = %v, want loopback context", err)
	}
}

func TestRejectProductionCodexBackendOverrideEnv_RejectsE2EOverride(t *testing.T) {
	err := RejectProductionCodexBackendOverrideEnv(config.MapEnv(map[string]string{
		EnvVarE2ECodexBackendBaseURL: "http://127.0.0.1:4555/backend-api",
	}))
	if err == nil {
		t.Fatal("RejectProductionCodexBackendOverrideEnv() error = nil, want rejection")
	}
	if !strings.Contains(err.Error(), EnvVarE2ECodexBackendBaseURL) {
		t.Fatalf("error = %v, want env var name", err)
	}
}

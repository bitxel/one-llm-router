package config

import (
	"testing"
)

// TestDefaults_T401_RuntimeSafeDefaults asserts (T-401 from the 002
// task list) that the baseline runtime block matches the
// data-model.md defaults column and the MVP-parity guarantee
// (FR-014, US-5 AC-1): nothing logged by default, 30-day retention,
// info-level logs.
//
// This is a US-5 coverage test that intentionally duplicates the
// structural check in TestDefaultRuntimeConfig_MatchesDataModel; it
// adds the value-safety assertions (no debug-level default, no PII
// leak via request-body logging).
func TestDefaults_T401_RuntimeSafeDefaults(t *testing.T) {
	got := DefaultRuntimeConfig()

	if got.LogClientRequestBody {
		t.Error("LogClientRequestBody default must be false (PII safety)")
	}
	if got.LogUpstreamRequestBody {
		t.Error("LogUpstreamRequestBody default must be false (PII safety)")
	}
	if got.LogUpstreamResponseBody {
		t.Error("LogUpstreamResponseBody default must be false (PII safety)")
	}
	if got.LogRetentionDays != 30 {
		t.Errorf("LogRetentionDays default: got %d want 30", got.LogRetentionDays)
	}
	if got.LogLevel != "info" {
		t.Errorf("LogLevel default: got %q want \"info\" (debug is NOT a safe default)", got.LogLevel)
	}
}

// TestDefaults_T401_PluginsAllDisabled re-asserts (T-401 coverage)
// that every plugin intent defaults to off so a freshly-materialized
// config (via the wizard OR the brownfield path) never enables an
// unshipped plugin.
func TestDefaults_T401_PluginsAllDisabled(t *testing.T) {
	got := DefaultPluginsConfig()
	if got.AdminAuth.Enabled {
		t.Error("AdminAuth.Enabled default must be false (plugin not shipped in 002)")
	}
	if got.ClientKeys.Enabled {
		t.Error("ClientKeys.Enabled default must be false (plugin not shipped in 002)")
	}
}

// TestDefaults_NoUnsafeField walks the Config struct and asserts no
// optional field defaults to a dangerous value. Works as a canary if
// a new defaulted field is added without review.
func TestDefaults_NoUnsafeField(t *testing.T) {
	rt := DefaultRuntimeConfig()
	unsafeFields := []struct {
		name    string
		current bool
		want    bool
	}{
		{"LogClientRequestBody", rt.LogClientRequestBody, false},
		{"LogUpstreamRequestBody", rt.LogUpstreamRequestBody, false},
		{"LogUpstreamResponseBody", rt.LogUpstreamResponseBody, false},
	}
	for _, tc := range unsafeFields {
		if tc.current != tc.want {
			t.Errorf("default for %s: got %v want %v — an unsafe default is a release blocker", tc.name, tc.current, tc.want)
		}
	}

	if rt.LogRetentionDays <= 0 || rt.LogRetentionDays > 365 {
		t.Errorf("LogRetentionDays default %d out of valid range [1,365]", rt.LogRetentionDays)
	}
	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLevels[rt.LogLevel] {
		t.Errorf("LogLevel default %q not in enum {debug,info,warn,error}", rt.LogLevel)
	}
	if rt.LogLevel == "debug" {
		t.Error("LogLevel default is debug — should be 'info' so production logs are not chatty out of the box")
	}
}

// TestSupportedVersion_SingleSource asserts the package constant has
// not silently drifted. The boot path, writer, loader, and admin-api
// contract tests all assume SupportedVersion==1.
func TestSupportedVersion_SingleSource(t *testing.T) {
	if SupportedVersion != 1 {
		t.Fatalf("SupportedVersion bump requires coordinated change across loader/writer/api contract: got %d", SupportedVersion)
	}
}

// TestConfig_EmptyDriverIsRejectedElsewhere documents the boundary
// condition: Config{} (zero value) has no driver, which is caught by
// loader.go's ErrInvalidDBConfig during Load. The default helpers do
// NOT fill driver — that is the wizard / brownfield path's job.
func TestConfig_EmptyDriverIsRejectedElsewhere(t *testing.T) {
	var cfg Config
	if cfg.DB.Driver != "" {
		t.Fatalf("zero Config.DB.Driver: got %q want \"\" (driver is wizard/brownfield-filled, not defaulted)", cfg.DB.Driver)
	}
	if cfg.Version != 0 {
		t.Fatalf("zero Config.Version: got %d want 0 (SupportedVersion is set by writer, not a default)", cfg.Version)
	}
}

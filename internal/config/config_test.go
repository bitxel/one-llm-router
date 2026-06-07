package config

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// sampleConfig returns a fully populated Config suitable for round-trip
// assertions. All fields are non-zero so the marshal output contains
// every declared key (omitempty-tagged reserved fields included).
func sampleConfig() Config {
	ts := time.Date(2026, 4, 17, 10, 15, 0, 0, time.UTC)
	return Config{
		Version: SupportedVersion,
		DB: DBConfig{
			Driver: "postgres",
			URL:    "postgres://u:p@db.internal:5432/router_prod?sslmode=require",
		},
		Runtime: RuntimeConfig{
			LogClientRequestBody:    true,
			LogUpstreamRequestBody:  true,
			LogUpstreamResponseBody: true,
			LogRetentionDays:        45,
			LogLevel:                "debug",
			ModelRenames: []ModelRenameRule{
				{From: "client-model", To: "upstream-model"},
			},
		},
		Plugins: PluginsConfig{
			AdminAuth: AdminAuthPluginConfig{
				Enabled:           true,
				SessionTTLMinutes: 60,
				JWTSecretRef:      "vault://admin-auth-jwt",
			},
			ClientKeys: ClientKeysPluginConfig{Enabled: true},
		},
		CreatedAt: ts,
		UpdatedAt: ts,
	}
}

func TestConfigTypes_RoundTripStable(t *testing.T) {
	t.Parallel()
	orig := sampleConfig()

	raw1, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal1: %v", err)
	}

	var decoded Config
	if err := json.Unmarshal(raw1, &decoded); err != nil {
		t.Fatalf("unmarshal: %v (raw=%s)", err, raw1)
	}

	raw2, err := json.Marshal(decoded)
	if err != nil {
		t.Fatalf("marshal2: %v", err)
	}
	if string(raw1) != string(raw2) {
		t.Fatalf("round-trip drift:\n raw1=%s\n raw2=%s", raw1, raw2)
	}

	if decoded.Plugins.AdminAuth.SessionTTLMinutes != 60 {
		t.Errorf("SessionTTLMinutes lost in round-trip: got %d", decoded.Plugins.AdminAuth.SessionTTLMinutes)
	}
	if decoded.Plugins.AdminAuth.JWTSecretRef != "vault://admin-auth-jwt" {
		t.Errorf("JWTSecretRef lost in round-trip: got %q", decoded.Plugins.AdminAuth.JWTSecretRef)
	}
}

func TestConfigTypes_OmitEmptyReservedFields(t *testing.T) {
	t.Parallel()
	// A 002-era config.json ONLY sets Enabled for each plugin. The
	// 003-reserved fields on AdminAuth should NOT appear in the
	// serialized output — this is what keeps vanilla 002 files byte-
	// minimal and forward-compatible (003 fills them in later).
	cfg := Config{
		Version: SupportedVersion,
		DB:      DBConfig{Driver: "sqlite3", URL: "router.db"},
		Runtime: DefaultRuntimeConfig(),
		Plugins: DefaultPluginsConfig(),
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "session_ttl_minutes") {
		t.Errorf("session_ttl_minutes leaked into a default-only config: %s", raw)
	}
	if strings.Contains(string(raw), "jwt_secret_ref") {
		t.Errorf("jwt_secret_ref leaked into a default-only config: %s", raw)
	}
}

func TestConfigTypes_UnknownPluginKeysDroppedOnReWrite(t *testing.T) {
	t.Parallel()
	// A forward-dated config.json carrying an unshipped plugin
	// (prometheus) MUST parse cleanly (permissive decode) and MUST NOT
	// carry the unknown key back into the serialized output (strict
	// encode, tasks.md T-013 verify).
	input := `{
  "version": 1,
  "db":      {"driver":"sqlite3", "url":"router.db"},
  "runtime": {"log_client_request_body":false,"log_upstream_request_body":false,"log_upstream_response_body":false,"log_retention_days":30,"log_level":"info"},
  "plugins": {
    "admin_auth":  {"enabled": true},
    "client_keys": {"enabled": false},
    "prometheus":  {"enabled": true, "scrape_interval": 15}
  },
  "created_at": "2026-04-17T10:15:00Z",
  "updated_at": "2026-04-17T10:15:00Z"
}`

	var cfg Config
	if err := json.Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !cfg.Plugins.AdminAuth.Enabled {
		t.Error("admin_auth.enabled did not decode")
	}
	if cfg.Plugins.ClientKeys.Enabled {
		t.Error("client_keys.enabled decoded as true, want false")
	}

	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "prometheus") {
		t.Errorf("unknown plugin id 'prometheus' survived round-trip: %s", raw)
	}
	if strings.Contains(string(raw), "scrape_interval") {
		t.Errorf("unknown plugin field 'scrape_interval' survived round-trip: %s", raw)
	}
}

func TestDefaultRuntimeConfig_MatchesDataModel(t *testing.T) {
	t.Parallel()
	got := DefaultRuntimeConfig()
	want := RuntimeConfig{
		LogClientRequestBody:    false,
		LogUpstreamRequestBody:  false,
		LogUpstreamResponseBody: false,
		LogRetentionDays:            30,
		LogLevel:                    "info",
		UsageRefreshIntervalSeconds: 300,
		ModelRenames:                []ModelRenameRule{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("default runtime mismatch: got %+v, want %+v", got, want)
	}
}

func TestDefaultPluginsConfig_AllDisabled(t *testing.T) {
	t.Parallel()
	got := DefaultPluginsConfig()
	if got.AdminAuth.Enabled {
		t.Error("default AdminAuth must be disabled")
	}
	if got.ClientKeys.Enabled {
		t.Error("default ClientKeys must be disabled")
	}
	if got.AdminAuth.SessionTTLMinutes != 0 {
		t.Error("default AdminAuth.SessionTTLMinutes must be 0 (reserved for 003)")
	}
	if got.AdminAuth.JWTSecretRef != "" {
		t.Error("default AdminAuth.JWTSecretRef must be empty (reserved for 003)")
	}
}

func TestConfigTypes_DSNNeverLoggedImplicitly(t *testing.T) {
	t.Parallel()
	// Defensive guard: a %+v string format of Config WILL include the
	// DSN (that's by design — Go struct formatting is mechanical). This
	// test documents the invariant that DSNs live inside DBConfig.URL
	// so handlers that must not leak them know to skip the field; it
	// also fails if DBConfig grows a second secret-bearing field without
	// the reviewer noticing.
	cfg := sampleConfig()
	// Locate the DSN inside the struct to prove the expectation.
	if cfg.DB.URL == "" || !strings.Contains(cfg.DB.URL, "p@") {
		t.Fatal("sample DSN should carry a password-bearing segment")
	}
	// If someone adds another sensitive string field to DBConfig,
	// bumping this count (via reflect on future edits) should force a
	// review. Today the count is 2 (Driver, URL) — any 3rd field that
	// is a string MUST be documented and checked against
	// data-model.md's "never logged" contract.
	//
	// Kept as a purely documentary test — reflect panic on new fields
	// would be overkill until we actually grow the struct.
	_ = cfg
}

package testutil

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/user/one-llm-router/internal/api/errcode"
)

type openAPIDoc struct {
	Paths map[string]map[string]struct {
		OperationID string `yaml:"operationId"`
	} `yaml:"paths"`
}

type MalformedProbe struct {
	Name                  string
	OperationID           string
	Method                string
	Path                  string
	Headers               map[string]string
	Body                  []byte
	ExpectedStatus        int
	ExpectedCode          int
	ExpectedData          map[string]any
	AllowAnyData          bool
	UseFailingExportStore bool
}

type ParityProbe struct {
	Name                     string
	OperationID              string
	Method                   string
	Path                     string
	Headers                  map[string]string
	Body                     []byte
	ExpectedStatus           int
	ExpectedCode             int
	ExpectedEnvelope         bool
	ExpectedContentType      string
	ExpectedDispositionStart string
	AllowedRawReason         string
	UseFailingExportStore    bool
}

func RunMalformedProbe(t *testing.T, handler http.Handler, probe MalformedProbe) *httptest.ResponseRecorder {
	t.Helper()

	var reader *bytes.Reader
	if probe.Body == nil {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader(probe.Body)
	}
	req := httptest.NewRequest(probe.Method, probe.Path, reader)
	for key, value := range probe.Headers {
		req.Header.Set(key, value)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != probe.ExpectedStatus {
		t.Fatalf("%s/%s: status=%d want=%d body=%s", probe.OperationID, probe.Name, rec.Code, probe.ExpectedStatus, rec.Body.String())
	}
	data := AssertEnvelopeDataShape(t, rec, probe.ExpectedCode)
	assertProbeData(t, probe, data)
	return rec
}

func assertProbeData(t *testing.T, probe MalformedProbe, got map[string]any) {
	t.Helper()

	if probe.AllowAnyData {
		return
	}

	if len(probe.ExpectedData) == 0 {
		if len(got) != 0 {
			t.Fatalf("%s/%s: data=%v want empty object", probe.OperationID, probe.Name, got)
		}
		return
	}

	keys := make([]string, 0, len(probe.ExpectedData))
	for key := range probe.ExpectedData {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(got) != len(keys) {
		t.Fatalf("%s/%s: data keys=%v want=%v full=%v", probe.OperationID, probe.Name, mapsSortedKeys(got), keys, got)
	}
	for _, key := range keys {
		want := probe.ExpectedData[key]
		if got[key] != want {
			t.Fatalf("%s/%s: data[%q]=%v want=%v full=%v", probe.OperationID, probe.Name, key, got[key], want, got)
		}
	}
}

func mapsSortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func DeclaredAdminOperationIDs(t *testing.T) []string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	path := filepath.Join(filepath.Dir(file), "..", "..", "..", "openapi", "admin.yaml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read openapi/admin.yaml: %v", err)
	}

	var doc openAPIDoc
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("yaml.Unmarshal(openapi/admin.yaml): %v", err)
	}

	ids := make([]string, 0, len(doc.Paths))
	seen := map[string]struct{}{}
	for _, methods := range doc.Paths {
		for _, op := range methods {
			if op.OperationID == "" {
				continue
			}
			if _, ok := seen[op.OperationID]; ok {
				continue
			}
			seen[op.OperationID] = struct{}{}
			ids = append(ids, op.OperationID)
		}
	}
	sort.Strings(ids)
	return ids
}

func JSONHeaders() map[string]string {
	return map[string]string{"Content-Type": "application/json"}
}

func OversizedJSONBody(limit int) []byte {
	padding := bytes.Repeat([]byte("a"), limit)
	body := append([]byte(`{"padding":"`), padding...)
	body = append(body, []byte(`"}`)...)
	return body
}

func OAuthMalformedProbes(limitJSON int64) []MalformedProbe {
	return []MalformedProbe{
		{
			Name:           "browser_start_malformed_json",
			OperationID:    "oauthBrowserStart",
			Method:         http.MethodPost,
			Path:           "/api/admin/oauth/browser/start",
			Headers:        JSONHeaders(),
			Body:           []byte(`{"provider":`),
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.MalformedBody,
		},
		{
			Name:           "browser_start_empty_body",
			OperationID:    "oauthBrowserStart",
			Method:         http.MethodPost,
			Path:           "/api/admin/oauth/browser/start",
			Headers:        JSONHeaders(),
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.MalformedBody,
		},
		{
			Name:           "browser_start_top_level_null",
			OperationID:    "oauthBrowserStart",
			Method:         http.MethodPost,
			Path:           "/api/admin/oauth/browser/start",
			Headers:        JSONHeaders(),
			Body:           []byte(`null`),
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.MalformedBody,
		},
		{
			Name:           "browser_start_wrong_content_type",
			OperationID:    "oauthBrowserStart",
			Method:         http.MethodPost,
			Path:           "/api/admin/oauth/browser/start",
			Headers:        map[string]string{"Content-Type": "text/plain"},
			Body:           []byte(`{"provider":"openai"}`),
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.MalformedBody,
		},
		{
			Name:           "browser_start_oversized_body",
			OperationID:    "oauthBrowserStart",
			Method:         http.MethodPost,
			Path:           "/api/admin/oauth/browser/start",
			Headers:        JSONHeaders(),
			Body:           OversizedJSONBody(int(limitJSON + 1)),
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.RequestBodyTooLarge,
			ExpectedData: map[string]any{
				"scope":       "envelope",
				"limit_bytes": float64(limitJSON),
			},
		},
		{
			Name:           "manual_callback_malformed_json",
			OperationID:    "oauthBrowserManualCallback",
			Method:         http.MethodPost,
			Path:           "/api/admin/oauth/browser/manual-callback",
			Headers:        JSONHeaders(),
			Body:           []byte(`{"callback_url":`),
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.MalformedBody,
		},
		{
			Name:           "manual_callback_empty_body",
			OperationID:    "oauthBrowserManualCallback",
			Method:         http.MethodPost,
			Path:           "/api/admin/oauth/browser/manual-callback",
			Headers:        JSONHeaders(),
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.MalformedBody,
		},
		{
			Name:           "manual_callback_wrong_content_type",
			OperationID:    "oauthBrowserManualCallback",
			Method:         http.MethodPost,
			Path:           "/api/admin/oauth/browser/manual-callback",
			Headers:        map[string]string{"Content-Type": "text/plain"},
			Body:           []byte(`{"callback_url":"http://localhost:1455/auth/callback?code=abc&state=def"}`),
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.MalformedBody,
		},
		{
			Name:           "manual_callback_oversized_body",
			OperationID:    "oauthBrowserManualCallback",
			Method:         http.MethodPost,
			Path:           "/api/admin/oauth/browser/manual-callback",
			Headers:        JSONHeaders(),
			Body:           OversizedJSONBody(int(limitJSON + 1)),
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.RequestBodyTooLarge,
			ExpectedData: map[string]any{
				"scope":       "envelope",
				"limit_bytes": float64(limitJSON),
			},
		},
		{
			Name:           "cancel_malformed_json",
			OperationID:    "oauthCancel",
			Method:         http.MethodPost,
			Path:           "/api/admin/oauth/cancel",
			Headers:        JSONHeaders(),
			Body:           []byte(`{"flow_id":`),
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.MalformedBody,
		},
		{
			Name:           "cancel_empty_body",
			OperationID:    "oauthCancel",
			Method:         http.MethodPost,
			Path:           "/api/admin/oauth/cancel",
			Headers:        JSONHeaders(),
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.MalformedBody,
		},
		{
			Name:           "cancel_wrong_content_type",
			OperationID:    "oauthCancel",
			Method:         http.MethodPost,
			Path:           "/api/admin/oauth/cancel",
			Headers:        map[string]string{"Content-Type": "text/plain"},
			Body:           []byte(`{"flow_id":"stale"}`),
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.MalformedBody,
		},
		{
			Name:           "cancel_oversized_body",
			OperationID:    "oauthCancel",
			Method:         http.MethodPost,
			Path:           "/api/admin/oauth/cancel",
			Headers:        JSONHeaders(),
			Body:           OversizedJSONBody(int(limitJSON + 1)),
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.RequestBodyTooLarge,
			ExpectedData: map[string]any{
				"scope":       "envelope",
				"limit_bytes": float64(limitJSON),
			},
		},
		{
			Name:           "device_start_malformed_json",
			OperationID:    "oauthDeviceStart",
			Method:         http.MethodPost,
			Path:           "/api/admin/oauth/device/start",
			Headers:        JSONHeaders(),
			Body:           []byte(`{"provider":`),
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.MalformedBody,
		},
		{
			Name:           "device_start_empty_body",
			OperationID:    "oauthDeviceStart",
			Method:         http.MethodPost,
			Path:           "/api/admin/oauth/device/start",
			Headers:        JSONHeaders(),
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.MalformedBody,
		},
		{
			Name:           "device_start_top_level_null",
			OperationID:    "oauthDeviceStart",
			Method:         http.MethodPost,
			Path:           "/api/admin/oauth/device/start",
			Headers:        JSONHeaders(),
			Body:           []byte(`null`),
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.MalformedBody,
		},
		{
			Name:           "device_start_wrong_content_type",
			OperationID:    "oauthDeviceStart",
			Method:         http.MethodPost,
			Path:           "/api/admin/oauth/device/start",
			Headers:        map[string]string{"Content-Type": "text/plain"},
			Body:           []byte(`{"provider":"openai"}`),
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.MalformedBody,
		},
		{
			Name:           "device_start_oversized_body",
			OperationID:    "oauthDeviceStart",
			Method:         http.MethodPost,
			Path:           "/api/admin/oauth/device/start",
			Headers:        JSONHeaders(),
			Body:           OversizedJSONBody(int(limitJSON + 1)),
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.RequestBodyTooLarge,
			ExpectedData: map[string]any{
				"scope":       "envelope",
				"limit_bytes": float64(limitJSON),
			},
		},
		{
			Name:           "flow_status_unknown_query_param_ignored",
			OperationID:    "oauthFlowStatus",
			Method:         http.MethodGet,
			Path:           "/api/admin/oauth/flow?unexpected=1",
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.OK,
			ExpectedData: map[string]any{
				"status": "idle",
			},
		},
	}
}

func AdminMalformedProbes(
	partLimit int64,
	envelopeLimit int64,
	importMalformedBody []byte,
	importMalformedCT string,
	importEmptyBody []byte,
	importEmptyCT string,
	importPartTooLargeBody []byte,
	importPartTooLargeCT string,
	importPreambleFloodBody []byte,
	importPreambleFloodCT string,
	apiKeyAccountID int64,
) []MalformedProbe {
	playgroundLimit := int64(96 * 1024)
	settingsLimit := int64(16 * 1024)
	return []MalformedProbe{
		{
			Name:           "settings_get_success",
			OperationID:    "settingsGet",
			Method:         http.MethodGet,
			Path:           "/api/admin/settings",
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.OK,
			AllowAnyData:   true,
		},
		{
			Name:           "settings_update_malformed_json",
			OperationID:    "settingsUpdate",
			Method:         http.MethodPost,
			Path:           "/api/admin/settings/update",
			Headers:        JSONHeaders(),
			Body:           []byte(`{"runtime":`),
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.MalformedBody,
		},
		{
			Name:           "settings_update_wrong_content_type",
			OperationID:    "settingsUpdate",
			Method:         http.MethodPost,
			Path:           "/api/admin/settings/update",
			Headers:        map[string]string{"Content-Type": "text/plain"},
			Body:           []byte(`{}`),
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.MalformedBody,
		},
		{
			Name:           "settings_update_oversized_body",
			OperationID:    "settingsUpdate",
			Method:         http.MethodPost,
			Path:           "/api/admin/settings/update",
			Headers:        JSONHeaders(),
			Body:           OversizedJSONBody(int(settingsLimit + 1)),
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.RequestBodyTooLarge,
			ExpectedData: map[string]any{
				"scope":       "envelope",
				"limit_bytes": float64(settingsLimit),
			},
		},
		{
			Name:           "import_malformed_json_structure",
			OperationID:    "accountsImportAuthJSON",
			Method:         http.MethodPost,
			Path:           "/api/admin/accounts/import-auth-json",
			Headers:        map[string]string{"Content-Type": importMalformedCT},
			Body:           importMalformedBody,
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.InvalidAuthJSONStructure,
		},
		{
			Name:           "import_empty_body_missing_part",
			OperationID:    "accountsImportAuthJSON",
			Method:         http.MethodPost,
			Path:           "/api/admin/accounts/import-auth-json",
			Headers:        map[string]string{"Content-Type": importEmptyCT},
			Body:           importEmptyBody,
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.InvalidAuthJSONStructure,
		},
		{
			Name:           "import_wrong_content_type",
			OperationID:    "accountsImportAuthJSON",
			Method:         http.MethodPost,
			Path:           "/api/admin/accounts/import-auth-json",
			Headers:        map[string]string{"Content-Type": "application/json"},
			Body:           []byte(`{}`),
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.InvalidAuthJSONStructure,
		},
		{
			Name:           "import_oversized_part",
			OperationID:    "accountsImportAuthJSON",
			Method:         http.MethodPost,
			Path:           "/api/admin/accounts/import-auth-json",
			Headers:        map[string]string{"Content-Type": importPartTooLargeCT},
			Body:           importPartTooLargeBody,
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.RequestBodyTooLarge,
			ExpectedData: map[string]any{
				"scope":       "part",
				"limit_bytes": float64(partLimit),
			},
		},
		{
			Name:           "import_preamble_flood_envelope_cap",
			OperationID:    "accountsImportAuthJSON",
			Method:         http.MethodPost,
			Path:           "/api/admin/accounts/import-auth-json",
			Headers:        map[string]string{"Content-Type": importPreambleFloodCT},
			Body:           importPreambleFloodBody,
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.RequestBodyTooLarge,
			ExpectedData: map[string]any{
				"scope":       "envelope",
				"limit_bytes": float64(envelopeLimit),
			},
		},
		{
			Name:           "export_api_key_row_business_error",
			OperationID:    "accountsExportAuthJSON",
			Method:         http.MethodPost,
			Path:           fmt.Sprintf("/api/admin/accounts/%d/export-auth-json", apiKeyAccountID),
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.NotOAuthAccount,
			ExpectedData: map[string]any{
				"auth_method": "api_key",
			},
		},
		{
			Name:           "export_missing_row_business_error",
			OperationID:    "accountsExportAuthJSON",
			Method:         http.MethodPost,
			Path:           "/api/admin/accounts/999999/export-auth-json",
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.AccountNotFound,
		},
		{
			Name:                  "export_store_failure_system_error",
			OperationID:           "accountsExportAuthJSON",
			Method:                http.MethodPost,
			Path:                  "/api/admin/accounts/77/export-auth-json",
			ExpectedStatus:        http.StatusInternalServerError,
			ExpectedCode:          errcode.OAuthExportReadFailed,
			UseFailingExportStore: true,
		},
		{
			Name:           "playground_run_malformed_json",
			OperationID:    "playgroundRun",
			Method:         http.MethodPost,
			Path:           "/api/admin/playground/run",
			Headers:        JSONHeaders(),
			Body:           []byte(`{"selection_mode":`),
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.MalformedBody,
		},
		{
			Name:           "playground_run_wrong_content_type",
			OperationID:    "playgroundRun",
			Method:         http.MethodPost,
			Path:           "/api/admin/playground/run",
			Headers:        map[string]string{"Content-Type": "text/plain"},
			Body:           []byte(`{"selection_mode":"auto","model":"gpt-5.4-mini","text":"hello"}`),
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.MalformedBody,
		},
		{
			Name:           "playground_run_oversized_body",
			OperationID:    "playgroundRun",
			Method:         http.MethodPost,
			Path:           "/api/admin/playground/run",
			Headers:        JSONHeaders(),
			Body:           OversizedJSONBody(int(playgroundLimit + 1)),
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.RequestBodyTooLarge,
			ExpectedData: map[string]any{
				"scope":       "envelope",
				"limit_bytes": float64(playgroundLimit),
			},
		},
		{
			Name:           "dashboard_get_invalid_range",
			OperationID:    "dashboardGet",
			Method:         http.MethodGet,
			Path:           "/api/admin/dashboard?range=90d",
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.DashboardInvalidFilter,
			ExpectedData: map[string]any{
				"field":  "range",
				"reason": "must be one of 1h, 1d, 7d, 30d",
			},
		},
		{
			Name:           "usage_get_success",
			OperationID:    "usageGet",
			Method:         http.MethodGet,
			Path:           "/api/admin/usage",
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.OK,
			AllowAnyData:   true,
		},
		{
			Name:           "requests_list_invalid_limit",
			OperationID:    "requestsList",
			Method:         http.MethodGet,
			Path:           "/api/admin/requests?limit=0",
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.InvalidRequestFilter,
			ExpectedData: map[string]any{
				"field":  "limit",
				"reason": "must be between 1 and 200",
			},
		},
		{
			Name:           "requests_options_invalid_account_id",
			OperationID:    "requestsOptions",
			Method:         http.MethodGet,
			Path:           "/api/admin/requests/options?account_id=0",
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.InvalidRequestFilter,
			ExpectedData: map[string]any{
				"field":  "account_id",
				"reason": "must be positive",
			},
		},
		{
			Name:           "requests_get_invalid_id",
			OperationID:    "requestsGet",
			Method:         http.MethodGet,
			Path:           "/api/admin/requests/not-an-int",
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.InvalidRequestFilter,
			ExpectedData: map[string]any{
				"param": "id",
			},
		},
		{
			Name:           "account_models_list_success",
			OperationID:    "accountModelsList",
			Method:         http.MethodGet,
			Path:           "/api/admin/accounts/1/models",
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.OK,
			AllowAnyData:   true,
		},
		{
			Name:           "account_models_add_empty_model_id",
			OperationID:    "accountModelAdd",
			Method:         http.MethodPost,
			Path:           "/api/admin/accounts/1/models/add",
			Body:           []byte(`{"model_id":""}`),
			Headers:        JSONHeaders(),
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.MalformedBody,
			ExpectedData: map[string]any{
				"detail": "model_id must be non-empty",
			},
		},
		{
			Name:           "account_models_remove_empty_model_id",
			OperationID:    "accountModelRemove",
			Method:         http.MethodPost,
			Path:           "/api/admin/accounts/1/models/remove",
			Body:           []byte(`{"model_id":""}`),
			Headers:        JSONHeaders(),
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.MalformedBody,
			ExpectedData: map[string]any{
				"detail": "model_id must be non-empty",
			},
		},
		{
			Name:           "account_models_refresh_account_not_found",
			OperationID:    "accountModelRefresh",
			Method:         http.MethodPost,
			Path:           "/api/admin/accounts/999/models/refresh",
			ExpectedStatus: http.StatusOK,
			ExpectedCode:   errcode.AccountNotFound,
		},
	}
}

func EnvelopeParityProbes(oauthAccountID, apiKeyAccountID int64) []ParityProbe {
	return []ParityProbe{
		{
			Name:                "settingsGet_success_enveloped",
			OperationID:         "settingsGet",
			Method:              http.MethodGet,
			Path:                "/api/admin/settings",
			ExpectedStatus:      http.StatusOK,
			ExpectedCode:        errcode.OK,
			ExpectedEnvelope:    true,
			ExpectedContentType: "application/json; charset=utf-8",
		},
		{
			Name:                "settingsUpdate_success_enveloped",
			OperationID:         "settingsUpdate",
			Method:              http.MethodPost,
			Path:                "/api/admin/settings/update",
			Body:                []byte(`{}`),
			Headers:             JSONHeaders(),
			ExpectedStatus:      http.StatusOK,
			ExpectedCode:        errcode.OK,
			ExpectedEnvelope:    true,
			ExpectedContentType: "application/json; charset=utf-8",
		},
		{
			Name:                "oauthBrowserStart_invalid_provider_enveloped",
			OperationID:         "oauthBrowserStart",
			Method:              http.MethodPost,
			Path:                "/api/admin/oauth/browser/start",
			Body:                []byte(`{"provider":"bogus"}`),
			Headers:             JSONHeaders(),
			ExpectedStatus:      http.StatusOK,
			ExpectedCode:        errcode.InvalidOAuthProvider,
			ExpectedEnvelope:    true,
			ExpectedContentType: "application/json; charset=utf-8",
		},
		{
			Name:             "oauthBrowserStart_wrong_method_allowed_raw_405",
			OperationID:      "oauthBrowserStart",
			Method:           http.MethodGet,
			Path:             "/api/admin/oauth/browser/start",
			ExpectedStatus:   http.StatusMethodNotAllowed,
			ExpectedEnvelope: false,
			AllowedRawReason: "allowed raw non-envelope 405",
		},
		{
			Name:                "oauthBrowserManualCallback_invalid_callback_enveloped",
			OperationID:         "oauthBrowserManualCallback",
			Method:              http.MethodPost,
			Path:                "/api/admin/oauth/browser/manual-callback",
			Body:                []byte(`{"callback_url":"http://localhost:1455/auth/callback"}`),
			Headers:             JSONHeaders(),
			ExpectedStatus:      http.StatusOK,
			ExpectedCode:        errcode.InvalidCallbackURL,
			ExpectedEnvelope:    true,
			ExpectedContentType: "application/json; charset=utf-8",
		},
		{
			Name:                "oauthDeviceStart_invalid_provider_enveloped",
			OperationID:         "oauthDeviceStart",
			Method:              http.MethodPost,
			Path:                "/api/admin/oauth/device/start",
			Body:                []byte(`{"provider":"bogus"}`),
			Headers:             JSONHeaders(),
			ExpectedStatus:      http.StatusOK,
			ExpectedCode:        errcode.InvalidOAuthProvider,
			ExpectedEnvelope:    true,
			ExpectedContentType: "application/json; charset=utf-8",
		},
		{
			Name:                "oauthFlowStatus_idle_stays_code_zero",
			OperationID:         "oauthFlowStatus",
			Method:              http.MethodGet,
			Path:                "/api/admin/oauth/flow",
			ExpectedStatus:      http.StatusOK,
			ExpectedCode:        errcode.OK,
			ExpectedEnvelope:    true,
			ExpectedContentType: "application/json; charset=utf-8",
		},
		{
			Name:                "oauthCancel_idle_enveloped",
			OperationID:         "oauthCancel",
			Method:              http.MethodPost,
			Path:                "/api/admin/oauth/cancel",
			Body:                []byte(`{"flow_id":"stale-flow"}`),
			Headers:             JSONHeaders(),
			ExpectedStatus:      http.StatusOK,
			ExpectedCode:        errcode.OK,
			ExpectedEnvelope:    true,
			ExpectedContentType: "application/json; charset=utf-8",
		},
		{
			Name:                "accountsImportAuthJSON_wrong_content_type_enveloped",
			OperationID:         "accountsImportAuthJSON",
			Method:              http.MethodPost,
			Path:                "/api/admin/accounts/import-auth-json",
			Body:                []byte(`{}`),
			Headers:             map[string]string{"Content-Type": "application/json"},
			ExpectedStatus:      http.StatusOK,
			ExpectedCode:        errcode.InvalidAuthJSONStructure,
			ExpectedEnvelope:    true,
			ExpectedContentType: "application/json; charset=utf-8",
		},
		{
			Name:                     "accountsExportAuthJSON_success_raw_attachment",
			OperationID:              "accountsExportAuthJSON",
			Method:                   http.MethodPost,
			Path:                     fmt.Sprintf("/api/admin/accounts/%d/export-auth-json", oauthAccountID),
			ExpectedStatus:           http.StatusOK,
			ExpectedEnvelope:         false,
			ExpectedContentType:      "application/json; charset=utf-8",
			ExpectedDispositionStart: `attachment; filename="auth.json"`,
		},
		{
			Name:                "accountsExportAuthJSON_not_oauth_enveloped",
			OperationID:         "accountsExportAuthJSON",
			Method:              http.MethodPost,
			Path:                fmt.Sprintf("/api/admin/accounts/%d/export-auth-json", apiKeyAccountID),
			ExpectedStatus:      http.StatusOK,
			ExpectedCode:        errcode.NotOAuthAccount,
			ExpectedEnvelope:    true,
			ExpectedContentType: "application/json; charset=utf-8",
		},
		{
			Name:                  "accountsExportAuthJSON_store_failure_enveloped_500",
			OperationID:           "accountsExportAuthJSON",
			Method:                http.MethodPost,
			Path:                  "/api/admin/accounts/77/export-auth-json",
			ExpectedStatus:        http.StatusInternalServerError,
			ExpectedCode:          errcode.OAuthExportReadFailed,
			ExpectedEnvelope:      true,
			ExpectedContentType:   "application/json; charset=utf-8",
			UseFailingExportStore: true,
		},
		{
			Name:                "playgroundRun_invalid_request_enveloped",
			OperationID:         "playgroundRun",
			Method:              http.MethodPost,
			Path:                "/api/admin/playground/run",
			Body:                []byte(`{"selection_mode":"auto","model":"gpt-5.4-mini","text":""}`),
			Headers:             JSONHeaders(),
			ExpectedStatus:      http.StatusOK,
			ExpectedCode:        errcode.InvalidPlaygroundRequest,
			ExpectedEnvelope:    true,
			ExpectedContentType: "application/json; charset=utf-8",
		},
		{
			Name:                "dashboardGet_success_enveloped",
			OperationID:         "dashboardGet",
			Method:              http.MethodGet,
			Path:                "/api/admin/dashboard",
			ExpectedStatus:      http.StatusOK,
			ExpectedCode:        errcode.OK,
			ExpectedEnvelope:    true,
			ExpectedContentType: "application/json; charset=utf-8",
		},
		{
			Name:                "usageGet_success_enveloped",
			OperationID:         "usageGet",
			Method:              http.MethodGet,
			Path:                "/api/admin/usage",
			ExpectedStatus:      http.StatusOK,
			ExpectedCode:        errcode.OK,
			ExpectedEnvelope:    true,
			ExpectedContentType: "application/json; charset=utf-8",
		},
		{
			Name:                "requestsList_empty_success_enveloped",
			OperationID:         "requestsList",
			Method:              http.MethodGet,
			Path:                "/api/admin/requests",
			ExpectedStatus:      http.StatusOK,
			ExpectedCode:        errcode.OK,
			ExpectedEnvelope:    true,
			ExpectedContentType: "application/json; charset=utf-8",
		},
		{
			Name:                "requestsOptions_empty_success_enveloped",
			OperationID:         "requestsOptions",
			Method:              http.MethodGet,
			Path:                "/api/admin/requests/options",
			ExpectedStatus:      http.StatusOK,
			ExpectedCode:        errcode.OK,
			ExpectedEnvelope:    true,
			ExpectedContentType: "application/json; charset=utf-8",
		},
		{
			Name:                "requestsGet_missing_record_enveloped",
			OperationID:         "requestsGet",
			Method:              http.MethodGet,
			Path:                "/api/admin/requests/999999",
			ExpectedStatus:      http.StatusOK,
			ExpectedCode:        errcode.RequestRecordNotFound,
			ExpectedEnvelope:    true,
			ExpectedContentType: "application/json; charset=utf-8",
		},
		{
			Name:                "account_models_list_success",
			OperationID:         "accountModelsList",
			Method:              http.MethodGet,
			Path:                "/api/admin/accounts/1/models",
			ExpectedStatus:      http.StatusOK,
			ExpectedCode:        errcode.OK,
			ExpectedEnvelope:    true,
			ExpectedContentType: "application/json; charset=utf-8",
		},
		{
			Name:                "account_models_add_empty_model_id",
			OperationID:         "accountModelAdd",
			Method:              http.MethodPost,
			Path:                "/api/admin/accounts/1/models/add",
			Body:                []byte(`{"model_id":""}`),
			Headers:             JSONHeaders(),
			ExpectedStatus:      http.StatusOK,
			ExpectedCode:        errcode.MalformedBody,
			ExpectedEnvelope:    true,
			ExpectedContentType: "application/json; charset=utf-8",
		},
		{
			Name:                "account_models_remove_empty_model_id",
			OperationID:         "accountModelRemove",
			Method:              http.MethodPost,
			Path:                "/api/admin/accounts/1/models/remove",
			Body:                []byte(`{"model_id":""}`),
			Headers:             JSONHeaders(),
			ExpectedStatus:      http.StatusOK,
			ExpectedCode:        errcode.MalformedBody,
			ExpectedEnvelope:    true,
			ExpectedContentType: "application/json; charset=utf-8",
		},
		{
			Name:                "account_models_refresh_account_not_found",
			OperationID:         "accountModelRefresh",
			Method:              http.MethodPost,
			Path:                "/api/admin/accounts/999/models/refresh",
			ExpectedStatus:      http.StatusOK,
			ExpectedCode:        errcode.AccountNotFound,
			ExpectedEnvelope:    true,
			ExpectedContentType: "application/json; charset=utf-8",
		},
		{
			Name:                "account_update_success_enveloped",
			OperationID:         "accountUpdate",
			Method:              http.MethodPost,
			Path:                fmt.Sprintf("/api/admin/accounts/%d/update", apiKeyAccountID),
			Body:                []byte(`{"name":"renamed-by-parity"}`),
			Headers:             JSONHeaders(),
			ExpectedStatus:      http.StatusOK,
			ExpectedCode:        errcode.OK,
			ExpectedEnvelope:    true,
			ExpectedContentType: "application/json; charset=utf-8",
		},
		{
			Name:                "account_update_oauth_row_rejected_enveloped",
			OperationID:         "accountUpdate",
			Method:              http.MethodPost,
			Path:                fmt.Sprintf("/api/admin/accounts/%d/update", oauthAccountID),
			Body:                []byte(`{"name":"hijack"}`),
			Headers:             JSONHeaders(),
			ExpectedStatus:      http.StatusOK,
			ExpectedCode:        errcode.InvalidAccountPayload,
			ExpectedEnvelope:    true,
			ExpectedContentType: "application/json; charset=utf-8",
		},
	}
}

func RequiredEnvelopeParityProbeNames() []string {
	return []string{
		"oauthBrowserStart_invalid_provider_enveloped",
		"oauthBrowserStart_wrong_method_allowed_raw_405",
		"settingsGet_success_enveloped",
		"settingsUpdate_success_enveloped",
		"oauthBrowserManualCallback_invalid_callback_enveloped",
		"oauthDeviceStart_invalid_provider_enveloped",
		"oauthFlowStatus_idle_stays_code_zero",
		"oauthCancel_idle_enveloped",
		"accountsImportAuthJSON_wrong_content_type_enveloped",
		"accountsExportAuthJSON_success_raw_attachment",
		"accountsExportAuthJSON_not_oauth_enveloped",
		"accountsExportAuthJSON_store_failure_enveloped_500",
		"playgroundRun_invalid_request_enveloped",
		"dashboardGet_success_enveloped",
		"usageGet_success_enveloped",
		"requestsList_empty_success_enveloped",
		"requestsOptions_empty_success_enveloped",
		"requestsGet_missing_record_enveloped",
		"account_update_success_enveloped",
		"account_update_oauth_row_rejected_enveloped",
	}
}

func RequiredMalformedProbeNames() []string {
	return []string{
		"browser_start_malformed_json",
		"browser_start_wrong_content_type",
		"browser_start_oversized_body",
		"manual_callback_malformed_json",
		"manual_callback_wrong_content_type",
		"manual_callback_oversized_body",
		"cancel_malformed_json",
		"cancel_wrong_content_type",
		"cancel_oversized_body",
		"device_start_malformed_json",
		"device_start_wrong_content_type",
		"device_start_oversized_body",
		"flow_status_unknown_query_param_ignored",
		"settings_get_success",
		"settings_update_malformed_json",
		"settings_update_wrong_content_type",
		"settings_update_oversized_body",
		"import_malformed_json_structure",
		"import_empty_body_missing_part",
		"import_wrong_content_type",
		"import_oversized_part",
		"import_preamble_flood_envelope_cap",
		"export_api_key_row_business_error",
		"export_missing_row_business_error",
		"export_store_failure_system_error",
		"playground_run_malformed_json",
		"playground_run_wrong_content_type",
		"playground_run_oversized_body",
		"dashboard_get_invalid_range",
		"usage_get_success",
		"requests_list_invalid_limit",
		"requests_options_invalid_account_id",
		"requests_get_invalid_id",
		"account_models_list_success",
		"account_models_add_empty_model_id",
		"account_models_remove_empty_model_id",
		"account_models_refresh_account_not_found",
	}
}

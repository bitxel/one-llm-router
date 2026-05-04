package setup_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/user/one-llm-router/internal/api/errcode"
	apisetup "github.com/user/one-llm-router/internal/api/setup"
	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/setup"
)

type fakeGate struct {
	open bool
	err  error
}

func (g *fakeGate) ProbeState() (bool, error) { return g.open, g.err }

type fakeFactory struct {
	openErr error
	upErr   error
}

func (f *fakeFactory) Open(_ context.Context, _, _ string) (setup.MigratorHandle, error) {
	if f.openErr != nil {
		return nil, f.openErr
	}
	return &fakeMig{upErr: f.upErr}, nil
}

type fakeMig struct{ upErr error }

func (m *fakeMig) Up(_ context.Context) error { return m.upErr }
func (m *fakeMig) Close() error               { return nil }

type fakeAcc struct {
	err error
	id  int64
}

func (c *fakeAcc) CreateAccount(_ context.Context, _, _ string, a *domain.UpstreamAccount) error {
	if c.err != nil {
		return c.err
	}
	c.id++
	a.ID = c.id
	return nil
}

func newHandler(t *testing.T, gate *fakeGate) (*apisetup.Handler, *fakeFactory, *fakeAcc, string, *bool) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	f := &fakeFactory{}
	c := &fakeAcc{}
	reloaded := false
	h := apisetup.NewHandler(gate, cfgPath, f, c,
		func(_ context.Context) error { reloaded = true; return nil }, nil)
	return h, f, c, cfgPath, &reloaded
}

func decodeEnvelope(t *testing.T, body io.Reader) (code int, data map[string]any) {
	t.Helper()
	raw, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var env struct {
		Code int            `json:"code"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode envelope: %v body=%q", err, string(raw))
	}
	return env.Code, env.Data
}

func TestStatus_Pending(t *testing.T) {
	t.Parallel()
	h, _, _, _, _ := newHandler(t, &fakeGate{open: false})

	req := httptest.NewRequest(http.MethodGet, "/api/setup/status", nil)
	w := httptest.NewRecorder()
	h.Status(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	code, data := decodeEnvelope(t, w.Body)
	if code != 0 {
		t.Fatalf("envelope code = %d, want 0", code)
	}
	if data["state"] != "pending" {
		t.Errorf("state = %v, want pending", data["state"])
	}
	if _, ok := data["supported_drivers"]; !ok {
		t.Error("missing supported_drivers")
	}
	if _, ok := data["defaults"]; !ok {
		t.Error("missing defaults")
	}
}

func TestStatus_Done(t *testing.T) {
	t.Parallel()
	h, _, _, _, _ := newHandler(t, &fakeGate{open: true})

	req := httptest.NewRequest(http.MethodGet, "/api/setup/status", nil)
	w := httptest.NewRecorder()
	h.Status(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	_, data := decodeEnvelope(t, w.Body)
	if data["state"] != "done" {
		t.Errorf("state = %v, want done", data["state"])
	}
}

func TestStatus_Error(t *testing.T) {
	t.Parallel()
	h, _, _, _, _ := newHandler(t, &fakeGate{err: errors.New("boom")})

	req := httptest.NewRequest(http.MethodGet, "/api/setup/status", nil)
	w := httptest.NewRecorder()
	h.Status(w, req)

	// F-007: ProbeState failures surface as HTTP 500 system errors
	// instead of `state:"error"` (which was a non-contract third
	// state). setup-api.md §GET /api/setup/status treats unreadable
	// config paths as a system fault.
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	code, _ := decodeEnvelope(t, w.Body)
	if code != errcode.InternalError {
		t.Errorf("code = %d, want %d", code, errcode.InternalError)
	}
}

func TestProbeDSN_Valid(t *testing.T) {
	t.Parallel()
	h, _, _, _, _ := newHandler(t, &fakeGate{open: false})

	dir := t.TempDir()
	dsn := filepath.Join(dir, "router.db")
	body := []byte(`{"db":{"driver":"sqlite3","url":"` + dsn + `"}}`)

	req := httptest.NewRequest(http.MethodPost, "/api/setup/probe-dsn", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ProbeDSN(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	code, data := decodeEnvelope(t, w.Body)
	if code != 0 {
		t.Fatalf("envelope code = %d, data = %v", code, data)
	}
	if data["ok"] != true {
		t.Errorf("data.ok = %v", data["ok"])
	}
}

func TestProbeDSN_InvalidDriver(t *testing.T) {
	t.Parallel()
	h, _, _, _, _ := newHandler(t, &fakeGate{})
	body := []byte(`{"db":{"driver":"mssql","url":"server=localhost"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/setup/probe-dsn", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ProbeDSN(w, req)

	code, _ := decodeEnvelope(t, w.Body)
	if code != errcode.InvalidDriver {
		t.Errorf("code = %d, want %d", code, errcode.InvalidDriver)
	}
}

func TestProbeDSN_EmptyDSN(t *testing.T) {
	t.Parallel()
	h, _, _, _, _ := newHandler(t, &fakeGate{})
	body := []byte(`{"db":{"driver":"sqlite3","url":""}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/setup/probe-dsn", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ProbeDSN(w, req)
	code, _ := decodeEnvelope(t, w.Body)
	if code != errcode.InvalidDSN {
		t.Errorf("code = %d, want %d", code, errcode.InvalidDSN)
	}
}

func TestProbeDSN_MalformedBody(t *testing.T) {
	t.Parallel()
	h, _, _, _, _ := newHandler(t, &fakeGate{})
	req := httptest.NewRequest(http.MethodPost, "/api/setup/probe-dsn",
		bytes.NewReader([]byte(`{not json`)))
	w := httptest.NewRecorder()
	h.ProbeDSN(w, req)
	code, _ := decodeEnvelope(t, w.Body)
	if code != errcode.MalformedBody {
		t.Errorf("code = %d, want %d", code, errcode.MalformedBody)
	}
}

func TestProbeDSN_BodyTooLarge(t *testing.T) {
	t.Parallel()
	h, _, _, _, _ := newHandler(t, &fakeGate{})
	big := bytes.Repeat([]byte(`a`), apisetup.MaxBodyBytes*2)
	req := httptest.NewRequest(http.MethodPost, "/api/setup/probe-dsn",
		bytes.NewReader(big))
	w := httptest.NewRecorder()
	h.ProbeDSN(w, req)
	code, data := decodeEnvelope(t, w.Body)
	if code != errcode.RequestBodyTooLarge {
		t.Errorf("code = %d, want %d", code, errcode.RequestBodyTooLarge)
	}
	// Detail must name the actual cap so clients can surface a
	// precise toast instead of a generic "too large" message.
	// Mismatch between MaxBodyBytes and the detail string is how
	// the 1 MiB / 8 KiB drift slipped in during the first review
	// pass (F011).
	if detail, _ := data["detail"].(string); !strings.Contains(detail, "8 KiB") {
		t.Errorf("detail = %q, want to mention '8 KiB'", detail)
	}
}

func TestProbeDSN_Unreachable(t *testing.T) {
	t.Parallel()
	h, _, _, _, _ := newHandler(t, &fakeGate{})
	body := []byte(`{"db":{"driver":"sqlite3","url":"/no/such/dir/db.sqlite"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/setup/probe-dsn", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ProbeDSN(w, req)
	code, data := decodeEnvelope(t, w.Body)
	if code != errcode.InvalidDSN {
		t.Fatalf("code = %d, want %d (%v)", code, errcode.InvalidDSN, data)
	}
	if data["ok"] != false {
		t.Errorf("data.ok = %v", data["ok"])
	}
}

func TestProbeDSN_AlreadyDone(t *testing.T) {
	t.Parallel()
	h, _, _, _, _ := newHandler(t, &fakeGate{open: true})
	body := []byte(`{"db":{"driver":"sqlite3","url":"/tmp/x.sqlite"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/setup/probe-dsn", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ProbeDSN(w, req)
	code, _ := decodeEnvelope(t, w.Body)
	if code != errcode.SetupAlreadyDone {
		t.Errorf("code = %d, want %d", code, errcode.SetupAlreadyDone)
	}
}

func TestCommit_AlreadyDone(t *testing.T) {
	t.Parallel()
	h, _, _, _, _ := newHandler(t, &fakeGate{open: true})
	body := validCommitBody()
	req := httptest.NewRequest(http.MethodPost, "/api/setup/commit", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.Commit(w, req)
	code, _ := decodeEnvelope(t, w.Body)
	if code != errcode.SetupAlreadyDone {
		t.Errorf("code = %d, want %d", code, errcode.SetupAlreadyDone)
	}
}

func TestCommit_Happy(t *testing.T) {
	t.Parallel()
	h, _, _, cfgPath, reloaded := newHandler(t, &fakeGate{open: false})
	body := validCommitBody()
	req := httptest.NewRequest(http.MethodPost, "/api/setup/commit", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.Commit(w, req)
	code, data := decodeEnvelope(t, w.Body)
	if code != 0 {
		t.Fatalf("code = %d, data = %v", code, data)
	}
	if data["redirect"] != "/admin/" {
		t.Errorf("redirect = %v, want /admin/", data["redirect"])
	}
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("config.json not written: %v", err)
	}
	if !*reloaded {
		t.Error("reloader not invoked")
	}
}

// setup-api.md v2.4 — a commit payload that omits first_account must
// succeed: config.json lands on disk, migrate runs, but CreateAccount
// is never called and the response body omits account_id.
func TestCommit_SkipsFirstAccount(t *testing.T) {
	t.Parallel()
	h, _, creator, cfgPath, reloaded := newHandler(t, &fakeGate{open: false})
	dir := filepath.Dir(cfgPath)
	payload := map[string]any{
		"db": map[string]any{
			"driver": "sqlite3",
			"url":    filepath.Join(dir, "router.db"),
		},
		"plugins": map[string]any{
			"admin_auth":  map[string]any{"enabled": false},
			"client_keys": map[string]any{"enabled": false},
		},
		// first_account deliberately omitted.
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/setup/commit", bytes.NewReader(b))
	w := httptest.NewRecorder()
	h.Commit(w, req)

	code, data := decodeEnvelope(t, w.Body)
	if code != 0 {
		t.Fatalf("code = %d, data = %v", code, data)
	}
	if data["redirect"] != "/admin/" {
		t.Errorf("redirect = %v, want /admin/", data["redirect"])
	}
	if _, has := data["account_id"]; has {
		t.Errorf("account_id should be absent when first_account is skipped; got %v", data["account_id"])
	}
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("config.json not written on skipped first_account: %v", err)
	}
	if !*reloaded {
		t.Error("reloader not invoked on skipped first_account")
	}
	if creator.id != 0 {
		t.Errorf("CreateAccount was called (id=%d); expected 0 invocations", creator.id)
	}
}

func TestCommit_UnknownPluginID(t *testing.T) {
	t.Parallel()
	h, _, _, _, _ := newHandler(t, &fakeGate{open: false})
	payload := map[string]any{
		"db": map[string]any{"driver": "sqlite3", "url": "x.db"},
		"first_account": map[string]any{
			"name": "prod", "provider": "openai", "api_key": "sk-x",
		},
		"plugins": map[string]any{
			"admin_auth":  map[string]any{"enabled": false},
			"client_keys": map[string]any{"enabled": false},
			"evil":        map[string]any{"enabled": true}, // unknown ID
		},
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/setup/commit", bytes.NewReader(b))
	w := httptest.NewRecorder()
	h.Commit(w, req)
	code, data := decodeEnvelope(t, w.Body)
	if code != errcode.InvalidPluginFlag {
		t.Fatalf("code = %d, want %d (data=%v)", code, errcode.InvalidPluginFlag, data)
	}
}

func TestCommit_InvalidAccountName(t *testing.T) {
	t.Parallel()
	h, _, _, _, _ := newHandler(t, &fakeGate{open: false})
	body := []byte(`{
	  "db":{"driver":"sqlite3","url":"x.db"},
	  "first_account":{"name":"bad name","provider":"openai","api_key":"sk-x"},
	  "plugins":{"admin_auth":{"enabled":false},"client_keys":{"enabled":false}}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/setup/commit", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.Commit(w, req)
	code, _ := decodeEnvelope(t, w.Body)
	if code != errcode.InvalidAccountName {
		t.Errorf("code = %d, want %d", code, errcode.InvalidAccountName)
	}
}

func TestCommit_PartialRuntimeRejected(t *testing.T) {
	t.Parallel()
	h, _, _, _, _ := newHandler(t, &fakeGate{open: false})
	payload := map[string]any{
		"db": map[string]any{"driver": "sqlite3", "url": "x.db"},
		"first_account": map[string]any{
			"name": "prod", "provider": "openai", "api_key": "sk-x",
		},
		"plugins": map[string]any{
			"admin_auth":  map[string]any{"enabled": false},
			"client_keys": map[string]any{"enabled": false},
		},
		"runtime": map[string]any{
			"log_client_request_body": true,
			// missing the other 4 fields
		},
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/setup/commit", bytes.NewReader(b))
	w := httptest.NewRecorder()
	h.Commit(w, req)
	code, _ := decodeEnvelope(t, w.Body)
	if code != errcode.MalformedBody {
		t.Errorf("code = %d, want %d", code, errcode.MalformedBody)
	}
}

func TestRegisterRoutes(t *testing.T) {
	t.Parallel()
	h, _, _, _, _ := newHandler(t, &fakeGate{open: false})
	mux := http.NewServeMux()
	apisetup.RegisterRoutes(mux, h)

	// Status should be reachable.
	req := httptest.NewRequest(http.MethodGet, "/api/setup/status", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("status endpoint registration failed: %d", w.Code)
	}
}

func validCommitBody() []byte {
	dir, _ := os.MkdirTemp("", "commit-body-")
	_ = dir // content doesn't need a real dir; handler uses its own cfgPath
	payload := map[string]any{
		"db": map[string]any{
			"driver": "sqlite3",
			"url":    filepath.Join(dir, "router.db"),
		},
		"first_account": map[string]any{
			"name": "prod-01", "provider": "openai", "api_key": "sk-abc",
		},
		"plugins": map[string]any{
			"admin_auth":  map[string]any{"enabled": false},
			"client_keys": map[string]any{"enabled": false},
		},
	}
	b, _ := json.Marshal(payload)
	return b
}

func TestDecode_StrictBodyCap(t *testing.T) {
	t.Parallel()
	// Ensure the body cap is strictly enforced on probe.
	h, _, _, _, _ := newHandler(t, &fakeGate{})
	body := []byte(`{"db":{"driver":"sqlite3","url":"` + strings.Repeat("x", apisetup.MaxBodyBytes) + `"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/setup/probe-dsn", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ProbeDSN(w, req)
	code, data := decodeEnvelope(t, w.Body)
	if code != errcode.RequestBodyTooLarge {
		t.Errorf("code = %d, want %d", code, errcode.RequestBodyTooLarge)
	}
	if detail, _ := data["detail"].(string); !strings.Contains(detail, "8 KiB") {
		t.Errorf("detail = %q, want to mention '8 KiB'", detail)
	}
}

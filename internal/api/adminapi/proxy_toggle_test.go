package adminapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/user/one-llm-router/internal/api/errcode"
	"github.com/user/one-llm-router/internal/config"
	"github.com/user/one-llm-router/internal/domain"
)

type fakeProxyToggleStore struct {
	accounts map[int64]*domain.UpstreamAccount
	updates  map[int64]bool
	getErr   error
}

func (s *fakeProxyToggleStore) GetByID(_ context.Context, id int64) (*domain.UpstreamAccount, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	acct, ok := s.accounts[id]
	if !ok {
		return nil, domain.ErrAccountNotFound
	}
	return acct, nil
}

func (s *fakeProxyToggleStore) UpdateUseProxy(_ context.Context, id int64, useProxy bool) error {
	if _, ok := s.accounts[id]; !ok {
		return domain.ErrAccountNotFound
	}
	if s.updates == nil {
		s.updates = map[int64]bool{}
	}
	s.updates[id] = useProxy
	return nil
}

type staticReader struct{ cfg *config.Config }

func (r staticReader) Load() *config.Config { return r.cfg }

func proxyToggleServer(store *fakeProxyToggleStore, cfg *config.Config) http.Handler {
	handler := NewProxyToggleHandler(store, staticReader{cfg: cfg}, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/admin/accounts/{id}/proxy/set", handler.SetUseProxy)
	return mux
}

func decodeToggleEnvelope(t *testing.T, rec *httptest.ResponseRecorder) (int, map[string]any) {
	t.Helper()
	var body struct {
		Code int            `json:"code"`
		Msg  string         `json:"msg"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200 (envelope policy)", rec.Code)
	}
	return body.Code, body.Data
}

func TestProxyToggle_SetTrueWithoutGlobalProxyReturns9002(t *testing.T) {
	t.Parallel()
	store := &fakeProxyToggleStore{accounts: map[int64]*domain.UpstreamAccount{1: {ID: 1, Status: domain.AccountStatusActive}}}
	srv := proxyToggleServer(store, &config.Config{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/accounts/1/proxy/set", strings.NewReader(`{"use_proxy":true}`))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(rec, req)

	code, data := decodeToggleEnvelope(t, rec)
	if code != errcode.ProxyURLRequired {
		t.Fatalf("code = %d, want %d (proxy_url_required)", code, errcode.ProxyURLRequired)
	}
	if data["field"] != "use_proxy" {
		t.Fatalf("data.field = %v, want use_proxy", data["field"])
	}
}

func TestProxyToggle_SetTrueWithGlobalProxySucceeds(t *testing.T) {
	t.Parallel()
	store := &fakeProxyToggleStore{accounts: map[int64]*domain.UpstreamAccount{1: {ID: 1, Status: domain.AccountStatusActive}}}
	srv := proxyToggleServer(store, &config.Config{Network: config.NetworkConfig{ProxyURL: "socks5://127.0.0.1:7890"}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/accounts/1/proxy/set", strings.NewReader(`{"use_proxy":true}`))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(rec, req)

	code, _ := decodeToggleEnvelope(t, rec)
	if code != errcode.OK {
		t.Fatalf("code = %d, want 0", code)
	}
	if !store.updates[1] {
		t.Fatal("UpdateUseProxy(true) not recorded")
	}
}

func TestProxyToggle_AllAuthMethodsAccepted(t *testing.T) {
	t.Parallel()
	for _, method := range []domain.AuthMethod{
		domain.AuthMethodAPIKey,
		domain.AuthMethodOAuthBrowser,
		domain.AuthMethodOAuthDevice,
		domain.AuthMethodOAuthImport,
	} {
		store := &fakeProxyToggleStore{accounts: map[int64]*domain.UpstreamAccount{
			7: {ID: 7, Status: domain.AccountStatusActive, AuthMethod: method},
		}}
		srv := proxyToggleServer(store, &config.Config{Network: config.NetworkConfig{ProxyURL: "http://p:1"}})

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/admin/accounts/7/proxy/set", strings.NewReader(`{"use_proxy":true}`))
		req.Header.Set("Content-Type", "application/json")
		srv.ServeHTTP(rec, req)

		if code, _ := decodeToggleEnvelope(t, rec); code != errcode.OK {
			t.Fatalf("auth_method %q: code = %d, want 0 — toggle is not credential-gated", method, code)
		}
	}
}

func TestProxyToggle_IdempotentAbsoluteAssignment(t *testing.T) {
	t.Parallel()
	store := &fakeProxyToggleStore{accounts: map[int64]*domain.UpstreamAccount{1: {ID: 1, Status: domain.AccountStatusActive}}}
	srv := proxyToggleServer(store, &config.Config{Network: config.NetworkConfig{ProxyURL: "http://p:1"}})

	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/admin/accounts/1/proxy/set", strings.NewReader(`{"use_proxy":false}`))
		req.Header.Set("Content-Type", "application/json")
		srv.ServeHTTP(rec, req)
		if code, _ := decodeToggleEnvelope(t, rec); code != errcode.OK {
			t.Fatalf("repeat %d: code = %d, want 0 (absolute assignment is idempotent)", i, code)
		}
	}
}

func TestProxyToggle_MissingAndDeletedAccounts(t *testing.T) {
	t.Parallel()
	store := &fakeProxyToggleStore{accounts: map[int64]*domain.UpstreamAccount{
		9: {ID: 9, Status: domain.AccountStatusDeleted},
	}}
	srv := proxyToggleServer(store, &config.Config{Network: config.NetworkConfig{ProxyURL: "http://p:1"}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/accounts/8/proxy/set", strings.NewReader(`{"use_proxy":false}`))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(rec, req)
	if code, _ := decodeToggleEnvelope(t, rec); code != errcode.AccountNotFound {
		t.Fatalf("missing account code = %d, want %d", code, errcode.AccountNotFound)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/admin/accounts/9/proxy/set", strings.NewReader(`{"use_proxy":false}`))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(rec, req)
	if code, _ := decodeToggleEnvelope(t, rec); code != errcode.AccountAlreadyInState {
		t.Fatalf("deleted account code = %d, want %d", code, errcode.AccountAlreadyInState)
	}
}

func TestProxyToggle_MalformedBody(t *testing.T) {
	t.Parallel()
	store := &fakeProxyToggleStore{accounts: map[int64]*domain.UpstreamAccount{1: {ID: 1, Status: domain.AccountStatusActive}}}
	srv := proxyToggleServer(store, &config.Config{Network: config.NetworkConfig{ProxyURL: "http://p:1"}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/accounts/1/proxy/set", strings.NewReader(`{"use_proxy":`))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(rec, req)
	if code, _ := decodeToggleEnvelope(t, rec); code != errcode.MalformedBody {
		t.Fatalf("code = %d, want %d", code, errcode.MalformedBody)
	}
}

func TestProxyToggle_RequiresExactlyOneBooleanField(t *testing.T) {
	t.Parallel()
	store := &fakeProxyToggleStore{accounts: map[int64]*domain.UpstreamAccount{
		1: {ID: 1, Status: domain.AccountStatusActive},
	}}
	srv := proxyToggleServer(store, &config.Config{Network: config.NetworkConfig{ProxyURL: "http://p:1"}})

	for _, body := range []string{`{}`, `{"use_proxy":null}`, `{"use_proxy":false,"extra":true}`} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/admin/accounts/1/proxy/set", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		srv.ServeHTTP(rec, req)
		if code, _ := decodeToggleEnvelope(t, rec); code != errcode.MalformedBody {
			t.Fatalf("body %s: code = %d, want %d", body, code, errcode.MalformedBody)
		}
	}
	if len(store.updates) != 0 {
		t.Fatalf("malformed requests updated account: %v", store.updates)
	}
}

func TestProxyToggle_NilReaderFailsClosed(t *testing.T) {
	t.Parallel()
	store := &fakeProxyToggleStore{accounts: map[int64]*domain.UpstreamAccount{1: {ID: 1, Status: domain.AccountStatusActive}}}
	handler := NewProxyToggleHandler(store, nil, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/admin/accounts/{id}/proxy/set", handler.SetUseProxy)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/accounts/1/proxy/set", strings.NewReader(`{"use_proxy":true}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	if code, _ := decodeToggleEnvelope(t, rec); code != errcode.ProxyURLRequired {
		t.Fatalf("nil reader code = %d, want %d (fail closed)", code, errcode.ProxyURLRequired)
	}
}

var errFakeStoreWrite = errors.New("fake store failure")

func TestProxyToggle_StoreFailureIsSystemError(t *testing.T) {
	t.Parallel()
	store := &fakeProxyToggleStore{
		accounts: map[int64]*domain.UpstreamAccount{1: {ID: 1, Status: domain.AccountStatusActive}},
		getErr:   nil,
	}
	// Force a write-path failure via an unwired update: use a wrapper that errors.
	failing := &failingUpdateStore{inner: store}
	handler := NewProxyToggleHandler(failing, staticReader{cfg: &config.Config{Network: config.NetworkConfig{ProxyURL: "http://p:1"}}}, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/admin/accounts/{id}/proxy/set", handler.SetUseProxy)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/accounts/1/proxy/set", strings.NewReader(`{"use_proxy":true}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	var body struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Code != errcode.InternalError {
		t.Fatalf("code = %d, want %d (system error, not business)", body.Code, errcode.InternalError)
	}
}

func TestProxyToggle_AccountLookupFailureIsSystemError(t *testing.T) {
	t.Parallel()
	store := &fakeProxyToggleStore{
		accounts: map[int64]*domain.UpstreamAccount{1: {ID: 1, Status: domain.AccountStatusActive}},
		getErr:   errFakeStoreWrite,
	}
	handler := NewProxyToggleHandler(store, staticReader{cfg: &config.Config{}}, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/admin/accounts/{id}/proxy/set", handler.SetUseProxy)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/accounts/1/proxy/set", strings.NewReader(`{"use_proxy":false}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	var body struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Code != errcode.InternalError {
		t.Fatalf("code = %d, want %d", body.Code, errcode.InternalError)
	}
}

type failingUpdateStore struct{ inner *fakeProxyToggleStore }

func (s *failingUpdateStore) GetByID(ctx context.Context, id int64) (*domain.UpstreamAccount, error) {
	return s.inner.GetByID(ctx, id)
}
func (s *failingUpdateStore) UpdateUseProxy(context.Context, int64, bool) error {
	return errFakeStoreWrite
}

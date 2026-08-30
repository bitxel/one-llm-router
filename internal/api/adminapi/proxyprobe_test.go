package adminapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/user/one-llm-router/internal/api/errcode"
	"github.com/user/one-llm-router/internal/proxydial"
)

type fakeProxyTester struct {
	err error
}

func (f fakeProxyTester) test(ctx context.Context, raw string, timeout time.Duration) error {
	return f.err
}

func proxyTestServer(tester fakeProxyTester) http.Handler {
	h := NewProxyTestHandler(nil)
	h.testConnectivity = tester.test
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/admin/settings/proxy/test", h.Test)
	return mux
}

func decodeProxyTestEnvelope(t *testing.T, rec *httptest.ResponseRecorder) (int, map[string]any) {
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

func TestProxyTest_Reachable(t *testing.T) {
	t.Parallel()
	srv := proxyTestServer(fakeProxyTester{err: nil})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/settings/proxy/test",
		strings.NewReader(`{"proxy_url":"socks5://user:pass@127.0.0.1:1080"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(rec, req)

	code, data := decodeProxyTestEnvelope(t, rec)
	if code != errcode.OK {
		t.Fatalf("code = %d, want 0", code)
	}
	if data["reachable"] != true {
		t.Fatalf("data.reachable = %v, want true", data["reachable"])
	}
}

func TestProxyTest_InvalidURL(t *testing.T) {
	t.Parallel()
	srv := proxyTestServer(fakeProxyTester{err: proxydial.ErrInvalidProxyURL})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/settings/proxy/test",
		strings.NewReader(`{"proxy_url":"ftp://nope"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(rec, req)

	code, data := decodeProxyTestEnvelope(t, rec)
	if code != errcode.InvalidProxyURL {
		t.Fatalf("code = %d, want %d", code, errcode.InvalidProxyURL)
	}
	if data["field"] != "proxy_url" {
		t.Fatalf("data.field = %v, want proxy_url", data["field"])
	}
}

func TestProxyTest_ConnectivityFailureCarriesCoarseReason(t *testing.T) {
	t.Parallel()
	srv := proxyTestServer(fakeProxyTester{
		err: &proxydial.ConnectivityError{Reason: "timeout", Err: errors.New("dial tcp: i/o timeout")},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/settings/proxy/test",
		strings.NewReader(`{"proxy_url":"http://10.0.0.1:8080"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(rec, req)

	code, data := decodeProxyTestEnvelope(t, rec)
	if code != errcode.ProxyTestFailed {
		t.Fatalf("code = %d, want %d (proxy_test_failed)", code, errcode.ProxyTestFailed)
	}
	if data["detail"] != "timeout" {
		t.Fatalf("data.detail = %v, want timeout", data["detail"])
	}
}

func TestProxyTest_MalformedBody(t *testing.T) {
	t.Parallel()
	srv := proxyTestServer(fakeProxyTester{err: nil})

	for _, body := range []string{
		`{"proxy_url":`,
		`{"proxy_url":null}`,
		`{}`,
		`{"proxy_url":"x","extra":1}`,
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/admin/settings/proxy/test",
			strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		srv.ServeHTTP(rec, req)
		if code, _ := decodeProxyTestEnvelope(t, rec); code != errcode.MalformedBody {
			t.Fatalf("body %q: code = %d, want %d", body, code, errcode.MalformedBody)
		}
	}
}

func TestProxyTest_EmptyURLIsInvalid(t *testing.T) {
	t.Parallel()
	srv := proxyTestServer(fakeProxyTester{err: nil})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/settings/proxy/test",
		strings.NewReader(`{"proxy_url":""}`))
	req.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(rec, req)

	if code, _ := decodeProxyTestEnvelope(t, rec); code != errcode.InvalidProxyURL {
		t.Fatalf("code = %d, want %d (invalid_proxy_url)", code, errcode.InvalidProxyURL)
	}
}

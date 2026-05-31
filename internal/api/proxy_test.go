package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/user/one-llm-router/internal/config"
	"github.com/user/one-llm-router/internal/core"
	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/oauth"
	"github.com/user/one-llm-router/internal/provider/openai"
	"github.com/user/one-llm-router/internal/store"
)

func setupProxyTest(t *testing.T, upstream *httptest.Server) (*ProxyHandler, *store.Store) {
	t.Helper()
	s, err := store.New("sqlite3", ":memory:", 1, 1)
	require.NoError(t, err)
	require.NoError(t, s.Migrate("sqlite3"))

	engine := s.Engine()
	accountRepo := store.NewAccountRepo(engine)
	recordRepo := store.NewRequestRecordRepo(engine)

	acct := &domain.UpstreamAccount{
		Name:     "test-account",
		Provider: "openai",
		APIKey:   "sk-test",
		BaseURL:  &upstream.URL,
		Status:   domain.AccountStatusActive,
	}
	require.NoError(t, accountRepo.Create(context.Background(), acct))

	selector := core.NewAccountSelector(accountRepo, core.NewConsistentHashRouter())
	recorder := core.NewRequestRecorder(recordRepo, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	recorder.Start()
	t.Cleanup(func() { _ = recorder.Close(context.Background()); _ = s.Close() })

	client := openai.NewClient(5_000_000_000) // 5s

	handler := NewProxyHandler(selector, recorder, client, false, 0, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	return handler, s
}

type proxyHarness struct {
	handler  *ProxyHandler
	store    *store.Store
	repo     *store.AccountRepo
	selector *core.AccountSelector
	client   *openai.Client
}

type failingProxyAccountRepo struct{}

func (failingProxyAccountRepo) Create(context.Context, *domain.UpstreamAccount) error {
	return errors.New("not used")
}

func (failingProxyAccountRepo) GetByID(context.Context, int64) (*domain.UpstreamAccount, error) {
	return nil, errors.New("not used")
}

func (failingProxyAccountRepo) List(context.Context, []string) ([]domain.UpstreamAccount, error) {
	return nil, errors.New("not used")
}

func (failingProxyAccountRepo) ListActive(context.Context) ([]domain.UpstreamAccount, error) {
	return nil, errors.New("db unavailable")
}

func (failingProxyAccountRepo) UpdateStatus(context.Context, int64, string) error {
	return errors.New("not used")
}

func newProxyHarness(t *testing.T, clientTimeout time.Duration) *proxyHarness {
	t.Helper()

	s, err := store.New("sqlite3", ":memory:", 1, 1)
	require.NoError(t, err)
	require.NoError(t, s.Migrate("sqlite3"))

	engine := s.Engine()
	accountRepo := store.NewAccountRepo(engine)
	recordRepo := store.NewRequestRecordRepo(engine)

	selector := core.NewAccountSelector(accountRepo, core.NewConsistentHashRouter())
	recorder := core.NewRequestRecorder(recordRepo, slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})))
	recorder.Start()
	t.Cleanup(func() {
		_ = recorder.Close(context.Background())
		_ = s.Close()
	})

	client := openai.NewClient(clientTimeout)
	handler := NewProxyHandler(selector, recorder, client, false, 0, slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})))
	return &proxyHarness{
		handler:  handler,
		store:    s,
		repo:     accountRepo,
		selector: selector,
		client:   client,
	}
}

func insertOAuthProxyAccount(
	t *testing.T,
	repo *store.AccountRepo,
	upstreamURL string,
	now time.Time,
	accessToken string,
	refreshToken string,
	accessExpiresAt time.Time,
) *domain.UpstreamAccount {
	t.Helper()

	acct := &domain.UpstreamAccount{
		Name:             "oauth-" + accessToken,
		Provider:         domain.ProviderOpenAI,
		BaseURL:          &upstreamURL,
		Status:           domain.AccountStatusActive,
		AuthMethod:       domain.AuthMethodOAuthBrowser,
		AccessToken:      []byte(accessToken),
		RefreshToken:     []byte(refreshToken),
		IDToken:          mustJWT(t, map[string]any{"email": accessToken + "@example.com"}),
		LastRefresh:      timePointer(now.UTC()),
		AccessExpiresAt:  timePointer(accessExpiresAt.UTC()),
		Email:            stringPointer(accessToken + "@example.com"),
		PlanType:         stringPointer("chatgpt-plus"),
		ChatGPTAccountID: stringPointer("acct-" + accessToken),
	}
	id, err := repo.InsertUpstreamAccount(context.Background(), acct)
	require.NoError(t, err)
	acct.ID = id
	return acct
}

func mustMarshalJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return string(data)
}

type proxyRefreshProvider struct {
	mu           sync.Mutex
	idx          int
	responses    []proxyRefreshResponse
	refreshGate  <-chan struct{}
	refreshCalls atomic.Int32
}

type proxyRefreshResponse struct {
	tokens oauth.Tokens
	err    error
}

func (*proxyRefreshProvider) BuildAuthorizeURL(string, string) (string, error) {
	return "", errors.New("not used in proxy refresh tests")
}

func (*proxyRefreshProvider) ExchangeCode(context.Context, string, string) (oauth.Tokens, error) {
	return oauth.Tokens{}, errors.New("not used in proxy refresh tests")
}

func (p *proxyRefreshProvider) Refresh(_ context.Context, _ []byte) (oauth.Tokens, error) {
	p.refreshCalls.Add(1)
	if p.refreshGate != nil {
		<-p.refreshGate
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.idx >= len(p.responses) {
		return oauth.Tokens{}, errors.New("unexpected refresh call")
	}
	response := p.responses[p.idx]
	p.idx++
	if response.err != nil {
		return oauth.Tokens{}, response.err
	}
	return response.tokens, nil
}

func (*proxyRefreshProvider) RequestDeviceCode(context.Context) (oauth.DeviceCode, error) {
	return oauth.DeviceCode{}, errors.New("not used in proxy refresh tests")
}

func (*proxyRefreshProvider) PollDeviceCode(context.Context, string, string) (oauth.Tokens, error) {
	return oauth.Tokens{}, errors.New("not used in proxy refresh tests")
}

func mustJWT(t *testing.T, payload map[string]any) []byte {
	t.Helper()

	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	bodyBytes, err := json.Marshal(payload)
	require.NoError(t, err)
	body := base64.RawURLEncoding.EncodeToString(bodyBytes)
	return []byte(header + "." + body + ".sig")
}

func timePointer(v time.Time) *time.Time {
	return &v
}

func stringPointer(v string) *string {
	return &v
}

func TestClassifySSEForwardFailure(t *testing.T) {
	t.Run("context canceled records cancelled without invalid upstream code", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		outcome, errCode := classifySSEForwardFailure(ctx, domain.OutcomeSuccess, context.Canceled)

		assert.Equal(t, domain.OutcomeCancelled, outcome)
		assert.Nil(t, errCode)
	})

	t.Run("read failure records invalid upstream response", func(t *testing.T) {
		outcome, errCode := classifySSEForwardFailure(context.Background(), domain.OutcomeSuccess, errors.New("bad SSE"))

		assert.Equal(t, domain.OutcomeRouterError, outcome)
		require.NotNil(t, errCode)
		assert.Equal(t, ErrCodeUpstreamRespInvalid, *errCode)
	})

	t.Run("upstream error outcome remains upstream error", func(t *testing.T) {
		outcome, errCode := classifySSEForwardFailure(context.Background(), domain.OutcomeUpstreamError, errors.New("bad SSE"))

		assert.Equal(t, domain.OutcomeUpstreamError, outcome)
		require.NotNil(t, errCode)
		assert.Equal(t, ErrCodeUpstreamRespInvalid, *errCode)
	})
}

func TestProxyHandler_JSONResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer sk-test", r.Header.Get("Authorization"))
		assert.Equal(t, "/v1/responses", r.URL.Path)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "resp_123",
			"usage": map[string]any{
				"input_tokens":  100,
				"output_tokens": 50,
			},
		})
	}))
	defer upstream.Close()

	handler, _ := setupProxyTest(t, upstream)

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body))
	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "resp_123", resp["id"])
}

func TestProxyHandler_ModelRenameRewritesUpstreamBodyAndRecordsMetadata(t *testing.T) {
	var upstreamBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&upstreamBody))
		assert.Equal(t, "gpt-5-mini", upstreamBody["model"])

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"resp_renamed","usage":{"input_tokens":1,"output_tokens":2}}`))
	}))
	defer upstream.Close()

	s, err := store.New("sqlite3", ":memory:", 1, 1)
	require.NoError(t, err)
	require.NoError(t, s.Migrate("sqlite3"))
	defer func() { _ = s.Close() }()

	engine := s.Engine()
	accountRepo := store.NewAccountRepo(engine)
	recordRepo := store.NewRequestRecordRepo(engine)

	url := upstream.URL
	acct := &domain.UpstreamAccount{Name: "test", Provider: "openai", APIKey: "sk-test", BaseURL: &url, Status: domain.AccountStatusActive}
	require.NoError(t, accountRepo.Create(context.Background(), acct))

	selector := core.NewAccountSelector(accountRepo, core.NewConsistentHashRouter())
	recorder := core.NewRequestRecorder(recordRepo, slog.New(slog.NewTextHandler(io.Discard, nil)))
	recorder.Start()

	client := openai.NewClient(5 * time.Second)
	handler := NewProxyHandler(selector, recorder, client, true, 0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	handler.SetModelRenameFunc(func() []config.ModelRenameRule {
		return []config.ModelRenameRule{{From: "codex-mini", To: "gpt-5-mini"}}
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"codex-mini","input":"hello"}`))
	handler.ServeHTTP(w, r)
	assert.Equal(t, http.StatusOK, w.Code)

	require.NoError(t, recorder.Close(context.Background()))
	records, err := recordRepo.Query(context.Background(), core.QueryParams{Limit: 10})
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.NotNil(t, records[0].ClientRequestBody)
	require.NotNil(t, records[0].UpstreamRequestBody)
	assert.Contains(t, *records[0].ClientRequestBody, `"model":"codex-mini"`)
	assert.Contains(t, *records[0].UpstreamRequestBody, `"model":"gpt-5-mini"`)

	require.NotNil(t, records[0].RouterMetadata)
	rawRename, ok := records[0].RouterMetadata[routerMetadataModelRenameKey].(map[string]interface{})
	require.True(t, ok, "router metadata missing model rename: %#v", records[0].RouterMetadata)
	assert.Equal(t, "codex-mini", rawRename["from"])
	assert.Equal(t, "gpt-5-mini", rawRename["to"])
	assert.Contains(t, records[0].RouterMetadata, openai.RouterMetadataBridgeKey)
}

func TestProxyHandler_SSEResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher := w.(http.Flusher)
		events := []string{
			"event: response.created\ndata: {\"type\":\"response.created\"}\n\n",
			"event: response.completed\ndata: {\"type\":\"response.completed\",\"usage\":{\"input_tokens\":100,\"output_tokens\":50}}\n\n",
		}
		for _, e := range events {
			_, _ = w.Write([]byte(e))
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	handler, _ := setupProxyTest(t, upstream)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"gpt-4o"}`))
	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "response.completed")
}

func TestProxyHandler_UpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited","type":"rate_limit_error"}}`))
	}))
	defer upstream.Close()

	handler, _ := setupProxyTest(t, upstream)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"gpt-4o"}`))
	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusTooManyRequests, w.Code)
}

func TestProxyHandler_NoCapacity(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer upstream.Close()

	s, err := store.New("sqlite3", ":memory:", 1, 1)
	require.NoError(t, err)
	require.NoError(t, s.Migrate("sqlite3"))
	defer func() { _ = s.Close() }()

	engine := s.Engine()
	accountRepo := store.NewAccountRepo(engine)
	recordRepo := store.NewRequestRecordRepo(engine)

	selector := core.NewAccountSelector(accountRepo, nil)
	recorder := core.NewRequestRecorder(recordRepo, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	recorder.Start()
	defer func() { _ = recorder.Close(context.Background()) }()

	client := openai.NewClient(5_000_000_000)
	handler := NewProxyHandler(selector, recorder, client, false, 0, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{}`))
	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	var envelope RouterErrorEnvelope
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	assert.Equal(t, ErrCodeNoAvailableAccount, envelope.Error.Code)
}

func TestProxyHandler_SessionStickiness(t *testing.T) {
	var (
		seenMu       sync.Mutex
		seenAuthKeys []string
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMu.Lock()
		seenAuthKeys = append(seenAuthKeys, r.Header.Get("Authorization"))
		seenMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"resp"}`))
	}))
	defer upstream.Close()

	s, err := store.New("sqlite3", ":memory:", 1, 1)
	require.NoError(t, err)
	require.NoError(t, s.Migrate("sqlite3"))
	defer func() { _ = s.Close() }()

	engine := s.Engine()
	accountRepo := store.NewAccountRepo(engine)
	recordRepo := store.NewRequestRecordRepo(engine)

	url := upstream.URL
	for _, name := range []string{"acct-1", "acct-2", "acct-3"} {
		acct := &domain.UpstreamAccount{Name: name, Provider: "openai", APIKey: "sk-" + name, BaseURL: &url, Status: domain.AccountStatusActive}
		require.NoError(t, accountRepo.Create(context.Background(), acct))
	}

	selector := core.NewAccountSelector(accountRepo, core.NewConsistentHashRouter())
	recorder := core.NewRequestRecorder(recordRepo, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	recorder.Start()
	defer func() { _ = recorder.Close(context.Background()) }()

	client := openai.NewClient(5_000_000_000)
	handler := NewProxyHandler(selector, recorder, client, false, 0, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	const iterations = 5
	for range iterations {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{}`))
		r.Header.Set("x-codex-session-id", "stable-session")
		handler.ServeHTTP(w, r)
		require.Equal(t, http.StatusOK, w.Code)
	}

	seenMu.Lock()
	defer seenMu.Unlock()
	require.Len(t, seenAuthKeys, iterations, "upstream should receive exactly %d requests", iterations)
	first := seenAuthKeys[0]
	for i, v := range seenAuthKeys {
		assert.Equal(t, first, v, "request %d went to a different account (stickiness broken): first=%q got=%q", i, first, v)
	}
}

func TestForwardSSE_TrailingDataWithoutBlankLine(t *testing.T) {
	sseStream := "data: {\"type\":\"response.completed\",\"usage\":{\"input_tokens\":42,\"output_tokens\":7}}"

	w := httptest.NewRecorder()
	result := ForwardSSE(w, strings.NewReader(sseStream), false)

	require.NotNil(t, result.TokenUsage, "should extract usage even without trailing blank line")
	assert.Equal(t, 42, result.TokenUsage["input"])
}

func TestProxyHandler_BodyLogging(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"resp_123","access_token":"secret-access-token","usage":{"input_tokens":10,"output_tokens":5}}`))
	}))
	defer upstream.Close()

	s, err := store.New("sqlite3", ":memory:", 1, 1)
	require.NoError(t, err)
	require.NoError(t, s.Migrate("sqlite3"))
	defer func() { _ = s.Close() }()

	engine := s.Engine()
	accountRepo := store.NewAccountRepo(engine)
	recordRepo := store.NewRequestRecordRepo(engine)

	url := upstream.URL
	acct := &domain.UpstreamAccount{Name: "test", Provider: "openai", APIKey: "sk-test", BaseURL: &url, Status: domain.AccountStatusActive}
	require.NoError(t, accountRepo.Create(context.Background(), acct))

	selector := core.NewAccountSelector(accountRepo, core.NewConsistentHashRouter())
	recorder := core.NewRequestRecorder(recordRepo, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	recorder.Start()
	defer func() { _ = recorder.Close(context.Background()) }()

	client := openai.NewClient(5_000_000_000)
	handler := NewProxyHandler(selector, recorder, client, true, 0, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"gpt-4o","input":"hello sk-live-secret-123456"}`))
	r.RemoteAddr = "203.0.113.55:54321"
	r.Header.Set("X-Forwarded-For", "198.51.100.99")
	handler.ServeHTTP(w, r)
	assert.Equal(t, http.StatusOK, w.Code)

	require.NoError(t, recorder.Close(context.Background()))
	records, err := recordRepo.Query(context.Background(), core.QueryParams{Limit: 10})
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.NotNil(t, records[0].ClientRequestBody)
	assert.Equal(t, "203.0.113.55", records[0].ClientIP)
	require.NotNil(t, records[0].UpstreamRequestBody)
	require.NotNil(t, records[0].UpstreamResponseBody)
	assert.NotContains(t, *records[0].ClientRequestBody, "sk-live-secret")
	assert.Contains(t, *records[0].ClientRequestBody, "[redacted]")
	assert.NotContains(t, *records[0].UpstreamRequestBody, "sk-live-secret")
	assert.Contains(t, *records[0].UpstreamRequestBody, "[redacted]")
	assert.NotContains(t, *records[0].UpstreamResponseBody, "secret-access-token")
	assert.Contains(t, *records[0].UpstreamResponseBody, `"access_token":"[redacted]"`)
}

func TestCapturedBodyStringRedactsBeforeTruncating(t *testing.T) {
	body := []byte(`{"input":"sk-live-secret-123456","padding":"` + strings.Repeat("x", bodyCaptureMaxBytes+128) + `"}`)

	got := capturedBodyString(body)

	assert.LessOrEqual(t, len(got), bodyCaptureMaxBytes+len(capturedBodyTruncatedMarker))
	assert.Contains(t, got, capturedBodyTruncatedMarker)
	assert.NotContains(t, got, "sk-live-secret")
	assert.Contains(t, got, "[redacted]")
}

func TestProxyHandler_BodyTooLarge(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	handler, _ := setupProxyTest(t, upstream)

	largeBody := bytes.Repeat([]byte("a"), 33<<20)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(largeBody))
	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}

func TestProxyHandler_UpstreamTimeout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(5 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	s, err := store.New("sqlite3", ":memory:", 1, 1)
	require.NoError(t, err)
	require.NoError(t, s.Migrate("sqlite3"))
	defer func() { _ = s.Close() }()

	engine := s.Engine()
	accountRepo := store.NewAccountRepo(engine)
	recordRepo := store.NewRequestRecordRepo(engine)

	url := upstream.URL
	acct := &domain.UpstreamAccount{Name: "test", Provider: "openai", APIKey: "sk-test", BaseURL: &url, Status: domain.AccountStatusActive}
	require.NoError(t, accountRepo.Create(context.Background(), acct))

	selector := core.NewAccountSelector(accountRepo, core.NewConsistentHashRouter())
	recorder := core.NewRequestRecorder(recordRepo, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	recorder.Start()
	defer func() { _ = recorder.Close(context.Background()) }()

	client := openai.NewClient(100_000_000) // 100ms
	handler := NewProxyHandler(selector, recorder, client, false, 0, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"gpt-4o"}`))
	handler.ServeHTTP(w, r)
	assert.Equal(t, http.StatusGatewayTimeout, w.Code)
}

func TestProxyHandler_UpstreamConnectFailed(t *testing.T) {
	s, err := store.New("sqlite3", ":memory:", 1, 1)
	require.NoError(t, err)
	require.NoError(t, s.Migrate("sqlite3"))
	defer func() { _ = s.Close() }()

	engine := s.Engine()
	accountRepo := store.NewAccountRepo(engine)
	recordRepo := store.NewRequestRecordRepo(engine)

	badURL := "http://127.0.0.1:1"
	acct := &domain.UpstreamAccount{Name: "test", Provider: "openai", APIKey: "sk-test", BaseURL: &badURL, Status: domain.AccountStatusActive}
	require.NoError(t, accountRepo.Create(context.Background(), acct))

	selector := core.NewAccountSelector(accountRepo, core.NewConsistentHashRouter())
	recorder := core.NewRequestRecorder(recordRepo, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	recorder.Start()
	defer func() { _ = recorder.Close(context.Background()) }()

	client := openai.NewClient(2_000_000_000) // 2s
	handler := NewProxyHandler(selector, recorder, client, false, 0, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"gpt-4o"}`))
	handler.ServeHTTP(w, r)
	assert.Equal(t, http.StatusBadGateway, w.Code)
}

func TestProxyHandler_UpstreamConnectFailedRecordsRequestBodiesWhenEnabled(t *testing.T) {
	s, err := store.New("sqlite3", ":memory:", 1, 1)
	require.NoError(t, err)
	require.NoError(t, s.Migrate("sqlite3"))
	defer func() { _ = s.Close() }()

	engine := s.Engine()
	accountRepo := store.NewAccountRepo(engine)
	recordRepo := store.NewRequestRecordRepo(engine)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	badURL := "http://" + listener.Addr().String()
	require.NoError(t, listener.Close())

	acct := &domain.UpstreamAccount{
		Name:     "connect-fail",
		Provider: "openai",
		APIKey:   "sk-test",
		BaseURL:  &badURL,
		Status:   domain.AccountStatusActive,
	}
	require.NoError(t, accountRepo.Create(context.Background(), acct))

	selector := core.NewAccountSelector(accountRepo, core.NewConsistentHashRouter())
	recorder := core.NewRequestRecorder(recordRepo, slog.New(slog.NewTextHandler(io.Discard, nil)))
	recorder.Start()
	defer func() { _ = recorder.Close(context.Background()) }()

	client := openai.NewClient(2 * time.Second)
	handler := NewProxyHandler(selector, recorder, client, true, 0, slog.New(slog.NewTextHandler(io.Discard, nil)))

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"gpt-4o","input":"hello sk-live-secret-123456"}`))
	handler.ServeHTTP(w, r)
	require.Equal(t, http.StatusBadGateway, w.Code)

	require.NoError(t, recorder.Close(context.Background()))
	records, err := recordRepo.Query(context.Background(), core.QueryParams{Limit: 10})
	require.NoError(t, err)
	require.Len(t, records, 1)
	rec := records[0]
	require.NotNil(t, rec.ClientRequestBody)
	require.NotNil(t, rec.UpstreamRequestBody)
	assert.Contains(t, *rec.ClientRequestBody, `"model":"gpt-4o"`)
	assert.Contains(t, *rec.UpstreamRequestBody, `"model":"gpt-4o"`)
	assert.NotContains(t, *rec.ClientRequestBody, "sk-live-secret")
	assert.NotContains(t, *rec.UpstreamRequestBody, "sk-live-secret")
	assert.Contains(t, *rec.ClientRequestBody, "[redacted]")
	assert.Contains(t, *rec.UpstreamRequestBody, "[redacted]")
	require.NotNil(t, rec.Model)
	assert.Equal(t, "gpt-4o", *rec.Model)
	require.NotNil(t, rec.ErrorCode)
	assert.Equal(t, ErrCodeUpstreamConnFailed, *rec.ErrorCode)
}

func TestProxyHandler_NoCapacity_ErrorEnvelope(t *testing.T) {
	s, err := store.New("sqlite3", ":memory:", 1, 1)
	require.NoError(t, err)
	require.NoError(t, s.Migrate("sqlite3"))
	defer func() { _ = s.Close() }()

	engine := s.Engine()
	accountRepo := store.NewAccountRepo(engine)
	recordRepo := store.NewRequestRecordRepo(engine)

	selector := core.NewAccountSelector(accountRepo, core.NewConsistentHashRouter())
	recorder := core.NewRequestRecorder(recordRepo, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	recorder.Start()
	defer func() { _ = recorder.Close(context.Background()) }()

	client := openai.NewClient(5_000_000_000)
	handler := NewProxyHandler(selector, recorder, client, false, 0, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{}`))
	r.RemoteAddr = "203.0.113.56:54321"
	r.Header.Set("X-Forwarded-For", "198.51.100.99")
	handler.ServeHTTP(w, r)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	errMap := resp["error"].(map[string]any)
	assert.Equal(t, "router_error", errMap["type"])
	assert.Equal(t, "no_available_account", errMap["code"])

	require.NoError(t, recorder.Close(context.Background()))
	records, err := recordRepo.Query(context.Background(), core.QueryParams{Limit: 10})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, "203.0.113.56", records[0].ClientIP)
}

func TestProxyHandler_NilBody(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"gpt-4o-mini","object":"model","created":1710000000,"owned_by":"openai"}]}`))
	}))
	defer upstream.Close()

	handler, _ := setupProxyTest(t, upstream)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/models", nil)
	r.Body = nil
	handler.ServeHTTP(w, r)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestProxyHandler_UpstreamBodyReadError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "99999")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"partial`))
		if hj, ok := w.(http.Hijacker); ok {
			conn, _, _ := hj.Hijack()
			_ = conn.Close()
		}
	}))
	defer upstream.Close()

	handler, st := setupProxyTest(t, upstream)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"gpt-4o"}`))
	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusBadGateway, w.Code)
	assert.Contains(t, w.Body.String(), "failed to read upstream response")

	recordRepo := store.NewRequestRecordRepo(st.Engine())
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		records, err := recordRepo.Query(context.Background(), core.QueryParams{Limit: 10})
		if !assert.NoError(c, err) || !assert.Len(c, records, 1) {
			return
		}
		assert.Equal(c, http.StatusBadGateway, records[0].StatusCode)
		assert.Equal(c, domain.OutcomeRouterError, records[0].Outcome)
	}, 2*time.Second, 20*time.Millisecond)
}

func TestProxyHandler_ErrorWithSessionKey(t *testing.T) {
	s, err := store.New("sqlite3", ":memory:", 1, 1)
	require.NoError(t, err)
	require.NoError(t, s.Migrate("sqlite3"))
	defer func() { _ = s.Close() }()

	engine := s.Engine()
	accountRepo := store.NewAccountRepo(engine)
	recordRepo := store.NewRequestRecordRepo(engine)

	selector := core.NewAccountSelector(accountRepo, core.NewConsistentHashRouter())
	recorder := core.NewRequestRecorder(recordRepo, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	recorder.Start()
	defer func() { _ = recorder.Close(context.Background()) }()

	client := openai.NewClient(5_000_000_000)
	handler := NewProxyHandler(selector, recorder, client, false, 0, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{}`))
	r.Header.Set("x-codex-session-id", "my-session")
	handler.ServeHTTP(w, r)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestProxyHandler_HopByHopHeadersStripped(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Connection", "keep-alive, X-Upstream-Hop")
		w.Header().Set("Transfer-Encoding", "chunked")
		w.Header().Set("Trailer", "Expires")
		w.Header().Set("X-Upstream-Hop", "drop-me")
		w.Header().Set("Set-Cookie", "provider_session=secret")
		w.Header().Set("X-Custom", "preserved")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"resp_1"}`))
	}))
	defer upstream.Close()

	handler, _ := setupProxyTest(t, upstream)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/responses/resp_1", nil)
	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Header().Get("Connection"))
	assert.Empty(t, w.Header().Get("Transfer-Encoding"))
	assert.Empty(t, w.Header().Get("Trailer"))
	assert.Empty(t, w.Header().Get("X-Upstream-Hop"))
	assert.Empty(t, w.Header().Get("Set-Cookie"))
	assert.Equal(t, "preserved", w.Header().Get("X-Custom"))
}

// TestProxyHandler_UpstreamRequestIDStripped locks in F008: an
// upstream provider that echoes its own X-Request-Id (OpenAI does
// this) must NOT leak into the router's outbound response. The
// RequestIDMiddleware owns the outbound header; the proxy must drop
// the upstream value during header copy. Breaking this contract
// misleads operator logs ("client sees id foo, router logs show
// bar") and can surface upstream account identifiers to clients.
func TestProxyHandler_UpstreamRequestIDStripped(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", "upstream-id-should-be-dropped")
		w.Header().Set("Request-Id", "legacy-upstream-id-should-be-dropped")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"resp_1"}`))
	}))
	defer upstream.Close()

	handler, _ := setupProxyTest(t, upstream)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/responses/resp_1", nil)
	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	if got := w.Header().Get("X-Request-Id"); got == "upstream-id-should-be-dropped" {
		t.Fatalf("X-Request-Id leaked from upstream: %q", got)
	}
	if got := w.Header().Get("Request-Id"); got == "legacy-upstream-id-should-be-dropped" {
		t.Fatalf("Request-Id leaked from upstream: %q", got)
	}
}

func TestProxyHandler_ModelsUnionMergesAPIKeyAndOAuthAccounts(t *testing.T) {
	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	var apiHits atomic.Int32
	apiUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiHits.Add(1)
		assert.Equal(t, "/v1/models", r.URL.Path)
		assert.Equal(t, "Bearer sk-api", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[
			{"id":"gpt-5.4","object":"model","created":1710000000,"owned_by":"openai"},
			{"id":"shared-model","object":"model","created":1710000001,"owned_by":"api-owner"}
		]}`))
	}))
	t.Cleanup(apiUpstream.Close)

	var oauthHits atomic.Int32
	oauthUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		oauthHits.Add(1)
		assert.Equal(t, "/codex/models", r.URL.Path)
		assert.Equal(t, "Bearer oauth-access", r.Header.Get("Authorization"))
		assert.Equal(t, "acct-oauth-access", r.Header.Get("chatgpt-account-id"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[
			{"slug":"shared-model","created":1720000000,"owned_by":"oauth-owner","display_name":"Shared OAuth"},
			{"slug":"gpt-5.4-codex","created":1720000001,"owned_by":"codex-owner","display_name":"Codex"}
		]}`))
	}))
	t.Cleanup(oauthUpstream.Close)

	h := newProxyHarness(t, 5*time.Second)
	apiBaseURL := apiUpstream.URL
	require.NoError(t, h.repo.Create(context.Background(), &domain.UpstreamAccount{
		Name:     "api",
		Provider: domain.ProviderOpenAI,
		APIKey:   "sk-api",
		BaseURL:  &apiBaseURL,
		Status:   domain.AccountStatusActive,
	}))
	h.client.SetCodexBackendBaseURLForTest(oauthUpstream.URL)
	insertOAuthProxyAccount(t, h.repo, oauthUpstream.URL, now, "oauth-access", "oauth-refresh", now.Add(2*time.Hour))
	coord := oauth.NewCoordinatorWithClock(oauth.NewFakeClock(now), &proxyRefreshProvider{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	coord.SetAccountStore(h.repo)
	h.selector.PreForward = coord.RefreshIfStale

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, int32(1), apiHits.Load())
	assert.Equal(t, int32(1), oauthHits.Load())
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")

	var body struct {
		Object string `json:"object"`
		Data   []struct {
			ID      string         `json:"id"`
			Object  string         `json:"object"`
			Created float64        `json:"created"`
			OwnedBy string         `json:"owned_by"`
			Meta    map[string]any `json:"metadata"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "list", body.Object)
	require.Len(t, body.Data, 3)
	assert.Equal(t, "gpt-5.4", body.Data[0].ID)
	assert.Equal(t, "shared-model", body.Data[1].ID)
	assert.Equal(t, "api-owner", body.Data[1].OwnedBy, "first account wins duplicate metadata")
	assert.Equal(t, "gpt-5.4-codex", body.Data[2].ID)
	assert.Equal(t, "codex-owner", body.Data[2].OwnedBy)
	require.NotNil(t, body.Data[2].Meta)
	assert.Equal(t, "Codex", body.Data[2].Meta["display_name"])

	recordRepo := store.NewRequestRecordRepo(h.store.Engine())
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		records, err := recordRepo.Query(context.Background(), core.QueryParams{Limit: 10})
		require.NoError(c, err)
		require.Len(c, records, 1)
		assert.Nil(c, records[0].UpstreamAccountID)
		assert.Equal(c, domain.OutcomeSuccess, records[0].Outcome)
		assert.Equal(c, http.StatusOK, records[0].StatusCode)
		raw, ok := records[0].RouterMetadata["models_union"]
		if !assert.True(c, ok, "models_union metadata missing") {
			return
		}
		meta, ok := raw.(map[string]any)
		if !assert.True(c, ok, "models_union metadata must be object") {
			return
		}
		assert.Equal(c, float64(2), meta["account_count"])
		assert.NotContains(c, records[0].RouterMetadata, openai.RouterMetadataBridgeKey)
	}, time.Second, 10*time.Millisecond)
}

func TestProxyHandler_ModelsUnionFailsOnInvalidProviderList(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"not-list","data":[]}`))
	}))
	t.Cleanup(upstream.Close)

	handler, _ := setupProxyTest(t, upstream)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadGateway, rec.Code, rec.Body.String())
	var envelope RouterErrorEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	assert.Equal(t, ErrCodeUpstreamRespInvalid, envelope.Error.Code)
}

func TestProxyHandler_ModelsUnionForcesIdentityEncoding(t *testing.T) {
	var upstreamAcceptEncoding atomic.Value
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamAcceptEncoding.Store(r.Header.Get("Accept-Encoding"))
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			w.Header().Set("Content-Encoding", "gzip")
			gz := gzip.NewWriter(w)
			_, _ = gz.Write([]byte(`{"object":"list","data":[{"id":"gpt-gzip","object":"model"}]}`))
			_ = gz.Close()
			return
		}
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"gpt-identity","object":"model"}]}`))
	}))
	t.Cleanup(upstream.Close)

	handler, _ := setupProxyTest(t, upstream)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "identity", upstreamAcceptEncoding.Load())
	var body openAIModelsListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Data, 1)
	assert.Equal(t, "gpt-identity", body.Data[0]["id"])
}

func TestProxyHandler_ModelsUnionPreservesProviderErrorAndRecordsFailingAccount(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"billing_hard_limit_reached","message":"quota","type":"insufficient_quota"}}`))
	}))
	t.Cleanup(upstream.Close)

	handler, s := setupProxyTest(t, upstream)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{"error":{"code":"billing_hard_limit_reached","message":"quota","type":"insufficient_quota"}}`, rec.Body.String())

	recordRepo := store.NewRequestRecordRepo(s.Engine())
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		records, err := recordRepo.Query(context.Background(), core.QueryParams{Limit: 10})
		require.NoError(c, err)
		require.Len(c, records, 1)
		require.NotNil(c, records[0].UpstreamAccountID)
		assert.Equal(c, int64(1), *records[0].UpstreamAccountID)
		assert.Equal(c, domain.OutcomeUpstreamError, records[0].Outcome)
		assert.Equal(c, http.StatusForbidden, records[0].StatusCode)
		require.NotNil(c, records[0].ErrorCode)
		assert.Equal(c, "billing_hard_limit_reached", *records[0].ErrorCode)
		assert.Contains(c, records[0].RouterMetadata, "models_union")
	}, time.Second, 10*time.Millisecond)
}

func TestRefreshIntegration_FreshOAuthUsesCurrentAccessToken(t *testing.T) {
	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer fresh-access", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"fresh"}`))
	}))
	defer upstream.Close()

	h := newProxyHarness(t, 5*time.Second)
	h.client.SetCodexBackendBaseURLForTest(upstream.URL)
	insertOAuthProxyAccount(t, h.repo, upstream.URL, now, "fresh-access", "fresh-refresh", now.Add(2*time.Hour))

	provider := &proxyRefreshProvider{}
	coord := oauth.NewCoordinatorWithClock(oauth.NewFakeClock(now), provider, slog.New(slog.NewTextHandler(io.Discard, nil)))
	coord.SetAccountStore(h.repo)
	h.selector.PreForward = coord.RefreshIfStale

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-4o-mini","input":"ping"}`))
	h.handler.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, int32(0), provider.refreshCalls.Load())
}

func TestProxyHandler_OAuthNonStreamingResponsesReturnsJSONAndRecordsCollectedJSONWhenBodyLoggingEnabled(t *testing.T) {
	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	var upstreamRequestBody []byte

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/codex/responses", r.URL.Path)
		require.Equal(t, "acct-oauth-access", r.Header.Get("chatgpt-account-id"))
		require.Equal(t, "Bearer oauth-access", r.Header.Get("Authorization"))
		var err error
		upstreamRequestBody, err = io.ReadAll(r.Body)
		require.NoError(t, err)

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(strings.Join([]string{
			`event: response.output_text.delta`,
			`data: {"type":"response.output_text.delta","delta":"OK"}`,
			``,
			`event: response.completed`,
			`data: {"type":"response.completed","response":{"id":"resp_oauth","access_token":"secret-access-token","usage":{"input_tokens":3,"output_tokens":1}}}`,
			``,
		}, "\n")))
	}))
	defer upstream.Close()

	h := newProxyHarness(t, 5*time.Second)
	h.client.SetCodexBackendBaseURLForTest(upstream.URL)
	insertOAuthProxyAccount(t, h.repo, upstream.URL, now, "oauth-access", "oauth-refresh", now.Add(2*time.Hour))
	provider := &proxyRefreshProvider{}
	coord := oauth.NewCoordinatorWithClock(oauth.NewFakeClock(now), provider, slog.New(slog.NewTextHandler(io.Discard, nil)))
	coord.SetAccountStore(h.repo)
	h.selector.PreForward = coord.RefreshIfStale
	h.handler.SetBodyLogFunc(func() (bool, bool, bool) {
		return true, true, true
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.4-mini","input":"hello sk-oauth-secret-123456","stream":false,"max_output_tokens":128}`))
	h.handler.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")
	assert.JSONEq(t, `{"id":"resp_oauth","output_text":"OK","access_token":"secret-access-token","usage":{"input_tokens":3,"output_tokens":1}}`, w.Body.String())
	assert.NotContains(t, string(upstreamRequestBody), "max_output_tokens")
	assert.Contains(t, string(upstreamRequestBody), `"stream":true`)

	recordRepo := store.NewRequestRecordRepo(h.store.Engine())
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		records, err := recordRepo.Query(context.Background(), core.QueryParams{Limit: 10})
		if !assert.NoError(c, err) || !assert.Len(c, records, 1) {
			return
		}
		rec := records[0]
		if !assert.NotNil(c, rec.ClientRequestBody) ||
			!assert.NotNil(c, rec.UpstreamRequestBody) ||
			!assert.NotNil(c, rec.UpstreamResponseBody) {
			return
		}
		assert.Contains(c, *rec.ClientRequestBody, `"max_output_tokens":128`)
		assert.Contains(c, *rec.ClientRequestBody, `"stream":false`)
		assert.Contains(c, *rec.UpstreamRequestBody, `"stream":true`)
		assert.NotContains(c, *rec.UpstreamRequestBody, "max_output_tokens")
		assert.JSONEq(c, `{"id":"resp_oauth","output_text":"OK","access_token":"[redacted]","usage":{"input_tokens":3,"output_tokens":1}}`, *rec.UpstreamResponseBody)
		assert.NotContains(c, *rec.UpstreamResponseBody, "event: response.completed")
		assert.NotContains(c, *rec.ClientRequestBody, "sk-oauth-secret")
		assert.NotContains(c, *rec.UpstreamRequestBody, "sk-oauth-secret")
		assert.NotContains(c, *rec.UpstreamResponseBody, "secret-access-token")
		assert.Contains(c, *rec.UpstreamResponseBody, `"access_token":"[redacted]"`)
	}, 2*time.Second, 20*time.Millisecond)
}

func TestProxyHandler_OAuthOmittedStreamResponsesReturnsJSON(t *testing.T) {
	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	var upstreamRequestBody []byte

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/codex/responses", r.URL.Path)
		var err error
		upstreamRequestBody, err = io.ReadAll(r.Body)
		require.NoError(t, err)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`event: response.output_text.delta`,
			`data: {"type":"response.output_text.delta","delta":"OK"}`,
			``,
			`event: response.completed`,
			`data: {"type":"response.completed","response":{"id":"resp_omitted"}}`,
			``,
		}, "\n")))
	}))
	defer upstream.Close()

	h := newProxyHarness(t, 5*time.Second)
	h.client.SetCodexBackendBaseURLForTest(upstream.URL)
	insertOAuthProxyAccount(t, h.repo, upstream.URL, now, "oauth-access", "oauth-refresh", now.Add(2*time.Hour))
	coord := oauth.NewCoordinatorWithClock(oauth.NewFakeClock(now), &proxyRefreshProvider{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	coord.SetAccountStore(h.repo)
	h.selector.PreForward = coord.RefreshIfStale

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.4-mini","input":"hello"}`))
	h.handler.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")
	assert.JSONEq(t, `{"id":"resp_omitted","output_text":"OK"}`, w.Body.String())
	assert.Contains(t, string(upstreamRequestBody), `"stream":true`)
}

func TestProxyHandler_OAuthNonStreamingCollectedJSONRecordIsNotTruncated(t *testing.T) {
	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	largeText := strings.Repeat("x", bodyCaptureMaxBytes+128)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`event: response.output_text.done`,
			`data: ` + mustMarshalJSON(t, map[string]any{
				"type": "response.output_text.done",
				"text": largeText,
			}),
			``,
			`event: response.completed`,
			`data: {"type":"response.completed","response":{"id":"resp_large"}}`,
			``,
		}, "\n")))
	}))
	defer upstream.Close()

	h := newProxyHarness(t, 5*time.Second)
	h.client.SetCodexBackendBaseURLForTest(upstream.URL)
	insertOAuthProxyAccount(t, h.repo, upstream.URL, now, "oauth-access", "oauth-refresh", now.Add(2*time.Hour))
	coord := oauth.NewCoordinatorWithClock(oauth.NewFakeClock(now), &proxyRefreshProvider{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	coord.SetAccountStore(h.repo)
	h.selector.PreForward = coord.RefreshIfStale
	h.handler.SetBodyLogFunc(func() (bool, bool, bool) {
		return false, false, true
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.4-mini","input":"hello","stream":false}`))
	h.handler.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	recordRepo := store.NewRequestRecordRepo(h.store.Engine())
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		records, err := recordRepo.Query(context.Background(), core.QueryParams{Limit: 10})
		if !assert.NoError(c, err) || !assert.Len(c, records, 1) {
			return
		}
		rec := records[0]
		require.NotNil(c, rec.UpstreamResponseBody)
		assert.NotContains(c, *rec.UpstreamResponseBody, capturedBodyTruncatedMarker)
		assert.Greater(c, len(*rec.UpstreamResponseBody), bodyCaptureMaxBytes)
		assert.Contains(c, *rec.UpstreamResponseBody, largeText)
	}, 2*time.Second, 20*time.Millisecond)
}

func TestProxyHandler_OAuthStreamingResponsesReturnsSSE(t *testing.T) {
	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	var upstreamRequestBody []byte
	sseBody := strings.Join([]string{
		`event: response.output_text.delta`,
		`data: {"type":"response.output_text.delta","delta":"OK"}`,
		``,
		`event: response.completed`,
		`data: {"type":"response.completed","response":{"id":"resp_stream","usage":{"input_tokens":3,"output_tokens":1}}}`,
		``,
	}, "\n")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/codex/responses", r.URL.Path)
		require.Equal(t, "acct-oauth-access", r.Header.Get("chatgpt-account-id"))
		require.Equal(t, "Bearer oauth-access", r.Header.Get("Authorization"))
		require.Equal(t, "text/event-stream", r.Header.Get("Accept"))
		require.Equal(t, "identity", r.Header.Get("Accept-Encoding"))
		var err error
		upstreamRequestBody, err = io.ReadAll(r.Body)
		require.NoError(t, err)

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sseBody))
	}))
	defer upstream.Close()

	h := newProxyHarness(t, 5*time.Second)
	h.client.SetCodexBackendBaseURLForTest(upstream.URL)
	insertOAuthProxyAccount(t, h.repo, upstream.URL, now, "oauth-access", "oauth-refresh", now.Add(2*time.Hour))
	provider := &proxyRefreshProvider{}
	coord := oauth.NewCoordinatorWithClock(oauth.NewFakeClock(now), provider, slog.New(slog.NewTextHandler(io.Discard, nil)))
	coord.SetAccountStore(h.repo)
	h.selector.PreForward = coord.RefreshIfStale
	h.handler.SetBodyLogFunc(func() (bool, bool, bool) {
		return true, true, true
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.4-mini","input":"hello","stream":true}`))
	h.handler.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/event-stream")
	assert.Equal(t, sseBody, w.Body.String())
	assert.Contains(t, string(upstreamRequestBody), `"stream":true`)

	recordRepo := store.NewRequestRecordRepo(h.store.Engine())
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		records, err := recordRepo.Query(context.Background(), core.QueryParams{Limit: 10})
		if !assert.NoError(c, err) || !assert.Len(c, records, 1) {
			return
		}
		rec := records[0]
		assert.Equal(c, domain.ResponseModeSSE, rec.ResponseMode)
		require.NotNil(c, rec.UpstreamResponseBody)
		assert.JSONEq(c, `{"id":"resp_stream","output_text":"OK","usage":{"input_tokens":3,"output_tokens":1}}`, *rec.UpstreamResponseBody)
		assert.NotContains(c, *rec.UpstreamResponseBody, "event: response.output_text.delta")
		require.NotNil(c, rec.TTFTMs)
		assert.GreaterOrEqual(c, *rec.TTFTMs, 0)
	}, 2*time.Second, 20*time.Millisecond)
}

func TestProxyHandler_OAuthStreamingResponsesWithJSONContentTypeStillRecordsSSEAndTTFT(t *testing.T) {
	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	sseBody := strings.Join([]string{
		`event: response.output_text.delta`,
		`data: {"type":"response.output_text.delta","delta":"Here"}`,
		``,
		`event: response.completed`,
		`data: {"type":"response.completed","response":{"id":"resp_mislabel","usage":{"input_tokens":4,"output_tokens":1}}}`,
		``,
	}, "\n")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/codex/responses", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sseBody))
	}))
	defer upstream.Close()

	h := newProxyHarness(t, 5*time.Second)
	h.client.SetCodexBackendBaseURLForTest(upstream.URL)
	insertOAuthProxyAccount(t, h.repo, upstream.URL, now, "oauth-access", "oauth-refresh", now.Add(2*time.Hour))
	coord := oauth.NewCoordinatorWithClock(oauth.NewFakeClock(now), &proxyRefreshProvider{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	coord.SetAccountStore(h.repo)
	h.selector.PreForward = coord.RefreshIfStale
	h.handler.SetBodyLogFunc(func() (bool, bool, bool) {
		return true, true, true
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.4-mini","input":"hello","stream":true}`))
	h.handler.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/event-stream")
	assert.Equal(t, sseBody, w.Body.String())

	recordRepo := store.NewRequestRecordRepo(h.store.Engine())
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		records, err := recordRepo.Query(context.Background(), core.QueryParams{Limit: 10})
		if !assert.NoError(c, err) || !assert.Len(c, records, 1) {
			return
		}
		rec := records[0]
		assert.Equal(c, domain.ResponseModeSSE, rec.ResponseMode)
		require.NotNil(c, rec.UpstreamResponseBody)
		assert.JSONEq(c, `{"id":"resp_mislabel","output_text":"Here","usage":{"input_tokens":4,"output_tokens":1}}`, *rec.UpstreamResponseBody)
		assert.NotContains(c, *rec.UpstreamResponseBody, "event:")
		require.NotNil(c, rec.TTFTMs)
		assert.GreaterOrEqual(c, *rec.TTFTMs, 0)
	}, 2*time.Second, 20*time.Millisecond)
}

func TestProxyHandler_OAuthInvalidResponsesJSONReturnsInvalidRequest(t *testing.T) {
	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	var upstreamHits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	h := newProxyHarness(t, 5*time.Second)
	var logs bytes.Buffer
	h.handler.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
	h.client.SetCodexBackendBaseURLForTest(upstream.URL)
	insertOAuthProxyAccount(t, h.repo, upstream.URL, now, "oauth-access", "oauth-refresh", now.Add(2*time.Hour))
	provider := &proxyRefreshProvider{}
	coord := oauth.NewCoordinatorWithClock(oauth.NewFakeClock(now), provider, slog.New(slog.NewTextHandler(io.Discard, nil)))
	coord.SetAccountStore(h.repo)
	h.selector.PreForward = coord.RefreshIfStale
	h.handler.SetBodyLogFunc(func() (bool, bool, bool) {
		return true, true, true
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":`))
	h.handler.ServeHTTP(w, r)

	require.Equal(t, http.StatusBadRequest, w.Code)
	var envelope RouterErrorEnvelope
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	assert.Equal(t, ErrCodeInvalidRequest, envelope.Error.Code)
	assert.Equal(t, "invalid request body", envelope.Error.Message)
	assert.Equal(t, int32(0), upstreamHits.Load())
	logOutput := logs.String()
	assert.Contains(t, logOutput, "router error")
	assert.Contains(t, logOutput, "error_code=invalid_request")
	assert.Contains(t, logOutput, "reason=")
	assert.Contains(t, logOutput, "build upstream request body")
	assert.Contains(t, logOutput, "decode codex upstream request body")
	assert.Contains(t, logOutput, "unexpected end of JSON input")
	assert.NotContains(t, logOutput, "oauth-access")
	assert.NotContains(t, logOutput, "oauth-refresh")

	recordRepo := store.NewRequestRecordRepo(h.store.Engine())
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		records, err := recordRepo.Query(context.Background(), core.QueryParams{Limit: 10})
		if !assert.NoError(c, err) || !assert.Len(c, records, 1) {
			return
		}
		rec := records[0]
		assert.Equal(c, http.StatusBadRequest, rec.StatusCode)
		require.NotNil(c, rec.ErrorCode)
		assert.Equal(c, ErrCodeInvalidRequest, *rec.ErrorCode)
		assert.Nil(c, rec.UpstreamRequestBody)
		assert.Nil(c, rec.UpstreamResponseBody)
		require.NotNil(c, rec.ClientRequestBody)
		assert.Equal(c, `{"model":`, *rec.ClientRequestBody)
	}, 2*time.Second, 20*time.Millisecond)
}

func TestProxyHandler_OAuthNonStreamingTerminalFailedReturnsJSON(t *testing.T) {
	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/codex/responses", r.URL.Path)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(strings.Join([]string{
			`event: response.failed`,
			`data: {"type":"response.failed","response":{"id":"resp_failed","status":"failed","error":{"code":"rate_limit_exceeded","message":"quota"}}}`,
			``,
		}, "\n")))
	}))
	defer upstream.Close()

	h := newProxyHarness(t, 5*time.Second)
	h.client.SetCodexBackendBaseURLForTest(upstream.URL)
	insertOAuthProxyAccount(t, h.repo, upstream.URL, now, "oauth-access", "oauth-refresh", now.Add(2*time.Hour))
	coord := oauth.NewCoordinatorWithClock(oauth.NewFakeClock(now), &proxyRefreshProvider{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	coord.SetAccountStore(h.repo)
	h.selector.PreForward = coord.RefreshIfStale
	h.handler.SetBodyLogFunc(func() (bool, bool, bool) {
		return true, true, true
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.4-mini","input":"hello","stream":false}`))
	h.handler.ServeHTTP(w, r)

	require.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")
	assert.JSONEq(t, `{"id":"resp_failed","status":"failed","output_text":"","error":{"code":"rate_limit_exceeded","message":"quota"}}`, w.Body.String())

	recordRepo := store.NewRequestRecordRepo(h.store.Engine())
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		records, err := recordRepo.Query(context.Background(), core.QueryParams{Limit: 10})
		if !assert.NoError(c, err) || !assert.Len(c, records, 1) {
			return
		}
		rec := records[0]
		assert.Equal(c, domain.OutcomeUpstreamError, rec.Outcome)
		assert.Equal(c, domain.ResponseModeJSON, rec.ResponseMode)
		require.NotNil(c, rec.UpstreamResponseBody)
		assert.JSONEq(c, `{"id":"resp_failed","status":"failed","output_text":"","error":{"code":"rate_limit_exceeded","message":"quota"}}`, *rec.UpstreamResponseBody)
	}, 2*time.Second, 20*time.Millisecond)
}

func TestProxyHandler_OAuthNonStreamingMalformedSSEReturnsInvalidUpstreamResponse(t *testing.T) {
	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`event: response.output_text.delta`,
			`data: {"type":"response.output_text.delta","delta":`,
			``,
		}, "\n")))
	}))
	defer upstream.Close()

	h := newProxyHarness(t, 5*time.Second)
	h.client.SetCodexBackendBaseURLForTest(upstream.URL)
	insertOAuthProxyAccount(t, h.repo, upstream.URL, now, "oauth-access", "oauth-refresh", now.Add(2*time.Hour))
	coord := oauth.NewCoordinatorWithClock(oauth.NewFakeClock(now), &proxyRefreshProvider{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	coord.SetAccountStore(h.repo)
	h.selector.PreForward = coord.RefreshIfStale
	h.handler.SetBodyLogFunc(func() (bool, bool, bool) {
		return true, true, true
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.4-mini","input":"hello","stream":false}`))
	h.handler.ServeHTTP(w, r)

	require.Equal(t, http.StatusBadGateway, w.Code)
	var envelope RouterErrorEnvelope
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	assert.Equal(t, ErrCodeUpstreamRespInvalid, envelope.Error.Code)
	assert.Equal(t, "invalid upstream response", envelope.Error.Message)

	recordRepo := store.NewRequestRecordRepo(h.store.Engine())
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		records, err := recordRepo.Query(context.Background(), core.QueryParams{Limit: 10})
		if !assert.NoError(c, err) || !assert.Len(c, records, 1) {
			return
		}
		rec := records[0]
		assert.Equal(c, domain.OutcomeRouterError, rec.Outcome)
		require.NotNil(c, rec.ErrorCode)
		assert.Equal(c, ErrCodeUpstreamRespInvalid, *rec.ErrorCode)
		require.NotNil(c, rec.UpstreamResponseBody)
		assert.Contains(c, *rec.UpstreamResponseBody, "response.output_text.delta")
		assertRecordBridgeMetadata(c, rec, openai.BridgeMetadata{
			OpID:             openai.OpOpenAIResponsesCreate,
			BridgeID:         openai.BridgeOpenAIResponsesToCodex,
			ClientContract:   openai.ContractOpenAIV1Responses,
			UpstreamContract: openai.ContractChatGPTBackendAPICodexResponses,
			CredentialClass:  openai.CredentialClassOAuth,
		})
	}, 2*time.Second, 20*time.Millisecond)
}

func TestRefreshIntegration_APIKeyPassthroughWithHookInstalled(t *testing.T) {
	var upstreamAuth string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"api-key"}`))
	}))
	defer upstream.Close()

	h := newProxyHarness(t, 5*time.Second)
	url := upstream.URL
	require.NoError(t, h.repo.Create(context.Background(), &domain.UpstreamAccount{
		Name:       "api-key-account",
		Provider:   domain.ProviderOpenAI,
		APIKey:     "sk-live",
		BaseURL:    &url,
		Status:     domain.AccountStatusActive,
		AuthMethod: domain.AuthMethodAPIKey,
	}))

	provider := &proxyRefreshProvider{}
	coord := oauth.NewCoordinatorWithClock(
		oauth.NewFakeClock(time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)),
		provider,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	coord.SetAccountStore(h.repo)
	h.selector.PreForward = coord.RefreshIfStale

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-4o-mini","input":"ping"}`))
	h.handler.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "Bearer sk-live", upstreamAuth)
	assert.Equal(t, int32(0), provider.refreshCalls.Load())
}

func TestProxyMixedAccounts_NonResponsesPathsUseAPIKeyAccounts(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "model retrieve", method: http.MethodGet, path: "/v1/models/gpt-4o-mini"},
		{name: "stored chat messages", method: http.MethodGet, path: "/v1/chat/completions/chatcmpl_123/messages"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
			var apiHits atomic.Int32
			var oauthHits atomic.Int32

			apiUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				apiHits.Add(1)
				assert.Equal(t, tc.path, r.URL.Path)
				assert.Equal(t, "Bearer sk-platform", r.Header.Get("Authorization"))
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"object":"ok"}`))
			}))
			defer apiUpstream.Close()

			oauthUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				oauthHits.Add(1)
				w.WriteHeader(http.StatusTeapot)
			}))
			defer oauthUpstream.Close()

			h := newProxyHarness(t, 5*time.Second)
			h.client.SetCodexBackendBaseURLForTest(oauthUpstream.URL)
			insertOAuthProxyAccount(t, h.repo, oauthUpstream.URL, now, "oauth-access", "oauth-refresh", now.Add(2*time.Hour))
			apiBaseURL := apiUpstream.URL
			require.NoError(t, h.repo.Create(context.Background(), &domain.UpstreamAccount{
				Name:       "api-key-account",
				Provider:   domain.ProviderOpenAI,
				APIKey:     "sk-platform",
				BaseURL:    &apiBaseURL,
				Status:     domain.AccountStatusActive,
				AuthMethod: domain.AuthMethodAPIKey,
			}))

			w := httptest.NewRecorder()
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			h.handler.ServeHTTP(w, r)

			require.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, int32(1), apiHits.Load())
			assert.Equal(t, int32(0), oauthHits.Load())
		})
	}
}

func TestProxyOAuthOnly_CompactPathUsesOAuthCodexBackend(t *testing.T) {
	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	var upstreamRequestBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/codex/responses/compact", r.URL.Path)
		assert.Equal(t, "Bearer oauth-access", r.Header.Get("Authorization"))
		assert.Equal(t, "acct-oauth-access", r.Header.Get("chatgpt-account-id"))
		assert.Equal(t, "identity", r.Header.Get("Accept-Encoding"))
		var err error
		upstreamRequestBody, err = io.ReadAll(r.Body)
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"compact":"ok"}`))
	}))
	defer upstream.Close()

	h := newProxyHarness(t, 5*time.Second)
	h.client.SetCodexBackendBaseURLForTest(upstream.URL)
	insertOAuthProxyAccount(t, h.repo, upstream.URL, now, "oauth-access", "oauth-refresh", now.Add(2*time.Hour))
	coord := oauth.NewCoordinatorWithClock(oauth.NewFakeClock(now), &proxyRefreshProvider{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	coord.SetAccountStore(h.repo)
	h.selector.PreForward = coord.RefreshIfStale

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/responses/compact", strings.NewReader(`{
		"model":"gpt-5.4-mini",
		"instructions":"Be concise.",
		"messages":[
			{"role":"system","content":"Use JSON."},
			{"role":"user","content":"hello"}
		]
	}`))
	h.handler.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"compact":"ok"}`, w.Body.String())
	assert.JSONEq(t, `{
		"model":"gpt-5.4-mini",
		"instructions":"Be concise.\n\nUse JSON.",
		"input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}]
	}`, string(upstreamRequestBody))
}

func TestProxyOAuthOnly_NonResponsesPathReturnsNoCapacity(t *testing.T) {
	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	var oauthHits atomic.Int32
	oauthUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		oauthHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer oauthUpstream.Close()

	h := newProxyHarness(t, 5*time.Second)
	h.client.SetCodexBackendBaseURLForTest(oauthUpstream.URL)
	insertOAuthProxyAccount(t, h.repo, oauthUpstream.URL, now, "oauth-access", "oauth-refresh", now.Add(2*time.Hour))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/models/gpt-4o-mini", nil)
	h.handler.ServeHTTP(w, r)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, int32(0), oauthHits.Load())
	assert.Contains(t, w.Body.String(), ErrCodeNoAvailableAccount)
}

func TestRefreshIntegration_StaleOAuthRefreshesBeforeForward(t *testing.T) {
	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer refreshed-access", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"refreshed"}`))
	}))
	defer upstream.Close()

	h := newProxyHarness(t, 5*time.Second)
	h.client.SetCodexBackendBaseURLForTest(upstream.URL)
	acct := insertOAuthProxyAccount(t, h.repo, upstream.URL, now.Add(-2*time.Hour), "stale-access", "stale-refresh", now.Add(-30*time.Minute))

	provider := &proxyRefreshProvider{
		responses: []proxyRefreshResponse{{
			tokens: oauth.Tokens{
				AccessToken:  []byte("refreshed-access"),
				RefreshToken: []byte("refreshed-refresh"),
				IDToken:      mustJWT(t, map[string]any{"email": "refreshed@example.com", "plan_type": "chatgpt-plus"}),
				ExpiresIn:    2 * time.Hour,
			},
		}},
	}
	coord := oauth.NewCoordinatorWithClock(oauth.NewFakeClock(now), provider, slog.New(slog.NewTextHandler(io.Discard, nil)))
	coord.SetAccountStore(h.repo)
	h.selector.PreForward = coord.RefreshIfStale

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-4o-mini","input":"ping"}`))
	h.handler.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, int32(1), provider.refreshCalls.Load())

	stored, err := h.repo.GetByID(context.Background(), acct.ID)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, []byte("refreshed-access"), stored.AccessToken)
	assert.Equal(t, []byte("refreshed-refresh"), stored.RefreshToken)
	require.NotNil(t, stored.LastRefresh)
	assert.Equal(t, now, stored.LastRefresh.UTC())
	require.NotNil(t, stored.AccessExpiresAt)
	assert.Equal(t, now.Add(2*time.Hour), stored.AccessExpiresAt.UTC())
}

func TestRefreshIntegration_TransientFallbackForwardsStaleToken(t *testing.T) {
	tests := []struct {
		name           string
		upstreamStatus int
		responseBody   string
	}{
		{name: "upstream ok", upstreamStatus: http.StatusOK, responseBody: `{"id":"stale"}`},
		{name: "upstream unauthorized", upstreamStatus: http.StatusUnauthorized, responseBody: `{"error":{"message":"unauthorized"}}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
			var sawFallback atomic.Bool

			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "Bearer stale-access", r.Header.Get("Authorization"))
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Upstream-Test", "preserved")
				w.WriteHeader(tc.upstreamStatus)
				_, _ = w.Write([]byte(tc.responseBody))
			}))
			defer upstream.Close()

			h := newProxyHarness(t, 5*time.Second)
			h.client.SetCodexBackendBaseURLForTest(upstream.URL)
			acct := insertOAuthProxyAccount(t, h.repo, upstream.URL, now.Add(-2*time.Hour), "stale-access", "stale-refresh", now.Add(-30*time.Minute))

			provider := &proxyRefreshProvider{
				responses: []proxyRefreshResponse{{
					err: context.DeadlineExceeded,
				}},
			}
			coord := oauth.NewCoordinatorWithClock(oauth.NewFakeClock(now), provider, slog.New(slog.NewTextHandler(io.Discard, nil)))
			coord.SetAccountStore(h.repo)
			h.selector.PreForward = func(ctx context.Context, acct *domain.UpstreamAccount) ([]byte, bool, error) {
				token, usedFallback, err := coord.RefreshIfStale(ctx, acct)
				sawFallback.Store(usedFallback)
				return token, usedFallback, err
			}

			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-4o-mini","input":"ping"}`))
			h.handler.ServeHTTP(w, r)

			require.Equal(t, tc.upstreamStatus, w.Code)
			assert.Equal(t, "preserved", w.Header().Get("X-Upstream-Test"))
			assert.JSONEq(t, tc.responseBody, w.Body.String())
			assert.True(t, sawFallback.Load())
			assert.Equal(t, int32(1), provider.refreshCalls.Load())

			stored, err := h.repo.GetByID(context.Background(), acct.ID)
			require.NoError(t, err)
			require.NotNil(t, stored)
			require.NotNil(t, stored.LastRefresh)
			require.NotNil(t, stored.AccessExpiresAt)
			assert.Equal(t, now.Add(-2*time.Hour), stored.LastRefresh.UTC())
			assert.Equal(t, now.Add(-30*time.Minute), stored.AccessExpiresAt.UTC())
		})
	}
}

func TestRefreshIntegration_PermanentRefreshFailureReturns502(t *testing.T) {
	var upstreamHits atomic.Int32

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	h := newProxyHarness(t, 5*time.Second)
	h.client.SetCodexBackendBaseURLForTest(upstream.URL)
	acct := insertOAuthProxyAccount(t, h.repo, upstream.URL, time.Date(2026, 4, 22, 10, 0, 0, 0, time.UTC), "stale-access", "stale-refresh", time.Date(2026, 4, 22, 11, 30, 0, 0, time.UTC))
	h.selector.PreForward = func(ctx context.Context, acct *domain.UpstreamAccount) ([]byte, bool, error) {
		if err := h.repo.UpdateStatus(ctx, acct.ID, domain.AccountStatusDisabled); err != nil {
			return nil, false, err
		}
		return nil, false, errors.New("account disabled mid-forward")
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-4o-mini","input":"ping"}`))
	h.handler.ServeHTTP(w, r)

	require.Equal(t, http.StatusBadGateway, w.Code)
	var envelope RouterErrorEnvelope
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	assert.Equal(t, ErrCodeInternalError, envelope.Error.Code)
	assert.Equal(t, "upstream credentials unavailable", envelope.Error.Message)
	assert.NotContains(t, w.Body.String(), "account disabled mid-forward")
	assert.NotContains(t, w.Body.String(), "stale-access")
	assert.Equal(t, int32(0), upstreamHits.Load())

	stored, err := h.repo.GetByID(context.Background(), acct.ID)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, domain.AccountStatusDisabled, stored.Status)
}

func TestRefreshIntegration_SingleflightDedupesConcurrentProxyRefresh(t *testing.T) {
	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	var upstreamHits atomic.Int32

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer refreshed-burst-access", r.Header.Get("Authorization"))
		upstreamHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"burst"}`))
	}))
	defer upstream.Close()

	h := newProxyHarness(t, 5*time.Second)
	h.client.SetCodexBackendBaseURLForTest(upstream.URL)
	insertOAuthProxyAccount(t, h.repo, upstream.URL, now.Add(-2*time.Hour), "burst-stale-access", "burst-stale-refresh", now.Add(-30*time.Minute))

	releaseRefresh := make(chan struct{})
	provider := &proxyRefreshProvider{
		refreshGate: releaseRefresh,
		responses: []proxyRefreshResponse{{
			tokens: oauth.Tokens{
				AccessToken:  []byte("refreshed-burst-access"),
				RefreshToken: []byte("refreshed-burst-refresh"),
				IDToken:      mustJWT(t, map[string]any{"email": "burst@example.com"}),
				ExpiresIn:    2 * time.Hour,
			},
		}},
	}
	coord := oauth.NewCoordinatorWithClock(oauth.NewFakeClock(now), provider, slog.New(slog.NewTextHandler(io.Discard, nil)))
	coord.SetAccountStore(h.repo)
	h.selector.PreForward = coord.RefreshIfStale

	const requests = 50
	var wg sync.WaitGroup
	statuses := make(chan int, requests)
	for range requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-4o-mini","input":"ping"}`))
			h.handler.ServeHTTP(w, r)
			statuses <- w.Code
		}()
	}

	require.Eventually(t, func() bool {
		return provider.refreshCalls.Load() == 1
	}, time.Second, 10*time.Millisecond, "singleflight leader never entered refresh")
	close(releaseRefresh)
	wg.Wait()
	close(statuses)

	for status := range statuses {
		assert.Equal(t, http.StatusOK, status)
	}
	assert.Equal(t, int32(1), provider.refreshCalls.Load())
	assert.Equal(t, int32(requests), upstreamHits.Load())
}

func TestRefreshIntegration_SelectorFailureStillReturns500(t *testing.T) {
	recorderStore, err := store.New("sqlite3", ":memory:", 1, 1)
	require.NoError(t, err)
	require.NoError(t, recorderStore.Migrate("sqlite3"))
	t.Cleanup(func() { _ = recorderStore.Close() })

	recordRepo := store.NewRequestRecordRepo(recorderStore.Engine())
	recorder := core.NewRequestRecorder(recordRepo, slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})))
	recorder.Start()
	t.Cleanup(func() { _ = recorder.Close(context.Background()) })

	selector := core.NewAccountSelector(failingProxyAccountRepo{}, nil)
	handler := NewProxyHandler(selector, recorder, openai.NewClient(5*time.Second), false, 0, slog.New(slog.NewTextHandler(io.Discard, nil)))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-4o-mini","input":"ping"}`))
	handler.ServeHTTP(w, r)

	require.Equal(t, http.StatusInternalServerError, w.Code)
	var envelope RouterErrorEnvelope
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	assert.Equal(t, ErrCodeInternalError, envelope.Error.Code)
	assert.Equal(t, "account selection failed", envelope.Error.Message)
}

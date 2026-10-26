package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/user/one-llm-router/internal/config"
	"github.com/user/one-llm-router/internal/core"
	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/store"
)

const decisionRequest = `{"model":"decision-free","state":{"number":9007199254740993,"text":"账单"},"questions":{"q":{"type":"noul","instructions":"urgent?"}}}`
const decisionResponse = `{"model":"decision-version-1","answers":{"q":{"type":"noul","noul":0.9}},"usage":{"input_tokens":12,"output_tokens":3,"input_tokens_details":{"cached_tokens":4},"output_tokens_details":{"reasoning_tokens":2}}}`

func TestSystemOneForwarding(t *testing.T) {
	for mask := 0; mask < 4; mask++ {
		t.Run(string(rune('0'+mask)), func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				assert.Equal(t, "/inference/systemone/v1/systemone", r.URL.Path)
				assert.Equal(t, "Bearer upstream-secret", r.Header.Get("Authorization"))
				for _, h := range []string{"Cookie", "chatgpt-account-id", "X-Admin-Token", "X-Drop"} {
					assert.Empty(t, r.Header.Get(h))
				}
				b, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				assert.Equal(t, decisionRequest, string(b))
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(decisionResponse))
			}))
			defer upstream.Close()
			h, s := setupProxyTest(t, upstream)
			repo := store.NewAccountRepo(s.Engine())
			base := upstream.URL + "/inference/systemone/v1/"
			caps := []string{"op.systemone"}
			if mask&1 != 0 {
				caps = append(caps, "op.openai.chat_completions")
			}
			if mask&2 != 0 {
				caps = append(caps, "op.openai.responses")
			}
			a := &domain.UpstreamAccount{Name: "decision", Status: domain.AccountStatusActive, Provider: "openai", APIKey: "upstream-secret", BaseURL: &base, Capabilities: caps}
			require.NoError(t, repo.Create(context.Background(), a))
			h.SetModelRenameFunc(func() []config.ModelRenameRule {
				return []config.ModelRenameRule{{From: "decision-free", To: "paid-model"}}
			})
			r := httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(decisionRequest))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Authorization", "Bearer client-secret")
			r.Header.Set("Cookie", "session=secret")
			r.Header.Set("chatgpt-account-id", "private")
			r.Header.Set("X-Admin-Token", "private")
			r.Header.Set("Connection", "X-Drop")
			r.Header.Set("X-Drop", "private")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			require.Equal(t, 200, w.Code, w.Body.String())
			assert.Equal(t, decisionResponse, w.Body.String())
			assert.Equal(t, int32(1), calls.Load())
			require.NoError(t, h.recorder.Close(context.Background()))
			var records []domain.RequestRecord
			require.NoError(t, s.Engine().Find(&records))
			require.Len(t, records, 1)
			assert.EqualValues(t, 12, records[0].TokenUsage["input"])
			assert.EqualValues(t, 3, records[0].TokenUsage["output"])
			assert.EqualValues(t, 4, records[0].TokenUsage["cached_input"])
			assert.EqualValues(t, 2, records[0].TokenUsage["reasoning"])
			assert.Equal(t, domain.OutcomeSuccess, records[0].Outcome)
			assert.Equal(t, a.ID, *records[0].UpstreamAccountID)
		})
	}
}

func TestSystemOneErrors(t *testing.T) {
	tests := []struct {
		name, body, content, encoding, response, responseType string
		upstreamStatus, want                                  int
		configured                                            bool
	}{
		{name: "no account", body: decisionRequest, content: "application/json", want: 503},
		{name: "invalid before selection", body: `{}`, content: "application/json", want: 400},
		{name: "compression", body: decisionRequest, content: "application/json", encoding: "gzip", want: 415},
		{name: "multipart", body: decisionRequest, content: "multipart/form-data", want: 415},
		{name: "stream", body: `{"model":"m","state":"s","questions":{"q":{"type":"noul"}},"stream":true}`, content: "application/json", want: 400},
		{name: "redirect", body: decisionRequest, content: "application/json", configured: true, upstreamStatus: 307, response: "redirect-body", responseType: "text/plain", want: 307},
		{name: "empty upstream error", body: decisionRequest, content: "application/json", configured: true, upstreamStatus: 500, responseType: "text/plain", want: 500},
		{name: "wrong success content type", body: decisionRequest, content: "application/json", configured: true, upstreamStatus: 200, response: decisionResponse, responseType: "text/plain", want: 502},
		{name: "upstream error", body: decisionRequest, content: "application/json", configured: true, upstreamStatus: 429, response: `{"detail":"quota exceeded"}`, responseType: "application/json", want: 429},
		{name: "missing usage", body: decisionRequest, content: "application/json", configured: true, upstreamStatus: 200, response: `{"model":"m","answers":{}}`, responseType: "application/json", want: 502},
		{name: "SSE", body: decisionRequest, content: "application/json", configured: true, upstreamStatus: 200, response: "data: {}\n\n", responseType: "text/event-stream", want: 502},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", tt.responseType)
				w.Header().Set("X-Upstream-Proof", "present")
				w.WriteHeader(tt.upstreamStatus)
				_, _ = w.Write([]byte(tt.response))
			}))
			defer up.Close()
			h, s := setupProxyTest(t, up)
			if tt.configured {
				_, err := store.NewAccountRepo(s.Engine()).UpdateDetails(context.Background(), 1, core.AccountDetailsPatch{Capabilities: []string{"op.systemone"}})
				require.NoError(t, err)
			}
			r := httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(tt.body))
			r.Header.Set("Content-Type", tt.content)
			r.Header.Set("Content-Encoding", tt.encoding)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			assert.Equal(t, tt.want, w.Code, w.Body.String())
			if tt.configured {
				assert.Equal(t, int32(1), calls.Load())
			} else {
				assert.Zero(t, calls.Load())
			}
			if tt.want == 502 {
				assert.Empty(t, w.Header().Get("X-Upstream-Proof"), "invalid success must be rejected before copying upstream headers")
			}
			if tt.want == 429 || tt.want == 307 || tt.want == 500 {
				assert.Equal(t, tt.response, w.Body.String())
			}
		})
	}
}

func TestSystemOneAccountEligibility(t *testing.T) {
	for mask := 1; mask < 8; mask++ {
		caps := []string{}
		if mask&1 != 0 {
			caps = append(caps, "op.openai.chat_completions")
		}
		if mask&2 != 0 {
			caps = append(caps, "op.openai.responses")
		}
		if mask&4 != 0 {
			caps = append(caps, "op.systemone")
		}
		a := domain.UpstreamAccount{AuthMethod: domain.AuthMethodAPIKey, Capabilities: caps}
		for _, tc := range []struct {
			path string
			want bool
		}{{"/v1/chat/completions", mask&1 != 0}, {"/v1/responses", mask&2 != 0}, {"/v1/systemone", mask&4 != 0}, {"/v1/conversations", mask&3 != 0}} {
			route := classifyGatewayRoute(http.MethodPost, tc.path, false)
			assert.Equal(t, tc.want, proxyAccountEligible(route, nil)(a), "mask=%d path=%s", mask, tc.path)
		}
	}
}

func TestSystemOneInvalidRequestBeforeAccountLookup(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("invalid request reached upstream")
	}))
	defer up.Close()
	h, _ := setupProxyTest(t, up)
	h.selector = core.NewAccountSelector(failingProxyAccountRepo{}, nil)
	for _, body := range []string{`{}`, `{"model":"m","state":"s","questions":{}}`} {
		r := httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		require.Equal(t, http.StatusBadRequest, w.Code, "must validate before the failing account repository")
	}
}

func TestSystemOneResponseReadCancellation(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "deadline"}[deadline], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			if deadline {
				ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
			}
			defer cancel()
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, `{"model":`)
				w.(http.Flusher).Flush()
				if !deadline {
					cancel()
				}
				<-r.Context().Done()
			}))
			defer up.Close()
			h, s := setupProxyTest(t, up)
			_, err := store.NewAccountRepo(s.Engine()).UpdateDetails(context.Background(), 1, core.AccountDetailsPatch{Capabilities: []string{"op.systemone"}})
			require.NoError(t, err)
			r := httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(decisionRequest)).WithContext(ctx)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			want := http.StatusBadGateway
			if deadline {
				want = http.StatusGatewayTimeout
			}
			require.Equal(t, want, w.Code, w.Body.String())
		})
	}
}

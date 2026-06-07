package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/one-llm-router/internal/core"
	"github.com/user/one-llm-router/internal/domain"
)

func TestBuildPlaygroundResponsesBody(t *testing.T) {
	body, err := BuildPlaygroundResponsesBody("gpt-5.4-mini", "Say hello.", 128)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Equal(t, "gpt-5.4-mini", got["model"])
	assert.Equal(t, "Say hello.", got["input"])
	assert.Equal(t, false, got["stream"])
	assert.Equal(t, float64(128), got["max_output_tokens"])
}

func TestBuildPlaygroundChatCompletionsBody(t *testing.T) {
	body, err := BuildPlaygroundChatCompletionsBody("gpt-5.4-mini", "Say hello.", 128)
	require.NoError(t, err)

	var got struct {
		Model               string `json:"model"`
		Stream              bool   `json:"stream"`
		MaxCompletionTokens int    `json:"max_completion_tokens"`
		Messages            []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Equal(t, "gpt-5.4-mini", got.Model)
	assert.False(t, got.Stream)
	assert.Equal(t, 128, got.MaxCompletionTokens)
	require.Len(t, got.Messages, 1)
	assert.Equal(t, "user", got.Messages[0].Role)
	assert.Equal(t, "Say hello.", got.Messages[0].Content)
}

func TestExtractPlaygroundOutputText(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
		ok   bool
	}{
		{
			name: "top level output_text",
			body: `{"output_text":"Hello from top level."}`,
			want: "Hello from top level.",
			ok:   true,
		},
		{
			name: "nested output content text",
			body: `{"output":[{"type":"message","content":[{"type":"output_text","text":"Hello "},{"type":"output_text","text":"there."}]}]}`,
			want: "Hello there.",
			ok:   true,
		},
		{
			name: "nested text value fallback",
			body: `{"output":[{"content":[{"type":"output_text","text":{"value":"rich text"}}]}]}`,
			want: "rich text",
			ok:   true,
		},
		{
			name: "no extractable text",
			body: `{"id":"resp_123","output":[{"content":[{"type":"image"}]}]}`,
			ok:   false,
		},
		{
			name: "invalid json",
			body: `not json`,
			ok:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ExtractPlaygroundOutputText([]byte(tc.body))
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestExtractPlaygroundChatOutputText(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
		ok   bool
	}{
		{
			name: "assistant message content",
			body: `{"choices":[{"message":{"role":"assistant","content":"Hello from chat."}}]}`,
			want: "Hello from chat.",
			ok:   true,
		},
		{
			name: "content parts",
			body: `{"choices":[{"message":{"content":[{"type":"text","text":"Hello "},{"type":"text","text":"there."}]}}]}`,
			want: "Hello there.",
			ok:   true,
		},
		{
			name: "no extractable content",
			body: `{"choices":[{"message":{"content":null}}]}`,
			ok:   false,
		},
		{
			name: "invalid json",
			body: `not json`,
			ok:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ExtractPlaygroundChatOutputText([]byte(tc.body))
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestRunPlayground(t *testing.T) {
	t.Run("posts non-streaming responses request and returns parsed output", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/responses", r.URL.Path)
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "Bearer sk-playground", r.Header.Get("Authorization"))

			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, "gpt-5.4-mini", body["model"])
			assert.Equal(t, "hello", body["input"])
			assert.Equal(t, false, body["stream"])
			assert.Equal(t, float64(128), body["max_output_tokens"])

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"id":"resp_123",
				"output_text":"Hello.",
				"usage":{"input_tokens":4,"output_tokens":2}
			}`))
		}))
		defer srv.Close()

		maxOutput := 128
		result, err := NewClient(time.Second).RunPlayground(context.Background(), domain.UpstreamAccount{
			ID:       7,
			Provider: domain.ProviderOpenAI,
			BaseURL:  &srv.URL,
		}, []byte("sk-playground"), core.PlaygroundRunRequest{
			SelectionMode:      core.PlaygroundSelectionAuto,
			Model:              "gpt-5.4-mini",
			Text:               "hello",
			MaxOutputTokens:    &maxOutput,
			IncludeRawResponse: true,
		})

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, 200, result.StatusCode)
		assert.Equal(t, domain.ResponseModeJSON, result.ResponseMode)
		assert.Equal(t, "Hello.", result.Text)
		assert.True(t, result.TextAvailable)
		assert.True(t, result.RawResponseAvailable)
		assert.Equal(t, "resp_123", result.RawResponse["id"])
		assert.Equal(t, 4, result.Usage["input"])
		assert.Equal(t, 2, result.Usage["output"])
		assert.JSONEq(t, `{"model":"gpt-5.4-mini","input":"hello","stream":false,"max_output_tokens":128}`, string(result.UpstreamRequestBody))
	})

	t.Run("posts chat completions request and returns parsed output", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/chat/completions", r.URL.Path)
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "Bearer sk-playground", r.Header.Get("Authorization"))

			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, "gpt-5.4-mini", body["model"])
			assert.Equal(t, false, body["stream"])
			assert.Equal(t, float64(128), body["max_completion_tokens"])
			messages, ok := body["messages"].([]any)
			require.True(t, ok)
			require.Len(t, messages, 1)
			message, ok := messages[0].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, "user", message["role"])
			assert.Equal(t, "hello", message["content"])

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"id":"chatcmpl_123",
				"object":"chat.completion",
				"choices":[{"message":{"role":"assistant","content":"Hello chat."}}],
				"usage":{"prompt_tokens":4,"completion_tokens":2}
			}`))
		}))
		defer srv.Close()

		maxOutput := 128
		result, err := NewClient(time.Second).RunPlayground(context.Background(), domain.UpstreamAccount{
			ID:       7,
			Provider: domain.ProviderOpenAI,
			BaseURL:  &srv.URL,
		}, []byte("sk-playground"), core.PlaygroundRunRequest{
			SelectionMode:      core.PlaygroundSelectionAuto,
			Endpoint:           core.PlaygroundEndpointChatCompletions,
			Model:              "gpt-5.4-mini",
			Text:               "hello",
			MaxOutputTokens:    &maxOutput,
			IncludeRawResponse: true,
		})

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, 200, result.StatusCode)
		assert.Equal(t, domain.ResponseModeJSON, result.ResponseMode)
		assert.Equal(t, "/chat/completions", result.UpstreamEndpoint)
		assert.Equal(t, "Hello chat.", result.Text)
		assert.True(t, result.TextAvailable)
		assert.True(t, result.RawResponseAvailable)
		assert.Equal(t, "chatcmpl_123", result.RawResponse["id"])
		assert.Equal(t, 4, result.Usage["input"])
		assert.Equal(t, 2, result.Usage["output"])
		assert.JSONEq(t, `{"model":"gpt-5.4-mini","messages":[{"role":"user","content":"hello"}],"stream":false,"max_completion_tokens":128}`, string(result.UpstreamRequestBody))
	})

	t.Run("oauth chat completions uses codex adapter and returns chat-shaped output", func(t *testing.T) {
		chatGPTAccountID := "chatgpt-account-123"
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/codex/responses", r.URL.Path)
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "Bearer oauth-playground", r.Header.Get("Authorization"))
			assert.Equal(t, chatGPTAccountID, r.Header.Get("chatgpt-account-id"))
			assert.Equal(t, CodexCLIUserAgent, r.Header.Get("User-Agent"))

			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, "gpt-5.4-mini", body["model"])
			assert.Equal(t, false, body["store"])
			assert.Equal(t, true, body["stream"])
			assert.NotContains(t, body, "max_output_tokens")
			input, ok := body["input"].([]any)
			require.True(t, ok)
			require.Len(t, input, 1)

			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(strings.Join([]string{
				`event: response.output_text.delta`,
				`data: {"type":"response.output_text.delta","delta":"Hello from adapted chat."}`,
				``,
				`event: response.completed`,
				`data: {"type":"response.completed","response":{"id":"resp_chat","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello from adapted chat."}]}],"usage":{"input_tokens":4,"output_tokens":3}}}`,
				``,
			}, "\n")))
		}))
		defer srv.Close()

		maxOutput := 128
		client := NewClient(time.Second)
		client.codexBackendBaseURL = srv.URL
		result, err := client.RunPlayground(context.Background(), domain.UpstreamAccount{
			ID:               7,
			Provider:         domain.ProviderOpenAI,
			AuthMethod:       domain.AuthMethodOAuthBrowser,
			ChatGPTAccountID: &chatGPTAccountID,
		}, []byte("oauth-playground"), core.PlaygroundRunRequest{
			SelectionMode:   core.PlaygroundSelectionAuto,
			Endpoint:        core.PlaygroundEndpointChatCompletions,
			Model:           "gpt-5.4-mini",
			Text:            "hello",
			MaxOutputTokens: &maxOutput,
		})

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "/codex/responses", result.UpstreamEndpoint)
		assert.Equal(t, "Hello from adapted chat.", result.Text)
		assert.Equal(t, "chat.completion", result.RawResponse["object"])
		assert.Equal(t, 4, result.Usage["input"])
		assert.Equal(t, 3, result.Usage["output"])
	})

	t.Run("oauth account uses codex backend route and chatgpt account header", func(t *testing.T) {
		chatGPTAccountID := "chatgpt-account-123"
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/codex/responses", r.URL.Path)
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "Bearer oauth-playground", r.Header.Get("Authorization"))
			assert.Equal(t, chatGPTAccountID, r.Header.Get("chatgpt-account-id"))
			assert.Equal(t, CodexCLIUserAgent, r.Header.Get("User-Agent"))

			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, "gpt-5.4-mini", body["model"])
			assert.Equal(t, "", body["instructions"])
			assert.Equal(t, false, body["store"])
			assert.Equal(t, true, body["stream"])
			assert.NotContains(t, body, "max_output_tokens")
			input, ok := body["input"].([]any)
			require.True(t, ok)
			require.Len(t, input, 1)

			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(strings.Join([]string{
				`event: response.output_text.delta`,
				`data: {"type":"response.output_text.delta","delta":"Hello from Codex."}`,
				``,
				`event: response.completed`,
				`data: {"type":"response.completed","response":{"id":"resp_codex","usage":{"input_tokens":4,"output_tokens":3}}}`,
				``,
			}, "\n")))
		}))
		defer srv.Close()

		maxOutput := 128
		client := NewClient(time.Second)
		client.codexBackendBaseURL = srv.URL
		result, err := client.RunPlayground(context.Background(), domain.UpstreamAccount{
			ID:               7,
			Provider:         domain.ProviderOpenAI,
			AuthMethod:       domain.AuthMethodOAuthBrowser,
			ChatGPTAccountID: &chatGPTAccountID,
		}, []byte("oauth-playground"), core.PlaygroundRunRequest{
			SelectionMode:   core.PlaygroundSelectionAuto,
			Model:           "gpt-5.4-mini",
			Text:            "hello",
			MaxOutputTokens: &maxOutput,
		})

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "/codex/responses", result.UpstreamEndpoint)
		assert.Equal(t, "Hello from Codex.", result.Text)
	})

	t.Run("oauth account accepts json success response without SSE collection", func(t *testing.T) {
		chatGPTAccountID := "chatgpt-account-123"
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/codex/responses", r.URL.Path)
			assert.Equal(t, chatGPTAccountID, r.Header.Get("chatgpt-account-id"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"resp_json","output_text":"Hello JSON.","usage":{"input_tokens":2,"output_tokens":3}}`))
		}))
		defer srv.Close()

		client := NewClient(time.Second)
		client.codexBackendBaseURL = srv.URL
		result, err := client.RunPlayground(context.Background(), domain.UpstreamAccount{
			ID:               7,
			Provider:         domain.ProviderOpenAI,
			AuthMethod:       domain.AuthMethodOAuthBrowser,
			ChatGPTAccountID: &chatGPTAccountID,
		}, []byte("oauth-playground"), core.PlaygroundRunRequest{
			SelectionMode: core.PlaygroundSelectionAuto,
			Model:         "gpt-5.4-mini",
			Text:          "hello",
		})

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, domain.ResponseModeJSON, result.ResponseMode)
		assert.Equal(t, "Hello JSON.", result.Text)
		assert.Equal(t, 2, result.Usage["input"])
		assert.Equal(t, 3, result.Usage["output"])
	})

	t.Run("omits raw response from API result when it was not requested", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"resp_hidden","output_text":"hidden raw"}`))
		}))
		defer srv.Close()

		result, err := NewClient(time.Second).RunPlayground(context.Background(), domain.UpstreamAccount{
			ID:       7,
			Provider: domain.ProviderOpenAI,
			BaseURL:  &srv.URL,
		}, []byte("sk-playground"), core.PlaygroundRunRequest{
			SelectionMode: core.PlaygroundSelectionAuto,
			Model:         "gpt-5.4-mini",
			Text:          "hello",
		})

		require.NoError(t, err)
		assert.False(t, result.RawResponseAvailable)
		require.NotNil(t, result.RawResponse)
		require.NotNil(t, result.RawResponseOmittedReason)
		assert.Equal(t, core.PlaygroundRawNotRequested, *result.RawResponseOmittedReason)
	})

	t.Run("non-2xx response maps to upstream error with provider detail", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"code":"rate_limit_exceeded","message":"slow down"}}`))
		}))
		defer srv.Close()

		_, err := NewClient(time.Second).RunPlayground(context.Background(), domain.UpstreamAccount{
			ID:       7,
			Provider: domain.ProviderOpenAI,
			BaseURL:  &srv.URL,
		}, []byte("sk-playground"), core.PlaygroundRunRequest{
			SelectionMode: core.PlaygroundSelectionAuto,
			Model:         "gpt-5.4-mini",
			Text:          "hello",
		})

		require.Error(t, err)
		var upstreamErr *core.PlaygroundUpstreamError
		require.ErrorAs(t, err, &upstreamErr)
		assert.Equal(t, http.StatusTooManyRequests, upstreamErr.UpstreamStatus)
		assert.JSONEq(t, `{"error":{"code":"rate_limit_exceeded","message":"slow down"}}`, string(upstreamErr.UpstreamResponseBody))
		require.NotNil(t, upstreamErr.ProviderError)
		assert.Equal(t, "rate_limit_exceeded", *upstreamErr.ProviderError)
		require.NotNil(t, upstreamErr.ProviderMessage)
		assert.Equal(t, "slow down", *upstreamErr.ProviderMessage)
	})

	t.Run("malformed and oversized success bodies map to typed router errors", func(t *testing.T) {
		tests := []struct {
			name    string
			body    string
			wantErr any
		}{
			{name: "invalid json", body: `not json`, wantErr: &core.PlaygroundResponseMalformedError{}},
			{name: "unsafe json", body: `[{"id":"array"}]`, wantErr: &core.PlaygroundResponseMalformedError{}},
			{name: "too large", body: strings.Repeat("x", core.PlaygroundResponseReadLimitBytes+1), wantErr: &core.PlaygroundResponseTooLargeError{}},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_, _ = w.Write([]byte(tc.body))
				}))
				defer srv.Close()

				_, err := NewClient(time.Second).RunPlayground(context.Background(), domain.UpstreamAccount{
					ID:       7,
					Provider: domain.ProviderOpenAI,
					BaseURL:  &srv.URL,
				}, []byte("sk-playground"), core.PlaygroundRunRequest{
					SelectionMode: core.PlaygroundSelectionAuto,
					Model:         "gpt-5.4-mini",
					Text:          "hello",
				})

				require.Error(t, err)
				switch tc.wantErr.(type) {
				case *core.PlaygroundResponseMalformedError:
					var got *core.PlaygroundResponseMalformedError
					require.ErrorAs(t, err, &got)
				case *core.PlaygroundResponseTooLargeError:
					var got *core.PlaygroundResponseTooLargeError
					require.ErrorAs(t, err, &got)
				}
			})
		}
	})

	t.Run("adapter wait limit returns typed timeout", func(t *testing.T) {
		old := playgroundWaitLimit
		playgroundWaitLimit = 5 * time.Millisecond
		t.Cleanup(func() { playgroundWaitLimit = old })

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(50 * time.Millisecond)
			_, _ = w.Write([]byte(`{"output_text":"too late"}`))
		}))
		defer srv.Close()

		_, err := NewClient(time.Second).RunPlayground(context.Background(), domain.UpstreamAccount{
			ID:       7,
			Provider: domain.ProviderOpenAI,
			BaseURL:  &srv.URL,
		}, []byte("sk-playground"), core.PlaygroundRunRequest{
			SelectionMode: core.PlaygroundSelectionAuto,
			Model:         "gpt-5.4-mini",
			Text:          "hello",
		})

		require.Error(t, err)
		var timeoutErr *core.PlaygroundUpstreamTimeoutError
		require.ErrorAs(t, err, &timeoutErr)
	})
}

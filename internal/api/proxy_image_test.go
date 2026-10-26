package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func imageProxySSE(t *testing.T, events ...map[string]any) string {
	t.Helper()
	var stream strings.Builder
	for _, event := range events {
		data, err := json.Marshal(event)
		require.NoError(t, err)
		stream.WriteString("event: " + event["type"].(string) + "\ndata: ")
		stream.Write(data)
		stream.WriteString("\n\n")
	}
	return stream.String()
}

func imageProxyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 896, 896))
	random := rand.New(rand.NewSource(20261026))
	_, err := random.Read(img.Pix)
	require.NoError(t, err)
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255
	}
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, img))
	return encoded.Bytes()
}

func TestProxyOAuthCodexImageGeneration(t *testing.T) {
	imageBytes := imageProxyPNG(t)
	imageResult := base64.StdEncoding.EncodeToString(imageBytes)
	// A real PNG whose base64 exceeds the former 2 MiB SSE line limit.
	require.Greater(t, len(imageResult), 3*1024*1024)
	item := map[string]any{
		"id": "ig_generated", "type": "image_generation_call", "status": "completed",
		"result": imageResult, "output_format": "png", "size": "896x896",
		"quality": "high", "background": "opaque", "revised_prompt": "An apple in watercolor.",
	}
	stream := imageProxySSE(t,
		map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_image", "status": "in_progress"}},
		map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item},
		map[string]any{"type": "response.completed", "response": map[string]any{
			"id": "resp_image", "status": "completed", "output": []any{},
			"usage": map[string]any{"input_tokens": 5, "output_tokens": 8},
		}},
	)
	for _, tc := range []struct {
		name   string
		path   string
		stream bool
		json   bool
	}{
		{"non_streaming_responses", "/v1/responses", false, true},
		{"streaming_responses", "/v1/responses", true, false},
		{"native_codex_responses", "/backend-api/codex/responses", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var upstreamCalls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upstreamCalls.Add(1)
				assert.Equal(t, "/codex/responses", r.URL.Path)
				assert.Equal(t, "Bearer oauth-access", r.Header.Get("Authorization"))
				assert.Equal(t, "acct-oauth-access", r.Header.Get("chatgpt-account-id"))
				var body struct {
					Stream bool `json:"stream"`
					Tools  []struct {
						Type string `json:"type"`
					} `json:"tools"`
				}
				if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&body)) {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				assert.True(t, body.Stream, "Codex upstream must always receive streaming requests")
				if assert.Len(t, body.Tools, 1) {
					assert.Equal(t, "image_generation", body.Tools[0].Type)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, err := io.WriteString(w, stream)
				assert.NoError(t, err)
			}))
			t.Cleanup(upstream.Close)
			h := newOAuthProxyHarness(t, upstream.URL)
			requestBody, err := json.Marshal(map[string]any{
				"model": "gpt-6.1-sol", "input": "Paint an apple.", "stream": tc.stream,
				"tools": []any{map[string]any{"type": "image_generation"}},
			})
			require.NoError(t, err)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, tc.path, bytes.NewReader(requestBody))
			req.Header.Set("Content-Type", "application/json")
			h.handler.ServeHTTP(rec, req)
			require.Equal(t, http.StatusOK, rec.Code)
			require.Equal(t, int32(1), upstreamCalls.Load())
			if !tc.json {
				require.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
				// Compare bytes without printing several MiB of base64 on failure.
				require.True(t, bytes.Equal([]byte(stream), rec.Body.Bytes()), "SSE body differs from complete upstream stream")
				return
			}
			require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
			var response struct {
				ID     string           `json:"id"`
				Status string           `json:"status"`
				Output []map[string]any `json:"output"`
				Usage  map[string]int   `json:"usage"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
			assert.Equal(t, "resp_image", response.ID)
			assert.Equal(t, "completed", response.Status)
			assert.Equal(t, map[string]int{"input_tokens": 5, "output_tokens": 8}, response.Usage)
			require.Len(t, response.Output, 1)
			result, ok := response.Output[0]["result"].(string)
			require.True(t, ok, "image result must be a base64 string")
			decoded, err := base64.StdEncoding.DecodeString(result)
			require.NoError(t, err)
			require.True(t, bytes.Equal(imageBytes, decoded), "decoded image differs from generated upstream PNG")
			decodedImage, err := png.Decode(bytes.NewReader(decoded))
			require.NoError(t, err)
			assert.Equal(t, image.Rect(0, 0, 896, 896), decodedImage.Bounds())
			delete(response.Output[0], "result")
			assert.Equal(t, map[string]any{
				"id": "ig_generated", "type": "image_generation_call", "status": "completed",
				"output_format": "png", "size": "896x896", "quality": "high",
				"background": "opaque", "revised_prompt": "An apple in watercolor.",
			}, response.Output[0])
		})
	}
}

func TestProxyOAuthCodexImageGenerationRejectsInvalidSSE(t *testing.T) {
	image := map[string]any{"id": "ig_generated", "type": "image_generation_call", "status": "completed", "result": "aW1hZ2U="}
	for _, tc := range []struct {
		name   string
		events []map[string]any
	}{
		{"missing_output_index", []map[string]any{
			{"type": "response.output_item.done", "item": image},
			{"type": "response.completed", "response": map[string]any{"id": "resp_image", "output": []any{}}},
		}},
		{"missing_terminal", []map[string]any{
			{"type": "response.output_item.done", "output_index": 0, "item": image},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := imageProxySSE(t, tc.events...)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/codex/responses", r.URL.Path)
				w.Header().Set("Content-Type", "text/event-stream")
				_, err := io.WriteString(w, stream)
				assert.NoError(t, err)
			}))
			t.Cleanup(upstream.Close)
			h := newOAuthProxyHarness(t, upstream.URL)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-6.1-sol","input":"Paint an apple.","stream":false,"tools":[{"type":"image_generation"}]}`))
			req.Header.Set("Content-Type", "application/json")
			h.handler.ServeHTTP(rec, req)
			require.Equal(t, http.StatusBadGateway, rec.Code, rec.Body.String())
			require.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
			var response struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
			assert.Equal(t, "upstream_response_invalid", response.Error.Code)
		})
	}
}

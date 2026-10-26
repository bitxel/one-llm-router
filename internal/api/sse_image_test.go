package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestForwardSSE_ImageResultAboveTwoMiB(t *testing.T) {
	image := strings.Repeat("YQ==", 768*1024)
	item := map[string]any{
		"id": "ig_apple", "type": "image_generation_call", "status": "completed",
		"result": image, "output_format": "png", "size": "1254x1254",
	}
	done, err := json.Marshal(map[string]any{
		"type": "response.output_item.done", "output_index": 0, "item": item,
	})
	require.NoError(t, err)
	stream := "event: response.output_item.done\ndata: " + string(done) + "\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_apple\",\"status\":\"completed\",\"output\":[]}}\n\n"

	for _, capture := range []bool{false, true} {
		t.Run(map[bool]string{false: "passthrough", true: "capture"}[capture], func(t *testing.T) {
			w := httptest.NewRecorder()
			result := ForwardSSE(w, strings.NewReader(stream), capture)
			require.NoError(t, result.Err)
			require.Equal(t, stream, w.Body.String())
			if capture {
				var response struct {
					Output []struct {
						Type   string `json:"type"`
						Result string `json:"result"`
					} `json:"output"`
				}
				require.NoError(t, json.Unmarshal([]byte(result.UpstreamResponseBody), &response))
				require.Len(t, response.Output, 1)
				require.Equal(t, "image_generation_call", response.Output[0].Type)
				require.True(t, image == response.Output[0].Result, "image result must be preserved byte for byte")
			}
		})
	}
}

func TestForwardSSE_DefaultImageEventLimit(t *testing.T) {
	for _, tc := range []struct {
		name      string
		size      int
		wantError bool
	}{
		{name: "within 16 MiB", size: 16<<20 - 1},
		{name: "exactly 16 MiB", size: 16 << 20},
		{name: "over 16 MiB", size: 16<<20 + 1, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const prefix = `{"type":"response.output_item.done","output_index":0,"item":{"type":"image_generation_call","result":"`
			const suffix = `"}}`
			data := prefix + strings.Repeat("A", tc.size-len(prefix)-len(suffix)) + suffix
			for _, newline := range []string{"\n", "\r\n"} {
				stream := "data: " + data + newline + newline
				w := httptest.NewRecorder()
				result := ForwardSSE(w, strings.NewReader(stream), false)
				if tc.wantError {
					require.ErrorIs(t, result.Err, ErrSSEEventTooLarge)
					continue
				}
				require.NoError(t, result.Err)
				require.True(t, "data: "+data+"\n\n" == w.Body.String(), "full event must be forwarded with normalized newlines")
			}
		})
	}
}

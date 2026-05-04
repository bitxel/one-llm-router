package api

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/provider/openai"
)

type SSEResult struct {
	TokenUsage           domain.JSONMap
	UpstreamResponseBody string
	TTFTMs               *int
	Err                  error
}

var ErrSSEEventTooLarge = errors.New("SSE event too large")

const (
	// DefaultMaxSSEEventBytes matches codex-lb's max_sse_event_bytes=2MB so
	// long reasoning events and multi-line tool-call deltas are not dropped
	// mid-stream. Exported so main/config can override via env.
	DefaultMaxSSEEventBytes = 2 << 20 // 2 MiB
	// bodyCaptureMaxBytes caps how much of the SSE response body we buffer
	// for request_records body fields when body logging is enabled.
	bodyCaptureMaxBytes = 1 << 20 // 1 MiB
)

// maxSSEEventBytes is package-level so main() can override it at process
// start from ROUTER_MAX_SSE_EVENT_MB. It is read-only after startup.
var maxSSEEventBytes = DefaultMaxSSEEventBytes

// SetMaxSSEEventBytes lets the entrypoint inject the configured cap before
// the first request arrives. Not safe for concurrent mutation after that.
func SetMaxSSEEventBytes(n int) {
	if n > 0 {
		maxSSEEventBytes = n
	}
}

func ForwardSSE(w http.ResponseWriter, upstream io.Reader, captureBody bool) SSEResult {
	start := time.Now()
	flusher, ok := w.(http.Flusher)
	if !ok {
		flusher = nil
	}

	scanner := bufio.NewScanner(upstream)
	scanner.Buffer(make([]byte, 0, 256*1024), maxSSEEventBytes)

	var result SSEResult
	var eventData bytes.Buffer
	var eventTooLarge bool
	var responseCollector *openai.ResponsesSSECollector
	if captureBody {
		responseCollector = &openai.ResponsesSSECollector{}
	}

	for scanner.Scan() {
		line := scanner.Text()

		_, _ = io.WriteString(w, line)
		_, _ = io.WriteString(w, "\n")

		// WHATWG SSE spec: lines beginning with ':' are comments and MUST be
		// ignored for field parsing (but we still forward them to the client
		// above to preserve heartbeat pings from upstream).
		if strings.HasPrefix(line, ":") {
			continue
		}
		// WHATWG: optional single leading space after the colon is stripped.
		// Accept both "data:foo" and "data: foo" to match codex-lb / the spec.
		if data, ok := sseField(line, "data"); ok {
			if !eventTooLarge {
				nextLen := eventData.Len() + len(data)
				if eventData.Len() > 0 {
					nextLen++
				}
				if nextLen > maxSSEEventBytes {
					eventData.Reset()
					eventTooLarge = true
					if result.Err == nil {
						result.Err = ErrSSEEventTooLarge
					}
				} else {
					if eventData.Len() > 0 {
						eventData.WriteByte('\n')
					}
					eventData.WriteString(data)
				}
			}
		} else if line == "" {
			if flusher != nil {
				flusher.Flush()
			}

			if !eventTooLarge && eventData.Len() > 0 {
				processSSEEvent(&result, responseCollector, eventData.Bytes(), start)
				eventData.Reset()
			}
			eventData.Reset()
			eventTooLarge = false
		}
	}

	if !eventTooLarge && eventData.Len() > 0 {
		processSSEEvent(&result, responseCollector, eventData.Bytes(), start)
	}

	if err := scanner.Err(); err != nil {
		result.Err = err
	}

	if flusher != nil {
		flusher.Flush()
	}

	if captureBody {
		body, ok, err := responseCollector.AggregateJSON(false)
		if err != nil && result.Err == nil {
			result.Err = fmt.Errorf("aggregate SSE response body: %w", err)
		}
		if ok {
			result.UpstreamResponseBody = string(body)
		}
	}

	return result
}

func processSSEEvent(result *SSEResult, responseCollector *openai.ResponsesSSECollector, data []byte, start time.Time) {
	if result.TTFTMs == nil && openai.IsOutputTextTokenEvent(data) {
		ms := int(time.Since(start).Milliseconds())
		result.TTFTMs = &ms
	}
	usage := openai.ExtractUsageFromEvent(data)
	if usage != nil {
		result.TokenUsage = usage
	}
	if responseCollector != nil {
		if err := responseCollector.AddEvent(data); err != nil && result.Err == nil {
			result.Err = fmt.Errorf("collect SSE response event: %w", err)
		}
	}
}

// sseField parses a single SSE line of the form "<field>:<value>" or
// "<field>:<SP><value>" per the WHATWG spec. Returns the value and true if
// the line matches the requested field. An empty colon case ("data:" with
// nothing after) returns ("", true).
func sseField(line, field string) (string, bool) {
	if !strings.HasPrefix(line, field) {
		return "", false
	}
	rest := line[len(field):]
	// Pure field name with no colon ("data" on its own) is valid SSE.
	if rest == "" {
		return "", true
	}
	if rest[0] != ':' {
		return "", false
	}
	value := strings.TrimPrefix(rest[1:], " ")
	return value, true
}

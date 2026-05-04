package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractSessionKey_SessionID(t *testing.T) {
	r, _ := http.NewRequest("POST", "/v1/responses", nil)
	r.Header.Set("x-codex-session-id", "sess-abc")
	assert.Equal(t, "sess-abc", ExtractSessionKey(r))
}

func TestExtractSessionKey_ConversationID(t *testing.T) {
	r, _ := http.NewRequest("POST", "/v1/responses", nil)
	r.Header.Set("x-codex-conversation-id", "conv-xyz")
	assert.Equal(t, "conv-xyz", ExtractSessionKey(r))
}

func TestExtractSessionKey_Precedence(t *testing.T) {
	r, _ := http.NewRequest("POST", "/v1/responses", nil)
	r.Header.Set("x-codex-session-id", "sess-abc")
	r.Header.Set("x-codex-conversation-id", "conv-xyz")
	assert.Equal(t, "sess-abc", ExtractSessionKey(r), "session-id takes precedence over conversation-id")
}

func TestExtractSessionKey_Empty(t *testing.T) {
	r, _ := http.NewRequest("POST", "/v1/responses", nil)
	assert.Equal(t, "", ExtractSessionKey(r))
}

// Q-B alignment with codex-lb: session_id is accepted at the lowest priority.
func TestExtractSessionKey_SessionIDHeader(t *testing.T) {
	r, _ := http.NewRequest("POST", "/v1/responses", nil)
	r.Header.Set("Session-Id", "legacy-session")
	assert.Equal(t, "legacy-session", ExtractSessionKey(r))
}

// Priority order: x-codex-session-id > x-codex-conversation-id > session_id.
func TestExtractSessionKey_FullPrecedence(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		want    string
	}{
		{
			name: "all three set",
			headers: map[string]string{
				"X-Codex-Session-Id":      "sess-A",
				"X-Codex-Conversation-Id": "conv-B",
				"Session-Id":              "legacy-C",
			},
			want: "sess-A",
		},
		{
			name: "conversation wins when session missing",
			headers: map[string]string{
				"X-Codex-Conversation-Id": "conv-B",
				"Session-Id":              "legacy-C",
			},
			want: "conv-B",
		},
		{
			name:    "legacy only",
			headers: map[string]string{"Session-Id": "legacy-C"},
			want:    "legacy-C",
		},
		{
			name: "whitespace is trimmed",
			headers: map[string]string{
				"X-Codex-Session-Id": "  sess-trim  \r",
			},
			want: "sess-trim",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := http.NewRequest("POST", "/v1/responses", nil)
			for k, v := range tc.headers {
				r.Header.Set(k, v)
			}
			assert.Equal(t, tc.want, ExtractSessionKey(r))
		})
	}
}

package api

import (
	"net/http"
	"strings"
)

// sessionHeaders is the ordered list (highest priority first) of HTTP
// headers that may carry a Codex session identifier. Mirrors codex-lb's
// _sticky_key_from_session_header so routing is consistent across routers.
var sessionHeaders = []string{
	"X-Codex-Session-Id",
	"X-Codex-Conversation-Id",
	"Session-Id",
}

// ExtractSessionKey returns the first non-empty session identifier found in
// r.Header per the priority above, or "" when none is present. Whitespace is
// trimmed so stray CRs from misbehaving clients don't poison the sticky
// hash ring.
func ExtractSessionKey(r *http.Request) string {
	for _, h := range sessionHeaders {
		if v := strings.TrimSpace(r.Header.Get(h)); v != "" {
			return v
		}
	}
	return ""
}

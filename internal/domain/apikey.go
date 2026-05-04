package domain

import "strings"

const (
	UpstreamAPIKeyMinLen = 1
	UpstreamAPIKeyMaxLen = 256
)

// ValidUpstreamAPIKey applies the shared admin-surface validity rule
// for upstream API keys: non-empty after trimming surrounding ASCII
// whitespace and no longer than 256 chars. The value itself is never
// echoed back to callers or logs.
func ValidUpstreamAPIKey(value string) bool {
	trimmed := strings.TrimSpace(value)
	n := len(trimmed)
	return n >= UpstreamAPIKeyMinLen && n <= UpstreamAPIKeyMaxLen
}

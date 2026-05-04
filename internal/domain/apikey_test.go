package domain

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidUpstreamAPIKey(t *testing.T) {
	assert.True(t, ValidUpstreamAPIKey("sk-live-123"))
	assert.True(t, ValidUpstreamAPIKey("  sk-trimmed-123  "))
	assert.False(t, ValidUpstreamAPIKey(""))
	assert.False(t, ValidUpstreamAPIKey("   "))
	assert.False(t, ValidUpstreamAPIKey("sk-"+strings.Repeat("x", 300)))
}

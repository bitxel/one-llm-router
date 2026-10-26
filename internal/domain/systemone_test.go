package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSystemOneBaseURLValidation(t *testing.T) {
	a := UpstreamAccount{AuthMethod: AuthMethodAPIKey, APIKey: "test-key", Capabilities: []string{"op.systemone"}}
	var validation *ValidationError
	require.ErrorAs(t, a.Validate(), &validation)
	assert.Equal(t, "base_url", validation.Field)
}

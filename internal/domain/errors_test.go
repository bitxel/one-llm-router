package domain

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidationError_Error(t *testing.T) {
	err := &ValidationError{Field: "name", Message: "account name is required"}
	assert.Equal(t, "account name is required", err.Error())
}

func TestIsValidationError_True(t *testing.T) {
	err := &ValidationError{Field: "x", Message: "bad"}
	assert.True(t, IsValidationError(err))
}

func TestIsValidationError_False(t *testing.T) {
	assert.False(t, IsValidationError(errors.New("plain")))
}

func TestTableName(t *testing.T) {
	assert.Equal(t, "upstream_accounts", (UpstreamAccount{}).TableName())
	assert.Equal(t, "request_records", (RequestRecord{}).TableName())
}

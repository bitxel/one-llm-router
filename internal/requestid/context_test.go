package requestid

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFromContext(t *testing.T) {
	var nilCtx context.Context
	assert.Equal(t, "", FromContext(nilCtx))
	assert.Equal(t, "", FromContext(context.Background()))

	ctx := WithContext(context.Background(), "req-123")
	assert.Equal(t, "req-123", FromContext(ctx))
}

func TestWithContextNilParent(t *testing.T) {
	var nilCtx context.Context
	ctx := WithContext(nilCtx, "req-nil")
	assert.Equal(t, "req-nil", FromContext(ctx))
}

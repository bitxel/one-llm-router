package clientip

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFromRemoteAddr(t *testing.T) {
	assert.Equal(t, "203.0.113.10", FromRemoteAddr("203.0.113.10:54321"))
	assert.Equal(t, "", FromRemoteAddr("203.0.113.10:notaport"))
	assert.Equal(t, "2001:db8::1", FromRemoteAddr("[2001:db8::1]:54321"))
	assert.Equal(t, "2001:db8::1", FromRemoteAddr("2001:db8::1"))
	assert.Equal(t, "2001:db8::1", FromRemoteAddr("[2001:db8::1]"))
	assert.Equal(t, "", FromRemoteAddr("[203.0.113.10"))
	assert.Equal(t, "", FromRemoteAddr("203.0.113.10]"))
	assert.Equal(t, "", FromRemoteAddr("[2001:db8::1"))
	assert.Equal(t, "", FromRemoteAddr("fe80::1%lo0"))
	assert.Equal(t, "127.0.0.1", FromRemoteAddr("127.0.0.1"))
	assert.Equal(t, "", FromRemoteAddr("client.example:54321"))
	assert.Equal(t, "", FromRemoteAddr(""))
}

func TestContext(t *testing.T) {
	assert.Equal(t, "", FromContext(context.Background()))
	assert.Equal(t, "203.0.113.10", FromContext(WithContext(context.Background(), "203.0.113.10")))
	assert.Equal(t, "203.0.113.10", FromContext(WithContext(context.Background(), "203.0.113.10:54321")))
	assert.Equal(t, "", FromContext(WithContext(context.Background(), "client.example")))
}

func TestFromRequest(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/v1/responses", nil)
	assert.NoError(t, err)
	req.RemoteAddr = "203.0.113.10:54321"
	assert.Equal(t, "203.0.113.10", FromRequest(req))

	req = req.WithContext(WithContext(req.Context(), "203.0.113.11"))
	assert.Equal(t, "203.0.113.11", FromRequest(req))

	assert.Equal(t, "", FromRequest(nil))
}

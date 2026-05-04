package domain

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRequestID_Format(t *testing.T) {
	id := NewRequestID()
	assert.True(t, strings.HasPrefix(id, "req_"), "request ID should start with req_")
	assert.Len(t, id, 20, "req_ (4) + 16 hex chars = 20")
}

func TestNewRequestID_Uniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for range 1000 {
		id := NewRequestID()
		require.False(t, seen[id], "duplicate request ID: %s", id)
		seen[id] = true
	}
}

func TestJSONMap_ValueAndScan(t *testing.T) {
	m := JSONMap{"input": 100, "output": 50}
	data, err := m.Value()
	require.NoError(t, err)
	require.NotNil(t, data)

	var m2 JSONMap
	require.NoError(t, m2.Scan(data))
	assert.Equal(t, float64(100), m2["input"])
	assert.Equal(t, float64(50), m2["output"])
}

func TestJSONMap_NilValue(t *testing.T) {
	var m JSONMap
	data, err := m.Value()
	require.NoError(t, err)
	assert.Nil(t, data)
}

func TestJSONMap_ScanNil(t *testing.T) {
	var m JSONMap
	require.NoError(t, m.Scan(nil))
	assert.Nil(t, m)
}

func TestJSONMapScan_InvalidJSON(t *testing.T) {
	var m JSONMap
	err := m.Scan([]byte(`not valid json`))
	require.Error(t, err)
}

func TestRequestRecordResponseModeConstants(t *testing.T) {
	assert.Equal(t, "json", ResponseModeJSON)
	assert.Equal(t, "sse", ResponseModeSSE)
	assert.Equal(t, "websocket", ResponseModeWebSocket)
}

func TestNewRequestID_RandReadFailurePanics(t *testing.T) {
	old := randRead
	randRead = func(b []byte) (int, error) { return 0, errors.New("rand failed") }
	defer func() { randRead = old }()

	assert.Panics(t, func() { NewRequestID() })
}

package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/user/one-llm-router/internal/domain"
)

func TestSystemOneModelRefresh(t *testing.T) {
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"test-model"}]}`))
	}))
	defer up.Close()
	r := NewModelRefresher(up.Client(), nil, "", "", discardLogger())
	repo := &recordingModelRepo{got: map[int64][]string{}}
	a := &domain.UpstreamAccount{ID: 1, AuthMethod: domain.AuthMethodAPIKey, BaseURL: &up.URL, Capabilities: []string{"op.systemone"}}
	_, _, _, err := r.Refresh(context.Background(), a, repo)
	require.Error(t, err)
	assert.Zero(t, calls.Load())
	a.Capabilities = append(a.Capabilities, "op.openai.responses")
	_, _, _, err = r.Refresh(context.Background(), a, repo)
	require.NoError(t, err)
	assert.Equal(t, int32(1), calls.Load())
}

func TestSystemOnePlaygroundIsolation(t *testing.T) {
	for _, mode := range []PlaygroundSelectionMode{PlaygroundSelectionAuto, PlaygroundSelectionAccount} {
		t.Run(string(mode), func(t *testing.T) {
			accounts := makeAccounts(7)
			accounts[0].Capabilities = []string{"op.systemone"}
			repo := &mockAccountRepo{active: accounts}
			up := &fakePlaygroundUpstream{}
			service := NewPlaygroundService(repo, NewAccountSelector(repo, nil), up)
			id := int64(7)
			req := PlaygroundRunRequest{SelectionMode: mode, Model: "m", Text: "ping"}
			if mode == PlaygroundSelectionAccount {
				req.AccountID = &id
			}
			_, err := service.Run(context.Background(), req)
			require.Error(t, err)
			assert.Zero(t, up.calls)
		})
	}
}

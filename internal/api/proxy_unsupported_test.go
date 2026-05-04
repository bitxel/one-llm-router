package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/user/one-llm-router/internal/core"
	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/provider/openai"
	"github.com/user/one-llm-router/internal/store"
)

type readTrackingBody struct {
	reads atomic.Int32
}

func (b *readTrackingBody) Read(_ []byte) (int, error) {
	b.reads.Add(1)
	return 0, errors.New("body must not be read for pre-body rejected routes")
}

func (*readTrackingBody) Close() error { return nil }

func TestProxyUnsupportedAndBlockedRoutesRejectBeforeBodyAndSelection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		method     string
		path       string
		statusCode int
		errorCode  string
	}{
		{
			name:       "audio transcription deferred",
			method:     http.MethodPost,
			path:       "/v1/audio/transcriptions",
			statusCode: http.StatusNotFound,
			errorCode:  ErrCodeUnsupportedEndpoint,
		},
		{
			name:       "files deferred",
			method:     http.MethodPost,
			path:       "/v1/files",
			statusCode: http.StatusNotFound,
			errorCode:  ErrCodeUnsupportedEndpoint,
		},
		{
			name:       "realtime websocket deferred",
			method:     http.MethodGet,
			path:       "/v1/realtime",
			statusCode: http.StatusNotFound,
			errorCode:  ErrCodeUnsupportedEndpoint,
		},
		{
			name:       "organization admin blocked",
			method:     http.MethodGet,
			path:       "/v1/organization/usage/completions",
			statusCode: http.StatusForbidden,
			errorCode:  ErrCodeBlockedEndpoint,
		},
		{
			name:       "arbitrary backend-api unsupported",
			method:     http.MethodPost,
			path:       "/backend-api/not-supported",
			statusCode: http.StatusNotFound,
			errorCode:  ErrCodeUnsupportedEndpoint,
		},
		{
			name:       "unknown v1 unsupported",
			method:     http.MethodPost,
			path:       "/v1/not-supported",
			statusCode: http.StatusNotFound,
			errorCode:  ErrCodeUnsupportedEndpoint,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler, recordRepo, closeFn := newUnsupportedProxyHandler(t)
			defer closeFn()

			body := &readTrackingBody{}
			req := httptest.NewRequest(tc.method, tc.path, body)
			if strings.Contains(tc.path, "/realtime") {
				req.Header.Set("Connection", "Upgrade")
				req.Header.Set("Upgrade", "websocket")
			}
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			assert.Equal(t, tc.statusCode, rec.Code)
			assert.Equal(t, int32(0), body.reads.Load(), "unsupported classifier must run before body read")
			assert.NotContains(t, rec.Body.String(), "request_id", "request id must remain header-only")

			var envelope RouterErrorEnvelope
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
			assert.Equal(t, "router_error", envelope.Error.Type)
			assert.Equal(t, tc.errorCode, envelope.Error.Code)

			var records []domain.RequestRecord
			require.EventuallyWithT(t, func(c *assert.CollectT) {
				var err error
				records, err = recordRepo.Query(context.Background(), core.QueryParams{Limit: 10})
				require.NoError(c, err)
				require.Len(c, records, 1)
			}, time.Second, 10*time.Millisecond)
			assert.Nil(t, records[0].UpstreamAccountID)
			assert.Equal(t, tc.method, records[0].Method)
			assert.Equal(t, tc.path, records[0].Path)
			assert.Equal(t, tc.statusCode, records[0].StatusCode)
			assert.Equal(t, domain.OutcomeRouterError, records[0].Outcome)
			require.NotNil(t, records[0].ErrorCode)
			assert.Equal(t, tc.errorCode, *records[0].ErrorCode)
			assert.Nil(t, records[0].ClientRequestBody)
			assert.Nil(t, records[0].UpstreamRequestBody)
			assert.Nil(t, records[0].UpstreamResponseBody)
		})
	}
}

func newUnsupportedProxyHandler(t *testing.T) (*ProxyHandler, *store.RequestRecordRepo, func()) {
	t.Helper()

	s, err := store.New("sqlite3", ":memory:", 1, 1)
	require.NoError(t, err)
	require.NoError(t, s.Migrate("sqlite3"))

	recordRepo := store.NewRequestRecordRepo(s.Engine())
	recorder := core.NewRequestRecorder(recordRepo, slog.New(slog.NewTextHandler(io.Discard, nil)))
	recorder.Start()

	selector := core.NewAccountSelector(failingProxyAccountRepo{}, nil)
	handler := NewProxyHandler(
		selector,
		recorder,
		openai.NewClient(5*time.Second),
		true,
		0,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	var closed atomic.Bool
	closeFn := func() {
		if !closed.CompareAndSwap(false, true) {
			return
		}
		_ = recorder.Close(context.Background())
		_ = s.Close()
	}
	t.Cleanup(closeFn)

	return handler, recordRepo, closeFn
}

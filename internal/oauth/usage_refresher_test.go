package oauth

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/provider/openai"
	"github.com/user/one-llm-router/internal/store"
)

type usageRefresherRecorder struct {
	snapshot store.UsageSnapshot
}

func (s *usageRefresherRecorder) InsertUpstreamAccount(context.Context, *domain.UpstreamAccount) (int64, error) {
	return 0, nil
}

func (s *usageRefresherRecorder) GetByID(context.Context, int64) (*domain.UpstreamAccount, error) {
	return nil, domain.ErrAccountNotFound
}

func (s *usageRefresherRecorder) UpdateCredentials(context.Context, int64, store.CredentialPatch) error {
	return nil
}

func (s *usageRefresherRecorder) UpdateStatusIfCurrent(context.Context, int64, string, time.Time) error {
	return nil
}

func (s *usageRefresherRecorder) UpdateUsage(_ context.Context, _ int64, snapshot store.UsageSnapshot) error {
	s.snapshot = snapshot
	return nil
}

func (s *usageRefresherRecorder) ListActive(context.Context) ([]domain.UpstreamAccount, error) {
	return nil, nil
}

func TestUsageRefresherRefreshOnePersistsWindowDeadline(t *testing.T) {
	// Real ChatGPT backend /wham/usage payload shape (limit_window_seconds
	// and reset_at are the authoritative window deadline fields).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/wham/usage", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"user_id":"user-1",
			"rate_limit":{
				"primary_window":{"used_percent":28,"limit_window_seconds":7200,"reset_at":1755123456},
				"secondary_window":{"used_percent":10,"limit_window_seconds":86400,"reset_at":1755163056}
			}
		}`))
	}))
	t.Cleanup(srv.Close)

	now := time.Date(2026, 4, 21, 16, 0, 0, 0, time.UTC)
	acc := &domain.UpstreamAccount{
		ID:              7,
		Name:            "deadline-oauth",
		Provider:        domain.ProviderOpenAI,
		Status:          domain.AccountStatusActive,
		AuthMethod:      domain.AuthMethodOAuthBrowser,
		AccessToken:     []byte("access-token"),
		RefreshToken:    []byte("refresh-token"),
		IDToken:         []byte("id-token"),
		LastRefresh:     &now,
		AccessExpiresAt: ptrTime(now.Add(2 * time.Hour)),
	}
	require.NoError(t, acc.Validate())

	coord := NewCoordinatorWithClock(NewFakeClock(now), &openAIProvider{}, newTestLogger(&bytes.Buffer{}, slog.LevelError))
	storeRec := &usageRefresherRecorder{}

	client := openai.NewClient(30 * time.Second)
	client.SetCodexBackendBaseURLForTest(srv.URL)

	refresher := NewUsageRefresher(storeRec, client, coord, newTestLogger(&bytes.Buffer{}, slog.LevelError), time.Hour)
	refresher.refreshOne(context.Background(), *acc)

	require.NotNil(t, storeRec.snapshot.PrimaryUsedPercent)
	assert.Equal(t, 28.0, *storeRec.snapshot.PrimaryUsedPercent)
	require.NotNil(t, storeRec.snapshot.PrimaryResetAt)
	assert.Equal(t, time.Unix(1755123456, 0).UTC(), *storeRec.snapshot.PrimaryResetAt)
	require.NotNil(t, storeRec.snapshot.PrimaryWindowSeconds)
	assert.Equal(t, int64(7200), *storeRec.snapshot.PrimaryWindowSeconds)

	require.NotNil(t, storeRec.snapshot.SecondaryUsedPercent)
	assert.Equal(t, 10.0, *storeRec.snapshot.SecondaryUsedPercent)
	require.NotNil(t, storeRec.snapshot.SecondaryResetAt)
	assert.Equal(t, time.Unix(1755163056, 0).UTC(), *storeRec.snapshot.SecondaryResetAt)
	require.NotNil(t, storeRec.snapshot.SecondaryWindowSeconds)
	assert.Equal(t, int64(86400), *storeRec.snapshot.SecondaryWindowSeconds)
}

func TestUsageRefresherSparsePayloadKeepsWindowsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rate_limit":{"primary_window":{"used_percent":28},"secondary_window":null}}`))
	}))
	t.Cleanup(srv.Close)

	now := time.Date(2026, 4, 21, 16, 0, 0, 0, time.UTC)
	acc := &domain.UpstreamAccount{
		ID:              8,
		Name:            "sparse-oauth",
		Provider:        domain.ProviderOpenAI,
		Status:          domain.AccountStatusActive,
		AuthMethod:      domain.AuthMethodOAuthDevice,
		AccessToken:     []byte("access-token"),
		RefreshToken:    []byte("refresh-token"),
		IDToken:         []byte("id-token"),
		LastRefresh:     &now,
		AccessExpiresAt: ptrTime(now.Add(2 * time.Hour)),
	}
	require.NoError(t, acc.Validate())

	coord := NewCoordinatorWithClock(NewFakeClock(now), &openAIProvider{}, newTestLogger(&bytes.Buffer{}, slog.LevelError))
	storeRec := &usageRefresherRecorder{}

	client := openai.NewClient(30 * time.Second)
	client.SetCodexBackendBaseURLForTest(srv.URL)

	refresher := NewUsageRefresher(storeRec, client, coord, newTestLogger(&bytes.Buffer{}, slog.LevelError), time.Hour)
	refresher.refreshOne(context.Background(), *acc)

	require.NotNil(t, storeRec.snapshot.PrimaryUsedPercent)
	assert.Equal(t, 28.0, *storeRec.snapshot.PrimaryUsedPercent)
	assert.Nil(t, storeRec.snapshot.PrimaryResetAt)
	assert.Nil(t, storeRec.snapshot.PrimaryWindowSeconds)
	assert.Nil(t, storeRec.snapshot.SecondaryUsedPercent)
	assert.Nil(t, storeRec.snapshot.SecondaryResetAt)
	assert.Nil(t, storeRec.snapshot.SecondaryWindowSeconds)
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

func TestUsageRefresherProbesResetAnchorWhenUnanchored(t *testing.T) {
	var usageHits, probeHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wham/usage":
			usageHits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"rate_limit":null}`))
		case "/codex/responses":
			probeHits.Add(1)
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "Bearer access-token", r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"probe"}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	now := time.Date(2026, 4, 21, 16, 0, 0, 0, time.UTC)
	acc := &domain.UpstreamAccount{
		ID:              9,
		Name:            "unanchored-oauth",
		Provider:        domain.ProviderOpenAI,
		Status:          domain.AccountStatusActive,
		AuthMethod:      domain.AuthMethodOAuthImport,
		AccessToken:     []byte("access-token"),
		RefreshToken:    []byte("refresh-token"),
		IDToken:         []byte("id-token"),
		LastRefresh:     &now,
		AccessExpiresAt: ptrTime(now.Add(2 * time.Hour)),
	}
	require.NoError(t, acc.Validate())

	coord := NewCoordinatorWithClock(NewFakeClock(now), &openAIProvider{}, newTestLogger(&bytes.Buffer{}, slog.LevelError))
	storeRec := &usageRefresherRecorder{}

	client := openai.NewClient(30 * time.Second)
	client.SetCodexBackendBaseURLForTest(srv.URL)

	refresher := NewUsageRefresher(storeRec, client, coord, newTestLogger(&bytes.Buffer{}, slog.LevelError), time.Hour)
	refresher.refreshOne(context.Background(), *acc)

	assert.Equal(t, int32(1), usageHits.Load(), "usage fetch must run once")
	assert.Equal(t, int32(1), probeHits.Load(), "unanchored window must trigger one reset-anchor probe")
}

func TestUsageRefresherSkipsProbeWhenAnchored(t *testing.T) {
	var probeHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wham/usage":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"rate_limit":{
					"primary_window":{"used_percent":5,"limit_window_seconds":18000,"reset_at":1755123456},
					"secondary_window":null
				}
			}`))
		case "/codex/responses":
			probeHits.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	now := time.Date(2026, 4, 21, 16, 0, 0, 0, time.UTC)
	acc := &domain.UpstreamAccount{
		ID:              10,
		Name:            "anchored-oauth",
		Provider:        domain.ProviderOpenAI,
		Status:          domain.AccountStatusActive,
		AuthMethod:      domain.AuthMethodOAuthBrowser,
		AccessToken:     []byte("access-token"),
		RefreshToken:    []byte("refresh-token"),
		IDToken:         []byte("id-token"),
		LastRefresh:     &now,
		AccessExpiresAt: ptrTime(now.Add(2 * time.Hour)),
	}
	require.NoError(t, acc.Validate())

	coord := NewCoordinatorWithClock(NewFakeClock(now), &openAIProvider{}, newTestLogger(&bytes.Buffer{}, slog.LevelError))
	storeRec := &usageRefresherRecorder{}

	client := openai.NewClient(30 * time.Second)
	client.SetCodexBackendBaseURLForTest(srv.URL)

	refresher := NewUsageRefresher(storeRec, client, coord, newTestLogger(&bytes.Buffer{}, slog.LevelError), time.Hour)
	refresher.refreshOne(context.Background(), *acc)

	assert.Zero(t, probeHits.Load(), "anchored window must NOT trigger a reset-anchor probe")
	require.NotNil(t, storeRec.snapshot.PrimaryResetAt)
}

func TestUsageResetFixed(t *testing.T) {
	now := time.Date(2026, 4, 21, 16, 0, 0, 0, time.UTC)
	period := int64(18000) // 5h
	cases := []struct {
		name  string
		usage *openai.UsageResponse
		want  bool
	}{
		{
			name:  "nil rate limit is not fixed",
			usage: &openai.UsageResponse{RateLimit: nil},
			want:  false,
		},
		{
			name: "used percent above zero is fixed fast path",
			usage: &openai.UsageResponse{RateLimit: &openai.UsageRateLimit{
				PrimaryWindow:   &openai.UsageWindow{UsedPercent: 5, LimitWindowSeconds: &period},
				SecondaryWindow: nil,
			}},
			want: true,
		},
		{
			name: "nominal rolling reset (now + period) is not fixed",
			usage: &openai.UsageResponse{RateLimit: &openai.UsageRateLimit{
				PrimaryWindow: &openai.UsageWindow{
					UsedPercent:        0,
					LimitWindowSeconds: &period,
					ResetAt:            ptrInt64(now.Add(5 * time.Hour).Unix()),
				},
				SecondaryWindow: nil,
			}},
			want: false,
		},
		{
			name: "reset anchored earlier (before full period) is fixed",
			usage: &openai.UsageResponse{RateLimit: &openai.UsageRateLimit{
				PrimaryWindow: &openai.UsageWindow{
					UsedPercent:        0,
					LimitWindowSeconds: &period,
					ResetAt:            ptrInt64(now.Add(3 * time.Hour).Unix()), // anchored 2h ago
				},
				SecondaryWindow: nil,
			}},
			want: true,
		},
		{
			name: "secondary nominal rolling reset is not fixed",
			usage: &openai.UsageResponse{RateLimit: &openai.UsageRateLimit{
				PrimaryWindow: &openai.UsageWindow{
					UsedPercent:        0,
					LimitWindowSeconds: &period,
					ResetAt:            ptrInt64(now.Add(5 * time.Hour).Unix()),
				},
				SecondaryWindow: &openai.UsageWindow{
					UsedPercent:        0,
					LimitWindowSeconds: ptrInt64(604800),
					ResetAt:            ptrInt64(now.Add(7 * 24 * time.Hour).Unix()),
				},
			}},
			want: false,
		},
		{
			name: "primary rolling with anchored secondary is not fixed",
			usage: &openai.UsageResponse{RateLimit: &openai.UsageRateLimit{
				PrimaryWindow: &openai.UsageWindow{
					UsedPercent:        0,
					LimitWindowSeconds: &period,
					ResetAt:            ptrInt64(now.Add(5 * time.Hour).Unix()),
				},
				SecondaryWindow: &openai.UsageWindow{
					UsedPercent:        27,
					LimitWindowSeconds: ptrInt64(604800),
					ResetAt:            ptrInt64(now.Add(5 * 24 * time.Hour).Unix()),
				},
			}},
			want: false,
		},
		{
			name: "anchored primary with absent secondary is fixed",
			usage: &openai.UsageResponse{RateLimit: &openai.UsageRateLimit{
				PrimaryWindow: &openai.UsageWindow{
					UsedPercent:        5,
					LimitWindowSeconds: &period,
					ResetAt:            ptrInt64(now.Add(4 * time.Hour).Unix()),
				},
				SecondaryWindow: nil,
			}},
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, usageResetFixed(tc.usage, now))
		})
	}
}

func TestUsageRefresherProbesRollingWindowWithNominalReset(t *testing.T) {
	now := time.Now().UTC()
	period := int64(18000) // 5h
	var probeHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wham/usage":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"rate_limit":{
				"primary_window":{"used_percent":0,"limit_window_seconds":%d,"reset_at":%d},
				"secondary_window":null
			}}`, period, now.Add(5*time.Hour).Unix())
		case "/codex/responses":
			probeHits.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	acc := &domain.UpstreamAccount{
		ID:              11,
		Name:            "rolling-oauth",
		Provider:        domain.ProviderOpenAI,
		Status:          domain.AccountStatusActive,
		AuthMethod:      domain.AuthMethodOAuthDevice,
		AccessToken:     []byte("access-token"),
		RefreshToken:    []byte("refresh-token"),
		IDToken:         []byte("id-token"),
		LastRefresh:     &now,
		AccessExpiresAt: ptrTime(now.Add(2 * time.Hour)),
	}
	require.NoError(t, acc.Validate())

	coord := NewCoordinatorWithClock(NewFakeClock(now), &openAIProvider{}, newTestLogger(&bytes.Buffer{}, slog.LevelError))
	storeRec := &usageRefresherRecorder{}

	client := openai.NewClient(30 * time.Second)
	client.SetCodexBackendBaseURLForTest(srv.URL)

	refresher := NewUsageRefresher(storeRec, client, coord, newTestLogger(&bytes.Buffer{}, slog.LevelError), time.Hour)
	refresher.refreshOne(context.Background(), *acc)

	assert.Equal(t, int32(1), probeHits.Load(), "nominal rolling reset (now+period) must trigger a probe")
}

func TestUsageRefresherSkipsProbeWhenResetAnchoredEarlier(t *testing.T) {
	now := time.Now().UTC()
	period := int64(18000) // 5h
	var probeHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wham/usage":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"rate_limit":{
				"primary_window":{"used_percent":0,"limit_window_seconds":%d,"reset_at":%d},
				"secondary_window":null
			}}`, period, now.Add(3*time.Hour).Unix()) // anchored 2h ago
		case "/codex/responses":
			probeHits.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	acc := &domain.UpstreamAccount{
		ID:              12,
		Name:            "fixed-oauth",
		Provider:        domain.ProviderOpenAI,
		Status:          domain.AccountStatusActive,
		AuthMethod:      domain.AuthMethodOAuthBrowser,
		AccessToken:     []byte("access-token"),
		RefreshToken:    []byte("refresh-token"),
		IDToken:         []byte("id-token"),
		LastRefresh:     &now,
		AccessExpiresAt: ptrTime(now.Add(2 * time.Hour)),
	}
	require.NoError(t, acc.Validate())

	coord := NewCoordinatorWithClock(NewFakeClock(now), &openAIProvider{}, newTestLogger(&bytes.Buffer{}, slog.LevelError))
	storeRec := &usageRefresherRecorder{}

	client := openai.NewClient(30 * time.Second)
	client.SetCodexBackendBaseURLForTest(srv.URL)

	refresher := NewUsageRefresher(storeRec, client, coord, newTestLogger(&bytes.Buffer{}, slog.LevelError), time.Hour)
	refresher.refreshOne(context.Background(), *acc)

	assert.Zero(t, probeHits.Load(), "reset anchored earlier than a full period must NOT trigger a probe")
}

func ptrInt64(v int64) *int64 { return &v }

func TestUsageRefresherProbesWhenPrimaryRollingButSecondaryUsed(t *testing.T) {
	now := time.Now().UTC()
	period := int64(18000) // 5h
	var probeHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wham/usage":
			w.Header().Set("Content-Type", "application/json")
			// primary rolling (used 0, reset = now + 5h), secondary anchored (used 27).
			_, _ = fmt.Fprintf(w, `{"rate_limit":{
				"primary_window":{"used_percent":0,"limit_window_seconds":%d,"reset_at":%d},
				"secondary_window":{"used_percent":27,"limit_window_seconds":604800,"reset_at":%d}
			}}`, period, now.Add(5*time.Hour).Unix(), now.Add(5*24*time.Hour).Unix())
		case "/codex/responses":
			probeHits.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	acc := &domain.UpstreamAccount{
		ID:              13,
		Name:            "primary-rolling-oauth",
		Provider:        domain.ProviderOpenAI,
		Status:          domain.AccountStatusActive,
		AuthMethod:      domain.AuthMethodOAuthDevice,
		AccessToken:     []byte("access-token"),
		RefreshToken:    []byte("refresh-token"),
		IDToken:         []byte("id-token"),
		LastRefresh:     &now,
		AccessExpiresAt: ptrTime(now.Add(2 * time.Hour)),
	}
	require.NoError(t, acc.Validate())

	coord := NewCoordinatorWithClock(NewFakeClock(now), &openAIProvider{}, newTestLogger(&bytes.Buffer{}, slog.LevelError))
	storeRec := &usageRefresherRecorder{}

	client := openai.NewClient(30 * time.Second)
	client.SetCodexBackendBaseURLForTest(srv.URL)

	refresher := NewUsageRefresher(storeRec, client, coord, newTestLogger(&bytes.Buffer{}, slog.LevelError), time.Hour)
	refresher.refreshOne(context.Background(), *acc)

	assert.Equal(t, int32(1), probeHits.Load(),
		"primary rolling (used 0) must trigger a probe even when the secondary is anchored")
}

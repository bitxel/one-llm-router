package exportapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/user/one-llm-router/internal/api/adminapi"

	"github.com/user/one-llm-router/internal/api/testutil"
	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/oauth"
	"github.com/user/one-llm-router/internal/requestid"
	"github.com/user/one-llm-router/internal/store"
)

func TestExportAuthJSON(t *testing.T) {
	t.Run("happy path streams auth.json attachment with fixed headers", func(t *testing.T) {
		h := newExportHarness(t)
		account := h.seedOAuthAccount(t, "exported@example.com", "acct_export_123")

		rec := h.post(t, "/api/admin/accounts/1/export-auth-json")

		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, exportSuccessContentType, rec.Header().Get("Content-Type"))
		assert.Equal(t, exportSuccessCacheControl, rec.Header().Get("Cache-Control"))
		assert.Equal(t, exportSuccessContentDisp, rec.Header().Get("Content-Disposition"))
		assert.Equal(t, exportSuccessNoSniff, rec.Header().Get("X-Content-Type-Options"))
		assert.Equal(t, h.requestID, rec.Header().Get("X-Request-Id"))

		var body map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		assert.Contains(t, body, "OPENAI_API_KEY")
		assert.Nil(t, body["OPENAI_API_KEY"])
		gotLastRefresh, err := time.Parse(time.RFC3339, body["last_refresh"].(string))
		require.NoError(t, err)
		assert.Equal(t, account.LastRefresh.UTC(), gotLastRefresh.UTC())

		tokens, ok := body["tokens"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, string(account.AccessToken), tokens["access_token"])
		assert.Equal(t, string(account.RefreshToken), tokens["refresh_token"])
		assert.Equal(t, string(account.IDToken), tokens["id_token"])
		assert.Equal(t, "acct_export_123", tokens["account_id"])

		logs := h.logs.String()
		assert.Contains(t, logs, "level=WARN")
		assert.Contains(t, logs, exportedEventName)
		assert.Contains(t, logs, "request_id="+h.requestID)
		assert.Contains(t, logs, "account_id=1")
		assert.Contains(t, logs, "operator_id="+exportAnonymousOperatorID)
		assert.Contains(t, logs, "email=exported@example.com")
		assert.NotContains(t, logs, string(account.AccessToken))
		assert.NotContains(t, logs, string(account.RefreshToken))
		assert.NotContains(t, logs, string(account.IDToken))
	})

	t.Run("api key rows return not_oauth_account envelope", func(t *testing.T) {
		h := newExportHarness(t)
		h.seedAPIKeyAccount(t, "sk-export-backstop")

		rec := h.post(t, "/api/admin/accounts/1/export-auth-json")

		data := testutil.AssertEnvelopeDataShape(t, rec, 3014)
		assert.Equal(t, exportSuccessContentType, rec.Header().Get("Content-Type"))
		assert.Empty(t, rec.Header().Get("Content-Disposition"))
		assert.Equal(t, string(domain.AuthMethodAPIKey), data["auth_method"])
		assert.Contains(t, h.logs.String(), exportRejectedEventName)
		assert.Contains(t, h.logs.String(), "error_code=not_oauth_account")
	})

	t.Run("missing row reuses account_not_found", func(t *testing.T) {
		h := newExportHarness(t)

		rec := h.post(t, "/api/admin/accounts/999/export-auth-json")

		data := testutil.AssertEnvelopeDataShape(t, rec, 1001)
		assert.Empty(t, data)
		assert.Empty(t, rec.Header().Get("Content-Disposition"))
		assert.Contains(t, h.logs.String(), exportRejectedEventName)
		assert.Contains(t, h.logs.String(), "error_code=account_not_found")
	})

	t.Run("deleted oauth row is treated as account_not_found", func(t *testing.T) {
		h := newExportHarness(t)
		account := h.seedOAuthAccount(t, "deleted@example.com", "acct_deleted_123")
		_, err := h.store.Engine().ID(account.ID).Cols("status").Update(&domain.UpstreamAccount{
			Status: domain.AccountStatusDeleted,
		})
		require.NoError(t, err)

		rec := h.post(t, "/api/admin/accounts/1/export-auth-json")

		data := testutil.AssertEnvelopeDataShape(t, rec, 1001)
		assert.Empty(t, data)
		assert.Empty(t, rec.Header().Get("Content-Disposition"))
		assert.Contains(t, h.logs.String(), exportRejectedEventName)
		assert.Contains(t, h.logs.String(), "error_code=account_not_found")
	})

	t.Run("store read failure is oauth_export_read_failed", func(t *testing.T) {
		logs := &bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
		handler := NewExportAuthJSONHandler(failingExportStore{err: errors.New("read blew up")}, logger)
		mux := http.NewServeMux()
		RegisterExportAuthJSONHandler(mux, handler, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/admin/accounts/7/export-auth-json", nil)
		req = req.WithContext(requestid.WithContext(req.Context(), "req_export_fail"))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		testutil.AssertEnvelope(t, rec, 3902)
		assert.Empty(t, rec.Header().Get("Content-Disposition"))
		assert.Contains(t, logs.String(), "read export auth.json payload failed")
		assert.Contains(t, logs.String(), "request_id=req_export_fail")
		assert.Contains(t, logs.String(), "account_id=7")
	})

	t.Run("corrupted oauth row is a system error, not not_oauth_account", func(t *testing.T) {
		h := newExportHarness(t)
		account := h.seedOAuthAccount(t, "corrupt@example.com", "acct_corrupt_123")
		_, err := h.store.Engine().ID(account.ID).Cols("access_token").Update(&domain.UpstreamAccount{
			AccessToken: nil,
		})
		require.NoError(t, err)

		rec := h.post(t, "/api/admin/accounts/1/export-auth-json")

		testutil.AssertEnvelope(t, rec, 3902)
		assert.Empty(t, rec.Header().Get("Content-Disposition"))
		assert.Contains(t, h.logs.String(), "corrupted oauth row")
		assert.Contains(t, h.logs.String(), "request_id="+h.requestID)
		assert.Contains(t, h.logs.String(), "account_id=1")
	})

	t.Run("round trip fidelity preserves top-level null and token payload", func(t *testing.T) {
		h := newExportHarness(t)
		h.seedOAuthAccount(t, "", "")

		rec := h.post(t, "/api/admin/accounts/1/export-auth-json")

		var doc struct {
			OpenAIAPIKey *string `json:"OPENAI_API_KEY"`
			Tokens       struct {
				AccessToken  string  `json:"access_token"`
				RefreshToken string  `json:"refresh_token"`
				IDToken      string  `json:"id_token"`
				AccountID    *string `json:"account_id"`
			} `json:"tokens"`
			LastRefresh time.Time `json:"last_refresh"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &doc))
		assert.Nil(t, doc.OpenAIAPIKey)
		assert.Equal(t, "access-export-123", doc.Tokens.AccessToken)
		assert.Equal(t, "refresh-export-456", doc.Tokens.RefreshToken)
		assert.Equal(t, "header."+mustJWTClaims(t, map[string]any{"email": ""})+".sig", doc.Tokens.IDToken)
		assert.Nil(t, doc.Tokens.AccountID)
		assert.Equal(t, time.Date(2026, 4, 22, 7, 5, 0, 0, time.UTC), doc.LastRefresh.UTC())
	})

	t.Run("exported auth.json round-trips through import contract", func(t *testing.T) {
		h := newExportHarness(t)
		source := h.seedOAuthAccount(t, "interop@example.com", "acct_interop_123")

		exportRec := h.post(t, "/api/admin/accounts/1/export-auth-json")
		require.Equal(t, http.StatusOK, exportRec.Code)

		importLogs := &bytes.Buffer{}
		importLogger := slog.New(slog.NewTextHandler(importLogs, &slog.HandlerOptions{Level: slog.LevelDebug}))
		importer := oauth.NewAuthJSONImportService(h.repo, func() time.Time {
			return time.Date(2026, 4, 22, 8, 0, 0, 0, time.UTC)
		})
		importMux := http.NewServeMux()
		adminapi.RegisterImportAuthJSONHandler(importMux, adminapi.NewImportAuthJSONHandler(importer, importLogger), nil)

		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("auth_json", "auth.json")
		require.NoError(t, err)
		_, err = part.Write(exportRec.Body.Bytes())
		require.NoError(t, err)
		require.NoError(t, writer.Close())

		importReq := httptest.NewRequest(http.MethodPost, adminapi.ImportAuthJSONRoute, &body)
		importReq.Header.Set("Content-Type", writer.FormDataContentType())
		importReq = importReq.WithContext(requestid.WithContext(importReq.Context(), "req_export_import"))
		importRec := httptest.NewRecorder()
		importMux.ServeHTTP(importRec, importReq)

		data := testutil.AssertEnvelopeDataShape(t, importRec, 0)
		accountData, ok := data["account"].(map[string]any)
		require.True(t, ok)
		importedID, ok := accountData["id"].(float64)
		require.True(t, ok)
		assert.NotEqual(t, float64(source.ID), importedID)
		assert.Equal(t, "oauth_import", accountData["auth_method"])
		assert.Equal(t, "interop@example.com", accountData["email"])
		assert.Equal(t, "acct_interop_123", accountData["chatgpt_account_id"])

		imported, err := h.repo.GetForExport(context.Background(), int64(importedID))
		require.NoError(t, err)
		assert.Equal(t, source.AccessToken, imported.AccessToken)
		assert.Equal(t, source.RefreshToken, imported.RefreshToken)
		assert.Equal(t, source.IDToken, imported.IDToken)
		require.NotNil(t, imported.ChatGPTAccountID)
		assert.Equal(t, "acct_interop_123", *imported.ChatGPTAccountID)
		require.NotNil(t, imported.LastRefresh)
		assert.Equal(t, source.LastRefresh.UTC(), imported.LastRefresh.UTC())
	})
}

type exportHarness struct {
	store     *store.Store
	repo      *store.AccountRepo
	server    http.Handler
	logs      *bytes.Buffer
	requestID string
}

func newExportHarness(t *testing.T) *exportHarness {
	t.Helper()

	s, err := store.New("sqlite3", ":memory:", 1, 1)
	require.NoError(t, err)
	require.NoError(t, s.Migrate("sqlite3"))

	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	repo := store.NewAccountRepo(s.Engine())
	mux := http.NewServeMux()
	RegisterExportAuthJSONHandler(mux, NewExportAuthJSONHandler(repo, logger), nil)

	t.Cleanup(func() {
		_ = s.Close()
	})

	return &exportHarness{
		store:     s,
		repo:      repo,
		server:    mux,
		logs:      logs,
		requestID: "req_export_contract",
	}
}

func (h *exportHarness) post(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, path, nil)
	req = req.WithContext(requestid.WithContext(req.Context(), h.requestID))
	rec := httptest.NewRecorder()
	h.server.ServeHTTP(rec, req)
	return rec
}

func (h *exportHarness) seedAPIKeyAccount(t *testing.T, key string) *domain.UpstreamAccount {
	t.Helper()

	account := &domain.UpstreamAccount{
		Name:       "api-key-export",
		Provider:   domain.ProviderOpenAI,
		APIKey:     key,
		Status:     domain.AccountStatusActive,
		AuthMethod: domain.AuthMethodAPIKey,
	}
	require.NoError(t, h.repo.Create(context.Background(), account))
	return account
}

func (h *exportHarness) seedOAuthAccount(t *testing.T, email, accountID string) *domain.UpstreamAccount {
	t.Helper()

	lastRefresh := time.Date(2026, 4, 22, 7, 5, 0, 0, time.UTC)
	expiresAt := lastRefresh.Add(90 * time.Minute)
	idToken := "header." + mustJWTClaims(t, map[string]any{"email": email}) + ".sig"

	var accountIDPtr *string
	if accountID != "" {
		accountIDPtr = &accountID
	}

	account := &domain.UpstreamAccount{
		Name:             "oauth-export",
		Provider:         domain.ProviderOpenAI,
		Status:           domain.AccountStatusActive,
		AuthMethod:       domain.AuthMethodOAuthImport,
		AccessToken:      []byte("access-export-123"),
		RefreshToken:     []byte("refresh-export-456"),
		IDToken:          []byte(idToken),
		LastRefresh:      &lastRefresh,
		AccessExpiresAt:  &expiresAt,
		Email:            stringPointer(email),
		ChatGPTAccountID: accountIDPtr,
	}
	_, err := h.repo.InsertUpstreamAccount(context.Background(), account)
	require.NoError(t, err)
	return account
}

type failingExportStore struct {
	err error
}

func (f failingExportStore) GetForExport(context.Context, int64) (*store.ExportPayload, error) {
	return nil, f.err
}

func (f failingExportStore) GetProjectionByID(context.Context, int64) (*domain.UpstreamAccount, error) {
	return nil, f.err
}

func mustJWTClaims(t *testing.T, claims map[string]any) string {
	t.Helper()

	raw, err := json.Marshal(claims)
	require.NoError(t, err)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func stringPointer(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

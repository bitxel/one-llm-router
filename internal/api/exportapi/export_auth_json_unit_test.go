package exportapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/one-llm-router/internal/api/errcode"
	"github.com/user/one-llm-router/internal/api/testutil"
	"github.com/user/one-llm-router/internal/domain"
	generatedadminapi "github.com/user/one-llm-router/internal/generated/adminapi"
	"github.com/user/one-llm-router/internal/requestid"
	"github.com/user/one-llm-router/internal/store"
)

type exportLookupStoreStub struct {
	row    *domain.UpstreamAccount
	rowErr error
}

type failingExportResponse struct{}

func (s exportLookupStoreStub) GetForExport(context.Context, int64) (*store.ExportPayload, error) {
	return nil, domain.ErrInvalidAccountShape
}

func (s exportLookupStoreStub) GetProjectionByID(context.Context, int64) (*domain.UpstreamAccount, error) {
	return s.row, s.rowErr
}

func (failingExportResponse) VisitAccountsExportAuthJSONResponse(http.ResponseWriter) error {
	return errors.New("response write failed")
}

func TestExportAuthJSONHelpers(t *testing.T) {
	t.Run("new handler and logger fallback", func(t *testing.T) {
		h := NewExportAuthJSONHandler(nil, nil)
		require.NotNil(t, h)
		assert.NotNil(t, exportLogger(h))
		assert.NotNil(t, exportLogger(nil))
	})

	t.Run("parseExportAccountID validates nil missing invalid and happy path", func(t *testing.T) {
		_, err := parseExportAccountID(nil)
		require.Error(t, err)

		req := httptest.NewRequest(http.MethodPost, "/api/admin/accounts//export-auth-json", nil)
		_, err = parseExportAccountID(req)
		require.Error(t, err)

		req.SetPathValue("id", "bogus")
		_, err = parseExportAccountID(req)
		require.Error(t, err)

		req.SetPathValue("id", "7")
		id, err := parseExportAccountID(req)
		require.NoError(t, err)
		assert.Equal(t, int64(7), id)
	})

	t.Run("buildCodexAuthJSONDocument handles nil and preserves fields", func(t *testing.T) {
		doc := buildCodexAuthJSONDocument(nil)
		assert.Empty(t, doc.Tokens.AccessToken)
		assert.Nil(t, doc.LastRefresh)

		accountID := "acct_export_123"
		lastRefresh := timeRef(2026, 4, 22, 9, 0)
		doc = buildCodexAuthJSONDocument(&store.ExportPayload{
			AccessToken:      []byte("acc"),
			RefreshToken:     []byte("ref"),
			IDToken:          []byte("jwt"),
			ChatGPTAccountID: &accountID,
			LastRefresh:      lastRefresh,
		})
		assert.Equal(t, "acc", doc.Tokens.AccessToken)
		assert.Equal(t, "ref", doc.Tokens.RefreshToken)
		assert.Equal(t, "jwt", doc.Tokens.IDToken)
		require.NotNil(t, doc.Tokens.AccountID)
		assert.Equal(t, "acct_export_123", *doc.Tokens.AccountID)
		require.NotNil(t, doc.LastRefresh)
		assert.Equal(t, lastRefresh.UTC(), doc.LastRefresh.UTC())
	})

	t.Run("response builders and writer preserve envelope shape", func(t *testing.T) {
		okBody, err := exportSuccessBody([]byte(`{"OPENAI_API_KEY":null,"tokens":{"access_token":"a","refresh_token":"b","id_token":"c","account_id":null},"last_refresh":"2026-04-22T09:00:00Z"}`))
		require.NoError(t, err)

		notFound, err := accountNotFoundResponse()
		require.NoError(t, err)
		notOAuth, err := notOAuthAccountResponse()
		require.NoError(t, err)

		for _, response := range []interface {
			VisitAccountsExportAuthJSONResponse(http.ResponseWriter) error
		}{
			generatedadminapi.ExportAuthJSONAttachmentResponse{Body: okBody},
			notFound,
			notOAuth,
		} {
			rec := httptest.NewRecorder()
			err := writeExportResponseObject(rec, "req_export_helper", response)
			require.NoError(t, err)
			assert.Equal(t, "req_export_helper", rec.Header().Get("X-Request-Id"))
		}

		rec := httptest.NewRecorder()
		err = writeExportResponseObject(rec, "req_export_helper", failingExportResponse{})
		require.Error(t, err)
	})
}

func TestExportAuthJSONWriteLookupError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("deleted row maps to account_not_found", func(t *testing.T) {
		h := NewExportAuthJSONHandler(exportLookupStoreStub{
			row: &domain.UpstreamAccount{ID: 7, Status: domain.AccountStatusDeleted},
		}, logger)
		req := httptest.NewRequest(http.MethodPost, "/api/admin/accounts/7/export-auth-json", nil)
		req = req.WithContext(requestid.WithContext(req.Context(), "req_deleted"))
		rec := httptest.NewRecorder()

		h.writeExportLookupError(req.Context(), rec, 7, domain.ErrInvalidAccountShape)
		testutil.AssertEnvelopeDataShape(t, rec, errcode.AccountNotFound)
	})

	t.Run("api key row maps to not_oauth_account", func(t *testing.T) {
		h := NewExportAuthJSONHandler(exportLookupStoreStub{
			row: &domain.UpstreamAccount{ID: 8, AuthMethod: domain.AuthMethodAPIKey, Status: domain.AccountStatusActive},
		}, logger)
		req := httptest.NewRequest(http.MethodPost, "/api/admin/accounts/8/export-auth-json", nil)
		req = req.WithContext(requestid.WithContext(req.Context(), "req_api_key"))
		rec := httptest.NewRecorder()

		h.writeExportLookupError(req.Context(), rec, 8, domain.ErrInvalidAccountShape)
		data := testutil.AssertEnvelopeDataShape(t, rec, errcode.NotOAuthAccount)
		assert.Equal(t, "api_key", data["auth_method"])
	})

	t.Run("corrupted oauth row and projection lookup failures stay system errors", func(t *testing.T) {
		h := NewExportAuthJSONHandler(exportLookupStoreStub{
			row: &domain.UpstreamAccount{ID: 9, AuthMethod: domain.AuthMethodOAuthBrowser, Status: domain.AccountStatusActive},
		}, logger)
		req := httptest.NewRequest(http.MethodPost, "/api/admin/accounts/9/export-auth-json", nil)
		req = req.WithContext(requestid.WithContext(req.Context(), "req_corrupt"))
		rec := httptest.NewRecorder()

		h.writeExportLookupError(req.Context(), rec, 9, domain.ErrInvalidAccountShape)
		testutil.AssertEnvelope(t, rec, errcode.OAuthExportReadFailed)

		h = NewExportAuthJSONHandler(exportLookupStoreStub{
			rowErr: errors.New("projection read failed"),
		}, logger)
		rec = httptest.NewRecorder()
		h.writeExportLookupError(req.Context(), rec, 9, domain.ErrInvalidAccountShape)
		testutil.AssertEnvelope(t, rec, errcode.OAuthExportReadFailed)
	})

	t.Run("projection not found after invalid shape stays system error", func(t *testing.T) {
		h := NewExportAuthJSONHandler(exportLookupStoreStub{
			rowErr: domain.ErrAccountNotFound,
		}, logger)
		req := httptest.NewRequest(http.MethodPost, "/api/admin/accounts/11/export-auth-json", nil)
		req = req.WithContext(requestid.WithContext(req.Context(), "req_projection_missing"))
		rec := httptest.NewRecorder()

		h.writeExportLookupError(req.Context(), rec, 11, domain.ErrInvalidAccountShape)
		testutil.AssertEnvelope(t, rec, errcode.OAuthExportReadFailed)
	})

	t.Run("account not found envelope write failure becomes oauth_internal_error", func(t *testing.T) {
		logs := &bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
		h := NewExportAuthJSONHandler(exportLookupStoreStub{}, logger)
		req := httptest.NewRequest(http.MethodPost, "/api/admin/accounts/12/export-auth-json", nil)
		req = req.WithContext(requestid.WithContext(req.Context(), "req_missing_write_fail"))
		writer := &failingHeaderWriter{header: http.Header{}}

		h.writeExportLookupError(req.Context(), writer, 12, domain.ErrAccountNotFound)
		assert.Equal(t, http.StatusInternalServerError, writer.status)
		assert.Contains(t, logs.String(), "write account_not_found export envelope")
		assert.Contains(t, logs.String(), "request_id=req_missing_write_fail")
		assert.Contains(t, logs.String(), "account_id=12")
	})
}

func TestExportAuthJSONServeHTTPEdgeCases(t *testing.T) {
	t.Run("misconfigured handler fails fast with oauth_internal_error", func(t *testing.T) {
		h := &ExportAuthJSONHandler{}
		req := httptest.NewRequest(http.MethodPost, "/api/admin/accounts/1/export-auth-json", nil)
		req = req.WithContext(requestid.WithContext(req.Context(), "req_nil_accounts"))
		rec := httptest.NewRecorder()

		h.ServeHTTP(rec, req)
		testutil.AssertEnvelope(t, rec, errcode.OAuthInternalError)
	})

	t.Run("invalid id is a business error", func(t *testing.T) {
		h := NewExportAuthJSONHandler(exportLookupStoreStub{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		req := httptest.NewRequest(http.MethodPost, "/api/admin/accounts/not-an-int/export-auth-json", nil)
		req.SetPathValue("id", "not-an-int")
		req = req.WithContext(requestid.WithContext(req.Context(), "req_bad_id"))
		rec := httptest.NewRecorder()

		h.ServeHTTP(rec, req)
		data := testutil.AssertEnvelopeDataShape(t, rec, errcode.InvalidAccountPayload)
		assert.Equal(t, "id", data["field"])
	})

	t.Run("invalid id token falls back to empty email and still streams attachment", func(t *testing.T) {
		logs := &bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
		accountID := "acct_bad_claims"
		lastRefresh := timeRef(2026, 4, 22, 12, 0)
		h := NewExportAuthJSONHandler(exportPayloadStoreStub{
			payload: &store.ExportPayload{
				AccountID:        12,
				AccessToken:      []byte("access-bad-claims"),
				RefreshToken:     []byte("refresh-bad-claims"),
				IDToken:          []byte("not-a-jwt"),
				ChatGPTAccountID: &accountID,
				LastRefresh:      lastRefresh,
			},
		}, logger)
		req := httptest.NewRequest(http.MethodPost, "/api/admin/accounts/12/export-auth-json", nil)
		req.SetPathValue("id", "12")
		req = req.WithContext(requestid.WithContext(req.Context(), "req_bad_claims"))
		rec := httptest.NewRecorder()

		h.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, exportSuccessContentDisp, rec.Header().Get("Content-Disposition"))
		assert.Contains(t, logs.String(), exportedEventName)
		assert.Contains(t, logs.String(), "request_id=req_bad_claims")
		assert.Contains(t, logs.String(), "account_id=12")
		assert.Contains(t, logs.String(), "email=")
		assert.NotContains(t, logs.String(), "email=exported@example.com")
	})

	t.Run("response writer failure after attachment headers becomes oauth_internal_error", func(t *testing.T) {
		logs := &bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
		accountID := "acct_fail_write"
		lastRefresh := timeRef(2026, 4, 22, 13, 0)
		h := NewExportAuthJSONHandler(exportPayloadStoreStub{
			payload: &store.ExportPayload{
				AccountID:        13,
				AccessToken:      []byte("access-write-fail"),
				RefreshToken:     []byte("refresh-write-fail"),
				IDToken:          []byte("header.eyJlbWFpbCI6IndyaXRlLWZhaWxAZXhhbXBsZS5jb20ifQ.sig"),
				ChatGPTAccountID: &accountID,
				LastRefresh:      lastRefresh,
			},
		}, logger)
		req := httptest.NewRequest(http.MethodPost, "/api/admin/accounts/13/export-auth-json", nil)
		req.SetPathValue("id", "13")
		req = req.WithContext(requestid.WithContext(req.Context(), "req_fail_write"))
		writer := &failingHeaderWriter{header: http.Header{}}

		h.ServeHTTP(writer, req)
		assert.Equal(t, http.StatusInternalServerError, writer.status)
		assert.Contains(t, logs.String(), "write export attachment response")
		assert.Contains(t, logs.String(), "request_id=req_fail_write")
		assert.Contains(t, logs.String(), "account_id=13")
	})
}

type exportPayloadStoreStub struct {
	payload *store.ExportPayload
	err     error
}

func (s exportPayloadStoreStub) GetForExport(context.Context, int64) (*store.ExportPayload, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.payload, nil
}

func (s exportPayloadStoreStub) GetProjectionByID(context.Context, int64) (*domain.UpstreamAccount, error) {
	return nil, domain.ErrAccountNotFound
}

type failingHeaderWriter struct {
	header http.Header
	status int
}

func (w *failingHeaderWriter) Header() http.Header {
	return w.header
}

func (w *failingHeaderWriter) WriteHeader(statusCode int) {
	w.status = statusCode
}

func (w *failingHeaderWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func timeRef(year int, month time.Month, day, hour, minute int) *time.Time {
	t := time.Date(year, month, day, hour, minute, 0, 0, time.UTC)
	return &t
}

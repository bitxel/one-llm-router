package adminapi

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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/one-llm-router/internal/api"
	"github.com/user/one-llm-router/internal/api/errcode"
	"github.com/user/one-llm-router/internal/api/testutil"
	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/oauth"
	"github.com/user/one-llm-router/internal/store"
)

type importAuthJSONHarness struct {
	mux    *http.ServeMux
	repo   *store.AccountRepo
	logs   *bytes.Buffer
	now    time.Time
	reqSeq int
	lastID string
}

func setupImportAuthJSONHarness(t *testing.T, now time.Time) *importAuthJSONHarness {
	t.Helper()

	s, err := store.New("sqlite3", ":memory:", 1, 1)
	require.NoError(t, err)
	require.NoError(t, s.Migrate("sqlite3"))
	t.Cleanup(func() { _ = s.Close() })

	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	repo := store.NewAccountRepo(s.Engine())
	handler := NewImportAuthJSONHandler(oauth.NewAuthJSONImportService(repo, func() time.Time { return now }), logger)

	mux := http.NewServeMux()
	RegisterImportAuthJSONHandler(mux, handler, nil)
	return &importAuthJSONHarness{
		mux:  mux,
		repo: repo,
		logs: logs,
		now:  now,
	}
}

func (h *importAuthJSONHarness) doMultipart(t *testing.T, fields map[string][]byte, filler map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, value := range filler {
		require.NoError(t, writer.WriteField(name, value))
	}
	for name, value := range fields {
		part, err := writer.CreateFormFile(name, name+".json")
		require.NoError(t, err)
		_, err = part.Write(value)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, ImportAuthJSONRoute, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req = req.WithContext(api.WithRequestID(req.Context(), h.nextRequestID()))

	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	return rec
}

func (h *importAuthJSONHarness) doRaw(t *testing.T, contentType string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, ImportAuthJSONRoute, bytes.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req = req.WithContext(api.WithRequestID(req.Context(), h.nextRequestID()))
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	return rec
}

func (h *importAuthJSONHarness) nextRequestID() string {
	h.reqSeq++
	h.lastID = "req-import-" + time.Date(2026, 4, 22, 0, 0, 0, h.reqSeq, time.UTC).Format("150405.000000000")
	return h.lastID
}

func TestImportAuthJSON(t *testing.T) {
	now := time.Date(2026, 4, 22, 20, 0, 0, 0, time.UTC)

	t.Run("happy path persists oauth_import row and derives account metadata", func(t *testing.T) {
		h := setupImportAuthJSONHarness(t, now)
		idToken := makeImportJWT(t, map[string]any{
			"email": "alice@example.com",
			"exp":   float64(now.Add(45 * time.Minute).Unix()),
			"https://api.openai.com/auth": map[string]any{
				"plan_type":          "chatgpt-plus",
				"chatgpt_account_id": "org_alice",
			},
		})
		rec := h.doMultipart(t, map[string][]byte{
			"auth_json": mustMarshalJSON(t, importAuthJSONFixture{
				AccessToken:  "acc-happy",
				RefreshToken: "ref-happy",
				IDToken:      idToken,
				AccountID:    "org_fallback",
				LastRefresh:  stringPtrTest("2026-04-10T09:00:00Z"),
			}),
		}, nil)

		data := testutil.AssertEnvelopeDataShape(t, rec, 0)
		accountData, ok := data["account"].(map[string]any)
		require.True(t, ok, "data.account must be object: %#v", data["account"])
		assert.Equal(t, "oauth_import", accountData["auth_method"])
		assert.Equal(t, "openai", accountData["provider"])
		assert.Equal(t, "alice@example.com", accountData["name"])
		assert.Equal(t, "alice@example.com", accountData["email"])
		assert.Equal(t, "chatgpt-plus", accountData["plan_type"])
		assert.Equal(t, "ChatGPT Plus", accountData["plan_type_label"])
		assert.Equal(t, "org_alice", accountData["chatgpt_account_id"])
		assert.NotContains(t, accountData, "api_key")
		assert.NotContains(t, accountData, "access_token")
		assert.NotContains(t, accountData, "refresh_token")
		assert.NotContains(t, accountData, "id_token")

		row, err := h.repo.GetByID(context.Background(), 1)
		require.NoError(t, err)
		assert.Equal(t, domain.AuthMethodOAuthImport, row.AuthMethod)
		assert.Equal(t, "alice@example.com", row.Name)
		assert.Equal(t, []byte("acc-happy"), row.AccessToken)
		assert.Equal(t, []byte("ref-happy"), row.RefreshToken)
		assert.Equal(t, []byte(idToken), row.IDToken)
		require.NotNil(t, row.LastRefresh)
		assert.Equal(t, time.Date(2026, 4, 10, 9, 0, 0, 0, time.UTC), row.LastRefresh.UTC())
		require.NotNil(t, row.AccessExpiresAt)
		assert.Equal(t, now.Add(45*time.Minute).UTC(), row.AccessExpiresAt.UTC())
		assert.Contains(t, h.logs.String(), "msg=account_created")
		assert.Contains(t, h.logs.String(), "request_id="+h.lastID)
		assert.Contains(t, h.logs.String(), "account_id=1")
		assert.NotContains(t, h.logs.String(), "msg=auth_json_import_last_refresh_fallback")
		assert.NotContains(t, h.logs.String(), "acc-happy")
		assert.NotContains(t, h.logs.String(), "ref-happy")
		assert.NotContains(t, h.logs.String(), idToken)
	})

	t.Run("json body with pasted auth_json persists oauth_import row", func(t *testing.T) {
		h := setupImportAuthJSONHarness(t, now)
		idToken := makeImportJWT(t, map[string]any{
			"email": "pasted@example.com",
			"exp":   float64(now.Add(30 * time.Minute).Unix()),
			"https://api.openai.com/auth": map[string]any{
				"plan_type":          "chatgpt-team",
				"chatgpt_account_id": "org_pasted",
			},
		})
		rawAuthJSON := mustMarshalJSON(t, importAuthJSONFixture{
			AccessToken:  "acc-pasted",
			RefreshToken: "ref-pasted",
			IDToken:      idToken,
			AccountID:    "org_pasted_fallback",
			LastRefresh:  stringPtrTest("2026-04-12T09:00:00Z"),
		})
		reqBody := mustMarshalJSON(t, map[string]string{"auth_json": string(rawAuthJSON)})

		rec := h.doRaw(t, "application/json", reqBody)

		data := testutil.AssertEnvelopeDataShape(t, rec, 0)
		accountData, ok := data["account"].(map[string]any)
		require.True(t, ok, "data.account must be object: %#v", data["account"])
		assert.Equal(t, "oauth_import", accountData["auth_method"])
		assert.Equal(t, "pasted@example.com", accountData["name"])
		assert.Equal(t, "ChatGPT Team", accountData["plan_type_label"])

		row, err := h.repo.GetByID(context.Background(), 1)
		require.NoError(t, err)
		assert.Equal(t, domain.AuthMethodOAuthImport, row.AuthMethod)
		assert.Equal(t, []byte("acc-pasted"), row.AccessToken)
		assert.Equal(t, []byte("ref-pasted"), row.RefreshToken)
		assert.Equal(t, []byte(idToken), row.IDToken)
		assert.Contains(t, h.logs.String(), "msg=account_created")
		assert.NotContains(t, h.logs.String(), "acc-pasted")
		assert.NotContains(t, h.logs.String(), "ref-pasted")
		assert.NotContains(t, h.logs.String(), idToken)
	})

	t.Run("wrong content-type returns invalid_auth_json_structure", func(t *testing.T) {
		h := setupImportAuthJSONHarness(t, now)
		rec := h.doRaw(t, "text/plain", mustMarshalJSON(t, importAuthJSONFixture{}))
		testutil.AssertEnvelope(t, rec, errcode.InvalidAuthJSONStructure)
		assertNoImportRows(t, h)
		assert.Contains(t, h.logs.String(), "msg=auth_json_import_rejected")
		assert.Contains(t, h.logs.String(), "request_id="+h.lastID)
		assert.Contains(t, h.logs.String(), "reason=unsupported_auth_json_content_type")
		assert.Contains(t, h.logs.String(), "error_code=invalid_auth_json_structure")
	})

	t.Run("json body missing auth_json returns invalid_auth_json_structure", func(t *testing.T) {
		h := setupImportAuthJSONHarness(t, now)
		rec := h.doRaw(t, "application/json", []byte(`{"other":"ignored"}`))
		testutil.AssertEnvelope(t, rec, errcode.InvalidAuthJSONStructure)
		assertNoImportRows(t, h)
		assert.Contains(t, h.logs.String(), "reason=missing_json_auth_json_field")
	})

	t.Run("json body oversized auth_json uses part scope", func(t *testing.T) {
		h := setupImportAuthJSONHarness(t, now)
		rec := h.doRaw(t, "application/json", mustMarshalJSON(t, map[string]string{
			"auth_json": strings.Repeat("x", int(ImportAuthJSONPartLimitBytes)+1),
		}))
		data := testutil.AssertEnvelopeDataShape(t, rec, errcode.RequestBodyTooLarge)
		assert.Equal(t, "part", data["scope"])
		assert.Equal(t, float64(ImportAuthJSONPartLimitBytes), data["limit_bytes"])
		assertNoImportRows(t, h)
		assert.Contains(t, h.logs.String(), "scope=part")
	})

	t.Run("multipart without boundary returns invalid_auth_json_structure", func(t *testing.T) {
		h := setupImportAuthJSONHarness(t, now)
		rec := h.doRaw(t, "multipart/form-data", []byte(`{"tokens":{}}`))
		testutil.AssertEnvelope(t, rec, errcode.InvalidAuthJSONStructure)
		assertNoImportRows(t, h)
		assert.Contains(t, h.logs.String(), "msg=auth_json_import_rejected")
		assert.Contains(t, h.logs.String(), "request_id="+h.lastID)
	})

	t.Run("duplicate content-type headers return invalid_auth_json_structure", func(t *testing.T) {
		h := setupImportAuthJSONHarness(t, now)
		req := httptest.NewRequest(http.MethodPost, ImportAuthJSONRoute, strings.NewReader(`{"tokens":{}}`))
		req.Header.Add("Content-Type", "multipart/form-data; boundary=a")
		req.Header.Add("Content-Type", "multipart/form-data; boundary=b")
		req = req.WithContext(api.WithRequestID(req.Context(), h.nextRequestID()))
		rec := httptest.NewRecorder()
		h.mux.ServeHTTP(rec, req)

		testutil.AssertEnvelope(t, rec, errcode.InvalidAuthJSONStructure)
		assertNoImportRows(t, h)
		assert.Contains(t, h.logs.String(), "msg=auth_json_import_rejected")
		assert.Contains(t, h.logs.String(), "request_id="+h.lastID)
	})

	t.Run("missing auth_json part returns invalid_auth_json_structure", func(t *testing.T) {
		h := setupImportAuthJSONHarness(t, now)
		rec := h.doMultipart(t, nil, map[string]string{"other": "ignored"})
		testutil.AssertEnvelope(t, rec, errcode.InvalidAuthJSONStructure)
		assertNoImportRows(t, h)
	})

	t.Run("non-json auth_json part returns invalid_auth_json_structure", func(t *testing.T) {
		h := setupImportAuthJSONHarness(t, now)
		rec := h.doMultipart(t, map[string][]byte{
			"auth_json": []byte("not-json acc-struct-leak ref-struct-leak eyJhbGciOiJub25lIn0.payload.sig"),
		}, nil)
		testutil.AssertEnvelope(t, rec, errcode.InvalidAuthJSONStructure)
		assertNoImportRows(t, h)
		assert.Contains(t, h.logs.String(), "request_id="+h.lastID)
		assert.Contains(t, h.logs.String(), "reason=malformed_structure")
		assert.NotContains(t, h.logs.String(), "acc-struct-leak")
		assert.NotContains(t, h.logs.String(), "ref-struct-leak")
		assert.NotContains(t, h.logs.String(), "eyJhbGciOiJub25lIn0.payload.sig")
	})

	t.Run("valid json non-object auth_json part returns invalid_auth_json_structure", func(t *testing.T) {
		h := setupImportAuthJSONHarness(t, now)
		rec := h.doMultipart(t, map[string][]byte{
			"auth_json": []byte(`["not","an","object"]`),
		}, nil)
		testutil.AssertEnvelope(t, rec, errcode.InvalidAuthJSONStructure)
		assertNoImportRows(t, h)
	})

	t.Run("missing required tokens field returns invalid_auth_json", func(t *testing.T) {
		h := setupImportAuthJSONHarness(t, now)
		idToken := makeImportJWT(t, map[string]any{"email": "missing@example.com"})
		rec := h.doMultipart(t, map[string][]byte{
			"auth_json": mustMarshalJSON(t, map[string]any{
				"tokens": map[string]any{
					"access_token": "acc",
					"id_token":     idToken,
				},
			}),
		}, nil)
		data := testutil.AssertEnvelopeDataShape(t, rec, errcode.InvalidAuthJSON)
		assert.Equal(t, []any{"tokens.refresh_token"}, data["missing_fields"])
		assertNoImportRows(t, h)
		assert.Contains(t, h.logs.String(), "request_id="+h.lastID)
		assert.NotContains(t, h.logs.String(), "acc")
		assert.NotContains(t, h.logs.String(), idToken)
	})

	t.Run("malformed id_token variants return invalid_auth_json for tokens.id_token", func(t *testing.T) {
		cases := map[string]string{
			"bad segments": "not-a-jwt",
			"bad base64":   "header.not*base64.signature",
			"bad json": func() string {
				header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
				payload := base64.RawURLEncoding.EncodeToString([]byte(`{"email":`))
				return header + "." + payload + ".sig"
			}(),
		}
		for name, token := range cases {
			t.Run(name, func(t *testing.T) {
				h := setupImportAuthJSONHarness(t, now)
				rec := h.doMultipart(t, map[string][]byte{
					"auth_json": mustMarshalJSON(t, importAuthJSONFixture{
						AccessToken:  "acc-idtoken",
						RefreshToken: "ref-idtoken",
						IDToken:      token,
					}),
				}, nil)
				data := testutil.AssertEnvelopeDataShape(t, rec, errcode.InvalidAuthJSON)
				assert.Equal(t, []any{"tokens.id_token"}, data["missing_fields"])
				assertNoImportRows(t, h)
				assert.NotContains(t, h.logs.String(), "acc-idtoken")
				assert.NotContains(t, h.logs.String(), "ref-idtoken")
				assert.NotContains(t, h.logs.String(), token)
			})
		}
	})

	t.Run("oversized auth_json part uses part scope", func(t *testing.T) {
		h := setupImportAuthJSONHarness(t, now)
		idToken := makeImportJWT(t, map[string]any{"email": "oversized@example.com"})
		oversized := sizedImportAuthJSON(t, 16*1024+1, importAuthJSONFixture{
			AccessToken:  "base",
			RefreshToken: "ref",
			IDToken:      idToken,
		})
		rec := h.doMultipart(t, map[string][]byte{"auth_json": oversized}, nil)
		data := testutil.AssertEnvelopeDataShape(t, rec, errcode.RequestBodyTooLarge)
		assert.Equal(t, "part", data["scope"])
		assert.Equal(t, float64(16384), data["limit_bytes"])
		assertNoImportRows(t, h)
		assert.Contains(t, h.logs.String(), "scope=part")
	})

	t.Run("16KB minus one auth_json part still succeeds", func(t *testing.T) {
		h := setupImportAuthJSONHarness(t, now)
		idToken := makeImportJWT(t, map[string]any{
			"email": "tight@example.com",
			"exp":   float64(now.Add(15 * time.Minute).Unix()),
		})
		raw := sizedImportAuthJSON(t, 16*1024-1, importAuthJSONFixture{
			AccessToken:  "base",
			RefreshToken: "ref",
			IDToken:      idToken,
		})
		rec := h.doMultipart(t, map[string][]byte{"auth_json": raw}, nil)
		testutil.AssertEnvelope(t, rec, 0)
	})

	t.Run("multipart envelope preamble flood uses envelope scope", func(t *testing.T) {
		h := setupImportAuthJSONHarness(t, now)
		idToken := makeImportJWT(t, map[string]any{"email": "flood@example.com"})
		rec := h.doMultipart(t, map[string][]byte{
			"auth_json": mustMarshalJSON(t, importAuthJSONFixture{
				AccessToken:  "acc-flood",
				RefreshToken: "ref-flood",
				IDToken:      idToken,
			}),
		}, map[string]string{
			"preamble": strings.Repeat("x", 72<<10),
		})
		data := testutil.AssertEnvelopeDataShape(t, rec, errcode.RequestBodyTooLarge)
		assert.Equal(t, "envelope", data["scope"])
		assert.Equal(t, float64(65536), data["limit_bytes"])
		assertNoImportRows(t, h)
		assert.Contains(t, rec.Header().Get("Content-Type"), "application/json; charset=utf-8")
		assert.Contains(t, h.logs.String(), "msg=auth_json_import_rejected")
		assert.Contains(t, h.logs.String(), "request_id="+h.lastID)
		assert.Contains(t, h.logs.String(), "scope=envelope")
	})

	t.Run("name falls back to account id when email claim is absent", func(t *testing.T) {
		h := setupImportAuthJSONHarness(t, now)
		idToken := makeImportJWT(t, map[string]any{
			"auth": map[string]any{
				"chatgpt_account_id": "plan-abc",
			},
		})
		rec := h.doMultipart(t, map[string][]byte{
			"auth_json": mustMarshalJSON(t, importAuthJSONFixture{
				AccessToken:  "acc-no-email",
				RefreshToken: "ref-no-email",
				IDToken:      idToken,
			}),
		}, nil)
		data := testutil.AssertEnvelopeDataShape(t, rec, 0)
		accountData := data["account"].(map[string]any)
		assert.Equal(t, "plan-abc", accountData["name"])
	})

	t.Run("name and chatgpt account id fall back to tokens.account_id when claims omit both", func(t *testing.T) {
		h := setupImportAuthJSONHarness(t, now)
		idToken := makeImportJWT(t, map[string]any{})
		rec := h.doMultipart(t, map[string][]byte{
			"auth_json": mustMarshalJSON(t, importAuthJSONFixture{
				AccessToken:  "acc-token-id",
				RefreshToken: "ref-token-id",
				IDToken:      idToken,
				AccountID:    "tok-account-id",
			}),
		}, nil)
		data := testutil.AssertEnvelopeDataShape(t, rec, 0)
		accountData := data["account"].(map[string]any)
		assert.Equal(t, "tok-account-id", accountData["name"])
		assert.Equal(t, "tok-account-id", accountData["chatgpt_account_id"])
		row, err := h.repo.GetByID(context.Background(), 1)
		require.NoError(t, err)
		require.NotNil(t, row.ChatGPTAccountID)
		assert.Equal(t, "tok-account-id", *row.ChatGPTAccountID)
	})

	t.Run("unknown top-level keys are ignored and duplicate account ids do not block inserts", func(t *testing.T) {
		h := setupImportAuthJSONHarness(t, now)
		idToken := makeImportJWT(t, map[string]any{
			"email": "dup@example.com",
			"https://api.openai.com/auth": map[string]any{
				"chatgpt_account_id": "dup-account",
			},
		})
		for i := 0; i < 2; i++ {
			rec := h.doMultipart(t, map[string][]byte{
				"auth_json": mustMarshalJSON(t, map[string]any{
					"tokens": map[string]any{
						"access_token":  "acc-dup-" + strings.Repeat("x", i),
						"refresh_token": "ref-dup-" + strings.Repeat("x", i),
						"id_token":      idToken,
						"account_id":    "dup-account",
					},
					"last_refresh": "2026-04-10T09:00:00Z",
					"ignored":      map[string]any{"foo": "bar"},
				}),
			}, nil)
			testutil.AssertEnvelope(t, rec, 0)
		}
		rows, err := h.repo.List(context.Background(), nil)
		require.NoError(t, err)
		require.Len(t, rows, 2)
		assert.Equal(t, int64(2), rows[1].ID)
	})

	t.Run("access_expires_at falls back when exp is absent", func(t *testing.T) {
		h := setupImportAuthJSONHarness(t, now)
		idToken := makeImportJWT(t, map[string]any{"email": "fallback-exp@example.com"})
		rec := h.doMultipart(t, map[string][]byte{
			"auth_json": mustMarshalJSON(t, importAuthJSONFixture{
				AccessToken:  "acc-exp-fallback",
				RefreshToken: "ref-exp-fallback",
				IDToken:      idToken,
			}),
		}, nil)
		testutil.AssertEnvelope(t, rec, 0)
		row, err := h.repo.GetByID(context.Background(), 1)
		require.NoError(t, err)
		require.NotNil(t, row.AccessExpiresAt)
		assert.Equal(t, now.Add(5*time.Minute).UTC(), row.AccessExpiresAt.UTC())
		assert.Contains(t, h.logs.String(), "msg=auth_json_import_last_refresh_fallback")
		assert.Contains(t, h.logs.String(), "request_id="+h.lastID)
		assert.Contains(t, h.logs.String(), "account_id=1")
		assert.Contains(t, h.logs.String(), "reason=id_token_exp_absent")
	})

	t.Run("last_refresh payload is preserved when parseable and not future", func(t *testing.T) {
		h := setupImportAuthJSONHarness(t, now)
		idToken := makeImportJWT(t, map[string]any{
			"email": "last-refresh@example.com",
			"exp":   float64(now.Add(30 * time.Minute).Unix()),
		})
		rec := h.doMultipart(t, map[string][]byte{
			"auth_json": mustMarshalJSON(t, importAuthJSONFixture{
				AccessToken:  "acc-last",
				RefreshToken: "ref-last",
				IDToken:      idToken,
				LastRefresh:  stringPtrTest("2026-04-10T09:00:00Z"),
			}),
		}, nil)
		testutil.AssertEnvelope(t, rec, 0)
		row, err := h.repo.GetByID(context.Background(), 1)
		require.NoError(t, err)
		require.NotNil(t, row.LastRefresh)
		assert.Equal(t, time.Date(2026, 4, 10, 9, 0, 0, 0, time.UTC), row.LastRefresh.UTC())
		assert.NotContains(t, h.logs.String(), "reason=payload_last_refresh_absent")
		assert.NotContains(t, h.logs.String(), "reason=payload_last_refresh_unparseable")
	})

	t.Run("last_refresh falls back when absent", func(t *testing.T) {
		h := setupImportAuthJSONHarness(t, now)
		idToken := makeImportJWT(t, map[string]any{"email": "absent-last@example.com"})
		rec := h.doMultipart(t, map[string][]byte{
			"auth_json": mustMarshalJSON(t, importAuthJSONFixture{
				AccessToken:  "acc-absent",
				RefreshToken: "ref-absent",
				IDToken:      idToken,
			}),
		}, nil)
		testutil.AssertEnvelope(t, rec, 0)
		row, err := h.repo.GetByID(context.Background(), 1)
		require.NoError(t, err)
		require.NotNil(t, row.LastRefresh)
		assert.Equal(t, now.UTC(), row.LastRefresh.UTC())
		assert.Contains(t, h.logs.String(), "request_id="+h.lastID)
		assert.Contains(t, h.logs.String(), "account_id=1")
		assert.Contains(t, h.logs.String(), "reason=payload_last_refresh_absent")
	})

	t.Run("last_refresh falls back when unparseable", func(t *testing.T) {
		h := setupImportAuthJSONHarness(t, now)
		idToken := makeImportJWT(t, map[string]any{"email": "bad-last@example.com"})
		rec := h.doMultipart(t, map[string][]byte{
			"auth_json": mustMarshalJSON(t, importAuthJSONFixture{
				AccessToken:  "acc-bad-last",
				RefreshToken: "ref-bad-last",
				IDToken:      idToken,
				LastRefresh:  stringPtrTest("garbage"),
			}),
		}, nil)
		testutil.AssertEnvelope(t, rec, 0)
		row, err := h.repo.GetByID(context.Background(), 1)
		require.NoError(t, err)
		require.NotNil(t, row.LastRefresh)
		assert.Equal(t, now.UTC(), row.LastRefresh.UTC())
		assert.Contains(t, h.logs.String(), "request_id="+h.lastID)
		assert.Contains(t, h.logs.String(), "account_id=1")
		assert.Contains(t, h.logs.String(), "reason=payload_last_refresh_unparseable")
	})

	t.Run("last_refresh falls back when future", func(t *testing.T) {
		h := setupImportAuthJSONHarness(t, now)
		idToken := makeImportJWT(t, map[string]any{"email": "future-last@example.com"})
		rec := h.doMultipart(t, map[string][]byte{
			"auth_json": mustMarshalJSON(t, importAuthJSONFixture{
				AccessToken:  "acc-future-last",
				RefreshToken: "ref-future-last",
				IDToken:      idToken,
				LastRefresh:  stringPtrTest(now.Add(time.Minute).Format(time.RFC3339)),
			}),
		}, nil)
		testutil.AssertEnvelope(t, rec, 0)
		row, err := h.repo.GetByID(context.Background(), 1)
		require.NoError(t, err)
		require.NotNil(t, row.LastRefresh)
		assert.Equal(t, now.UTC(), row.LastRefresh.UTC())
		assert.Contains(t, h.logs.String(), "request_id="+h.lastID)
		assert.Contains(t, h.logs.String(), "account_id=1")
		assert.Contains(t, h.logs.String(), "reason=payload_last_refresh_future")
	})

	t.Run("nil store fails as oauth_internal_error instead of panicking", func(t *testing.T) {
		logs := &bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
		handler := NewImportAuthJSONHandler(nil, logger)
		mux := http.NewServeMux()
		RegisterImportAuthJSONHandler(mux, handler, nil)

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, ImportAuthJSONRoute, nil)
		req.Header.Set("Content-Type", "multipart/form-data; boundary=test")
		req = req.WithContext(api.WithRequestID(req.Context(), "req-import-nil-store"))
		mux.ServeHTTP(rec, req)

		testutil.AssertEnvelope(t, rec, errcode.OAuthInternalError)
		assert.Contains(t, logs.String(), "import handler misconfigured")
	})

	t.Run("store failure returns oauth_store_failed and logs stay scrubbed", func(t *testing.T) {
		logs := &bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
		handler := NewImportAuthJSONHandler(oauth.NewAuthJSONImportService(failingImportAuthJSONStore{err: errors.New("boom insert")}, func() time.Time { return now }), logger)
		mux := http.NewServeMux()
		RegisterImportAuthJSONHandler(mux, handler, nil)

		idToken := makeImportJWT(t, map[string]any{"email": "store-fail@example.com"})
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("auth_json", "auth.json")
		require.NoError(t, err)
		_, err = part.Write(mustMarshalJSON(t, importAuthJSONFixture{
			AccessToken:  "acc-store-fail",
			RefreshToken: "ref-store-fail",
			IDToken:      idToken,
		}))
		require.NoError(t, err)
		require.NoError(t, writer.Close())

		req := httptest.NewRequest(http.MethodPost, ImportAuthJSONRoute, &body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		req = req.WithContext(api.WithRequestID(req.Context(), "req-import-store-fail"))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		testutil.AssertEnvelope(t, rec, errcode.OAuthStoreFailed)
		assert.Contains(t, logs.String(), "persist imported auth.json account")
		assert.NotContains(t, logs.String(), "acc-store-fail")
		assert.NotContains(t, logs.String(), "ref-store-fail")
		assert.NotContains(t, logs.String(), idToken)
	})
}

type importAuthJSONFixture struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
	AccountID    string
	LastRefresh  *string
}

func (f importAuthJSONFixture) MarshalJSON() ([]byte, error) {
	body := map[string]any{
		"OPENAI_API_KEY": nil,
		"tokens": map[string]any{
			"access_token":  f.AccessToken,
			"refresh_token": f.RefreshToken,
			"id_token":      f.IDToken,
		},
	}
	if f.AccountID != "" {
		body["tokens"].(map[string]any)["account_id"] = f.AccountID
	}
	if f.LastRefresh != nil {
		body["last_refresh"] = *f.LastRefresh
	}
	return json.Marshal(body)
}

func mustMarshalJSON(t *testing.T, v any) []byte {
	t.Helper()
	body, err := json.Marshal(v)
	require.NoError(t, err)
	return body
}

func makeImportJWT(t *testing.T, payload map[string]any) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	payloadPart := base64.RawURLEncoding.EncodeToString(body)
	signature := base64.RawURLEncoding.EncodeToString([]byte("sig"))
	return header + "." + payloadPart + "." + signature
}

func sizedImportAuthJSON(t *testing.T, target int, fixture importAuthJSONFixture) []byte {
	t.Helper()
	lo, hi := 0, target
	for {
		fixture.AccessToken = strings.Repeat("A", hi)
		body := mustMarshalJSON(t, fixture)
		if len(body) >= target {
			break
		}
		hi *= 2
	}

	var best []byte
	for lo <= hi {
		mid := (lo + hi) / 2
		fixture.AccessToken = strings.Repeat("A", mid)
		body := mustMarshalJSON(t, fixture)
		switch {
		case len(body) == target:
			return body
		case len(body) < target:
			best = body
			lo = mid + 1
		default:
			hi = mid - 1
		}
	}

	if target > 0 && len(best) == target-1 {
		return best
	}
	t.Fatalf("unable to size auth.json payload to %d bytes (best=%d)", target, len(best))
	return nil
}

func stringPtrTest(v string) *string { return &v }

func assertNoImportRows(t *testing.T, h *importAuthJSONHarness) {
	t.Helper()
	rows, err := h.repo.List(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, rows, 0)
}

type failingImportAuthJSONStore struct {
	err error
}

func (f failingImportAuthJSONStore) InsertUpstreamAccount(context.Context, *domain.UpstreamAccount) (int64, error) {
	return 0, f.err
}

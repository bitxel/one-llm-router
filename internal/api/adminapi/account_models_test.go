package adminapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/user/one-llm-router/internal/api/errcode"
	"github.com/user/one-llm-router/internal/core"
	"github.com/user/one-llm-router/internal/domain"
)

type fakeAccountReader struct {
	accounts map[int64]*domain.UpstreamAccount
}

func (f *fakeAccountReader) GetByID(_ context.Context, id int64) (*domain.UpstreamAccount, error) {
	if a, ok := f.accounts[id]; ok {
		return a, nil
	}
	return nil, nil
}

type fakeModelRepo struct {
	models    map[int64][]domain.AccountModel
	insertErr error
	deleteErr error
	hasErr    error
	replaceFn func(ctx context.Context, accountID int64, modelIDs []string) (int, int, error)
}

func (f *fakeModelRepo) ListByAccount(_ context.Context, accountID int64) ([]domain.AccountModel, error) {
	return f.models[accountID], nil
}

func (f *fakeModelRepo) Insert(_ context.Context, accountID int64, modelID string, source string) error {
	if f.insertErr != nil {
		return f.insertErr
	}
	f.models[accountID] = append(f.models[accountID], domain.AccountModel{
		AccountID: accountID,
		ModelID:   modelID,
		Source:    source,
	})
	return nil
}

func (f *fakeModelRepo) Delete(_ context.Context, accountID int64, modelID string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	existing := f.models[accountID]
	filtered := make([]domain.AccountModel, 0, len(existing))
	for _, m := range existing {
		if m.ModelID != modelID {
			filtered = append(filtered, m)
		}
	}
	f.models[accountID] = filtered
	return nil
}

func (f *fakeModelRepo) HasModel(_ context.Context, accountID int64, modelID string) (bool, error) {
	if f.hasErr != nil {
		return false, f.hasErr
	}
	for _, m := range f.models[accountID] {
		if m.ModelID == modelID {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeModelRepo) ReplaceUpstreamModels(ctx context.Context, accountID int64, modelIDs []string) (int, int, error) {
	if f.replaceFn != nil {
		return f.replaceFn(ctx, accountID, modelIDs)
	}
	return 0, 0, nil
}

func newFakeHandler(client *http.Client) *AccountModelHandler {
	modelRefresher := core.NewModelRefresher(
		client,
		"",
		"0.160.0",
		slog.Default(),
	)
	return &AccountModelHandler{
		accountRepo: &fakeAccountReader{
			accounts: map[int64]*domain.UpstreamAccount{
				1: {ID: 1, Name: "test-account"},
			},
		},
		modelRepo: &fakeModelRepo{
			models: map[int64][]domain.AccountModel{
				1: {
					{AccountID: 1, ModelID: "gpt-4o", Source: domain.AccountModelSourceManual},
				},
			},
		},
		modelRefresher: modelRefresher,
		logger:         slog.Default(),
	}
}

func amRequest(method, target string, body string, id string) *http.Request {
	req := requestWithID(method, target, body)
	req.SetPathValue("id", id)
	return req
}

type envelopeData struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

func decodeAMEnvelope(t *testing.T, body []byte) envelopeData {
	t.Helper()
	var env envelopeData
	require.NoError(t, json.Unmarshal(body, &env))
	return env
}

func TestListModels_Success(t *testing.T) {
	h := newFakeHandler(http.DefaultClient)
	rec := httptest.NewRecorder()
	req := amRequest(http.MethodGet, "/api/admin/accounts/1/models", "", "1")
	h.ListModels(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	env := decodeAMEnvelope(t, rec.Body.Bytes())
	require.Equal(t, 0, env.Code)
	require.Contains(t, string(env.Data), "gpt-4o")
}

func TestListModels_AccountNotFound(t *testing.T) {
	h := newFakeHandler(http.DefaultClient)
	rec := httptest.NewRecorder()
	req := amRequest(http.MethodGet, "/api/admin/accounts/999/models", "", "999")
	h.ListModels(rec, req)

	env := decodeAMEnvelope(t, rec.Body.Bytes())
	require.Equal(t, errcode.AccountNotFound, env.Code)
}

func TestAddModel_Success(t *testing.T) {
	h := newFakeHandler(http.DefaultClient)
	rec := httptest.NewRecorder()
	req := amRequest(http.MethodPost, "/api/admin/accounts/1/models/add", `{"model_id":"gpt-4o-mini"}`, "1")
	h.AddModel(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	env := decodeAMEnvelope(t, rec.Body.Bytes())
	require.Equal(t, 0, env.Code)
	require.Contains(t, string(env.Data), "gpt-4o-mini")
}

func TestAddModel_Duplicate(t *testing.T) {
	h := newFakeHandler(http.DefaultClient)
	h.modelRepo.(*fakeModelRepo).insertErr = domain.ErrAccountModelDuplicate
	rec := httptest.NewRecorder()
	req := amRequest(http.MethodPost, "/api/admin/accounts/1/models/add", `{"model_id":"gpt-4o"}`, "1")
	h.AddModel(rec, req)

	env := decodeAMEnvelope(t, rec.Body.Bytes())
	require.Equal(t, errcode.AccountModelDuplicate, env.Code)
}

func TestAddModel_AccountNotFound(t *testing.T) {
	h := newFakeHandler(http.DefaultClient)
	rec := httptest.NewRecorder()
	req := amRequest(http.MethodPost, "/api/admin/accounts/999/models/add", `{"model_id":"gpt-4o"}`, "999")
	h.AddModel(rec, req)

	env := decodeAMEnvelope(t, rec.Body.Bytes())
	require.Equal(t, errcode.AccountNotFound, env.Code)
}

func TestAddModel_EmptyModelID(t *testing.T) {
	h := newFakeHandler(http.DefaultClient)
	rec := httptest.NewRecorder()
	req := amRequest(http.MethodPost, "/api/admin/accounts/1/models/add", `{"model_id":""}`, "1")
	h.AddModel(rec, req)

	env := decodeAMEnvelope(t, rec.Body.Bytes())
	require.Equal(t, errcode.MalformedBody, env.Code)
}

func TestRemoveModel_Success(t *testing.T) {
	h := newFakeHandler(http.DefaultClient)
	rec := httptest.NewRecorder()
	req := amRequest(http.MethodPost, "/api/admin/accounts/1/models/remove", `{"model_id":"gpt-4o"}`, "1")
	h.RemoveModel(rec, req)

	env := decodeAMEnvelope(t, rec.Body.Bytes())
	require.Equal(t, 0, env.Code)
}

func TestRemoveModel_NotFound(t *testing.T) {
	h := newFakeHandler(http.DefaultClient)
	rec := httptest.NewRecorder()
	req := amRequest(http.MethodPost, "/api/admin/accounts/1/models/remove", `{"model_id":"nonexistent"}`, "1")
	h.RemoveModel(rec, req)

	env := decodeAMEnvelope(t, rec.Body.Bytes())
	require.Equal(t, errcode.AccountNotFound, env.Code)
}

func TestRemoveModel_AccountNotFound(t *testing.T) {
	h := newFakeHandler(http.DefaultClient)
	rec := httptest.NewRecorder()
	req := amRequest(http.MethodPost, "/api/admin/accounts/999/models/remove", `{"model_id":"gpt-4o"}`, "999")
	h.RemoveModel(rec, req)

	env := decodeAMEnvelope(t, rec.Body.Bytes())
	require.Equal(t, errcode.AccountNotFound, env.Code)
}

func TestRemoveModel_EmptyModelID(t *testing.T) {
	h := newFakeHandler(http.DefaultClient)
	rec := httptest.NewRecorder()
	req := amRequest(http.MethodPost, "/api/admin/accounts/1/models/remove", `{"model_id":""}`, "1")
	h.RemoveModel(rec, req)

	env := decodeAMEnvelope(t, rec.Body.Bytes())
	require.Equal(t, errcode.MalformedBody, env.Code)
}

func TestRefreshModels_AccountNotFound(t *testing.T) {
	h := newFakeHandler(http.DefaultClient)
	rec := httptest.NewRecorder()
	req := amRequest(http.MethodPost, "/api/admin/accounts/999/models/refresh", "", "999")
	h.RefreshModels(rec, req)

	env := decodeAMEnvelope(t, rec.Body.Bytes())
	require.Equal(t, errcode.AccountNotFound, env.Code)
}

func TestRefreshModels_UpstreamFetchError(t *testing.T) {
	transport := roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused")
	})
	h := newFakeHandler(&http.Client{Transport: transport})
	rec := httptest.NewRecorder()
	req := amRequest(http.MethodPost, "/api/admin/accounts/1/models/refresh", "", "1")
	h.RefreshModels(rec, req)

	env := decodeAMEnvelope(t, rec.Body.Bytes())
	require.Equal(t, errcode.AccountModelRefreshFailed, env.Code)
}

func TestRefreshModels_ReplaceError(t *testing.T) {
	h := newFakeHandler(&http.Client{Transport: roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"object":"list","data":[{"id":"gpt-4o"}]}`)),
			Header:     http.Header{},
		}, nil
	})})
	h.modelRepo.(*fakeModelRepo).replaceFn = func(_ context.Context, _ int64, _ []string) (int, int, error) {
		return 0, 0, errors.New("replace failed")
	}
	rec := httptest.NewRecorder()
	req := amRequest(http.MethodPost, "/api/admin/accounts/1/models/refresh", "", "1")
	h.RefreshModels(rec, req)

	env := decodeAMEnvelope(t, rec.Body.Bytes())
	require.Equal(t, errcode.AccountModelInternalError, env.Code)
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestRefreshModels_EmptyBodyResponse(t *testing.T) {
	h := newFakeHandler(&http.Client{Transport: roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       http.NoBody,
			Header:     http.Header{},
		}, nil
	})})
	rec := httptest.NewRecorder()
	req := amRequest(http.MethodPost, "/api/admin/accounts/1/models/refresh", "", "1")
	h.RefreshModels(rec, req)

	env := decodeAMEnvelope(t, rec.Body.Bytes())
	require.Equal(t, errcode.AccountModelRefreshFailed, env.Code)
}

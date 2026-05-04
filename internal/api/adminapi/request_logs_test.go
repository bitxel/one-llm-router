package adminapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/one-llm-router/internal/api/errcode"
	"github.com/user/one-llm-router/internal/api/testutil"
	"github.com/user/one-llm-router/internal/core"
	"github.com/user/one-llm-router/internal/domain"
	generatedadminapi "github.com/user/one-llm-router/internal/generated/adminapi"
	"github.com/user/one-llm-router/internal/store"
)

func TestRequestLogsHandler_ListMapsAccountsAndKeepsBodiesOutOfRows(t *testing.T) {
	accountID := int64(7)
	clientReqBody := `{"prompt":"client-secret"}`
	upstreamReqBody := `{"input":"upstream-request-secret"}`
	upstreamRespBody := `{"output":"upstream-response-secret"}`
	repo := &requestLogRepoStub{
		records: []domain.RequestRecord{
			{
				ID:                   12,
				RequestID:            "req_a",
				CreatedAt:            time.Unix(100, 0).UTC(),
				ClientIP:             "203.0.113.10",
				UpstreamAccountID:    &accountID,
				Method:               http.MethodPost,
				Path:                 "/v1/responses",
				StatusCode:           200,
				LatencyMs:            34,
				Outcome:              domain.OutcomeSuccess,
				Model:                stringPtr("gpt-5.4-mini"),
				ResponseMode:         domain.ResponseModeJSON,
				ClientRequestBody:    &clientReqBody,
				UpstreamRequestBody:  &upstreamReqBody,
				UpstreamResponseBody: &upstreamRespBody,
			},
			{ID: 11, RequestID: "req_b", CreatedAt: time.Unix(99, 0).UTC(), Method: http.MethodPost, Path: "/v1/responses", StatusCode: 200, LatencyMs: 35, Outcome: domain.OutcomeSuccess, ResponseMode: domain.ResponseModeJSON},
		},
	}
	handler := newRequestLogsTestHandler(repo, []store.AccountListItem{{
		ID:         accountID,
		Name:       "primary",
		Provider:   "openai",
		AuthMethod: domain.AuthMethodAPIKey,
		Status:     domain.AccountStatusActive,
		CreatedAt:  time.Unix(1, 0).UTC(),
		UpdatedAt:  time.Unix(2, 0).UTC(),
	}})

	rec := runRequestLogsRequest(handler, http.MethodGet, "/api/admin/requests?limit=1")
	testutil.AssertEnvelope(t, rec, errcode.OK)
	assert.NotContains(t, rec.Body.String(), "client_request_body")
	assert.NotContains(t, rec.Body.String(), "upstream_request_body")
	assert.NotContains(t, rec.Body.String(), "upstream_response_body")
	assert.NotContains(t, rec.Body.String(), "client-secret")
	assert.NotContains(t, rec.Body.String(), "upstream-request-secret")
	assert.NotContains(t, rec.Body.String(), "upstream-response-secret")

	body := decodeRequestsListBody(t, rec)
	env, err := body.AsRequestsListEnvelope()
	require.NoError(t, err)
	assert.True(t, env.Data.HasMore)
	require.NotNil(t, env.Data.NextBeforeId)
	assert.Equal(t, int64(12), *env.Data.NextBeforeId)
	require.Len(t, env.Data.Records, 1)
	assert.Equal(t, "req_a", env.Data.Records[0].RequestId)
	assert.Equal(t, "203.0.113.10", env.Data.Records[0].ClientIp)
	require.NotNil(t, env.Data.Records[0].Account)
	assert.Equal(t, "primary", env.Data.Records[0].Account.Name)
	assert.Equal(t, 2, repo.lastQuery.Limit, "service should fetch limit+1")
}

func TestRequestLogsHandler_GetReturnsCapturedBodies(t *testing.T) {
	clientReqBody := `{"prompt":"hello"}`
	upstreamReqBody := `{"input":"hello"}`
	upstreamRespBody := `{"text":"world"}`
	repo := &requestLogRepoStub{
		records: []domain.RequestRecord{{
			ID:                   22,
			RequestID:            "req_detail",
			CreatedAt:            time.Unix(100, 0).UTC(),
			ClientIP:             "203.0.113.11",
			Method:               http.MethodPost,
			Path:                 "/v1/responses",
			StatusCode:           200,
			LatencyMs:            44,
			Outcome:              domain.OutcomeSuccess,
			ResponseMode:         domain.ResponseModeJSON,
			ClientRequestBody:    &clientReqBody,
			UpstreamRequestBody:  &upstreamReqBody,
			UpstreamResponseBody: &upstreamRespBody,
		}},
	}

	rec := runRequestLogsRequest(newRequestLogsTestHandler(repo, nil), http.MethodGet, "/api/admin/requests/22")
	testutil.AssertEnvelope(t, rec, errcode.OK)

	var body generatedadminapi.RequestDetailResponseBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	env, err := body.AsRequestDetailEnvelope()
	require.NoError(t, err)
	assert.Equal(t, "req_detail", env.Data.Record.RequestId)
	assert.Equal(t, "203.0.113.11", env.Data.Record.ClientIp)
	require.NotNil(t, env.Data.Record.ClientRequestBody)
	assert.Equal(t, clientReqBody, *env.Data.Record.ClientRequestBody)
	require.NotNil(t, env.Data.Record.UpstreamRequestBody)
	assert.Equal(t, upstreamReqBody, *env.Data.Record.UpstreamRequestBody)
	require.NotNil(t, env.Data.Record.UpstreamResponseBody)
	assert.Equal(t, upstreamRespBody, *env.Data.Record.UpstreamResponseBody)
}

func TestRequestLogsHandler_ListAcceptsWebSocketRowsAndFilters(t *testing.T) {
	repo := &requestLogRepoStub{
		records: []domain.RequestRecord{{
			ID:           31,
			RequestID:    "req_ws",
			CreatedAt:    time.Unix(101, 0).UTC(),
			Method:       http.MethodGet,
			Path:         "/v1/responses",
			StatusCode:   http.StatusSwitchingProtocols,
			LatencyMs:    250,
			Outcome:      domain.OutcomeSuccess,
			ResponseMode: domain.ResponseModeWebSocket,
		}},
	}

	rec := runRequestLogsRequest(
		newRequestLogsTestHandler(repo, nil),
		http.MethodGet,
		"/api/admin/requests?response_mode=websocket",
	)
	testutil.AssertEnvelope(t, rec, errcode.OK)
	assert.Equal(t, []string{domain.ResponseModeWebSocket}, repo.lastQuery.ResponseModes)

	body := decodeRequestsListBody(t, rec)
	env, err := body.AsRequestsListEnvelope()
	require.NoError(t, err)
	require.Len(t, env.Data.Records, 1)
	assert.Equal(t, generatedadminapi.RequestResponseModeWebsocket, env.Data.Records[0].ResponseMode)
}

func TestRequestLogsHandler_GetAggregatesPersistedRawSSEResponseBody(t *testing.T) {
	upstreamRespBody := `event: response.output_text.delta
data: {"type":"response.output_text.delta","delta":"OK"}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_legacy","usage":{"input_tokens":3,"output_tokens":1}}}
`
	repo := &requestLogRepoStub{
		records: []domain.RequestRecord{{
			ID:                   24,
			RequestID:            "req_sse_detail",
			CreatedAt:            time.Unix(100, 0).UTC(),
			Method:               http.MethodPost,
			Path:                 "/v1/responses",
			StatusCode:           200,
			LatencyMs:            44,
			Outcome:              domain.OutcomeSuccess,
			ResponseMode:         domain.ResponseModeJSON,
			UpstreamResponseBody: &upstreamRespBody,
		}},
	}

	rec := runRequestLogsRequest(newRequestLogsTestHandler(repo, nil), http.MethodGet, "/api/admin/requests/24")
	testutil.AssertEnvelope(t, rec, errcode.OK)

	var body generatedadminapi.RequestDetailResponseBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	env, err := body.AsRequestDetailEnvelope()
	require.NoError(t, err)
	require.NotNil(t, env.Data.Record.UpstreamResponseBody)
	assert.JSONEq(t, `{"id":"resp_legacy","output_text":"OK","usage":{"input_tokens":3,"output_tokens":1}}`, *env.Data.Record.UpstreamResponseBody)
	assert.NotContains(t, *env.Data.Record.UpstreamResponseBody, "event:")
}

func TestRequestLogsHandler_GetRedactsPersistedCapturedBodies(t *testing.T) {
	clientReqBody := `{"prompt":"sk-live-client-secret-123456"}`
	upstreamReqBody := `{"Authorization":"Bearer sk-live-upstream-secret-123456"}`
	upstreamRespBody := `{"access_token":"secret-access-token"}`
	repo := &requestLogRepoStub{
		records: []domain.RequestRecord{{
			ID:                   23,
			RequestID:            "req_detail_redacted",
			CreatedAt:            time.Unix(100, 0).UTC(),
			Method:               http.MethodPost,
			Path:                 "/v1/responses",
			StatusCode:           200,
			LatencyMs:            44,
			Outcome:              domain.OutcomeSuccess,
			ResponseMode:         domain.ResponseModeJSON,
			ClientRequestBody:    &clientReqBody,
			UpstreamRequestBody:  &upstreamReqBody,
			UpstreamResponseBody: &upstreamRespBody,
		}},
	}

	rec := runRequestLogsRequest(newRequestLogsTestHandler(repo, nil), http.MethodGet, "/api/admin/requests/23")
	testutil.AssertEnvelope(t, rec, errcode.OK)

	bodyText := rec.Body.String()
	assert.NotContains(t, bodyText, "sk-live-client-secret")
	assert.NotContains(t, bodyText, "sk-live-upstream-secret")
	assert.NotContains(t, bodyText, "secret-access-token")
	assert.Contains(t, bodyText, "[redacted]")
}

func TestRequestLogsHandler_PreservesBridgeMetadataAndHistoricalRows(t *testing.T) {
	bridgeMetadata := domain.JSONMap{
		"op_id":             "op.openai.chat_completions.create",
		"bridge_id":         "bridge.openai.chat_completions.to_codex",
		"client_contract":   "contract.openai.v1.chat_completions",
		"upstream_contract": "contract.chatgpt.backend_api.codex.responses",
		"credential_class":  "oauth",
	}
	repo := &requestLogRepoStub{
		records: []domain.RequestRecord{
			{
				ID:           42,
				RequestID:    "req_bridge_metadata",
				CreatedAt:    time.Unix(102, 0).UTC(),
				Method:       http.MethodPost,
				Path:         "/v1/chat/completions",
				StatusCode:   200,
				LatencyMs:    88,
				Outcome:      domain.OutcomeSuccess,
				ResponseMode: domain.ResponseModeSSE,
				ModelParams: domain.JSONMap{
					"service_tier": "default",
				},
				RouterMetadata: domain.JSONMap{
					"bridge": bridgeMetadata,
				},
			},
			{
				ID:           41,
				RequestID:    "req_historical_without_bridge",
				CreatedAt:    time.Unix(101, 0).UTC(),
				Method:       http.MethodPost,
				Path:         "/v1/responses",
				StatusCode:   200,
				LatencyMs:    77,
				Outcome:      domain.OutcomeSuccess,
				ResponseMode: domain.ResponseModeJSON,
			},
		},
	}
	handler := newRequestLogsTestHandler(repo, nil)

	listRec := runRequestLogsRequest(handler, http.MethodGet, "/api/admin/requests?limit=10")
	testutil.AssertEnvelope(t, listRec, errcode.OK)
	listBody := decodeRequestsListBody(t, listRec)
	listEnv, err := listBody.AsRequestsListEnvelope()
	require.NoError(t, err)
	require.Len(t, listEnv.Data.Records, 2)

	require.NotNil(t, listEnv.Data.Records[0].ModelParams)
	assert.Equal(t, "default", (*listEnv.Data.Records[0].ModelParams)["service_tier"])
	require.NotNil(t, listEnv.Data.Records[0].RouterMetadata)
	rawBridge, ok := (*listEnv.Data.Records[0].RouterMetadata)["bridge"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, map[string]interface{}(bridgeMetadata), rawBridge)
	assert.Nil(t, listEnv.Data.Records[1].ModelParams)
	assert.Nil(t, listEnv.Data.Records[1].RouterMetadata)

	detailRec := runRequestLogsRequest(handler, http.MethodGet, "/api/admin/requests/42")
	testutil.AssertEnvelope(t, detailRec, errcode.OK)
	var detailBody generatedadminapi.RequestDetailResponseBody
	require.NoError(t, json.Unmarshal(detailRec.Body.Bytes(), &detailBody))
	detailEnv, err := detailBody.AsRequestDetailEnvelope()
	require.NoError(t, err)
	require.NotNil(t, detailEnv.Data.Record.ModelParams)
	assert.Equal(t, "default", (*detailEnv.Data.Record.ModelParams)["service_tier"])
	require.NotNil(t, detailEnv.Data.Record.RouterMetadata)
	detailBridge, ok := (*detailEnv.Data.Record.RouterMetadata)["bridge"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, map[string]interface{}(bridgeMetadata), detailBridge)
}

func TestRequestLogsHandler_OptionsReturnsDynamicFacets(t *testing.T) {
	accountID := int64(9)
	repo := &requestLogRepoStub{
		options: core.RequestFilterOptions{
			AccountIDs:    []int64{accountID},
			Outcomes:      []string{domain.OutcomeSuccess},
			Models:        []string{"gpt-5.4-mini"},
			ResponseModes: []string{domain.ResponseModeJSON, domain.ResponseModeWebSocket},
		},
	}
	handler := newRequestLogsTestHandler(repo, []store.AccountListItem{{
		ID:         accountID,
		Name:       "oauth-plus",
		Provider:   "openai",
		AuthMethod: domain.AuthMethodOAuthBrowser,
		Status:     domain.AccountStatusActive,
		CreatedAt:  time.Unix(1, 0).UTC(),
		UpdatedAt:  time.Unix(2, 0).UTC(),
		Email:      stringPtr("plus@example.com"),
		PlanType:   stringPtr("chatgpt-plus"),
	}})

	rec := runRequestLogsRequest(handler, http.MethodGet, "/api/admin/requests/options")
	testutil.AssertEnvelope(t, rec, errcode.OK)

	var body generatedadminapi.RequestsOptionsResponseBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	env, err := body.AsRequestsOptionsEnvelope()
	require.NoError(t, err)
	require.Len(t, env.Data.Accounts, 1)
	assert.Equal(t, "oauth-plus", env.Data.Accounts[0].Label)
	require.NotNil(t, env.Data.Accounts[0].Account)
	assert.Equal(t, "ChatGPT Plus", *env.Data.Accounts[0].Account.PlanTypeLabel)
	assert.Equal(t, []string{"gpt-5.4-mini"}, env.Data.Models)
	assert.Equal(t,
		[]generatedadminapi.RequestResponseMode{
			generatedadminapi.RequestResponseModeJson,
			generatedadminapi.RequestResponseModeWebsocket,
		},
		env.Data.ResponseModes,
	)
}

func TestRequestLogsHandler_ParsesListAndOptionsFilters(t *testing.T) {
	start := "2026-04-25T01:00:00Z"
	end := "2026-04-25T02:00:00Z"
	repo := &requestLogRepoStub{}
	handler := newRequestLogsTestHandler(repo, nil)

	rec := runRequestLogsRequest(
		handler,
		http.MethodGet,
		"/api/admin/requests?start="+start+"&end="+end+"&account_id=7&outcome=success&model=gpt-5.4-mini&response_mode=json&search=req_13&before=99&limit=10",
	)
	testutil.AssertEnvelope(t, rec, errcode.OK)
	assert.Equal(t, []int64{7}, repo.lastQuery.AccountIDs)
	assert.Equal(t, []string{domain.OutcomeSuccess}, repo.lastQuery.Outcomes)
	assert.Equal(t, []string{"gpt-5.4-mini"}, repo.lastQuery.Models)
	assert.Equal(t, []string{domain.ResponseModeJSON}, repo.lastQuery.ResponseModes)
	require.NotNil(t, repo.lastQuery.Search)
	assert.Equal(t, "req_13", *repo.lastQuery.Search)
	require.NotNil(t, repo.lastQuery.BeforeID)
	assert.Equal(t, int64(99), *repo.lastQuery.BeforeID)
	assert.Equal(t, 11, repo.lastQuery.Limit)
	require.NotNil(t, repo.lastQuery.Start)
	require.NotNil(t, repo.lastQuery.End)

	rec = runRequestLogsRequest(
		handler,
		http.MethodGet,
		"/api/admin/requests/options?account_id=7&outcome=success&model=gpt-5.4-mini&response_mode=json&search=req_13",
	)
	testutil.AssertEnvelope(t, rec, errcode.OK)
	assert.Equal(t, 0, repo.lastQuery.Limit)
	assert.Equal(t, []int64{7}, repo.lastQuery.AccountIDs)
	assert.Equal(t, []string{domain.ResponseModeJSON}, repo.lastQuery.ResponseModes)
}

func TestRequestLogsHandler_BusinessErrors(t *testing.T) {
	handler := newRequestLogsTestHandler(&requestLogRepoStub{}, nil)

	t.Run("invalid filter", func(t *testing.T) {
		rec := runRequestLogsRequest(handler, http.MethodGet, "/api/admin/requests?limit=0")
		data := testutil.AssertEnvelope(t, rec, errcode.InvalidRequestFilter)
		assert.Equal(t, "limit", data["field"])
	})

	t.Run("blank search", func(t *testing.T) {
		rec := runRequestLogsRequest(handler, http.MethodGet, "/api/admin/requests?search=%20%20")
		data := testutil.AssertEnvelope(t, rec, errcode.InvalidRequestFilter)
		assert.Equal(t, "search", data["field"])
	})

	t.Run("too many repeated values", func(t *testing.T) {
		target := "/api/admin/requests?"
		for i := 1; i <= requestLogFilterMaxItems+1; i++ {
			if i > 1 {
				target += "&"
			}
			target += "account_id=" + strconv.Itoa(i)
		}
		rec := runRequestLogsRequest(handler, http.MethodGet, target)
		data := testutil.AssertEnvelope(t, rec, errcode.InvalidRequestFilter)
		assert.Equal(t, "account_id", data["field"])
	})

	t.Run("missing detail", func(t *testing.T) {
		rec := runRequestLogsRequest(handler, http.MethodGet, "/api/admin/requests/404")
		testutil.AssertEnvelope(t, rec, errcode.RequestRecordNotFound)
	})

	t.Run("invalid detail id", func(t *testing.T) {
		rec := runRequestLogsRequest(handler, http.MethodGet, "/api/admin/requests/0")
		data := testutil.AssertEnvelope(t, rec, errcode.InvalidRequestFilter)
		assert.Equal(t, "id", data["field"])
	})
}

func TestRequestLogsHandler_SystemErrors(t *testing.T) {
	t.Run("list repo failure", func(t *testing.T) {
		handler := newRequestLogsTestHandler(&requestLogRepoStub{queryErr: assert.AnError}, nil)
		rec := runRequestLogsRequest(handler, http.MethodGet, "/api/admin/requests")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		testutil.AssertEnvelope(t, rec, errcode.InternalError)
	})

	t.Run("options repo failure", func(t *testing.T) {
		handler := newRequestLogsTestHandler(&requestLogRepoStub{optionsErr: assert.AnError}, nil)
		rec := runRequestLogsRequest(handler, http.MethodGet, "/api/admin/requests/options")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		testutil.AssertEnvelope(t, rec, errcode.InternalError)
	})

	t.Run("detail repo failure", func(t *testing.T) {
		handler := newRequestLogsTestHandler(&requestLogRepoStub{getErr: assert.AnError}, nil)
		rec := runRequestLogsRequest(handler, http.MethodGet, "/api/admin/requests/1")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		testutil.AssertEnvelope(t, rec, errcode.InternalError)
	})

	t.Run("account index failure", func(t *testing.T) {
		repo := &requestLogRepoStub{records: []domain.RequestRecord{{
			ID:           1,
			RequestID:    "req_account_error",
			CreatedAt:    time.Unix(100, 0).UTC(),
			Method:       http.MethodPost,
			Path:         "/v1/responses",
			StatusCode:   200,
			LatencyMs:    1,
			Outcome:      domain.OutcomeSuccess,
			ResponseMode: domain.ResponseModeJSON,
		}}}
		handler := newRequestLogsTestHandlerWithProvider(repo, requestLogAccountsErrStub{err: assert.AnError})
		rec := runRequestLogsRequest(handler, http.MethodGet, "/api/admin/requests")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		testutil.AssertEnvelope(t, rec, errcode.InternalError)
	})

	t.Run("options account index failure", func(t *testing.T) {
		repo := &requestLogRepoStub{options: core.RequestFilterOptions{
			AccountIDs:    []int64{7},
			Outcomes:      []string{domain.OutcomeSuccess},
			ResponseModes: []string{domain.ResponseModeJSON},
		}}
		handler := newRequestLogsTestHandlerWithProvider(repo, requestLogAccountsErrStub{err: assert.AnError})
		rec := runRequestLogsRequest(handler, http.MethodGet, "/api/admin/requests/options")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		testutil.AssertEnvelope(t, rec, errcode.InternalError)
	})

	t.Run("detail account index failure", func(t *testing.T) {
		repo := &requestLogRepoStub{records: []domain.RequestRecord{{
			ID:           1,
			RequestID:    "req_detail_account_error",
			CreatedAt:    time.Unix(100, 0).UTC(),
			Method:       http.MethodPost,
			Path:         "/v1/responses",
			StatusCode:   200,
			LatencyMs:    1,
			Outcome:      domain.OutcomeSuccess,
			ResponseMode: domain.ResponseModeJSON,
		}}}
		handler := newRequestLogsTestHandlerWithProvider(repo, requestLogAccountsErrStub{err: assert.AnError})
		rec := runRequestLogsRequest(handler, http.MethodGet, "/api/admin/requests/1")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		testutil.AssertEnvelope(t, rec, errcode.InternalError)
	})

	t.Run("nil account provider is a wiring failure", func(t *testing.T) {
		repo := &requestLogRepoStub{records: []domain.RequestRecord{{
			ID:           1,
			RequestID:    "req_nil_account_provider",
			CreatedAt:    time.Unix(100, 0).UTC(),
			Method:       http.MethodPost,
			Path:         "/v1/responses",
			StatusCode:   200,
			LatencyMs:    1,
			Outcome:      domain.OutcomeSuccess,
			ResponseMode: domain.ResponseModeJSON,
		}}}
		handler := newRequestLogsTestHandlerWithProvider(repo, nil)
		rec := runRequestLogsRequest(handler, http.MethodGet, "/api/admin/requests")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		testutil.AssertEnvelope(t, rec, errcode.InternalError)
	})

	t.Run("invalid persisted enum in list is system error", func(t *testing.T) {
		repo := &requestLogRepoStub{records: []domain.RequestRecord{{
			ID:           1,
			RequestID:    "req_bad_enum",
			CreatedAt:    time.Unix(100, 0).UTC(),
			Method:       http.MethodPost,
			Path:         "/v1/responses",
			StatusCode:   200,
			LatencyMs:    1,
			Outcome:      "bogus",
			ResponseMode: domain.ResponseModeJSON,
		}}}
		handler := newRequestLogsTestHandler(repo, nil)
		rec := runRequestLogsRequest(handler, http.MethodGet, "/api/admin/requests")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		testutil.AssertEnvelope(t, rec, errcode.InternalError)
	})

	t.Run("invalid persisted enum in options is system error", func(t *testing.T) {
		repo := &requestLogRepoStub{options: core.RequestFilterOptions{
			Outcomes:      []string{"bogus"},
			ResponseModes: []string{domain.ResponseModeJSON},
		}}
		handler := newRequestLogsTestHandler(repo, nil)
		rec := runRequestLogsRequest(handler, http.MethodGet, "/api/admin/requests/options")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		testutil.AssertEnvelope(t, rec, errcode.InternalError)
	})
}

func TestRequestLogQueryValidation(t *testing.T) {
	start := time.Unix(200, 0).UTC()
	end := time.Unix(100, 0).UTC()
	longSearch := ""
	for i := 0; i < 257; i++ {
		longSearch += "x"
	}

	cases := []struct {
		name  string
		query core.QueryParams
		field string
	}{
		{name: "start after end", query: core.QueryParams{Start: &start, End: &end}, field: "start"},
		{name: "bad before", query: core.QueryParams{BeforeID: int64Ptr(0)}, field: "before"},
		{name: "bad limit", query: core.QueryParams{Limit: 201}, field: "limit"},
		{name: "bad account", query: core.QueryParams{AccountIDs: []int64{0}}, field: "account_id"},
		{name: "bad outcome", query: core.QueryParams{Outcomes: []string{"bogus"}}, field: "outcome"},
		{name: "bad model", query: core.QueryParams{Models: []string{""}}, field: "model"},
		{name: "bad response mode", query: core.QueryParams{ResponseModes: []string{"xml"}}, field: "response_mode"},
		{name: "long search", query: core.QueryParams{Search: &longSearch}, field: "search"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			invalid := validateRequestLogQuery(tc.query)
			require.NotNil(t, invalid)
			assert.Equal(t, tc.field, invalid.field)
		})
	}

	invalidLimit := 0
	_, invalid := queryParamsFromRequestsList(generatedadminapi.RequestsListParams{Limit: &invalidLimit})
	require.NotNil(t, invalid)
	assert.Equal(t, "limit", invalid.field)
}

func newRequestLogsTestHandler(repo *requestLogRepoStub, accounts []store.AccountListItem) http.Handler {
	return newRequestLogsTestHandlerWithProvider(repo, requestLogAccountsStub(accounts))
}

func newRequestLogsTestHandlerWithProvider(repo *requestLogRepoStub, accounts RequestLogAccountProvider) http.Handler {
	mux := http.NewServeMux()
	RegisterRequestLogsHandler(
		mux,
		NewRequestLogsHandler(
			core.NewRequestService(repo),
			accounts,
			slog.New(slog.NewTextHandler(io.Discard, nil)),
		),
		nil,
	)
	return mux
}

func runRequestLogsRequest(handler http.Handler, method string, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decodeRequestsListBody(t *testing.T, rec *httptest.ResponseRecorder) generatedadminapi.RequestsListResponseBody {
	t.Helper()
	var body generatedadminapi.RequestsListResponseBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body
}

type requestLogAccountsStub []store.AccountListItem

func (s requestLogAccountsStub) ListForAdminAPI(context.Context) ([]store.AccountListItem, error) {
	return []store.AccountListItem(s), nil
}

type requestLogAccountsErrStub struct {
	err error
}

func (s requestLogAccountsErrStub) ListForAdminAPI(context.Context) ([]store.AccountListItem, error) {
	return nil, s.err
}

type requestLogRepoStub struct {
	records    []domain.RequestRecord
	options    core.RequestFilterOptions
	lastQuery  core.QueryParams
	queryErr   error
	getErr     error
	optionsErr error
}

func (s *requestLogRepoStub) Insert(context.Context, *domain.RequestRecord) error { return nil }

func (s *requestLogRepoStub) GetByID(_ context.Context, id int64) (*domain.RequestRecord, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	for _, record := range s.records {
		if record.ID == id {
			return &record, nil
		}
	}
	return nil, domain.ErrRequestRecordNotFound
}

func (s *requestLogRepoStub) Query(_ context.Context, params core.QueryParams) ([]domain.RequestRecord, error) {
	s.lastQuery = params
	if s.queryErr != nil {
		return nil, s.queryErr
	}
	if params.Limit > 0 && params.Limit < len(s.records) {
		return s.records[:params.Limit], nil
	}
	return s.records, nil
}

func (s *requestLogRepoStub) UsageSummary(context.Context) (core.RequestUsageSummary, error) {
	return core.RequestUsageSummary{}, nil
}

func (s *requestLogRepoStub) FilterOptions(_ context.Context, params core.QueryParams) (core.RequestFilterOptions, error) {
	s.lastQuery = params
	if s.optionsErr != nil {
		return core.RequestFilterOptions{}, s.optionsErr
	}
	return s.options, nil
}

func (s *requestLogRepoStub) DeleteBefore(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func stringPtr(value string) *string { return &value }

func int64Ptr(value int64) *int64 { return &value }

package domain

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// randRead is the random source for NewRequestID; tests may replace it to assert failure handling.
var randRead = rand.Read

const (
	OutcomeSuccess            = "success"
	OutcomeUpstreamError      = "upstream_error"
	OutcomeNoAvailableAccount = "no_available_account"
	OutcomeRouterError        = "router_error"
	OutcomeAccountUnavailable = "account_unavailable"
	OutcomeUpstreamTimeout    = "upstream_timeout"
	OutcomeCancelled          = "cancelled"
	OutcomeNoExtractableText  = "no_extractable_text"

	ResponseModeJSON      = "json"
	ResponseModeSSE       = "sse"
	ResponseModeWebSocket = "websocket"
)

type JSONMap map[string]any

func (j JSONMap) Value() ([]byte, error) {
	if j == nil {
		return nil, nil
	}
	return json.Marshal(j)
}

func (j *JSONMap) Scan(src []byte) error {
	if src == nil {
		*j = nil
		return nil
	}
	m := make(JSONMap)
	if err := json.Unmarshal(src, &m); err != nil {
		return err
	}
	*j = m
	return nil
}

type RequestRecord struct {
	ID                   int64     `xorm:"pk autoincr 'id'" json:"id"`
	RequestID            string    `xorm:"not null unique 'request_id'" json:"request_id"`
	CreatedAt            time.Time `xorm:"created not null index 'created_at'" json:"created_at"`
	ClientIP             string    `xorm:"'client_ip'" json:"client_ip"`
	UpstreamAccountID    *int64    `xorm:"index 'upstream_account_id'" json:"upstream_account_id,omitempty"`
	SessionKey           *string   `xorm:"index 'session_key'" json:"session_key,omitempty"`
	Method               string    `xorm:"not null 'method'" json:"method"`
	Path                 string    `xorm:"not null 'path'" json:"path"`
	StatusCode           int       `xorm:"not null 'status_code'" json:"status_code"`
	LatencyMs            int       `xorm:"not null 'latency_ms'" json:"latency_ms"`
	TTFTMs               *int      `xorm:"'ttft_ms'" json:"ttft_ms,omitempty"`
	Outcome              string    `xorm:"not null index 'outcome'" json:"outcome"`
	ErrorCode            *string   `xorm:"'error_code'" json:"error_code,omitempty"`
	Model                *string   `xorm:"'model'" json:"model,omitempty"`
	ModelParams          JSONMap   `xorm:"json 'model_params'" json:"model_params,omitempty"`
	RouterMetadata       JSONMap   `xorm:"json 'router_metadata'" json:"router_metadata,omitempty"`
	ResponseMode         string    `xorm:"not null default('json') 'response_mode'" json:"response_mode"`
	TokenUsage           JSONMap   `xorm:"json 'token_usage'" json:"token_usage,omitempty"`
	ClientRequestBody    *string   `xorm:"'client_request_body'" json:"client_request_body,omitempty"`
	UpstreamRequestBody  *string   `xorm:"'upstream_request_body'" json:"upstream_request_body,omitempty"`
	UpstreamResponseBody *string   `xorm:"'upstream_response_body'" json:"upstream_response_body,omitempty"`
}

func (r RequestRecord) TableName() string {
	return "request_records"
}

func NewRequestID() string {
	b := make([]byte, 8)
	if _, err := randRead(b); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	return "req_" + hex.EncodeToString(b)
}

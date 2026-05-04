CREATE TABLE upstream_accounts (
    id          bigserial PRIMARY KEY,
    name        text NOT NULL,
    provider    text NOT NULL DEFAULT 'openai',
    api_key     text NOT NULL,
    base_url    text,
    status      text NOT NULL DEFAULT 'active',
    created_at  timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_upstream_accounts_status ON upstream_accounts (status);

CREATE TABLE request_records (
    id                   bigserial PRIMARY KEY,
    request_id           text NOT NULL UNIQUE,
    created_at           timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    upstream_account_id  bigint REFERENCES upstream_accounts(id) ON DELETE SET NULL,
    session_key          text,
    method               text NOT NULL,
    path                 text NOT NULL,
    status_code          integer NOT NULL,
    latency_ms           integer NOT NULL,
    outcome              text NOT NULL,
    error_code           text,
    model                text,
    model_params         jsonb,
    router_metadata      jsonb,
    response_mode        text NOT NULL DEFAULT 'json',
    token_usage          jsonb,
    request_body         text,
    response_body        text
);

-- Hot-path indexes match the admin list API sort (created_at DESC). id DESC
-- is a secondary key so cursor pagination is stable when multiple rows land
-- in the same microsecond (burst traffic). Mirrors codex-lb's
-- (requested_at DESC, id DESC) convention.
CREATE INDEX idx_request_records_created_at          ON request_records (created_at DESC, id DESC);
CREATE INDEX idx_request_records_request_id          ON request_records (request_id);
CREATE INDEX idx_request_records_account_created     ON request_records (upstream_account_id, created_at DESC, id DESC);
CREATE INDEX idx_request_records_outcome_created     ON request_records (outcome, created_at DESC, id DESC);
CREATE INDEX idx_request_records_session_key_created ON request_records (session_key, created_at DESC, id DESC) WHERE session_key IS NOT NULL;

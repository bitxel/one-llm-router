CREATE TABLE upstream_accounts (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL,
    provider    TEXT NOT NULL DEFAULT 'openai',
    api_key     TEXT NOT NULL,
    base_url    TEXT,
    status      TEXT NOT NULL DEFAULT 'active',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_upstream_accounts_status ON upstream_accounts (status);

CREATE TABLE request_records (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    request_id           TEXT NOT NULL UNIQUE,
    created_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    upstream_account_id  INTEGER REFERENCES upstream_accounts(id) ON DELETE SET NULL,
    session_key          TEXT,
    method               TEXT NOT NULL,
    path                 TEXT NOT NULL,
    status_code          INTEGER NOT NULL,
    latency_ms           INTEGER NOT NULL,
    outcome              TEXT NOT NULL,
    error_code           TEXT,
    model                TEXT,
    model_params         TEXT,
    router_metadata      TEXT,
    response_mode        TEXT NOT NULL DEFAULT 'json',
    token_usage          TEXT,
    request_body         TEXT,
    response_body        TEXT
);

-- Hot-path indexes mirror codex-lb: filter columns first, created_at DESC,
-- id DESC tiebreaker so cursor pagination is stable.
CREATE INDEX idx_request_records_created_at          ON request_records (created_at DESC, id DESC);
CREATE INDEX idx_request_records_request_id          ON request_records (request_id);
CREATE INDEX idx_request_records_account_created     ON request_records (upstream_account_id, created_at DESC, id DESC);
CREATE INDEX idx_request_records_outcome_created     ON request_records (outcome, created_at DESC, id DESC);
CREATE INDEX idx_request_records_session_key_created ON request_records (session_key, created_at DESC, id DESC);

CREATE TABLE upstream_accounts (
    id          BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    name        VARCHAR(255) NOT NULL,
    provider    VARCHAR(64) NOT NULL DEFAULT 'openai',
    api_key     TEXT NOT NULL,
    base_url    TEXT NULL,
    status      VARCHAR(16) NOT NULL DEFAULT 'active',
    created_at  DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at  DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_upstream_accounts_status ON upstream_accounts (status);

CREATE TABLE request_records (
    id                   BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    request_id           VARCHAR(64) NOT NULL,
    created_at           DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    upstream_account_id  BIGINT NULL,
    session_key          VARCHAR(255) NULL,
    method               VARCHAR(16) NOT NULL,
    path                 VARCHAR(512) NOT NULL,
    status_code          INT NOT NULL,
    latency_ms           INT NOT NULL,
    outcome              VARCHAR(32) NOT NULL,
    error_code           VARCHAR(64) NULL,
    model                VARCHAR(128) NULL,
    model_params         JSON NULL,
    router_metadata      JSON NULL,
    response_mode        VARCHAR(16) NOT NULL DEFAULT 'json',
    token_usage          JSON NULL,
    request_body         MEDIUMTEXT NULL,
    response_body        MEDIUMTEXT NULL,
    UNIQUE KEY uq_request_records_request_id (request_id),
    CONSTRAINT fk_request_records_account
        FOREIGN KEY (upstream_account_id) REFERENCES upstream_accounts(id)
        ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Hot-path indexes mirror codex-lb's pattern:
--   (filter_cols..., created_at DESC, id DESC)
-- created_at DESC matches the admin list API sort order; id DESC is a
-- tiebreaker so cursor pagination is stable even when two rows share the
-- same created_at microsecond (burst traffic / clock fuzz).
CREATE INDEX idx_request_records_created_at           ON request_records (created_at DESC, id DESC);
CREATE INDEX idx_request_records_account_created      ON request_records (upstream_account_id, created_at DESC, id DESC);
CREATE INDEX idx_request_records_outcome_created      ON request_records (outcome, created_at DESC, id DESC);
CREATE INDEX idx_request_records_session_key_created  ON request_records (session_key, created_at DESC, id DESC);

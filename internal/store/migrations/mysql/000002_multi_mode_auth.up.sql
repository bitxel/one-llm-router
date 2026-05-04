-- 002 → 003: Multi-mode Codex auth (Feature 003).
--
-- MySQL ≥ 8.0 supports multi-clause ALTER TABLE and inline CHECK
-- constraints. Token columns use VARBINARY(8192) — larger than the
-- original 4 KB ceiling because JWT access tokens with embedded
-- claims routinely exceed 4 KB, and OpenAI rotates refresh tokens
-- whose size is provider-controlled.
--
-- Timestamp types for the OAuth-only columns (last_refresh,
-- access_expires_at) use TIMESTAMP(6) rather than DATETIME(6)
-- because RefreshIfStale compares wall-clocks across router
-- restarts and we want the DB to convert session-local times to
-- UTC on write and back on read. DATETIME is timezone-agnostic and
-- would silently mis-compare if two routers ran with different
-- session timezones. TIMESTAMP's 2038 cap is irrelevant: OAuth
-- `expires_in` is at most a few days and `last_refresh` is
-- recomputed on every refresh. See data-model.md §Timestamp types
-- and §Row-level invariants rule 1.
-- (created_at/updated_at in 001 stay DATETIME(6) for backward
-- compatibility; those are written only via CURRENT_TIMESTAMP(6)
-- which always resolves to the server's session zone.)

ALTER TABLE upstream_accounts
    ADD COLUMN auth_method         VARCHAR(32)       NOT NULL DEFAULT 'api_key',
    ADD COLUMN access_token        VARBINARY(8192)   NULL,
    ADD COLUMN refresh_token       VARBINARY(8192)   NULL,
    ADD COLUMN id_token            VARBINARY(8192)   NULL,
    ADD COLUMN last_refresh        TIMESTAMP(6)      NULL DEFAULT NULL,
    ADD COLUMN access_expires_at   TIMESTAMP(6)      NULL DEFAULT NULL,
    ADD COLUMN email               VARCHAR(320)      NULL,
    ADD COLUMN plan_type           VARCHAR(64)       NULL,
    ADD COLUMN chatgpt_account_id  VARCHAR(128)      NULL,
    ADD CONSTRAINT chk_auth_method
        CHECK (auth_method IN ('api_key','oauth_browser','oauth_device','oauth_import')),
    MODIFY COLUMN api_key TEXT NULL;

CREATE INDEX idx_upstream_accounts_auth_method ON upstream_accounts (auth_method);

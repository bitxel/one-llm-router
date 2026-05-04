-- 002 → 003: Multi-mode Codex auth (Feature 003).
--
-- PostgreSQL supports ALTER TABLE ... ADD COLUMN / ALTER COLUMN
-- natively, so we do not need SQLite's table-rename dance. All 9 new
-- columns + the NOT NULL relaxation land in a single ALTER statement
-- inside the golang-migrate transaction.
--
-- Token columns use BYTEA (not TEXT) so we store raw bytes, not
-- base64-re-encoded strings. BYTEA in Postgres has no practical size
-- limit (up to 1 GB per row), so no VARBINARY-style ceiling is needed.
--
-- The CHECK constraint on auth_method is defined inline so a bad
-- insert is rejected at the DB layer even if domain.Validate() is
-- bypassed (defence-in-depth).

ALTER TABLE upstream_accounts
    ADD COLUMN auth_method         TEXT        NOT NULL DEFAULT 'api_key',
    ADD COLUMN access_token        BYTEA       NULL,
    ADD COLUMN refresh_token       BYTEA       NULL,
    ADD COLUMN id_token            BYTEA       NULL,
    ADD COLUMN last_refresh        TIMESTAMPTZ NULL,
    ADD COLUMN access_expires_at   TIMESTAMPTZ NULL,
    ADD COLUMN email               TEXT        NULL,
    ADD COLUMN plan_type           TEXT        NULL,
    ADD COLUMN chatgpt_account_id  TEXT        NULL,
    ADD CONSTRAINT chk_auth_method
        CHECK (auth_method IN ('api_key','oauth_browser','oauth_device','oauth_import'));

ALTER TABLE upstream_accounts ALTER COLUMN api_key DROP NOT NULL;

CREATE INDEX idx_upstream_accounts_auth_method ON upstream_accounts (auth_method);

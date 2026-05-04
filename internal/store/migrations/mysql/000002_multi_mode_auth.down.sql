-- Rollback of 000002_multi_mode_auth.up.sql (MySQL).
--
-- Drop the 9 new columns and restore api_key NOT NULL. OAuth rows
-- (api_key IS NULL) are DELETEd first — there is no 002 column to
-- hold their access_token bytes. Matches the PostgreSQL symmetric
-- rollback; see data-model.md §Rollback.

DELETE FROM upstream_accounts WHERE api_key IS NULL;

ALTER TABLE upstream_accounts MODIFY COLUMN api_key TEXT NOT NULL;

ALTER TABLE upstream_accounts DROP INDEX idx_upstream_accounts_auth_method;

ALTER TABLE upstream_accounts
    DROP CHECK chk_auth_method,
    DROP COLUMN auth_method,
    DROP COLUMN access_token,
    DROP COLUMN refresh_token,
    DROP COLUMN id_token,
    DROP COLUMN last_refresh,
    DROP COLUMN access_expires_at,
    DROP COLUMN email,
    DROP COLUMN plan_type,
    DROP COLUMN chatgpt_account_id;

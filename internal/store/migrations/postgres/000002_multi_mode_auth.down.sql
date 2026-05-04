-- Rollback of 000002_multi_mode_auth.up.sql (PostgreSQL).
--
-- Drop the 9 new columns and restore api_key NOT NULL. OAuth rows
-- (api_key IS NULL) are DELETEd first — there is no 002 column to
-- hold their access_token bytes, so restoring NOT NULL would fail.
-- This is documented in data-model.md §Rollback and the operator is
-- warned before running `one-llm-router migrate down`.

DELETE FROM upstream_accounts WHERE api_key IS NULL;

ALTER TABLE upstream_accounts ALTER COLUMN api_key SET NOT NULL;

DROP INDEX IF EXISTS idx_upstream_accounts_auth_method;

ALTER TABLE upstream_accounts
    DROP CONSTRAINT IF EXISTS chk_auth_method,
    DROP COLUMN IF EXISTS auth_method,
    DROP COLUMN IF EXISTS access_token,
    DROP COLUMN IF EXISTS refresh_token,
    DROP COLUMN IF EXISTS id_token,
    DROP COLUMN IF EXISTS last_refresh,
    DROP COLUMN IF EXISTS access_expires_at,
    DROP COLUMN IF EXISTS email,
    DROP COLUMN IF EXISTS plan_type,
    DROP COLUMN IF EXISTS chatgpt_account_id;

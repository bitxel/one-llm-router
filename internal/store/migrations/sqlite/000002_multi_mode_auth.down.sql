-- Rollback of 000002_multi_mode_auth.up.sql (SQLite).
--
-- SQLite ≥ 3.35 supports `ALTER TABLE … DROP COLUMN` natively, so the
-- rollback can drop the 9 new columns one at a time and reinstate the
-- api_key NOT NULL constraint via the same writable_schema trick the
-- up migration used — no table rewrite, no FK risk.
--
-- Data policy (data-model.md §Rollback):
--   OAuth rows written under 003 are LOST on rollback. There is no
--   002 column that can hold access_token bytes, so we DELETE every
--   row whose api_key IS NULL before reinstating NOT NULL — otherwise
--   the bump would fail with "NOT NULL constraint failed" on the
--   OAuth rows. The operator is warned before running
--   `one-llm-router migrate down`.

DELETE FROM upstream_accounts WHERE api_key IS NULL;

DROP INDEX IF EXISTS idx_upstream_accounts_auth_method;

ALTER TABLE upstream_accounts DROP COLUMN chatgpt_account_id;
ALTER TABLE upstream_accounts DROP COLUMN plan_type;
ALTER TABLE upstream_accounts DROP COLUMN email;
ALTER TABLE upstream_accounts DROP COLUMN access_expires_at;
ALTER TABLE upstream_accounts DROP COLUMN last_refresh;
ALTER TABLE upstream_accounts DROP COLUMN id_token;
ALTER TABLE upstream_accounts DROP COLUMN refresh_token;
ALTER TABLE upstream_accounts DROP COLUMN access_token;
ALTER TABLE upstream_accounts DROP COLUMN auth_method;

PRAGMA writable_schema = 1;

UPDATE sqlite_master
   SET sql = replace(sql, 'api_key     TEXT NULL,', 'api_key     TEXT NOT NULL,')
 WHERE type = 'table'
   AND name = 'upstream_accounts';

PRAGMA writable_schema = 0;

-- Bump schema_version one more step so the restored NOT NULL takes
-- effect immediately on this connection.
PRAGMA schema_version = 4;

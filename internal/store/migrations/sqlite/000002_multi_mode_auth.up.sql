-- 002 → 003: Multi-mode Codex auth (Feature 003).
--
-- Adds 9 new columns to upstream_accounts (1 NOT NULL discriminator
-- with DEFAULT 'api_key' + 8 nullable OAuth fields) and relaxes the
-- api_key NOT NULL constraint so oauth_* rows can omit it.
--
-- Zero backfill: every existing row ends up auth_method='api_key' via
-- the DEFAULT, with all 8 OAuth columns NULL — the legal shape for
-- api_key rows per data-model.md §Invariants rule 1.
--
-- SQLite-specific challenge: ALTER COLUMN does not exist, and the
-- classic table-rename dance fails here because:
--
--   1. golang-migrate wraps every SQLite migration in a transaction,
--      and `PRAGMA foreign_keys=OFF` is a connection-level setting
--      that becomes a no-op once a transaction is open.
--
--   2. `ALTER TABLE upstream_accounts RENAME TO …` in SQLite ≥ 3.25
--      automatically rewrites FK references in other tables to follow
--      the new name (legacy_alter_table=ON does NOT suppress this
--      inside a transaction — verified empirically on SQLite 3.51).
--      That leaves request_records pointing at the renamed table,
--      which we then DROP, so the FK dangles and the next INSERT into
--      request_records fails with "no such table".
--
-- Solution: ADD COLUMN for the 9 new columns (SQLite supports this in
-- place) and use `PRAGMA writable_schema` to edit sqlite_master in
-- place, surgically replacing "api_key TEXT NOT NULL" with
-- "api_key TEXT NULL" in the table's CREATE statement. A
-- `PRAGMA schema_version` bump forces SQLite to reload the schema
-- within the same connection so the relaxed constraint takes effect
-- immediately. No table rewrite, no FK risk.
--
-- The writable_schema edit keys off the EXACT byte-for-byte CREATE
-- statement produced by 000001_init.up.sql (line 5):
--   `    api_key     TEXT NOT NULL,`
-- Any hand-edit to that line in 000001 will break this migration;
-- a paired regression test (T-010 verify) runs up + down + up to
-- catch such drift.
--
-- See specs/003-multi-mode-codex-auth/data-model.md §Migration Strategy.

ALTER TABLE upstream_accounts ADD COLUMN auth_method         TEXT     NOT NULL DEFAULT 'api_key';
ALTER TABLE upstream_accounts ADD COLUMN access_token        BLOB     NULL;
ALTER TABLE upstream_accounts ADD COLUMN refresh_token       BLOB     NULL;
ALTER TABLE upstream_accounts ADD COLUMN id_token            BLOB     NULL;
ALTER TABLE upstream_accounts ADD COLUMN last_refresh        DATETIME NULL;
ALTER TABLE upstream_accounts ADD COLUMN access_expires_at   DATETIME NULL;
ALTER TABLE upstream_accounts ADD COLUMN email               TEXT     NULL;
ALTER TABLE upstream_accounts ADD COLUMN plan_type           TEXT     NULL;
ALTER TABLE upstream_accounts ADD COLUMN chatgpt_account_id  TEXT     NULL;

PRAGMA writable_schema = 1;

UPDATE sqlite_master
   SET sql = replace(sql, 'api_key     TEXT NOT NULL,', 'api_key     TEXT NULL,')
 WHERE type = 'table'
   AND name = 'upstream_accounts';

PRAGMA writable_schema = 0;

-- Bump schema_version so SQLite reloads the edited CREATE statement
-- and the NOT NULL relaxation takes effect on this connection (and
-- all future ones).
PRAGMA schema_version = 3;

CREATE INDEX idx_upstream_accounts_auth_method ON upstream_accounts (auth_method);

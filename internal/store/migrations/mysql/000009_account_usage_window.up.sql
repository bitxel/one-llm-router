ALTER TABLE upstream_accounts ADD COLUMN primary_reset_at DATETIME NULL;
ALTER TABLE upstream_accounts ADD COLUMN secondary_reset_at DATETIME NULL;
ALTER TABLE upstream_accounts ADD COLUMN primary_window_seconds BIGINT NULL;
ALTER TABLE upstream_accounts ADD COLUMN secondary_window_seconds BIGINT NULL;

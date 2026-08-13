ALTER TABLE upstream_accounts ADD COLUMN primary_reset_at DATETIME;
ALTER TABLE upstream_accounts ADD COLUMN secondary_reset_at DATETIME;
ALTER TABLE upstream_accounts ADD COLUMN primary_window_seconds INTEGER;
ALTER TABLE upstream_accounts ADD COLUMN secondary_window_seconds INTEGER;

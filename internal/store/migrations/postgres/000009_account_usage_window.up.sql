ALTER TABLE upstream_accounts ADD COLUMN primary_reset_at TIMESTAMP WITH TIME ZONE;
ALTER TABLE upstream_accounts ADD COLUMN secondary_reset_at TIMESTAMP WITH TIME ZONE;
ALTER TABLE upstream_accounts ADD COLUMN primary_window_seconds BIGINT;
ALTER TABLE upstream_accounts ADD COLUMN secondary_window_seconds BIGINT;

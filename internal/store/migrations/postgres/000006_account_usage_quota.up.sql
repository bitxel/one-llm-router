ALTER TABLE upstream_accounts ADD COLUMN primary_used_percent DOUBLE PRECISION;
ALTER TABLE upstream_accounts ADD COLUMN secondary_used_percent DOUBLE PRECISION;
ALTER TABLE upstream_accounts ADD COLUMN usage_updated_at TIMESTAMP WITH TIME ZONE;

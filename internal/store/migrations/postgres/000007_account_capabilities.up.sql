ALTER TABLE upstream_accounts ADD COLUMN capabilities JSONB DEFAULT '[]'::jsonb;

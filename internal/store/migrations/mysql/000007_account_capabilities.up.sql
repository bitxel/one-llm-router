ALTER TABLE upstream_accounts ADD COLUMN capabilities JSON DEFAULT (JSON_ARRAY());

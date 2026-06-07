CREATE TABLE IF NOT EXISTS account_models (
    id          SERIAL PRIMARY KEY,
    account_id  INTEGER NOT NULL REFERENCES upstream_accounts(id) ON DELETE CASCADE,
    model_id    TEXT    NOT NULL,
    source      TEXT    NOT NULL DEFAULT 'manual' CHECK(source IN ('manual', 'upstream')),
    created_at  TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at  TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(account_id, model_id)
);

CREATE INDEX IF NOT EXISTS idx_account_models_account_id ON account_models(account_id);
CREATE INDEX IF NOT EXISTS idx_account_models_model_id   ON account_models(model_id);

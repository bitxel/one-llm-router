CREATE TABLE IF NOT EXISTS account_models (
    id          INTEGER PRIMARY KEY AUTO_INCREMENT,
    account_id  INTEGER NOT NULL,
    model_id    VARCHAR(255) NOT NULL,
    source      VARCHAR(16)  NOT NULL DEFAULT 'manual',
    created_at  TIMESTAMP(6) DEFAULT CURRENT_TIMESTAMP(6),
    updated_at  TIMESTAMP(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    UNIQUE KEY uk_account_model (account_id, model_id),
    CONSTRAINT fk_account_models_account FOREIGN KEY (account_id) REFERENCES upstream_accounts(id) ON DELETE CASCADE,
    CONSTRAINT chk_account_models_source CHECK (source IN ('manual', 'upstream'))
);

CREATE INDEX idx_account_models_account_id ON account_models(account_id);
CREATE INDEX idx_account_models_model_id   ON account_models(model_id);

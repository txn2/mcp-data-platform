ALTER TABLE portal_assets ADD COLUMN IF NOT EXISTS idempotency_key TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS idx_portal_assets_idempotency
    ON portal_assets (owner_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

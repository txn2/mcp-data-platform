DROP INDEX IF EXISTS idx_portal_assets_idempotency;

ALTER TABLE portal_assets DROP COLUMN IF EXISTS idempotency_key;

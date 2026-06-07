-- Reverse 000061. Prompt shares cannot survive the restored two-target check,
-- so remove them before dropping the column.
ALTER TABLE portal_shares DROP CONSTRAINT IF EXISTS chk_shares_target;
DELETE FROM portal_shares WHERE prompt_id IS NOT NULL;
DROP INDEX IF EXISTS idx_portal_shares_prompt_id;
ALTER TABLE portal_shares DROP COLUMN IF EXISTS prompt_id;
ALTER TABLE portal_shares ADD CONSTRAINT chk_shares_target CHECK (
    (asset_id IS NOT NULL AND collection_id IS NULL) OR
    (asset_id IS NULL AND collection_id IS NOT NULL)
);

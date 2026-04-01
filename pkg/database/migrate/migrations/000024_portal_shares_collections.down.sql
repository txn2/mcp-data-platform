ALTER TABLE portal_shares DROP CONSTRAINT IF EXISTS chk_shares_target;

DROP INDEX IF EXISTS idx_portal_shares_collection_id;

-- Remove collection shares before restoring NOT NULL on asset_id.
DELETE FROM portal_shares WHERE collection_id IS NOT NULL;

ALTER TABLE portal_shares DROP COLUMN IF EXISTS collection_id;

ALTER TABLE portal_shares ALTER COLUMN asset_id SET NOT NULL;

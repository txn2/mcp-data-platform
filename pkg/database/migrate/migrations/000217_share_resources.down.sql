DELETE FROM access_requests WHERE target_type = 'resource';
ALTER TABLE access_requests DROP CONSTRAINT IF EXISTS access_requests_target_type_check;
ALTER TABLE access_requests ADD CONSTRAINT access_requests_target_type_check
    CHECK (target_type IN ('asset', 'collection', 'prompt'));

DELETE FROM portal_shares WHERE resource_id IS NOT NULL;

ALTER TABLE portal_shares DROP CONSTRAINT IF EXISTS chk_shares_target;
ALTER TABLE portal_shares ADD CONSTRAINT chk_shares_target CHECK (
    (asset_id IS NOT NULL AND collection_id IS NULL AND prompt_id IS NULL) OR
    (asset_id IS NULL AND collection_id IS NOT NULL AND prompt_id IS NULL) OR
    (asset_id IS NULL AND collection_id IS NULL AND prompt_id IS NOT NULL)
);

DROP INDEX IF EXISTS idx_portal_shares_resource_id;
ALTER TABLE portal_shares DROP COLUMN IF EXISTS resource_id;

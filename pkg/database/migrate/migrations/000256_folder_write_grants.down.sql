DELETE FROM portal_shares WHERE folder_path IS NOT NULL;
ALTER TABLE portal_shares DROP CONSTRAINT IF EXISTS chk_folder_share_editor;
ALTER TABLE portal_shares DROP CONSTRAINT IF EXISTS chk_shares_target;
ALTER TABLE portal_shares ADD CONSTRAINT chk_shares_target
    CHECK (num_nonnulls(asset_id, collection_id, prompt_id, resource_id) = 1);
DROP INDEX IF EXISTS idx_portal_shares_folder_path;
ALTER TABLE portal_shares DROP COLUMN IF EXISTS folder_path;

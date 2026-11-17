-- #2684: an administrator grants a person, an address or a persona write on a
-- folder of the Global library. The grant is a share whose target is the
-- folder's path: it covers the folder and every path beneath it by segment,
-- follows the folder when an administrator renames or moves it, and is
-- deleted with it. Only the editor permission means anything on a Global
-- folder, which everyone signed in already reads.
ALTER TABLE portal_shares ADD COLUMN IF NOT EXISTS folder_path TEXT;

CREATE INDEX IF NOT EXISTS idx_portal_shares_folder_path ON portal_shares (folder_path)
    WHERE folder_path IS NOT NULL;

-- Every share has exactly one target.
ALTER TABLE portal_shares DROP CONSTRAINT IF EXISTS chk_shares_target;
ALTER TABLE portal_shares ADD CONSTRAINT chk_shares_target
    CHECK (num_nonnulls(asset_id, collection_id, prompt_id, resource_id, folder_path) = 1);

ALTER TABLE portal_shares DROP CONSTRAINT IF EXISTS chk_folder_share_editor;
ALTER TABLE portal_shares ADD CONSTRAINT chk_folder_share_editor
    CHECK (folder_path IS NULL OR (permission = 'editor' AND folder_path <> ''));

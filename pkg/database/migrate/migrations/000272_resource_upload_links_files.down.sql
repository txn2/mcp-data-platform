DROP INDEX IF EXISTS idx_resource_upload_links_user;
ALTER TABLE resource_upload_links
    DROP COLUMN IF EXISTS revoked_at,
    DROP COLUMN IF EXISTS resource_ids,
    DROP COLUMN IF EXISTS files_used,
    DROP COLUMN IF EXISTS max_files;

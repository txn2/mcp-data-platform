DROP INDEX IF EXISTS idx_audit_logs_resource_read;
DROP INDEX IF EXISTS idx_resources_last_read;
ALTER TABLE resources DROP COLUMN IF EXISTS last_read_at;
DROP INDEX IF EXISTS idx_resource_versions_resource;
DROP TABLE IF EXISTS resource_versions;

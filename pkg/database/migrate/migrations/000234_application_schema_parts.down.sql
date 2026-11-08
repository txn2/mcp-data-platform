DROP TABLE IF EXISTS application_run_roles;
ALTER TABLE applications
    DROP COLUMN IF EXISTS schema_added_at,
    DROP COLUMN IF EXISTS schema_added_by,
    DROP COLUMN IF EXISTS schema_adopted_version;

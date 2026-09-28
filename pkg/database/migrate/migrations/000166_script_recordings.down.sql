ALTER TABLE scripts DROP COLUMN IF EXISTS tests_optional;
ALTER TABLE script_versions DROP COLUMN IF EXISTS change_agreed_at;
ALTER TABLE script_versions DROP COLUMN IF EXISTS change_agreed_by;
ALTER TABLE script_versions DROP COLUMN IF EXISTS change_summary;
DROP TABLE IF EXISTS script_recordings;

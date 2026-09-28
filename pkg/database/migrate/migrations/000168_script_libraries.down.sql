DROP INDEX IF EXISTS idx_scripts_library_name;
ALTER TABLE scripts DROP COLUMN IF EXISTS library_loads;
ALTER TABLE scripts DROP COLUMN IF EXISTS library;

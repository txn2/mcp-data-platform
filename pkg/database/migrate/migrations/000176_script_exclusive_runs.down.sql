DROP INDEX IF EXISTS idx_script_runs_exclusive_open;
ALTER TABLE script_runs DROP COLUMN IF EXISTS exclusive;
ALTER TABLE scripts DROP COLUMN IF EXISTS exclusive;

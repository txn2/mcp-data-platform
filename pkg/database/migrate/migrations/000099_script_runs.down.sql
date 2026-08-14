-- Dropping script_runs first releases the RESTRICT reference it holds on
-- script_versions, so the column drops below cannot be blocked by run history.
DROP INDEX IF EXISTS idx_script_runs_finished;
DROP INDEX IF EXISTS idx_script_runs_script;
DROP INDEX IF EXISTS idx_script_runs_due;
DROP TABLE IF EXISTS script_runs;

-- The execution gate loses its writer: a script left pointing at a version
-- whose grant is gone must not stay executable, so the pointer is cleared with
-- the columns that gave it meaning.
UPDATE scripts SET approved_version_id = NULL WHERE approved_version_id IS NOT NULL;

ALTER TABLE script_versions DROP COLUMN IF EXISTS grants;
ALTER TABLE script_versions DROP COLUMN IF EXISTS author_roles;

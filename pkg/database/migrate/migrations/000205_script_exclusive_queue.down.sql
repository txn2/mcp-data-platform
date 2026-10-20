DROP INDEX IF EXISTS idx_script_runs_queued_running;
ALTER TABLE script_runs DROP COLUMN IF EXISTS queued;
ALTER TABLE scripts ADD COLUMN IF NOT EXISTS exclusive BOOLEAN NOT NULL DEFAULT FALSE;
UPDATE scripts SET exclusive = TRUE WHERE exclusive_mode <> 'off';
ALTER TABLE scripts DROP COLUMN IF EXISTS exclusive_mode;

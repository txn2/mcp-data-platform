ALTER TABLE script_dry_runs DROP COLUMN IF EXISTS state_written;
ALTER TABLE script_runs DROP COLUMN IF EXISTS state_revision_written;
ALTER TABLE script_runs DROP COLUMN IF EXISTS state_written;
ALTER TABLE script_runs DROP COLUMN IF EXISTS state_read;
ALTER TABLE script_runs DROP COLUMN IF EXISTS state_revision;
DROP TABLE IF EXISTS script_state;

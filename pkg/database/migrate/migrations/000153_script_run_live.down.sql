-- A canceled run has no place in the narrower status set; it is recorded as
-- failed with the reason it already carries.
UPDATE script_runs SET status = 'failed' WHERE status = 'canceled';

ALTER TABLE script_runs DROP CONSTRAINT IF EXISTS script_runs_status_check;
ALTER TABLE script_runs ADD CONSTRAINT script_runs_status_check
    CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'skipped_overlap'));

ALTER TABLE script_runs
    DROP COLUMN IF EXISTS cancel_requested_by,
    DROP COLUMN IF EXISTS cancel_requested_at,
    DROP COLUMN IF EXISTS progress_at,
    DROP COLUMN IF EXISTS progress_total,
    DROP COLUMN IF EXISTS progress_done,
    DROP COLUMN IF EXISTS progress_message,
    DROP COLUMN IF EXISTS result;

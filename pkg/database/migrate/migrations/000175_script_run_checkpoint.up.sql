-- 000175: a run's committed state can be its last checkpoint (#2003).
--
-- platform.checkpoint stages state the platform commits however the run ends,
-- so a failed or halted incremental run keeps the cursor for the work that
-- landed. state_checkpoint says the run's state_written is that checkpoint,
-- not a final save_state, so a run's page and get_run can say it failed after
-- checkpointing.
ALTER TABLE script_runs
    ADD COLUMN IF NOT EXISTS state_checkpoint BOOLEAN NOT NULL DEFAULT FALSE;

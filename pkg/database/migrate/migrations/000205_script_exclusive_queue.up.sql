-- 000205: a script's runs may wait their turn instead of being refused (#2209).
--
-- scripts.exclusive (000176) was on or off: on, a second open run is refused.
-- An author starting a batch of runs by hand against an upstream that takes
-- one request at a time wants the second to wait instead. The setting becomes
-- a mode: off, refuse (what true was) or queue.
--
-- A queued run is pending until no other run of its script is running. Each
-- run carries the setting as it stood when written, as exclusive does, and
-- the index below holds a queue-mode script to one running run: a claim that
-- would make a second one running is refused by it, so two replicas claiming
-- at once cannot both start one.
ALTER TABLE scripts
    ADD COLUMN IF NOT EXISTS exclusive_mode TEXT NOT NULL DEFAULT 'off'
        CHECK (exclusive_mode IN ('off', 'refuse', 'queue'));
UPDATE scripts SET exclusive_mode = 'refuse' WHERE exclusive;
ALTER TABLE scripts DROP COLUMN IF EXISTS exclusive;

ALTER TABLE script_runs
    ADD COLUMN IF NOT EXISTS queued BOOLEAN NOT NULL DEFAULT FALSE;

CREATE UNIQUE INDEX IF NOT EXISTS idx_script_runs_queued_running
    ON script_runs(script_id)
    WHERE queued AND status = 'running';

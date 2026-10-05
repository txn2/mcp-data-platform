-- 000176: a script whose runs never overlap, whatever started them (#1986).
--
-- idx_script_runs_schedule_open (000100) allows one open run per schedule. A
-- run started with run_script or from the portal has no schedule_id, so it is
-- invisible to that index: a fire materialized beside a manual run, and two
-- manual runs executed at once. scripts.exclusive is the owner's setting that
-- a script has at most one open (pending or running) run.
--
-- An index predicate cannot read another table, so each run carries the
-- setting as it stood when the row was written, and the index below holds
-- one open exclusive run per script. Changing the setting re-stamps the
-- script's open runs in the same transaction, so turning it on while two runs
-- are open is refused by this index rather than leaving them both open.
-- A reclaimed or retried run is the same row, so it never counts twice.
ALTER TABLE scripts
    ADD COLUMN IF NOT EXISTS exclusive BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE script_runs
    ADD COLUMN IF NOT EXISTS exclusive BOOLEAN NOT NULL DEFAULT FALSE;

CREATE UNIQUE INDEX IF NOT EXISTS idx_script_runs_exclusive_open
    ON script_runs(script_id)
    WHERE exclusive AND status IN ('pending', 'running');

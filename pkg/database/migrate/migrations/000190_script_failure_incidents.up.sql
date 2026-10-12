-- The scheduled automations that are failing now, one row each (#2041).
--
-- A failed scheduled run mails its owner. Without this table it did so on
-- every failed run and never again once the script recovered, so an overnight
-- outage was dozens of identical emails and the end of it was silence. A row
-- is the incident: the failed run that opens it (no row present) is the one
-- that is told, the failed runs while it is open are counted on it, and the
-- run that succeeds deletes it and is told as the recovery, with what the row
-- counted.
--
-- The insert and the delete are each one statement, so two replicas finishing
-- runs of one script at once elect one opener and one closer, and the state
-- outlives a restart.
CREATE TABLE IF NOT EXISTS script_failure_incidents (
    script_id      UUID        PRIMARY KEY REFERENCES scripts(id) ON DELETE CASCADE,
    opened_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    opening_run_id TEXT        NOT NULL,            -- the failed run that opened it
    opening_cause  TEXT        NOT NULL DEFAULT '', -- that run's failure cause
    opening_error  TEXT        NOT NULL DEFAULT '', -- the last line of that run's error, which names the failure
    failed_runs    INTEGER     NOT NULL DEFAULT 1,  -- failed runs while open, the opener included
    last_failed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

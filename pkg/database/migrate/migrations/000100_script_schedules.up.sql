-- 000100: cron schedules for managed scripts (#1286).
--
-- 000099 built the run queue and noted that a schedule would be a second
-- producer of run rows, extending the trigger_kind check when it arrived. This
-- is that extension, and it adds no scheduler process: a schedule is a row that
-- says when, and materializing a fire means inserting a script_runs row the
-- existing claim predicate (scheduled_for <= NOW()) picks up on its own. The
-- platform therefore has exactly one thing that decides what executes next, and
-- it is the queue.
--
-- A script has AT MOST ONE schedule (script_id is UNIQUE). A second cadence
-- over the same code is a second script, which keeps one schedule's run history
-- readable as the refresh history of one thing rather than as two cadences
-- interleaved in a single list.
--
-- next_run_at is nullable, and NULL means "no further fire". It is otherwise
-- the due index the materializer reads; correctness does not rest on it (see
-- the unique index below), so a replica that reads a stale value costs nothing.

CREATE TABLE IF NOT EXISTS script_schedules (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    script_id    UUID        NOT NULL UNIQUE REFERENCES scripts(id) ON DELETE CASCADE,
    cron_spec    TEXT        NOT NULL,
    timezone     TEXT        NOT NULL DEFAULT 'UTC',
    -- params carries the bindings with their tokens UNEXPANDED (${fire_date}),
    -- because the schedule means "the day it fires", not the day somebody set
    -- it. Expansion happens at materialization and the expanded values land on
    -- the run, which is what makes a scheduled run reproducible.
    params       JSONB       NOT NULL DEFAULT '{}',
    enabled      BOOLEAN     NOT NULL DEFAULT TRUE,
    next_run_at  TIMESTAMPTZ,
    last_fire_at TIMESTAMPTZ,
    -- missed_fires accumulates the fires the misfire policy stepped over. The
    -- policy is fire-once-latest: after downtime spanning several fires one run
    -- materializes, for the most recent, and the rest land here. A catch-up
    -- storm the moment the platform recovers is worse than a visible gap, and
    -- this column is what makes the gap visible.
    missed_fires INTEGER     NOT NULL DEFAULT 0,
    created_by   TEXT        NOT NULL DEFAULT '',
    updated_by   TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The materializer's due query: enabled schedules whose fire has arrived.
CREATE INDEX IF NOT EXISTS idx_script_schedules_due
    ON script_schedules(next_run_at)
    WHERE enabled;

-- schedule_id is ON DELETE NO ACTION for the reason script_version_id is: both
-- refuse to let a schedule that produced runs be deleted out from under them,
-- while NO ACTION (checked at statement end) still allows deleting the SCRIPT,
-- whose cascade removes the schedule and the runs in one statement. There is
-- deliberately no way to delete a schedule on its own — a schedule is retired
-- by disabling it, so the row that explains its runs stays.
ALTER TABLE script_runs
    ADD COLUMN IF NOT EXISTS schedule_id UUID REFERENCES script_schedules(id) ON DELETE NO ACTION;

-- Both checks were written inline in 000099, so PostgreSQL named them
-- <table>_<column>_check; they are replaced here under the same names.
--
-- trigger_kind gains 'schedule'. status gains 'skipped_overlap', which is a
-- terminal status no worker ever claims: it records a fire that was not
-- executed because the previous run of the same schedule was still open.
ALTER TABLE script_runs DROP CONSTRAINT IF EXISTS script_runs_trigger_kind_check;
ALTER TABLE script_runs ADD CONSTRAINT script_runs_trigger_kind_check
    CHECK (trigger_kind IN ('tool', 'schedule'));

ALTER TABLE script_runs DROP CONSTRAINT IF EXISTS script_runs_status_check;
ALTER TABLE script_runs ADD CONSTRAINT script_runs_status_check
    CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'skipped_overlap'));

-- THE single-fire guarantee. Every replica with a worker materializes, so
-- several will notice the same fire at the same moment; they all insert, and
-- this index means exactly one of those inserts survives. Everything else about
-- materialization — the due index, the conditional advance — is an efficiency
-- measure layered over this.
--
-- It is keyed on fire_time, NOT on scheduled_for. The two are equal when a
-- scheduled run is created, but a run returned to the queue by an
-- infrastructure retry gets a NEW scheduled_for (000099 explains why), which
-- would take the row out from under a unique key built on it and let a second
-- materializer insert a duplicate for the same fire. fire_time is pinned at
-- creation and never moves, which is exactly the property this guarantee needs.
CREATE UNIQUE INDEX IF NOT EXISTS idx_script_runs_schedule_fire
    ON script_runs(schedule_id, fire_time)
    WHERE schedule_id IS NOT NULL;

-- The overlap policy, enforced rather than checked: one OPEN run per schedule.
-- A fire arriving while the previous run is still pending or running conflicts
-- here, and the materializer records the skip as its own terminal row (which
-- does not match this predicate, so it cannot conflict with itself). Following
-- the one-open-job shape index_jobs uses (000051).
CREATE UNIQUE INDEX IF NOT EXISTS idx_script_runs_schedule_open
    ON script_runs(schedule_id)
    WHERE schedule_id IS NOT NULL AND status IN ('pending', 'running');

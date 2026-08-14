-- 000099: the managed-script execution gate and its run queue (#1284).
--
-- 000098 created the script domain with approved_version_id already in place
-- but nothing able to write it: stage one deliberately shipped the authoring
-- loop only, and every draft executed under its author's own identity. This
-- migration adds the two things that make governed execution real — the
-- capability grant an approval binds to a version, and the queue that executes
-- the approved version — so the column that has been read-only since 000098
-- acquires a writer and a consumer at the same time.
--
-- script_versions gains two columns:
--
--   author_roles  The authority the author held when the snapshot was written.
--                 The middleware resolves a persona from ROLES, and an approved
--                 run happens with nobody present: no token, no session, no
--                 live identity to resolve. So the authority a run presents has
--                 to have been captured earlier, and capturing it from the
--                 AUTHOR is what keeps approval from being a privilege-granting
--                 act — an approver copies these roles and cannot widen them,
--                 so a script can never do what the person who wrote it could
--                 not already do.
--
--   grants        The capability set bound at approval: the roles above, the
--                 connections the script may query, the host bindings it may
--                 call, and the destinations its output may reach. Written only
--                 by the approval action. Grants are deliberately not
--                 persona-shaped — a persona drifts as an organization changes,
--                 while a script's needs are a static property of reviewed code
--                 — and carry no wildcards, because a reviewer has to be able to
--                 read the grant and know exactly what was approved.

ALTER TABLE script_versions
    ADD COLUMN IF NOT EXISTS author_roles TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE script_versions
    ADD COLUMN IF NOT EXISTS grants JSONB NOT NULL DEFAULT '{}';

-- script_runs is both the queue and the history of script execution, following
-- the notifications queue (000082) which in turn follows index_jobs (000051):
-- a claim predicate of "due, or leased by a worker whose lease expired" with
-- FOR UPDATE SKIP LOCKED, so crashed-worker recovery is part of claiming and
-- there is no reaper, no leader election, and no limit on how many replicas run
-- a worker.
--
-- The two roles are one table on purpose. A run's history IS its queue record —
-- which version executed, with which parameters, at whose request, how long it
-- took, what it wrote — and separating them would mean copying every column to
-- keep the history readable. Retention is therefore generous and configurable
-- (scripts.run_retention_days) rather than the queue-shaped 30 days: a
-- dashboard's run history is its refresh history, which is product surface.
--
-- id is TEXT, not UUID, because a run id is also the run's SESSION id: it is
-- minted with the dpx_ prefix and threaded onto the in-memory MCP session the
-- run drives, so every audit row the run produces carries it and the whole run
-- reads as one session instead of one session per capability call.
--
-- script_version_id is NO ACTION rather than RESTRICT, and the difference
-- matters: both refuse to let a version a run executed be deleted on its own —
-- a run cannot be explained if the code it executed is gone — but RESTRICT is
-- checked immediately, which would also block deleting the SCRIPT, whose
-- cascade removes its versions and its runs in one statement. NO ACTION is
-- checked at the end of the statement, by which time the runs are gone too.
CREATE TABLE IF NOT EXISTS script_runs (
    id                TEXT        PRIMARY KEY,
    script_id         UUID        NOT NULL REFERENCES scripts(id) ON DELETE CASCADE,
    script_version_id UUID        NOT NULL REFERENCES script_versions(id) ON DELETE NO ACTION,
    version           INTEGER     NOT NULL DEFAULT 0,
    -- 'trigger' is a reserved word in PostgreSQL; the column names what
    -- produced the run. Scheduled fires are a second producer and arrive with
    -- the scheduler, which extends this check.
    trigger_kind      TEXT        NOT NULL DEFAULT 'tool'
                                  CHECK (trigger_kind IN ('tool')),
    status            TEXT        NOT NULL DEFAULT 'pending'
                                  CHECK (status IN ('pending', 'running', 'succeeded', 'failed')),
    params            JSONB       NOT NULL DEFAULT '{}',
    -- fire_time is what the script reads as run.fire_time, and it is distinct
    -- from scheduled_for on purpose. scheduled_for is the queue's due time and
    -- MOVES when an infrastructure failure returns a run to the queue with a
    -- backoff; fire_time is pinned when the run is created and never moves, so
    -- a run delayed by a retry still computes the report it was asked for
    -- rather than one shifted by however long the platform was unwell.
    fire_time         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    requested_by      TEXT        NOT NULL DEFAULT '',
    scheduled_for     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at        TIMESTAMPTZ,
    finished_at       TIMESTAMPTZ,
    -- attempt and locked_by are the fencing token: a worker whose lease expired
    -- and whose run was reclaimed writes against a row whose (locked_by,
    -- attempt) pair has moved on, so its late write matches nothing instead of
    -- overwriting the result of the worker that took over.
    attempt           INTEGER     NOT NULL DEFAULT 0,
    locked_until      TIMESTAMPTZ,
    locked_by         TEXT        NOT NULL DEFAULT '',
    error             TEXT        NOT NULL DEFAULT '',
    log_text          TEXT        NOT NULL DEFAULT '',
    log_truncated     BOOLEAN     NOT NULL DEFAULT FALSE,
    metrics           JSONB       NOT NULL DEFAULT '{}',
    -- outputs is written as each output is persisted rather than at the end, so
    -- a reclaimed run can tell what it already wrote and a retry cannot create
    -- a second asset version for the same output.
    outputs           JSONB       NOT NULL DEFAULT '[]',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The worker's claim predicate filters on status + scheduled_for + locked_until.
CREATE INDEX IF NOT EXISTS idx_script_runs_due
    ON script_runs(status, scheduled_for);
-- Run history for one script, newest first.
CREATE INDEX IF NOT EXISTS idx_script_runs_script
    ON script_runs(script_id, created_at DESC);
-- The retention sweep deletes terminal rows by age.
CREATE INDEX IF NOT EXISTS idx_script_runs_finished
    ON script_runs(finished_at);

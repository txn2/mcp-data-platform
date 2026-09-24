-- What happened to each attempt of a script run (#1860, #1859).
--
-- A worker that dies (OOM kill, node loss) leaves its run 'running' with a
-- lease nobody renews, and the claim used to take it over with no bound, so a
-- run that killed its worker killed the next one too. reclaims counts the
-- claims that took a run over from a worker whose lease expired, apart from
-- attempt, which also counts the platform's own retries; the claim refuses a
-- run whose reclaims are spent and the worker fails it instead.
--
-- attempts is the history an operator reads to see that: one entry per
-- attempt, naming the worker, when it claimed the run, when the attempt ended
-- and how (finished, retried, released at shutdown, requeued to relieve
-- memory, or its lease expired with the worker gone). claimed_at is when the
-- current attempt was claimed and heartbeat_at when its worker last reported,
-- which is what tells a run whose worker is executing it from one whose worker
-- stopped reporting while the lease still runs.
--
-- failure_cause records why a failed run failed (script, upstream, memory,
-- worker_lost, platform), which decides whether a retry is expected to
-- succeed.
ALTER TABLE script_runs
    ADD COLUMN IF NOT EXISTS reclaims INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS attempts JSONB NOT NULL DEFAULT '[]',
    ADD COLUMN IF NOT EXISTS claimed_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS heartbeat_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS failure_cause TEXT NOT NULL DEFAULT '';

-- A run already executing when this lands was claimed at or after it started;
-- its start is the closest record of the claim there is.
UPDATE script_runs SET claimed_at = started_at WHERE status = 'running' AND claimed_at IS NULL;

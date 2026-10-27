-- 000222: an automation runs on each event a webhook source receives (#2332).
--
-- scripts.event_source names the webhook source a script runs for; empty, it
-- runs for none. Each event that source stores enqueues one run with
-- trigger_kind 'event', carrying the event (its id, type, key, time and
-- payload) as run.event. A run is made once per event: the index on
-- (script_id, event_source, event_id) refuses a second, so a retried delivery
-- or a second replica noticing the same event makes no second run.
--
-- A burst is bounded per script: past scripts.events.max_queued pending event
-- runs, an event is recorded as a run with status 'skipped_backlog' and the
-- reason, and never executed, as a skipped fire is.
ALTER TABLE scripts
    ADD COLUMN IF NOT EXISTS event_source TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_scripts_event_source
    ON scripts(event_source)
    WHERE event_source <> '';

ALTER TABLE script_runs
    ADD COLUMN IF NOT EXISTS event_source TEXT  NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS event_id     TEXT  NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS event        JSONB;

CREATE UNIQUE INDEX IF NOT EXISTS idx_script_runs_event_once
    ON script_runs(script_id, event_source, event_id)
    WHERE event_id <> '';

CREATE INDEX IF NOT EXISTS idx_script_runs_event_pending
    ON script_runs(script_id)
    WHERE trigger_kind = 'event' AND status = 'pending';

ALTER TABLE script_runs DROP CONSTRAINT IF EXISTS script_runs_trigger_kind_check;
ALTER TABLE script_runs ADD CONSTRAINT script_runs_trigger_kind_check
    CHECK (trigger_kind IN ('tool', 'schedule', 'portal', 'event'));

ALTER TABLE script_runs DROP CONSTRAINT IF EXISTS script_runs_status_check;
ALTER TABLE script_runs ADD CONSTRAINT script_runs_status_check
    CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'skipped_overlap', 'canceled', 'skipped_backlog'));

-- 000222 down: no script runs on events. The event runs are deleted, since
-- the checks below admit neither their trigger nor a skipped backlog.
DELETE FROM script_runs WHERE trigger_kind = 'event' OR status = 'skipped_backlog';

ALTER TABLE script_runs DROP CONSTRAINT IF EXISTS script_runs_status_check;
ALTER TABLE script_runs ADD CONSTRAINT script_runs_status_check
    CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'skipped_overlap', 'canceled'));

ALTER TABLE script_runs DROP CONSTRAINT IF EXISTS script_runs_trigger_kind_check;
ALTER TABLE script_runs ADD CONSTRAINT script_runs_trigger_kind_check
    CHECK (trigger_kind IN ('tool', 'schedule', 'portal'));

DROP INDEX IF EXISTS idx_script_runs_event_pending;
DROP INDEX IF EXISTS idx_script_runs_event_once;
ALTER TABLE script_runs
    DROP COLUMN IF EXISTS event,
    DROP COLUMN IF EXISTS event_id,
    DROP COLUMN IF EXISTS event_source;

DROP INDEX IF EXISTS idx_scripts_event_source;
ALTER TABLE scripts DROP COLUMN IF EXISTS event_source;

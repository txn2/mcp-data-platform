-- Scheduled runs lose the thing that produced them, so they lose the trigger
-- and the status that only a schedule can write. The rows stay: a run is
-- history, and a downgrade is not a reason to forget what executed.
--
-- A skipped_overlap row is the exception. It records a fire that was NEVER
-- executed, and the restored check has no value for it; deleting those rows is
-- the honest reversal, since nothing about them explains an execution.
DELETE FROM script_runs WHERE status = 'skipped_overlap';
UPDATE script_runs SET trigger_kind = 'tool' WHERE trigger_kind = 'schedule';

DROP INDEX IF EXISTS idx_script_runs_schedule_open;
DROP INDEX IF EXISTS idx_script_runs_schedule_fire;

ALTER TABLE script_runs DROP CONSTRAINT IF EXISTS script_runs_status_check;
ALTER TABLE script_runs ADD CONSTRAINT script_runs_status_check
    CHECK (status IN ('pending', 'running', 'succeeded', 'failed'));

ALTER TABLE script_runs DROP CONSTRAINT IF EXISTS script_runs_trigger_kind_check;
ALTER TABLE script_runs ADD CONSTRAINT script_runs_trigger_kind_check
    CHECK (trigger_kind IN ('tool'));

-- The column goes before the table it references, so the NO ACTION reference
-- cannot block the drop.
ALTER TABLE script_runs DROP COLUMN IF EXISTS schedule_id;

DROP INDEX IF EXISTS idx_script_schedules_due;
DROP TABLE IF EXISTS script_schedules;

-- Reverse 000114. Drop the dry-run accounts and narrow trigger_kind back.
--
-- Runs recorded as 'portal' are relabelled 'tool' before the check is
-- narrowed, because the alternative is a constraint that cannot be added. The
-- relabelling loses which surface asked for the run and keeps the run itself,
-- which is the right way round: a run is evidence, its label is a decoration.
--
-- The dry-run accounts are discarded. Nothing depends on them: a version with
-- no matching account reads as one nobody dry-ran, which is exactly what every
-- version read as before this migration.

DROP TABLE IF EXISTS script_dry_runs;

UPDATE script_runs SET trigger_kind = 'tool' WHERE trigger_kind = 'portal';

ALTER TABLE script_runs DROP CONSTRAINT IF EXISTS script_runs_trigger_kind_check;
ALTER TABLE script_runs ADD CONSTRAINT script_runs_trigger_kind_check
    CHECK (trigger_kind IN ('tool', 'schedule'));

-- 000132: a script carries one JSON object of state from run to run (#1537).
--
-- A run knew run.run_id, run.fire_time and run.params, and nothing else about
-- the world outside its own tool calls, so an incremental job computed its
-- window from fire_time and a gap after downtime was a person's problem. A
-- script now has one JSON object of state, persisted by the platform, read at
-- the start of a run and written by the run. A watermark is
-- state["synced_through"]; nothing here knows what the keys mean.
--
-- script_state is one row per script, deleted with the script and untouched by
-- a version save, a disable, or an ownership transfer, because state belongs
-- to the script rather than to a version of it. revision counts writes: a run's
-- write is applied in the transaction that marks the run succeeded, as an
-- upsert predicated on the revision the run read, so two runs that both read
-- revision N cannot both write N+1 -- one succeeds and the other fails at its
-- write, naming the one that wrote. run_id and updated_by say who wrote the
-- current revision: a run, or the person who reset it.
--
-- script_runs records the state read at creation (state_revision and the
-- object itself, state_read) beside params, because the state read is an
-- input of the run exactly as the parameters are, and a run delayed past a
-- reset still executes against what it recorded and then fails at its write.
-- On success it records what it saved (state_written) and the revision that
-- produced (state_revision_written); both stay NULL on a run that saved
-- nothing and on one whose write was refused.
--
-- script_dry_runs records the state a draft would have saved, beside the
-- outputs it would have written; a draft persists neither.

CREATE TABLE IF NOT EXISTS script_state (
    script_id  UUID        PRIMARY KEY REFERENCES scripts(id) ON DELETE CASCADE,
    state      JSONB       NOT NULL DEFAULT '{}',
    revision   BIGINT      NOT NULL DEFAULT 0,
    run_id     TEXT        NOT NULL DEFAULT '',
    updated_by TEXT        NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE script_runs
    ADD COLUMN IF NOT EXISTS state_revision BIGINT NOT NULL DEFAULT 0;
ALTER TABLE script_runs
    ADD COLUMN IF NOT EXISTS state_read JSONB NOT NULL DEFAULT '{}';
ALTER TABLE script_runs
    ADD COLUMN IF NOT EXISTS state_written JSONB;
ALTER TABLE script_runs
    ADD COLUMN IF NOT EXISTS state_revision_written BIGINT;

ALTER TABLE script_dry_runs
    ADD COLUMN IF NOT EXISTS state_written JSONB;

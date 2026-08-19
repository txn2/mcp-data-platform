-- 000114: the script owner's portal loop
--
-- Two facts the portal now produces and had nowhere to put (#1363, #1364).
--
-- 1. trigger_kind gains 'portal'. A run an owner asks for on the script's page
--    was recorded as 'tool', which is what run_script produces, so the run
--    history said an agent asked for a run the person was looking at when they
--    clicked it. The three producers execute identically -- same worker, same
--    grant, same audit -- and the column exists to say which one it was.
--    000100 extended this check for the scheduler and named the pattern.
--
-- 2. script_dry_runs records what a draft run did. A dry run persists nothing
--    by design (it previews exports, it never writes, and it holds no
--    approval), and that is unchanged: what is stored here is the ACCOUNT of
--    one -- who ran it, over which source, how it ended, what it printed, and
--    the shape of the outputs it would have written. Nothing here executes and
--    nothing here grants.
--
--    It exists for the reviewer. Approving a version means agreeing to run
--    code, and until now nobody could tell whether the author had ever run it.
--    The row is keyed to the SOURCE it executed rather than to a version,
--    because an author dry-runs an edit before saving it: matching by source
--    digest links the account to whichever version later carries that exact
--    source, in either order, and to no other.
--
--    source_sha256 is BYTEA. It holds a raw SHA-256, and raw digest bytes are
--    not a UTF-8 string; declaring such a column TEXT is what made the calls
--    index fail silently on every write (#1365).
--
--    log is bounded when it is captured (the engine's log cap), so storing it
--    whole is bounded too. Growth is bounded at write time instead of by a
--    sweeper: the store keeps a small number of the newest rows per author per
--    script, which is the authoring loop's own working set.

ALTER TABLE script_runs DROP CONSTRAINT IF EXISTS script_runs_trigger_kind_check;
ALTER TABLE script_runs ADD CONSTRAINT script_runs_trigger_kind_check
    CHECK (trigger_kind IN ('tool', 'schedule', 'portal'));

CREATE TABLE IF NOT EXISTS script_dry_runs (
    -- id is the run id the draft executed under, which is also its session id,
    -- so the audit rows the dry run produced are reachable from this account.
    id            TEXT        PRIMARY KEY,
    script_id     UUID        NOT NULL REFERENCES scripts(id) ON DELETE CASCADE,
    source_sha256 BYTEA       NOT NULL,
    requested_by  TEXT        NOT NULL DEFAULT '',
    status        TEXT        NOT NULL
                              CHECK (status IN ('succeeded', 'failed')),
    error         TEXT        NOT NULL DEFAULT '',
    log           TEXT        NOT NULL DEFAULT '',
    log_truncated BOOLEAN     NOT NULL DEFAULT FALSE,
    -- metrics is what the run cost, outputs the shape of what it would have
    -- written. Both are the engine's own result record, stored as it reports
    -- it rather than flattened into columns nothing queries by.
    metrics       JSONB       NOT NULL DEFAULT '{}',
    outputs       JSONB       NOT NULL DEFAULT '[]',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The reviewer's lookup: the newest account of this exact source for this
-- script. The author's lookup, when trimming their own history, is the same
-- index's leading column plus a filter.
CREATE INDEX IF NOT EXISTS idx_script_dry_runs_source
    ON script_dry_runs (script_id, source_sha256, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_script_dry_runs_author
    ON script_dry_runs (script_id, requested_by, created_at DESC);

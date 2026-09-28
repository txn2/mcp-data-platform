-- 000166: recorded runs, tests on save, and agreed behavior changes (#1939, #1940, #1942).
--
-- script_recordings holds, for a run or a draft, every host call it made and
-- the answer it was given, gzipped (internal/platform/scriptrec). A script's
-- tests replay one, and a save replays the script's recent runs through the
-- new source. It holds upstream rows and responses, so it is read under the
-- rules run history is: the script's owner and administrators. It is swept
-- with the runs at scripts.run_retention_days, except a recording a test in
-- the script's latest version names (kept), which a test would otherwise lose.
--
-- run_id is the run's id, or the draft's; there is no foreign key because a
-- draft of a script not yet saved has no script and no run row, and a
-- recording a test keeps outlives the run it recorded. script_id is NULL for
-- such a draft until the script that names it is saved. data is NULL when
-- the recording outgrew its cap, and reason then says so.
CREATE TABLE IF NOT EXISTS script_recordings (
    run_id        TEXT        PRIMARY KEY,
    script_id     UUID        REFERENCES scripts(id) ON DELETE CASCADE,
    script_name   TEXT        NOT NULL DEFAULT '',
    kind          TEXT        NOT NULL CHECK (kind IN ('run', 'draft')),
    recorded_by   TEXT        NOT NULL DEFAULT '',
    version       INTEGER,
    source_sha256 TEXT        NOT NULL DEFAULT '',
    succeeded     BOOLEAN     NOT NULL DEFAULT FALSE,
    reason        TEXT        NOT NULL DEFAULT '',
    bytes         INTEGER     NOT NULL DEFAULT 0,
    data          BYTEA,
    kept          BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_script_recordings_script
    ON script_recordings (script_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_script_recordings_sweep
    ON script_recordings (created_at) WHERE NOT kept;

-- A version saved with a change its replay or its reach showed carries the
-- plain-language summary of what the automation now does differently, and who
-- confirmed the person it runs for agreed to it, and when (#1942).
ALTER TABLE script_versions ADD COLUMN IF NOT EXISTS change_summary   TEXT NOT NULL DEFAULT '';
ALTER TABLE script_versions ADD COLUMN IF NOT EXISTS change_agreed_by TEXT NOT NULL DEFAULT '';
ALTER TABLE script_versions ADD COLUMN IF NOT EXISTS change_agreed_at TIMESTAMPTZ;

-- Every script that exists now was saved before tests were required. It keeps
-- running and saving as it does; a new version of it that has tests must keep
-- them passing and may not lower their coverage. The column is added TRUE and
-- its default then becomes FALSE, as 000165 does for legacy.
ALTER TABLE scripts ADD COLUMN IF NOT EXISTS tests_optional BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE scripts ALTER COLUMN tests_optional SET DEFAULT FALSE;

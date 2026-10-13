-- 000195: a draft run longer than the inline wait (#2117).
--
-- An administrator can run_draft an unsaved script past 60 seconds, up to
-- scripts.draft.max_seconds. Such a draft runs in the background and answers
-- with its id at once; get_run reads it here on any replica: its log and
-- progress as the executing replica writes them, and the draft's whole result
-- (draft) once it ends. A finished draft is kept for seven days. reported_at
-- is when the executing replica last wrote it, so a draft whose replica
-- stopped reads as failed within a minute rather than at its deadline.
CREATE TABLE IF NOT EXISTS script_draft_runs (
    id          TEXT        PRIMARY KEY,
    owner_email TEXT        NOT NULL,
    script_name TEXT        NOT NULL,
    status      TEXT        NOT NULL CHECK (status IN ('running', 'succeeded', 'failed')),
    log         TEXT        NOT NULL DEFAULT '',
    progress    JSONB,
    error       TEXT        NOT NULL DEFAULT '',
    draft       JSONB,
    started_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    reported_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deadline    TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_script_draft_runs_finished
    ON script_draft_runs (finished_at) WHERE finished_at IS NOT NULL;

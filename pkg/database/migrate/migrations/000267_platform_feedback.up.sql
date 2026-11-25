-- Platform feedback (#2820): what agents report the platform could not do for
-- them, for whoever runs the platform. One row per distinct report; a repeat
-- close enough to an open one is counted on it and recorded as a report.
-- The text fields hold only what passed the content secret check, and calls
-- holds mcp:call: references, never data a tool returned.
CREATE TABLE IF NOT EXISTS platform_feedback (
    id                TEXT        PRIMARY KEY,
    trying_to         TEXT        NOT NULL,
    blocked_by        TEXT        NOT NULL,
    tool              TEXT        NOT NULL DEFAULT '',
    suggestion        TEXT        NOT NULL DEFAULT '',
    status            TEXT        NOT NULL DEFAULT 'open'
                      CHECK (status IN ('open', 'planned', 'done', 'declined')),
    report_count      INTEGER     NOT NULL DEFAULT 1,
    -- The embedding of the post's text, compared in the service against open
    -- posts to fold near-duplicates. REAL[] rather than vector(n) so a
    -- deployment's model dimension is not fixed in the schema.
    embedding         REAL[],
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_reported_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    status_changed_by TEXT        NOT NULL DEFAULT '',
    status_changed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_platform_feedback_status
    ON platform_feedback (status, last_reported_at DESC);

-- Each report of a post: who reported it, in which session, and the calls it
-- names. The per-session and per-persona limits count these rows.
CREATE TABLE IF NOT EXISTS platform_feedback_reports (
    id          BIGSERIAL   PRIMARY KEY,
    feedback_id TEXT        NOT NULL REFERENCES platform_feedback (id) ON DELETE CASCADE,
    persona     TEXT        NOT NULL DEFAULT '',
    user_id     TEXT        NOT NULL DEFAULT '',
    session_id  TEXT        NOT NULL DEFAULT '',
    calls       TEXT[]      NOT NULL DEFAULT '{}',
    reported_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_platform_feedback_reports_post
    ON platform_feedback_reports (feedback_id, reported_at DESC);
CREATE INDEX IF NOT EXISTS idx_platform_feedback_reports_session
    ON platform_feedback_reports (session_id, reported_at);
CREATE INDEX IF NOT EXISTS idx_platform_feedback_reports_persona
    ON platform_feedback_reports (persona, reported_at);

-- A post refused before it was stored: why, never what it said.
CREATE TABLE IF NOT EXISTS platform_feedback_refusals (
    id         BIGSERIAL   PRIMARY KEY,
    reason     TEXT        NOT NULL,
    persona    TEXT        NOT NULL DEFAULT '',
    refused_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_platform_feedback_refusals_at
    ON platform_feedback_refusals (refused_at DESC);

-- Type checks of site releases (#2793): one row per check of one release,
-- queued at publish for a site whose _build.json asks for one, or on request.
-- result holds the finished check: its status, each diagnostic (file, line,
-- column, code, message), the packages it could not read, and its counts.
CREATE TABLE IF NOT EXISTS site_typechecks (
    id             BIGSERIAL   PRIMARY KEY,
    site_id        TEXT        NOT NULL,
    release_number INTEGER     NOT NULL,
    environment    TEXT        NOT NULL,
    tsconfig       TEXT        NOT NULL,
    status         TEXT        NOT NULL DEFAULT 'queued'
                   CHECK (status IN ('queued', 'running', 'clean', 'errors', 'over_limit', 'unsupported', 'failed')),
    result         JSONB,
    requested_by   TEXT        NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at     TIMESTAMPTZ,
    finished_at    TIMESTAMPTZ,
    -- A running check whose claim lapsed (its replica stopped) is claimed again.
    claimed_until  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_site_typechecks_queue
    ON site_typechecks (status, created_at);
CREATE INDEX IF NOT EXISTS idx_site_typechecks_release
    ON site_typechecks (site_id, release_number, created_at DESC);

-- Each environment's incremental state: the compiler's .tsbuildinfo after its
-- last check, so the next check of a release built on it takes seconds.
CREATE TABLE IF NOT EXISTS site_typecheck_state (
    site_id     TEXT        NOT NULL,
    environment TEXT        NOT NULL,
    tsbuildinfo BYTEA       NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (site_id, environment)
);

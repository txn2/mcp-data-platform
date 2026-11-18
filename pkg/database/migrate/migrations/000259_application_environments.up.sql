-- 000259: application environments and variables (#2696).
--
-- An application runs in an ordered list of environments, the first (dev by
-- default) receiving each new release and the last (prod) being what
-- /endpoints/<name> and every handler with no environment run. Each
-- environment records the release it runs; a promote copies that number one
-- step on. scripts.released_version and portal_assets.released_version keep
-- holding the LAST environment's pins, so everything that names no
-- environment behaves as before. Every environment uses the application's
-- schema.
CREATE TABLE IF NOT EXISTS application_environments (
    application_id    TEXT        NOT NULL REFERENCES applications (id) ON DELETE CASCADE,
    name              TEXT        NOT NULL CHECK (name ~ '^[a-z][a-z0-9-]{0,31}$'),
    position          INT         NOT NULL,
    release           INT         NOT NULL DEFAULT 0,
    requires_approval BOOLEAN     NOT NULL DEFAULT FALSE,
    updated_by        TEXT        NOT NULL DEFAULT '',
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (application_id, name),
    UNIQUE (application_id, position) DEFERRABLE INITIALLY DEFERRED
);

-- An existing application keeps running exactly as it does: one environment,
-- prod, running its current release.
INSERT INTO application_environments (application_id, name, position, release, updated_by, updated_at)
SELECT id, 'prod', 0, current_release, created_by, updated_at FROM applications
ON CONFLICT (application_id, name) DO NOTHING;

-- A variable is NAME=value for one environment, or the application-wide
-- default when environment is ''. A value is plain text; a stored secret is
-- referenced as {{secret:<name>}} and filled when a request is sent.
CREATE TABLE IF NOT EXISTS application_variables (
    application_id TEXT        NOT NULL REFERENCES applications (id) ON DELETE CASCADE,
    environment    TEXT        NOT NULL DEFAULT '',
    name           TEXT        NOT NULL CHECK (name ~ '^[A-Z_][A-Z0-9_]{0,63}$'),
    value          TEXT        NOT NULL,
    updated_by     TEXT        NOT NULL DEFAULT '',
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (application_id, environment, name)
);

-- The environment a run executes in, '' for the last; a schedule or webhook
-- handler names the environment its runs are made in.
ALTER TABLE script_runs ADD COLUMN IF NOT EXISTS environment TEXT NOT NULL DEFAULT '';
ALTER TABLE script_schedules ADD COLUMN IF NOT EXISTS environment TEXT NOT NULL DEFAULT '';
ALTER TABLE script_webhook_handlers ADD COLUMN IF NOT EXISTS environment TEXT NOT NULL DEFAULT '';

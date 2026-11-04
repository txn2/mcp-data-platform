-- 000228: applications released as a pinned set (#2411).
--
-- An application is one record naming what a system built on the platform is
-- made of: an application database schema with its migrations, managed
-- scripts (an endpoint or a form is its script's binding), webhook sources
-- and the portal assets that are its user interface. A part belongs to at
-- most one application. A release pins the migration version and each
-- script's and asset's version; scripts.released_version and
-- portal_assets.released_version are what a run executes and what the portal
-- serves while the part belongs to a released application, so saving a
-- script or an asset changes nothing live until the next release. A rollback
-- is a release of an earlier release's pins.
CREATE TABLE IF NOT EXISTS applications (
    id              TEXT        PRIMARY KEY,
    name            TEXT        NOT NULL UNIQUE,
    description     TEXT        NOT NULL DEFAULT '',
    owner_email     TEXT        NOT NULL,
    connection      TEXT        NOT NULL DEFAULT '',
    schema_name     TEXT        NOT NULL DEFAULT '',
    current_release INT         NOT NULL DEFAULT 0,
    created_by      TEXT        NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- One application per schema.
CREATE UNIQUE INDEX IF NOT EXISTS applications_schema
    ON applications (connection, schema_name) WHERE connection <> '';

CREATE TABLE IF NOT EXISTS application_parts (
    application_id TEXT        NOT NULL REFERENCES applications (id) ON DELETE CASCADE,
    kind           TEXT        NOT NULL CHECK (kind IN ('script', 'asset', 'source')),
    part_id        TEXT        NOT NULL,
    added_by       TEXT        NOT NULL,
    added_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- A part belongs to at most one application.
    PRIMARY KEY (kind, part_id)
);

CREATE INDEX IF NOT EXISTS application_parts_application ON application_parts (application_id);

CREATE TABLE IF NOT EXISTS application_releases (
    application_id    TEXT        NOT NULL REFERENCES applications (id) ON DELETE CASCADE,
    number            INT         NOT NULL CHECK (number > 0),
    action            TEXT        NOT NULL CHECK (action IN ('release', 'rollback', 'import')),
    migration_version INT         NOT NULL DEFAULT 0,
    -- The release a rollback restored the pins of; 0 for any other action.
    restored          INT         NOT NULL DEFAULT 0,
    -- What the release changed: the parts whose pinned version moved, the
    -- migrations it applied or reverted.
    changes           JSONB       NOT NULL DEFAULT '{}'::jsonb,
    note              TEXT        NOT NULL DEFAULT '',
    created_by        TEXT        NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (application_id, number)
);

CREATE TABLE IF NOT EXISTS application_release_pins (
    application_id TEXT NOT NULL,
    number         INT  NOT NULL,
    kind           TEXT NOT NULL CHECK (kind IN ('script', 'asset')),
    part_id        TEXT NOT NULL,
    version        INT  NOT NULL CHECK (version > 0),
    PRIMARY KEY (application_id, number, kind, part_id),
    FOREIGN KEY (application_id, number) REFERENCES application_releases (application_id, number) ON DELETE CASCADE
);

ALTER TABLE scripts ADD COLUMN IF NOT EXISTS released_version INT;
ALTER TABLE portal_assets ADD COLUMN IF NOT EXISTS released_version INT;

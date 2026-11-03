-- Schema migrations for application databases (#2404). The platform keeps
-- each migration an application database is built from: the SQL that moves
-- its schema up and back down, who wrote it and the checksum an apply checks
-- the target's ledger against. The target database holds its own ledger of
-- what was applied (mcp_schema.migrations); these rows are the source of truth
-- that ledger is compared with.
CREATE TABLE IF NOT EXISTS appdb_migrations (
    connection    TEXT        NOT NULL,
    schema_name   TEXT        NOT NULL,
    version       INTEGER     NOT NULL CHECK (version > 0),
    name          TEXT        NOT NULL,
    up_sql        TEXT        NOT NULL,
    down_sql      TEXT        NOT NULL DEFAULT '',
    transactional BOOLEAN     NOT NULL DEFAULT TRUE,
    checksum      TEXT        NOT NULL,
    author        TEXT        NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (connection, schema_name, version)
);

-- What happened to each migration: added, edited while pending, applied,
-- reverted, and each apply or revert that failed, with who did it, when and
-- how long it took.
CREATE TABLE IF NOT EXISTS appdb_migration_events (
    id          BIGSERIAL   PRIMARY KEY,
    connection  TEXT        NOT NULL,
    schema_name TEXT        NOT NULL,
    version     INTEGER     NOT NULL,
    event       TEXT        NOT NULL CHECK (event IN ('added', 'edited', 'applied', 'reverted', 'failed')),
    actor       TEXT        NOT NULL DEFAULT '',
    checksum    TEXT        NOT NULL DEFAULT '',
    duration_ms BIGINT      NOT NULL DEFAULT 0,
    detail      TEXT        NOT NULL DEFAULT '',
    at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_appdb_migration_events_target
    ON appdb_migration_events (connection, schema_name, at DESC);

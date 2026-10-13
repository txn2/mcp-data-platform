-- The platform's own catalog (#1917): datasets, their columns, and the
-- glossary terms, glossary nodes, tags, domains and data products curation
-- refers to. The existing catalog_datasets table is the search index built from
-- DataHub and is unrelated.
CREATE TABLE IF NOT EXISTS catalog_entities (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    kind                TEXT        NOT NULL CHECK (kind IN (
                            'dataset', 'column', 'glossary_term', 'glossary_node',
                            'tag', 'domain', 'data_product')),
    -- The canonical mcp: reference. Derived from connection and path for a
    -- dataset or column, from the id for a definition.
    reference           TEXT        NOT NULL UNIQUE,
    connection_kind     TEXT        NOT NULL DEFAULT '',
    connection_name     TEXT        NOT NULL DEFAULT '',
    -- A dataset's path in reference form; a column carries its dataset's.
    path                TEXT        NOT NULL DEFAULT '',
    column_name         TEXT        NOT NULL DEFAULT '',
    -- A column's dataset, or the glossary node a term or node sits under.
    parent_id           UUID        REFERENCES catalog_entities (id) ON DELETE CASCADE,
    name                TEXT        NOT NULL,
    -- The readable key a definition's reference may use in place of its id;
    -- a tag's name.
    slug                TEXT        NOT NULL DEFAULT '',
    dataset_type        TEXT        NOT NULL DEFAULT '',
    schema_resource_id  TEXT        NOT NULL DEFAULT '',
    data_type           TEXT        NOT NULL DEFAULT '',
    ordinal             INTEGER     NOT NULL DEFAULT 0,
    nullable            BOOLEAN     NOT NULL DEFAULT TRUE,
    -- Set when a scan no longer sees the dataset. Its curation stays readable;
    -- a later scan that sees it again clears this.
    removed_at          TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_catalog_entities_parent ON catalog_entities (parent_id);
CREATE INDEX IF NOT EXISTS idx_catalog_entities_connection
    ON catalog_entities (connection_kind, connection_name) WHERE kind = 'dataset';
CREATE UNIQUE INDEX IF NOT EXISTS idx_catalog_entities_slug
    ON catalog_entities (kind, slug) WHERE slug <> '';

-- The current value of each aspect written on an entity. An aspect never
-- written, or cleared, has no row.
CREATE TABLE IF NOT EXISTS catalog_aspects (
    entity_id   UUID        NOT NULL REFERENCES catalog_entities (id) ON DELETE CASCADE,
    aspect      TEXT        NOT NULL,
    value       JSONB       NOT NULL,
    version     INTEGER     NOT NULL,
    written_by  TEXT        NOT NULL DEFAULT '',
    written_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (entity_id, aspect)
);

-- Every write of every aspect, with the value it replaced, so a change can be
-- rolled back. A NULL value means the aspect was absent (before) or cleared
-- (after). A rollback is a change of its own that names the one it undid.
CREATE TABLE IF NOT EXISTS catalog_aspect_history (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_id   UUID        NOT NULL REFERENCES catalog_entities (id) ON DELETE CASCADE,
    aspect      TEXT        NOT NULL,
    version     INTEGER     NOT NULL,
    previous    JSONB,
    value       JSONB,
    written_by  TEXT        NOT NULL DEFAULT '',
    written_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    reverts     UUID        REFERENCES catalog_aspect_history (id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_catalog_aspect_history_entity
    ON catalog_aspect_history (entity_id, aspect, version DESC);

-- Lineage edges, from a source dataset to a target dataset (#1922). Both ends
-- are references rather than entity ids: a statement can create a table the
-- next scan has not cataloged yet, and the edge is recorded when the data
-- moved. writer_kind says what recorded the edge and writer_ref which one (a
-- script, an audited call, a registration).
CREATE TABLE IF NOT EXISTS catalog_lineage (
    source_ref  TEXT        NOT NULL,
    target_ref  TEXT        NOT NULL,
    writer_kind TEXT        NOT NULL CHECK (writer_kind IN ('script', 'sql', 'registration', 'webhook', 'manual')),
    writer_ref  TEXT        NOT NULL DEFAULT '',
    written_by  TEXT        NOT NULL DEFAULT '',
    written_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (source_ref, target_ref, writer_kind, writer_ref)
);

CREATE INDEX IF NOT EXISTS idx_catalog_lineage_target ON catalog_lineage (target_ref);

-- The last scan of each connection into the catalog (#1918): when it ran,
-- what it wrote, what it marked removed, and why it stopped if it failed.
CREATE TABLE IF NOT EXISTS catalog_scans (
    connection_kind TEXT        NOT NULL,
    connection_name TEXT        NOT NULL,
    started_at      TIMESTAMPTZ NOT NULL,
    finished_at     TIMESTAMPTZ NOT NULL,
    datasets        INTEGER     NOT NULL DEFAULT 0,
    removed         INTEGER     NOT NULL DEFAULT 0,
    error           TEXT        NOT NULL DEFAULT '',
    -- The parts of the connection the scan could not read, each a dataset
    -- path prefix and the reason: [{"path": "...", "error": "..."}].
    unread          JSONB       NOT NULL DEFAULT '[]'::jsonb,
    PRIMARY KEY (connection_kind, connection_name)
);

-- The scan's own state (#1918), one row: when the last whole scan started and
-- when a scan was last requested. The replica that takes the scan lock records
-- the start before it reads anything, so a request made during a scan, on any
-- replica, is newer than that start and owes another scan.
CREATE TABLE IF NOT EXISTS catalog_scan_state (
    id           BOOLEAN     PRIMARY KEY DEFAULT TRUE CHECK (id),
    started_at   TIMESTAMPTZ,
    requested_at TIMESTAMPTZ,
    requested_by TEXT        NOT NULL DEFAULT ''
);

-- 000193: how a resource is indexed is decided per resource (#2111).
--
-- index_policy is full (metadata and a content prefix are embedded, the prefix
-- kept for the lexical arm), metadata (the name, description, folder, filename
-- and tags are embedded, no content is read), or none (no index job, no blob
-- read, no stored text, no vector). index_policy_explicit records that the
-- writer chose it; a policy the operator's rules gave is the one a
-- reclassification may change.
ALTER TABLE resources
    ADD COLUMN IF NOT EXISTS index_policy TEXT NOT NULL DEFAULT 'full'
        CHECK (index_policy IN ('full', 'metadata', 'none')),
    ADD COLUMN IF NOT EXISTS index_policy_explicit BOOLEAN NOT NULL DEFAULT FALSE;

-- A table of values was embedded on its metadata alone before this column
-- existed (#1988); its rows say so now, so a reindex keeps doing what it did.
-- The content those rows still carry is cleared by a reclassification, not
-- here, so the migration does not rewrite every stored prefix.
UPDATE resources SET index_policy = 'metadata'
WHERE lower(split_part(mime_type, ';', 1)) IN ('text/csv', 'text/tab-separated-values', 'application/x-ndjson');

CREATE INDEX IF NOT EXISTS idx_resources_unindexed_folder
    ON resources (scope, (COALESCE(scope_id, '')), path)
    WHERE index_policy = 'none';

-- A folder whose files are not indexed is searchable as one thing: its path,
-- the description its writer or an editor gave it, and a summary of what it
-- holds, embedded once rather than once per file.
ALTER TABLE resource_folders
    ADD COLUMN IF NOT EXISTS description         TEXT        NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS summary_text        TEXT        NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS embedding           vector(768),
    ADD COLUMN IF NOT EXISTS embedding_model     TEXT        NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS embedding_text_hash BYTEA;

-- The last reclassification by the rules, one row the replicas share: a run
-- started on one replica is reported by every replica, and a second start is
-- refused while it runs. A run not finished within an hour is taken to have
-- stopped with its replica.
CREATE TABLE IF NOT EXISTS resource_reclassify_runs (
    id          SMALLINT    PRIMARY KEY CHECK (id = 1),
    started_at  TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ,
    examined    INTEGER     NOT NULL DEFAULT 0,
    changed     INTEGER     NOT NULL DEFAULT 0,
    cleared     INTEGER     NOT NULL DEFAULT 0,
    error       TEXT        NOT NULL DEFAULT ''
);

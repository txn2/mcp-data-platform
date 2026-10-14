-- Each connection's schema document as the catalog scan last read it: an API
-- connection's OpenAPI spec, a GraphQL connection's SDL (#1918). A document
-- belongs to its connection and is read under that connection's boundary, the
-- one its datasets are read under, so it is kept here rather than in a
-- resource library every signed-in user can open.
CREATE TABLE IF NOT EXISTS catalog_documents (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_kind TEXT        NOT NULL,
    connection_name TEXT        NOT NULL,
    filename        TEXT        NOT NULL,
    mime_type       TEXT        NOT NULL,
    content         BYTEA       NOT NULL,
    content_sha256  TEXT        NOT NULL,
    version         INTEGER     NOT NULL DEFAULT 1,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (connection_kind, connection_name, filename)
);

-- The document a dataset was read from, by filename under the dataset's own
-- connection. Empty for a dataset read from none.
ALTER TABLE catalog_entities ADD COLUMN IF NOT EXISTS schema_document TEXT NOT NULL DEFAULT '';

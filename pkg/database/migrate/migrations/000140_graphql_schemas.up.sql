-- 000140: graphql_connection_schemas + graphql_operation_embeddings
--
-- The graphql connection kind (#1277) keeps each connection's schema
-- so a restart does not re-read every endpoint, and so the operation
-- index and its embeddings have something stable to be keyed on.
--
-- graphql_connection_schemas holds one row per connection. The schema
-- is stored as SDL, gzip-compressed: SDL is what the parser loads, what
-- an operator can read when diagnosing a refused document, and the one
-- form both the introspection path and the admin upload path normalize
-- to. schema_hash is the SHA-256 of the uncompressed SDL and is what
-- identifies the version an index belongs to. source records whether
-- the platform read the schema itself or an operator supplied it, which
-- is the difference between a connection that can refresh and one whose
-- endpoint disables introspection.
--
-- graphql_operation_embeddings holds the per-operation vectors that
-- semantic and hybrid ranking in graphql_discover rank against, written
-- by the index-jobs consumer registered under source_kind
-- "graphql_operations". Keyed on (connection, schema_hash,
-- operation_id): the schema hash is in the key so a schema change
-- leaves the old vectors addressable until the new pass replaces them,
-- and a connection reverted to a previous schema finds its index still
-- there. text_hash is the SHA-256 of the text fed to the provider, so a
-- refresh skips the provider call for an operation whose text did not
-- change. model and dim record the provider identity and dimensionality
-- at write time.
--
-- pgvector is already enabled by migration 000031_memory_records.
-- Re-enabled defensively so this migration is self-contained.

CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS graphql_connection_schemas (
    connection   TEXT        NOT NULL PRIMARY KEY,
    schema_hash  TEXT        NOT NULL,
    sdl_gzip     BYTEA       NOT NULL,
    source       TEXT        NOT NULL DEFAULT 'introspection',
    fetched_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS graphql_operation_embeddings (
    connection   TEXT        NOT NULL,
    schema_hash  TEXT        NOT NULL,
    operation_id TEXT        NOT NULL,
    text_hash    BYTEA       NOT NULL,
    embedding    vector(768) NOT NULL,
    model        TEXT        NOT NULL DEFAULT '',
    dim          INTEGER     NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (connection, schema_hash, operation_id)
);

CREATE INDEX IF NOT EXISTS idx_graphql_operation_embeddings_connection
    ON graphql_operation_embeddings (connection, schema_hash);

-- 000121: an uploaded file registered as an external table on a Trino
-- connection (#1327).
--
-- One table serves both kinds. A managed resource and a portal asset are
-- different records with different owners and different surfaces, but a
-- registration says the same thing about either: this object's directory is
-- readable as this table, on this connection, with these columns. Two tables
-- would mean two stores, two sweeps, and two joins wherever a search hit needs
-- to say "you can query this".
--
-- source_kind and source_id are deliberately not a foreign key. The rows they
-- name live in portal_assets and managed_resources, and a single FK column
-- cannot point at both; the registrar deletes the registration when its source
-- goes, and the unique index below is what keeps the table itself consistent.
--
-- location is the directory external_location was pointed at, kept as written
-- rather than recomputed. It is what makes staleness answerable: a resource
-- revision or an asset version moves the head key to a new directory, and a
-- registration whose location no longer matches the head key's directory
-- serves the revision it was registered against, not the current one.
CREATE TABLE IF NOT EXISTS table_registrations (
    id              TEXT PRIMARY KEY,
    source_kind     TEXT NOT NULL CHECK (source_kind IN ('resource', 'asset')),
    source_id       TEXT NOT NULL,
    connection_name TEXT NOT NULL,
    catalog_name    TEXT NOT NULL,
    schema_name     TEXT NOT NULL,
    table_name      TEXT NOT NULL,
    location        TEXT NOT NULL,
    columns         JSONB NOT NULL DEFAULT '[]'::jsonb,
    registered_by   TEXT NOT NULL,
    registered_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The scratch schema is a shared workspace: everyone granted the connection
-- sees every table in it. Two registrations claiming one name would leave the
-- DDL as the only arbiter of which object the name resolves to, decided by
-- whoever registered last. The name is claimed here instead, and the registrar
-- turns a collision into a refusal that names the current holder.
CREATE UNIQUE INDEX IF NOT EXISTS table_registrations_name_key
    ON table_registrations (connection_name, catalog_name, schema_name, table_name);

-- The read path a source's detail view and its delete take: every registration
-- of one resource or asset.
CREATE INDEX IF NOT EXISTS table_registrations_source_idx
    ON table_registrations (source_kind, source_id);

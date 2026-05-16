-- 000042: api_catalogs + api_catalog_specs
--
-- OpenAPI documentation is a property of the API itself (Salesforce
-- REST, Stripe, GitHub, ...), not of any individual connection that
-- authenticates against it. An organization with N tenants of the
-- same vendor has N connections sharing one set of specs; per-
-- connection inline specs duplicated the same content N times and
-- drifted independently.
--
-- An API catalog is a versioned, named bundle of component OpenAPI
-- specs. Each (name, version) pair is its own row, so cloning to a
-- new version creates a new row, leaving existing connections on
-- the old one until the operator migrates them deliberately.
--
-- Specs are public documentation; content is plain TEXT (no
-- field-level encryption). Credentials remain in
-- connection_instances.config and the existing oauth/token tables.

CREATE TABLE api_catalogs (
    id           TEXT        NOT NULL PRIMARY KEY,
    name         TEXT        NOT NULL,
    version      TEXT        NOT NULL DEFAULT '',
    display_name TEXT        NOT NULL,
    description  TEXT        NOT NULL DEFAULT '',
    created_by   TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (name, version)
);

CREATE INDEX api_catalogs_name_idx ON api_catalogs (name);

CREATE TABLE api_catalog_specs (
    catalog_id      TEXT        NOT NULL
        REFERENCES api_catalogs(id) ON DELETE CASCADE,
    spec_name       TEXT        NOT NULL,
    content         TEXT        NOT NULL,
    source_kind     TEXT        NOT NULL
        CHECK (source_kind IN ('inline', 'upload', 'url')),
    source_url      TEXT        NOT NULL DEFAULT '',
    etag            TEXT        NOT NULL DEFAULT '',
    last_fetched_at TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (catalog_id, spec_name)
);

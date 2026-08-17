-- Call records: every data-access call as a reusable, findable record with a
-- purpose and a fate (issue #1321).
--
-- These rows are not audit rows. Audit retention is the deployment's stated
-- history window and audit parameter policy may drop or redact a statement; a
-- record is the query itself, kept for as long as it is worth reusing. The two
-- are joined by event_id, which is the id the call already handed back to its
-- caller as mcp:call:<event_id>.
--
-- What a record does NOT store is its outcome. failed / satisfied / superseded
-- / ran are derived on read from the record's own success flag and from what
-- later cited it, so an outcome can never be stale with respect to the asset or
-- the insight that gives it meaning. The two indexes at the bottom are what
-- make that derivation an index lookup rather than a scan.

CREATE TABLE IF NOT EXISTS call_records (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- event_id is the audit event this call was recorded under, and the key of
    -- its mcp:call:<event_id> reference. Unique: one call, one record.
    event_id        TEXT NOT NULL UNIQUE,

    -- kind is 'sql' or 'api'. One record shape serves both; the columns each
    -- kind fills differ and the other kind leaves empty.
    kind            TEXT NOT NULL,
    tool_name       TEXT NOT NULL,
    connection      TEXT NOT NULL DEFAULT '',

    -- SQL calls: the statement, and the whitespace-normalized form reuse
    -- matching compares (a re-run with different indentation is the same query).
    statement       TEXT NOT NULL DEFAULT '',
    statement_norm  TEXT NOT NULL DEFAULT '',

    -- API calls: the request line the catalog names.
    method          TEXT NOT NULL DEFAULT '',
    path            TEXT NOT NULL DEFAULT '',
    operation_id    TEXT NOT NULL DEFAULT '',

    -- targets are what the call addressed: dataset URNs parsed out of the SQL,
    -- or the endpoint identity for an API call.
    targets         JSONB NOT NULL DEFAULT '[]',

    -- purpose is the reason the caller stated for the call (#1317).
    purpose         TEXT NOT NULL DEFAULT '',

    user_id         TEXT NOT NULL DEFAULT '',
    user_email      TEXT NOT NULL DEFAULT '',
    session_id      TEXT NOT NULL DEFAULT '',
    persona         TEXT NOT NULL DEFAULT '',

    success         BOOLEAN NOT NULL DEFAULT TRUE,
    error_message   TEXT NOT NULL DEFAULT '',
    duration_ms     BIGINT NOT NULL DEFAULT 0,
    -- response_chars is the size of what came back. It is not a row count:
    -- the audit log records no row count, and a record must not invent one.
    response_chars  INTEGER NOT NULL DEFAULT 0,

    -- Promotion state. A promoted record carries the URN of the DataHub Query
    -- entity (sql) or the saved endpoint example (api) it became; a rejected
    -- one carries who rejected it and why, so it is not suggested again.
    promoted_urn    TEXT NOT NULL DEFAULT '',
    promoted_at     TIMESTAMPTZ,
    promoted_by     TEXT NOT NULL DEFAULT '',
    rejected_at     TIMESTAMPTZ,
    rejected_by     TEXT NOT NULL DEFAULT '',
    rejection_note  TEXT NOT NULL DEFAULT '',

    -- Search index breadcrumbs, mirroring the memory records' shape: the
    -- embedding of the record's own text, and what it was computed from.
    embedding            vector(768),
    embedding_model      TEXT NOT NULL DEFAULT '',
    embedding_text_hash  TEXT NOT NULL DEFAULT '',

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The user's own catalog, newest first.
CREATE INDEX IF NOT EXISTS idx_call_records_user_created
    ON call_records(user_id, created_at DESC);

-- The session's calls, which is how supersession is evaluated: a later
-- successful call in the same session over the same targets.
CREATE INDEX IF NOT EXISTS idx_call_records_session
    ON call_records(session_id, created_at);

-- Reuse matching and the supersession predicate both key on this triple.
CREATE INDEX IF NOT EXISTS idx_call_records_match
    ON call_records(kind, connection, statement_norm);

CREATE INDEX IF NOT EXISTS idx_call_records_operation
    ON call_records(kind, connection, operation_id);

CREATE INDEX IF NOT EXISTS idx_call_records_created_at
    ON call_records(created_at DESC);

-- Records addressing one dataset: the enrichment path lists a table's
-- satisfied queries.
CREATE INDEX IF NOT EXISTS idx_call_records_targets
    ON call_records USING gin (targets jsonb_path_ops);

-- Discovery. A record is findable two ways, and the search path runs one arm
-- against each index rather than one blended ORDER BY that would forfeit both:
-- the words of its purpose and its statement, and the meaning of them.
CREATE INDEX IF NOT EXISTS idx_call_records_fts
    ON call_records USING gin (
        to_tsvector('english',
            purpose || ' ' || statement || ' ' || method || ' ' || path || ' ' || operation_id));

CREATE INDEX IF NOT EXISTS idx_call_records_embedding
    ON call_records USING hnsw (embedding vector_cosine_ops);

-- call_record_fetches records that a session dereferenced a record. It is the
-- first half of reuse: reuse is not "someone ran the same query", it is
-- "someone found this record and then ran what it holds".
CREATE TABLE IF NOT EXISTS call_record_fetches (
    call_record_id  UUID NOT NULL REFERENCES call_records(id) ON DELETE CASCADE,
    session_id      TEXT NOT NULL,
    user_id         TEXT NOT NULL DEFAULT '',
    -- fetched_at is stated by the application, not defaulted to NOW(): reuse
    -- compares it against a record's created_at, which is the audit event's
    -- timestamp and so the application's clock. Mixing the database's clock
    -- into that comparison puts a sighting after the call it preceded whenever
    -- the two drift, and silently drops the credit.
    fetched_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (call_record_id, session_id)
);

CREATE INDEX IF NOT EXISTS idx_call_record_fetches_session
    ON call_record_fetches(session_id);

-- call_record_reuse is the credited half: one row per session that fetched the
-- record and then ran it. The primary key is what keeps a session from
-- crediting the same record twice, so reuse_count is a COUNT over this table
-- rather than a counter that can drift.
CREATE TABLE IF NOT EXISTS call_record_reuse (
    call_record_id  UUID NOT NULL REFERENCES call_records(id) ON DELETE CASCADE,
    session_id      TEXT NOT NULL,
    user_id         TEXT NOT NULL DEFAULT '',
    reused_event_id TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (call_record_id, session_id)
);

-- api_endpoint_examples is where a promoted API call lands. An endpoint has no
-- catalog entity of its own to hang a query on, so what a promoted API record
-- becomes is an example on the endpoint: a request that is known to have
-- worked, shown to the next agent that reads the endpoint's schema.
--
-- It is keyed by connection rather than by catalog, because an example is
-- evidence from one upstream. Two connections can share a spec and disagree
-- about what a working request looks like, and an example promoted against one
-- of them says nothing about the other.
CREATE TABLE IF NOT EXISTS api_endpoint_examples (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    connection     TEXT NOT NULL,
    operation_id   TEXT NOT NULL DEFAULT '',
    method         TEXT NOT NULL DEFAULT '',
    path           TEXT NOT NULL DEFAULT '',
    name           TEXT NOT NULL,
    description    TEXT NOT NULL DEFAULT '',
    -- call_record_id leads back to the call the example was promoted from. It
    -- survives that record's deletion as NULL: the example is still a working
    -- request even once the call it came from has aged out.
    call_record_id UUID REFERENCES call_records(id) ON DELETE SET NULL,
    created_by     TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (connection, operation_id, name)
);

CREATE INDEX IF NOT EXISTS idx_api_endpoint_examples_lookup
    ON api_endpoint_examples(connection, operation_id);

-- Outcome derivation reads two tables that already hold the answer. Both
-- lookups are jsonb containment, so both get a GIN index rather than the scan
-- they would otherwise be.

-- An asset's provenance names the calls it was built from, by event id, inside
-- each capture it recorded (#1320).
CREATE INDEX IF NOT EXISTS idx_portal_assets_provenance
    ON portal_assets USING gin (provenance jsonb_path_ops);

-- A captured insight names the calls it confirms in metadata.sources, in
-- mcp:call:<event_id> form.
CREATE INDEX IF NOT EXISTS idx_memory_records_metadata
    ON memory_records USING gin (metadata jsonb_path_ops);

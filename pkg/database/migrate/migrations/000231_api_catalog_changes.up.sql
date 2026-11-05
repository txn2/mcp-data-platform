-- 000231: API catalog change reports, upstream warnings and callers (#2469).
--
-- api_catalog_spec_watch is how a spec is kept current (#2483, #2484): how
-- often its upstream is checked (0 is never), whether a changed spec waits
-- for an administrator, and for an uploaded or pasted spec the upstream URL
-- it is compared with but never replaced by, with the last copy read from it.
-- It is a row of its own, not columns on api_catalog_specs, because a spec's
-- write path replaces that row whole on every save.
CREATE TABLE IF NOT EXISTS api_catalog_spec_watch (
    catalog_id               TEXT        NOT NULL,
    spec_name                TEXT        NOT NULL,
    refresh_interval_seconds INTEGER     NOT NULL DEFAULT 0 CHECK (refresh_interval_seconds >= 0),
    hold_changes             BOOLEAN     NOT NULL DEFAULT FALSE,
    upstream_url             TEXT        NOT NULL DEFAULT '',
    upstream_etag            TEXT        NOT NULL DEFAULT '',
    upstream_content         TEXT        NOT NULL DEFAULT '',
    next_check_at            TIMESTAMPTZ,
    last_checked_at          TIMESTAMPTZ,
    last_error               TEXT        NOT NULL DEFAULT '',
    updated_by               TEXT        NOT NULL DEFAULT '',
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (catalog_id, spec_name),
    FOREIGN KEY (catalog_id, spec_name)
        REFERENCES api_catalog_specs (catalog_id, spec_name) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_api_catalog_spec_watch_due
    ON api_catalog_spec_watch (next_check_at)
    WHERE refresh_interval_seconds > 0;

-- api_catalog_spec_changes is one check that found the upstream different
-- (#2483): the report, and what became of it. A held change keeps the
-- fetched document in pending_content until it is applied or discarded; a
-- spec has at most one change waiting.
CREATE TABLE IF NOT EXISTS api_catalog_spec_changes (
    id              TEXT        NOT NULL PRIMARY KEY,
    catalog_id      TEXT        NOT NULL,
    spec_name       TEXT        NOT NULL,
    status          TEXT        NOT NULL
        CHECK (status IN ('applied', 'pending', 'discarded', 'reported', 'superseded')),
    trigger_kind    TEXT        NOT NULL CHECK (trigger_kind IN ('schedule', 'manual')),
    report          JSONB       NOT NULL,
    pending_content TEXT        NOT NULL DEFAULT '',
    pending_etag    TEXT        NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    decided_by      TEXT        NOT NULL DEFAULT '',
    decided_at      TIMESTAMPTZ,
    FOREIGN KEY (catalog_id, spec_name)
        REFERENCES api_catalog_specs (catalog_id, spec_name) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_api_catalog_spec_changes_spec
    ON api_catalog_spec_changes (catalog_id, spec_name, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_api_catalog_spec_changes_one_pending
    ON api_catalog_spec_changes (catalog_id, spec_name) WHERE status = 'pending';

-- api_operation_warnings is what an upstream said about an operation in its
-- answers (#2486): a Deprecation or Sunset header, or a 404/410 from an
-- operation that answered 2xx before. One row per connection, operation and
-- kind; notified_at is set by the one write that created it, so a warning is
-- sent once. operation is the catalog's operation id or "METHOD template",
-- never a concrete path, which can carry a filled secret.
CREATE TABLE IF NOT EXISTS api_operation_warnings (
    connection    TEXT        NOT NULL,
    operation     TEXT        NOT NULL,
    kind          TEXT        NOT NULL CHECK (kind IN ('deprecation', 'sunset', 'removed')),
    detail        TEXT        NOT NULL DEFAULT '',
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    seen_count    BIGINT      NOT NULL DEFAULT 1,
    notified_at   TIMESTAMPTZ,
    PRIMARY KEY (connection, operation, kind)
);

-- api_operation_successes is when an operation last answered 2xx, which is
-- what makes a later 404 or 410 a removal rather than a bad request.
CREATE TABLE IF NOT EXISTS api_operation_successes (
    connection      TEXT        NOT NULL,
    operation       TEXT        NOT NULL,
    last_success_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (connection, operation)
);

-- api_operation_callers is which managed scripts called an operation in a
-- run (#2485), recorded from the audit pipeline: a call the source computes
-- is only known once it is made. Only the script, connection and operation
-- are kept, never the call's arguments.
CREATE TABLE IF NOT EXISTS api_operation_callers (
    connection   TEXT        NOT NULL,
    operation    TEXT        NOT NULL,
    script_id    TEXT        NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (connection, operation, script_id)
);

CREATE INDEX IF NOT EXISTS idx_api_operation_callers_seen
    ON api_operation_callers (last_seen_at);

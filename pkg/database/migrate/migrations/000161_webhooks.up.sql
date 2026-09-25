-- 000161: inbound webhook sources (#1870).
--
-- Events never go into this database. An event is written to object storage
-- by the receiver and compacted there into Parquet; what is kept here is the
-- control data only: which sources exist, how each authenticates, and which
-- hours of each have been compacted, dirtied or deleted.
--
-- auth and config are JSONB rather than a column per setting because each
-- auth mode needs a different subset (an HMAC source has a signature header,
-- a path_token source has none), and the shapes are refused in Go, which can
-- name the field the operator got wrong. Secrets inside auth are encrypted by
-- the platform's field encryptor before they are written.
CREATE TABLE IF NOT EXISTS webhook_sources (
    name            TEXT        PRIMARY KEY,
    enabled         BOOLEAN     NOT NULL DEFAULT TRUE,
    auth            JSONB       NOT NULL DEFAULT '{}'::jsonb,
    config          JSONB       NOT NULL DEFAULT '{}'::jsonb,
    connection_name TEXT        NOT NULL,
    created_by      TEXT        NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- One row per compaction window of one source that has received at least one
-- segment. A window is window_seconds long, starts at window_start (UTC), and
-- is the source's compact_every setting at the time its first segment landed;
-- a later segment for the same start only ever lengthens it, so a window is
-- never compacted while a segment can still land in it.
--
-- generation increases each time a receiver records a segment for the window,
-- which it does both before and after writing one. The compactor records the
-- generation it claimed the window at; a segment recorded while a compaction
-- ran leaves generation ahead of it, so the window is owed another compaction
-- rather than silently missing events from its Parquet file.
--
-- compacted_generation, resource_id and location describe the last
-- compaction: the resource the window's Parquet file is, and the directory its
-- partition is registered at. raw_deleted_at and expired_at are retention's
-- record, kept here rather than read back from an object listing so a pass
-- interrupted between unregistering a partition and deleting its objects is
-- resumed by the next one.
CREATE TABLE IF NOT EXISTS webhook_windows (
    source               TEXT        NOT NULL REFERENCES webhook_sources(name) ON DELETE CASCADE,
    window_start         TIMESTAMPTZ NOT NULL,
    window_seconds       INTEGER     NOT NULL,
    generation           BIGINT      NOT NULL DEFAULT 0,
    last_segment_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    compacted_generation BIGINT      NOT NULL DEFAULT 0,
    compacted_at         TIMESTAMPTZ,
    segments             INTEGER     NOT NULL DEFAULT 0,
    events               BIGINT      NOT NULL DEFAULT 0,
    duplicates           BIGINT      NOT NULL DEFAULT 0,
    digest               TEXT        NOT NULL DEFAULT '',
    resource_id          TEXT        NOT NULL DEFAULT '',
    location             TEXT        NOT NULL DEFAULT '',
    raw_deleted_at       TIMESTAMPTZ,
    unregistered_at      TIMESTAMPTZ,
    expired_at           TIMESTAMPTZ,
    claimed_until        TIMESTAMPTZ,
    attempts             INTEGER     NOT NULL DEFAULT 0,
    last_error           TEXT        NOT NULL DEFAULT '',
    PRIMARY KEY (source, window_start)
);

-- The compactor's claim: windows whose segments are not all in the Parquet
-- file. Partial, because a compacted window is the common case and is never
-- scanned for work.
CREATE INDEX IF NOT EXISTS idx_webhook_windows_owed
    ON webhook_windows (window_start)
    WHERE generation <> compacted_generation AND expired_at IS NULL;

-- The resource a window was written as, for the search hit that has to find
-- the source's table from the resource it is looking at.
CREATE INDEX IF NOT EXISTS idx_webhook_windows_resource
    ON webhook_windows (resource_id)
    WHERE resource_id <> '';

-- Request counts per source, minute and outcome, written by each receiver
-- from counts it accumulates in memory. The source's page reads the last hour
-- and the last day from here; the compactor prunes rows older than two days.
CREATE TABLE IF NOT EXISTS webhook_request_counts (
    source  TEXT        NOT NULL REFERENCES webhook_sources(name) ON DELETE CASCADE,
    minute  TIMESTAMPTZ NOT NULL,
    outcome TEXT        NOT NULL,
    count   BIGINT      NOT NULL DEFAULT 0,
    PRIMARY KEY (source, minute, outcome)
);

-- The most recent rejected requests of each source: when, which outcome, and
-- why. Never the body. Each write keeps the newest 50 per source.
CREATE TABLE IF NOT EXISTS webhook_rejections (
    id          BIGSERIAL   PRIMARY KEY,
    source      TEXT        NOT NULL REFERENCES webhook_sources(name) ON DELETE CASCADE,
    rejected_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    outcome     TEXT        NOT NULL,
    reason      TEXT        NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_webhook_rejections_source
    ON webhook_rejections (source, id DESC);

-- A source's table is recorded as a registration like any other, so the
-- table listing and every surface that reads registrations sees it. Its
-- source_id is the source's name.
ALTER TABLE table_registrations DROP CONSTRAINT IF EXISTS table_registrations_source_kind_check;
ALTER TABLE table_registrations ADD CONSTRAINT table_registrations_source_kind_check
    CHECK (source_kind IN ('resource', 'asset', 'webhook'));

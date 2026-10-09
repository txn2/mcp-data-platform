-- 000184: the basemap regions a deployment keeps (#2068).
--
-- A street map an agent draws loads its basemap from this deployment, not a
-- public tile host: an extract of the OpenStreetMap build, cut to a region and
-- a zoom range, written to the operator's bucket and served by range. One row
-- is one region. A region the platform fetched records the box and zoom it
-- asked for; a region an operator uploaded by hand records what its archive's
-- header says.
--
-- state is the region's last fetch: queued, fetching, ready or failed. The
-- archive_* columns are the archive being served, and they are replaced only
-- when a fetch completes, so a refresh that fails leaves the previous build
-- serving. progress_bytes and total_bytes report a fetch in flight; total is
-- the extract's exact size, known before its tiles are copied.
CREATE TABLE IF NOT EXISTS map_regions (
    id               TEXT             PRIMARY KEY,
    name             TEXT             NOT NULL,
    preset           TEXT             NOT NULL DEFAULT '',
    origin           TEXT             NOT NULL DEFAULT 'fetch',
    min_lon          DOUBLE PRECISION NOT NULL,
    min_lat          DOUBLE PRECISION NOT NULL,
    max_lon          DOUBLE PRECISION NOT NULL,
    max_lat          DOUBLE PRECISION NOT NULL,
    max_zoom         SMALLINT         NOT NULL,
    state            TEXT             NOT NULL DEFAULT 'queued',
    progress_bytes   BIGINT           NOT NULL DEFAULT 0,
    total_bytes      BIGINT           NOT NULL DEFAULT 0,
    error            TEXT             NOT NULL DEFAULT '',
    requested_at     TIMESTAMPTZ      NOT NULL DEFAULT NOW(),
    started_at       TIMESTAMPTZ,
    finished_at      TIMESTAMPTZ,
    archive_bucket   TEXT             NOT NULL DEFAULT '',
    archive_key      TEXT             NOT NULL DEFAULT '',
    archive_size     BIGINT           NOT NULL DEFAULT 0,
    archive_build    TEXT             NOT NULL DEFAULT '',
    archive_min_zoom SMALLINT         NOT NULL DEFAULT 0,
    archive_max_zoom SMALLINT         NOT NULL DEFAULT 0,
    archive_min_lon  DOUBLE PRECISION NOT NULL DEFAULT 0,
    archive_min_lat  DOUBLE PRECISION NOT NULL DEFAULT 0,
    archive_max_lon  DOUBLE PRECISION NOT NULL DEFAULT 0,
    archive_max_lat  DOUBLE PRECISION NOT NULL DEFAULT 0,
    archive_ready_at TIMESTAMPTZ,
    created_by       TEXT             NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ      NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ      NOT NULL DEFAULT NOW()
);

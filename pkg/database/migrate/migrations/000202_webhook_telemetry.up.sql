-- 000202: webhook telemetry history (#2150).
--
-- Request counts and acknowledgement latency used to be pruned at 48 hours.
-- They are now kept for webhooks.compactor.telemetry_retention_days (default
-- 14): per minute for the first 48 hours, then rolled up into hourly rows (the
-- row at the hour's first minute holds the hour).
--
-- webhook_status_counts counts the same requests by the HTTP status the sender
-- was answered with; 0 is a request the sender gave up on before it was
-- answered. It is a table of its own rather than a column of
-- webhook_request_counts so that table keeps the key a replica of the previous
-- release writes it under during a rolling upgrade.
CREATE TABLE IF NOT EXISTS webhook_status_counts (
    source      TEXT        NOT NULL REFERENCES webhook_sources(name) ON DELETE CASCADE,
    minute      TIMESTAMPTZ NOT NULL,
    http_status SMALLINT    NOT NULL,
    count       BIGINT      NOT NULL DEFAULT 0,
    PRIMARY KEY (source, minute, http_status)
);

-- The time each stage of the minute's acknowledged deliveries took, summed,
-- so the page charts the average buffer, mark and put (or spool sync) wait
-- over time and not only the slowest delivery's.
ALTER TABLE webhook_ack_latency ADD COLUMN IF NOT EXISTS buffer_ms_sum BIGINT NOT NULL DEFAULT 0;
ALTER TABLE webhook_ack_latency ADD COLUMN IF NOT EXISTS mark_ms_sum BIGINT NOT NULL DEFAULT 0;
ALTER TABLE webhook_ack_latency ADD COLUMN IF NOT EXISTS put_ms_sum BIGINT NOT NULL DEFAULT 0;

-- Each receiver's minute: the events it accepted, the events and bytes it
-- wrote to object storage (drained from its spool, or written straight from
-- its buffer), and what its spool held at the minute's last report. Receive
-- rate against drain rate is what says whether a burst is being absorbed.
CREATE TABLE IF NOT EXISTS webhook_replica_minutes (
    replica            TEXT        NOT NULL,
    minute             TIMESTAMPTZ NOT NULL,
    received_events    BIGINT      NOT NULL DEFAULT 0,
    drained_events     BIGINT      NOT NULL DEFAULT 0,
    drained_bytes      BIGINT      NOT NULL DEFAULT 0,
    spool_bytes        BIGINT      NOT NULL DEFAULT 0,
    spool_events       BIGINT      NOT NULL DEFAULT 0,
    oldest_age_seconds BIGINT      NOT NULL DEFAULT 0,
    PRIMARY KEY (replica, minute)
);

-- One row per compaction of a window: how long it took, how far behind the
-- window's end it ran, and its size read (the gzipped segments), held (the
-- events in memory) and written (the Parquet file). The page charts lag and
-- duration and sets the largest window against the memory limit.
CREATE TABLE IF NOT EXISTS webhook_compactions (
    source        TEXT        NOT NULL,
    window_start  TIMESTAMPTZ NOT NULL,
    compacted_at  TIMESTAMPTZ NOT NULL,
    duration_ms   BIGINT      NOT NULL,
    lag_seconds   BIGINT      NOT NULL,
    segments      INTEGER     NOT NULL,
    events        BIGINT      NOT NULL,
    input_bytes   BIGINT      NOT NULL,
    decoded_bytes BIGINT      NOT NULL,
    output_bytes  BIGINT      NOT NULL,
    PRIMARY KEY (source, window_start, compacted_at)
);
CREATE INDEX IF NOT EXISTS idx_webhook_compactions_at ON webhook_compactions (compacted_at);

-- Each window's raw segment bytes and count since its raw segments were last
-- released, and its compacted file's size: storage per source, raw and
-- compacted, read from the window records rather than a bucket listing.
ALTER TABLE webhook_windows ADD COLUMN IF NOT EXISTS raw_bytes BIGINT NOT NULL DEFAULT 0;
ALTER TABLE webhook_windows ADD COLUMN IF NOT EXISTS raw_segments BIGINT NOT NULL DEFAULT 0;
ALTER TABLE webhook_windows ADD COLUMN IF NOT EXISTS compacted_bytes BIGINT NOT NULL DEFAULT 0;

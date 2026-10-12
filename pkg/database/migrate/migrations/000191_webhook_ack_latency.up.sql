-- 000191: a webhook source's acknowledgement latency, and what identifies a
-- refused delivery that carried events (#2107).
--
-- Each receiver sums the latency of the deliveries it answered 202 into one
-- row per source and minute: a count per bucket (the bounds are fixed in
-- whstore, the last bucket holds everything slower), the slowest delivery's
-- total, and how long that delivery spent in the buffer, recording its window
-- and writing its segment. Replicas writing the same minute add into the same
-- row. The compactor prunes rows older than two days with the request counts.
CREATE TABLE IF NOT EXISTS webhook_ack_latency (
    source            TEXT        NOT NULL REFERENCES webhook_sources(name) ON DELETE CASCADE,
    minute            TIMESTAMPTZ NOT NULL,
    buckets           BIGINT[]    NOT NULL,
    max_ms            BIGINT      NOT NULL DEFAULT 0,
    slowest_at        TIMESTAMPTZ NOT NULL,
    slowest_buffer_ms BIGINT      NOT NULL DEFAULT 0,
    slowest_mark_ms   BIGINT      NOT NULL DEFAULT 0,
    slowest_put_ms    BIGINT      NOT NULL DEFAULT 0,
    PRIMARY KEY (source, minute)
);

-- A refused delivery whose events were read carries the SHA-256 of its body
-- and the ids of its first events, so an operator can look them up in the
-- source's table. A row carrying ids is never folded into a run.
ALTER TABLE webhook_rejections
    ADD COLUMN IF NOT EXISTS request_hash TEXT   NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS event_ids    TEXT[] NOT NULL DEFAULT '{}';

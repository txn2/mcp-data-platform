-- 000200: each webhook receiver's local spool, as it last reported it (#2121).
--
-- A receiver with a spool acknowledges a request once its events are synced to
-- its own disk and drains them to object storage behind the answer. The spool
-- is on one replica's disk and the admin page is served by any replica, so each
-- receiver writes what its spool holds here on its stats flush: the bytes,
-- events and files waiting, when the oldest event waiting was received, and how
-- its drain is going. A row not updated for a week is removed with the next
-- write.
CREATE TABLE IF NOT EXISTS webhook_spool_state (
    replica         TEXT        PRIMARY KEY,
    dir             TEXT        NOT NULL,
    bytes           BIGINT      NOT NULL DEFAULT 0,
    events          BIGINT      NOT NULL DEFAULT 0,
    files           INTEGER     NOT NULL DEFAULT 0,
    oldest_at       TIMESTAMPTZ,
    max_bytes       BIGINT      NOT NULL DEFAULT 0,
    max_age_seconds BIGINT      NOT NULL DEFAULT 0,
    drained_events  BIGINT      NOT NULL DEFAULT 0,
    drain_failures  BIGINT      NOT NULL DEFAULT 0,
    last_drain_at   TIMESTAMPTZ,
    last_error      TEXT        NOT NULL DEFAULT '',
    started_at      TIMESTAMPTZ NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL
);

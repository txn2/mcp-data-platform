-- 000201: leveled alert state, storage history, spool volume space (#2148, #2149).
--
-- alert_conditions is each warning or critical condition a leveled alert
-- (storage capacity, webhook receiver health) found on its last evaluation,
-- one row per condition key. A condition is notified at its level every
-- evaluation, under a key the channel's repeat window applies to; the row is
-- what tells the next evaluation that a condition which has cleared was
-- alerting, so a resolved notification is sent once and the row removed.
CREATE TABLE IF NOT EXISTS alert_conditions (
    key        TEXT        PRIMARY KEY,
    family     TEXT        NOT NULL,
    level      TEXT        NOT NULL,
    title      TEXT        NOT NULL DEFAULT '',
    since      TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_alert_conditions_family ON alert_conditions (family);

-- storage_samples is the history of each store's usage and capacity, one row
-- per store per sample: the database, the object store cluster, a bucket
-- against its budget, and each replica's webhook spool volume. total_bytes is
-- NULL where the platform knows no capacity for the store. Kept for the
-- storage settings' history period (default 180 days).
CREATE TABLE IF NOT EXISTS storage_samples (
    store       TEXT        NOT NULL,
    sampled_at  TIMESTAMPTZ NOT NULL,
    used_bytes  BIGINT      NOT NULL,
    total_bytes BIGINT,
    PRIMARY KEY (store, sampled_at)
);

-- A receiver reports the filesystem its spool directory is on beside what the
-- spool holds, so the volume filling is seen before the spool refuses
-- deliveries for want of disk rather than for its own max_bytes. NULL where
-- the platform cannot read it.
ALTER TABLE webhook_spool_state ADD COLUMN IF NOT EXISTS fs_total_bytes BIGINT;
ALTER TABLE webhook_spool_state ADD COLUMN IF NOT EXISTS fs_free_bytes BIGINT;

-- How many drains in a row have failed and since when, so a drain that keeps
-- failing is told from one that failed once in a day (#2149). Reset by the
-- next drain that succeeds.
ALTER TABLE webhook_spool_state ADD COLUMN IF NOT EXISTS consecutive_failures BIGINT NOT NULL DEFAULT 0;
ALTER TABLE webhook_spool_state ADD COLUMN IF NOT EXISTS failing_since TIMESTAMPTZ;

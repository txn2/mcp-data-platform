-- 000250: each site's traffic and latency, recorded by the platform (#2647).
--
-- Each replica tallies requests in memory and adds its counts once a minute
-- into rows its replicas share: one per (kind, subject, environment) that had
-- traffic, at the minute grain and the hour grain, and one per replica across
-- every site. subject is a site's id, or an application's name for its page
-- origin (kind pages). c2xx..cother count status classes; l0..l11 count
-- requests by latency bucket (<=5, 10, 25, 50, 100, 250, 500, 1000, 2500,
-- 5000, 10000 ms, and more), from which percentiles are read. No row holds
-- anything a request carried. Minute rows are kept 24 hours and hour rows 90
-- days by default (sites.traffic).
CREATE TABLE IF NOT EXISTS site_traffic (
    grain       TEXT        NOT NULL CHECK (grain IN ('minute', 'hour')),
    bucket      TIMESTAMPTZ NOT NULL,
    kind        TEXT        NOT NULL CHECK (kind IN ('site', 'pages')),
    subject     TEXT        NOT NULL,
    environment TEXT        NOT NULL DEFAULT '',
    c2xx        BIGINT      NOT NULL DEFAULT 0,
    c3xx        BIGINT      NOT NULL DEFAULT 0,
    c4xx        BIGINT      NOT NULL DEFAULT 0,
    c5xx        BIGINT      NOT NULL DEFAULT 0,
    cother      BIGINT      NOT NULL DEFAULT 0,
    l0          BIGINT      NOT NULL DEFAULT 0,
    l1          BIGINT      NOT NULL DEFAULT 0,
    l2          BIGINT      NOT NULL DEFAULT 0,
    l3          BIGINT      NOT NULL DEFAULT 0,
    l4          BIGINT      NOT NULL DEFAULT 0,
    l5          BIGINT      NOT NULL DEFAULT 0,
    l6          BIGINT      NOT NULL DEFAULT 0,
    l7          BIGINT      NOT NULL DEFAULT 0,
    l8          BIGINT      NOT NULL DEFAULT 0,
    l9          BIGINT      NOT NULL DEFAULT 0,
    l10         BIGINT      NOT NULL DEFAULT 0,
    l11         BIGINT      NOT NULL DEFAULT 0,
    PRIMARY KEY (grain, bucket, kind, subject, environment)
);
CREATE INDEX IF NOT EXISTS idx_site_traffic_subject ON site_traffic (kind, subject, grain, bucket);

CREATE TABLE IF NOT EXISTS site_traffic_replicas (
    grain       TEXT        NOT NULL CHECK (grain IN ('minute', 'hour')),
    bucket      TIMESTAMPTZ NOT NULL,
    instance    TEXT        NOT NULL,
    c2xx        BIGINT      NOT NULL DEFAULT 0,
    c3xx        BIGINT      NOT NULL DEFAULT 0,
    c4xx        BIGINT      NOT NULL DEFAULT 0,
    c5xx        BIGINT      NOT NULL DEFAULT 0,
    cother      BIGINT      NOT NULL DEFAULT 0,
    l0          BIGINT      NOT NULL DEFAULT 0,
    l1          BIGINT      NOT NULL DEFAULT 0,
    l2          BIGINT      NOT NULL DEFAULT 0,
    l3          BIGINT      NOT NULL DEFAULT 0,
    l4          BIGINT      NOT NULL DEFAULT 0,
    l5          BIGINT      NOT NULL DEFAULT 0,
    l6          BIGINT      NOT NULL DEFAULT 0,
    l7          BIGINT      NOT NULL DEFAULT 0,
    l8          BIGINT      NOT NULL DEFAULT 0,
    l9          BIGINT      NOT NULL DEFAULT 0,
    l10         BIGINT      NOT NULL DEFAULT 0,
    l11         BIGINT      NOT NULL DEFAULT 0,
    PRIMARY KEY (grain, bucket, instance)
);

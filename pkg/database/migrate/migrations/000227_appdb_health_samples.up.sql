-- 000227: the health history of each application database (#2409).
--
-- One replica samples every application database on an interval and keeps
-- the reading reduced to numbers here: sizes, transaction id age,
-- connections, the counts of idle-in-transaction, long-running and blocked
-- sessions, of unused, duplicate and invalid indexes, the highest dead tuple
-- ratio, and how many objects differ from what the applied migrations build.
-- No statement or session text, role or value is kept: the report an
-- administrator reads on the connection page is read from the database when
-- it is asked for.
CREATE TABLE IF NOT EXISTS appdb_health_samples (
    connection TEXT        NOT NULL,
    sampled_at TIMESTAMPTZ NOT NULL,
    summary    JSONB       NOT NULL,
    PRIMARY KEY (connection, sampled_at)
);

CREATE INDEX IF NOT EXISTS appdb_health_samples_sampled_at ON appdb_health_samples (sampled_at);

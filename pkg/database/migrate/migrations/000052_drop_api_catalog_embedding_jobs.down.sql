-- 000052 down: recreate api_catalog_embedding_jobs as migrations
-- 000045 + 000046 left it, so a downgrade lands on the prior schema.
-- (The pre-000052 application code that consumed this table is
-- restored by reverting the accompanying code change.)

CREATE TABLE IF NOT EXISTS api_catalog_embedding_jobs (
    id                BIGSERIAL   PRIMARY KEY,
    catalog_id        TEXT        NOT NULL,
    spec_name         TEXT        NOT NULL,
    kind              TEXT        NOT NULL,
    status            TEXT        NOT NULL DEFAULT 'pending',
    attempts          INTEGER     NOT NULL DEFAULT 0,
    last_error        TEXT        NOT NULL DEFAULT '',
    next_run_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    worker_id         TEXT        NOT NULL DEFAULT '',
    lease_expires_at  TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at        TIMESTAMPTZ,
    completed_at      TIMESTAMPTZ,
    embedded_so_far   INTEGER     NOT NULL DEFAULT 0,
    FOREIGN KEY (catalog_id, spec_name)
        REFERENCES api_catalog_specs(catalog_id, spec_name)
        ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS api_catalog_embedding_jobs_open
    ON api_catalog_embedding_jobs (catalog_id, spec_name)
    WHERE status IN ('pending', 'running');

CREATE INDEX IF NOT EXISTS api_catalog_embedding_jobs_ready
    ON api_catalog_embedding_jobs (next_run_at)
    WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS api_catalog_embedding_jobs_lease
    ON api_catalog_embedding_jobs (lease_expires_at)
    WHERE status = 'running';

CREATE INDEX IF NOT EXISTS api_catalog_embedding_jobs_history
    ON api_catalog_embedding_jobs (catalog_id, spec_name, id DESC);

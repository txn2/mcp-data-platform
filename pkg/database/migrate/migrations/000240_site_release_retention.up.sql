-- 000240: each environment keeps its last N releases, and a sweep prunes the
-- rest (#2602).
--
-- keep_releases is an environment's limit; NULL follows the deployment's
-- default (sites.releases.keep_default) and 0 keeps every release. A pruned
-- release keeps its row, marked by pruned_at, so the deployments and approval
-- requests that name it stay as history; its file rows and the objects only it
-- named are deleted.
ALTER TABLE site_environments ADD COLUMN IF NOT EXISTS keep_releases INT
    CHECK (keep_releases IS NULL OR keep_releases >= 0);

ALTER TABLE site_releases ADD COLUMN IF NOT EXISTS pruned_at TIMESTAMPTZ;

-- What each sweep did, or in report mode would have done: counts only, never a
-- path or a key. The sweep keeps the most recent rows.
CREATE TABLE IF NOT EXISTS site_retention_sweeps (
    id           BIGSERIAL   PRIMARY KEY,
    mode         TEXT        NOT NULL CHECK (mode IN ('report', 'delete')),
    run_by       TEXT        NOT NULL DEFAULT '',
    scope        TEXT        NOT NULL DEFAULT '',
    sites        INT         NOT NULL DEFAULT 0,
    releases     INT         NOT NULL DEFAULT 0,
    rewritten    INT         NOT NULL DEFAULT 0,
    files        INT         NOT NULL DEFAULT 0,
    bytes        BIGINT      NOT NULL DEFAULT 0,
    orphans      INT         NOT NULL DEFAULT 0,
    orphan_bytes BIGINT      NOT NULL DEFAULT 0,
    skipped      INT         NOT NULL DEFAULT 0,
    refused      INT         NOT NULL DEFAULT 0,
    failed       INT         NOT NULL DEFAULT 0,
    started_at   TIMESTAMPTZ NOT NULL,
    finished_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_site_retention_sweeps_finished ON site_retention_sweeps (finished_at DESC);

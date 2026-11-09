-- 000236: who can reach a site, and publishing in the background (#2559).
--
-- A site has one address of its own, <site>.<pages domain>, which serves its
-- default environment to its owner, administrators and the people in
-- site_grants. Every other environment answers on the domains added to it.
-- The platform does not check DNS, certificates or domain ownership.

-- 'manage' publishes, promotes, rolls back, adds domains and shares view
-- access. 'view' opens the site's own address and gives nothing else. email is
-- stored lowercased, as the access check reads it.
CREATE TABLE IF NOT EXISTS site_grants (
    site_id    TEXT        NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    email      TEXT        NOT NULL CHECK (email = lower(email)),
    level      TEXT        NOT NULL CHECK (level IN ('manage', 'view')),
    granted_by TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (site_id, email)
);

-- Everyone whose persona is not external may open the site's own address: a
-- company intranet.
ALTER TABLE sites ADD COLUMN IF NOT EXISTS internal_view BOOLEAN NOT NULL DEFAULT FALSE;

-- Every site gets the default environment its own address serves, at the
-- release dev was serving.
INSERT INTO site_environments (site_id, name, release_number, updated_by)
SELECT s.id, 'default', dev.release_number, s.created_by
  FROM sites s
  LEFT JOIN site_environments dev ON dev.site_id = s.id AND dev.name = 'dev'
ON CONFLICT (site_id, name) DO NOTHING;

-- A hostname routes as soon as it is added, so one that never passed the old
-- verification must not start routing now.
DELETE FROM site_domains WHERE verified_at IS NULL;
ALTER TABLE site_domains DROP COLUMN IF EXISTS token;
ALTER TABLE site_domains DROP COLUMN IF EXISTS verified_at;
ALTER TABLE site_domains DROP COLUMN IF EXISTS verified_by;

DROP TABLE IF EXISTS site_certificates;

-- A publish or a copy running apart from the request that started it. updated_at
-- is its heartbeat; stop_requested is how another replica stops it.
CREATE TABLE IF NOT EXISTS site_jobs (
    id             TEXT        NOT NULL PRIMARY KEY,
    site_id        TEXT        NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    kind           TEXT        NOT NULL CHECK (kind IN ('publish', 'crawl')),
    status         TEXT        NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'done', 'failed', 'stopped')),
    phase          TEXT        NOT NULL DEFAULT '',
    environment    TEXT        NOT NULL,
    source         TEXT        NOT NULL DEFAULT '',
    done           INT         NOT NULL DEFAULT 0,
    total          INT         NOT NULL DEFAULT 0,
    pages          INT         NOT NULL DEFAULT 0,
    bytes          BIGINT      NOT NULL DEFAULT 0,
    skipped_count  INT         NOT NULL DEFAULT 0,
    redirects      INT         NOT NULL DEFAULT 0,
    skipped        JSONB       NOT NULL DEFAULT '[]',
    release_number INT         NOT NULL DEFAULT 0,
    error          TEXT        NOT NULL DEFAULT '',
    stop_requested BOOLEAN     NOT NULL DEFAULT FALSE,
    created_by     TEXT        NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_site_jobs_site ON site_jobs (site_id, created_at DESC);

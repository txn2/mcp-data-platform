-- 000258: a primary domain per environment, and domains that redirect to it
-- (#2687).
--
-- One domain of an environment may be its primary; every other domain of that
-- environment either serves the release (serve, every domain until now) or
-- answers a permanent redirect to the primary (redirect). An application's
-- domain is always serve.

ALTER TABLE site_domains ADD COLUMN IF NOT EXISTS is_primary BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE site_domains ADD COLUMN IF NOT EXISTS mode TEXT NOT NULL DEFAULT 'serve';

ALTER TABLE site_domains DROP CONSTRAINT IF EXISTS site_domains_mode_check;
ALTER TABLE site_domains ADD CONSTRAINT site_domains_mode_check CHECK (
    mode IN ('serve', 'redirect')
    AND NOT (is_primary AND mode = 'redirect')
    AND (site_id IS NOT NULL OR (NOT is_primary AND mode = 'serve'))
);

-- At most one primary per environment.
CREATE UNIQUE INDEX IF NOT EXISTS site_domains_one_primary
    ON site_domains (site_id, environment) WHERE is_primary;

-- 000241: a site mounts managed-resource folders as its media (#2591).
--
-- Each row serves the files of one folder of a resource library at a URL
-- prefix on every environment of the site, after the release's own files and
-- before its redirects. Media sits outside releases: publish, promote and
-- rollback leave it alone, and it counts toward no release limit. A file
-- under a mount is refused deletion unless forced, the way a referenced file
-- is.
CREATE TABLE IF NOT EXISTS site_media (
    site_id    TEXT        NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    prefix     TEXT        NOT NULL,
    scope      TEXT        NOT NULL CHECK (scope IN ('global', 'persona', 'user')),
    scope_id   TEXT        NOT NULL DEFAULT '',
    folder     TEXT        NOT NULL,
    created_by TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (site_id, prefix)
);
CREATE INDEX IF NOT EXISTS idx_site_media_library ON site_media (scope, scope_id);

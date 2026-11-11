-- 000242: what holds a site's stored objects while they are written or
-- deleted (#2602).
--
-- A publish stores its files before it records the release that names them,
-- and a release sweep deletes the objects no kept release names after it
-- commits. Without a hold, a publish re-storing a file a pruned release named
-- could have it deleted under it. A publish holds an upload and a sweep holds
-- a sweep, each taken under the site's row lock, and each refused while the
-- other is held: a sweep leaves a site with an upload held alone, and a
-- publish waits for a sweep's deletes to finish. Each hold expires, so a
-- process that died holding one blocks the other only until then.
CREATE TABLE IF NOT EXISTS site_object_holds (
    id         TEXT        NOT NULL PRIMARY KEY,
    site_id    TEXT        NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    kind       TEXT        NOT NULL CHECK (kind IN ('upload', 'sweep')),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_site_object_holds_site ON site_object_holds (site_id, kind, expires_at);

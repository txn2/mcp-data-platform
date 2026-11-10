-- 000237: a release can be derived from another (#2588). A patch changes one
-- file of a release that may hold 20,000, so a derived release keeps only what
-- differs from its parent, and the file set is read by walking the chain.
--
-- base_number is the release it was made from, for people reading the history;
-- manifest_parent is the chain and is NULL on a release that holds its whole
-- file set, which every release does past a bounded depth even when derived.
ALTER TABLE site_releases ADD COLUMN IF NOT EXISTS base_number INT NOT NULL DEFAULT 0;
ALTER TABLE site_releases ADD COLUMN IF NOT EXISTS manifest_parent TEXT REFERENCES site_releases(id) ON DELETE CASCADE;
ALTER TABLE site_releases ADD COLUMN IF NOT EXISTS manifest_depth INT NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_site_releases_manifest_parent ON site_releases (manifest_parent) WHERE manifest_parent IS NOT NULL;

-- removed marks a path a derived release takes out of its parent.
ALTER TABLE site_release_files ADD COLUMN IF NOT EXISTS removed BOOLEAN NOT NULL DEFAULT FALSE;

-- A file's objects now live under its digest. stored_in names the release whose
-- own prefix holds a file written before that, and is empty for every other, so
-- nothing in object storage has to move.
ALTER TABLE site_release_files ADD COLUMN IF NOT EXISTS stored_in TEXT NOT NULL DEFAULT '';
UPDATE site_release_files SET stored_in = release_id WHERE stored_in = '';

-- 000272: an upload link an external service can use (#2844).
--
-- A link issued by 000252 took one file and was spent by the first PUT,
-- whatever came of it. A link now takes up to max_files files; files_used
-- counts those filed, a PUT that files nothing gives its place back, and the
-- service names each file in the URL when the link was issued with no
-- filename. resource_ids lists every file the link filed, and revoked_at ends
-- a link before it expires, from the portal.
--
-- A link 000252 recorded as used has filed its one file (or failed it), so it
-- is counted as full and is never taken again.
ALTER TABLE resource_upload_links
    ADD COLUMN IF NOT EXISTS max_files    INTEGER NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS files_used   INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS resource_ids TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS revoked_at   TIMESTAMPTZ;

UPDATE resource_upload_links SET files_used = 1 WHERE used_at IS NOT NULL AND files_used = 0;

CREATE INDEX IF NOT EXISTS idx_resource_upload_links_user ON resource_upload_links (user_id, expires_at);

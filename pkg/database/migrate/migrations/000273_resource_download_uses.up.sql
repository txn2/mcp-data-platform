-- 000273: the single-download links that have been used (#2847).
--
-- A download link is signed and stored nowhere; a single-download link also
-- carries an id, and its first full download records that id here, so every
-- replica refuses a second one. A HEAD request or a byte-range read records
-- nothing. A row outlives its link by a day and is swept as links are used.
CREATE TABLE IF NOT EXISTS resource_download_uses (
    link_id     TEXT PRIMARY KEY,
    resource_id TEXT NOT NULL,
    used_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_resource_download_uses_expires ON resource_download_uses (expires_at);

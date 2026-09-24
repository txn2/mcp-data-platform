-- 000159: a document the renderer cannot draw leaves the queue (#1868).
--
-- A render that failed because the renderer stopped answering, or because the
-- stored file could not be read, recorded nothing, so the same row was claimed
-- again on every pass, forever. A document that pins the renderer kept it busy
-- for hours and every other tile waited behind it.
--
-- thumbnail_attempts counts the claims taken on a row since its last recorded
-- result. The claim adds one, so a replica that dies mid-render still charges
-- the document; a recorded tile or failure, and asking for the tile again,
-- return it to zero. Past the configured bound the worker records the
-- document as not drawable, which it stays until it changes.
ALTER TABLE portal_assets
    ADD COLUMN IF NOT EXISTS thumbnail_attempts INTEGER NOT NULL DEFAULT 0;

ALTER TABLE resources
    ADD COLUMN IF NOT EXISTS thumbnail_attempts INTEGER NOT NULL DEFAULT 0;

-- A collection's mosaic had no failure record at all, so a collection whose
-- mosaic could not be composed could never leave the queue. A failure is held
-- against the source it was composed from, so a collection is owed again once
-- a member is added, removed or redrawn.
ALTER TABLE portal_collections
    ADD COLUMN IF NOT EXISTS thumbnail_attempts INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS thumbnail_failure TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS thumbnail_failed_source TEXT NOT NULL DEFAULT '';

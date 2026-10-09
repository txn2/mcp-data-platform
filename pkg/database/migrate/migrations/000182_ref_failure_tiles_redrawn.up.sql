-- 000182: a tile withheld because a reference failed to load is drawn again (#2062).
--
-- The JSX frame's policy left the reference route out of font-src, so a JSX
-- document that declared a managed-resource font in an @font-face rule had the
-- font refused, and the tile worker counted the refusal as a reference that
-- could not be loaded and recorded the document as not drawable. The policy now
-- grants the route to fonts, and a font that still fails no longer withholds a
-- tile. Clearing the recorded failure, and the attempts charged against it,
-- puts each such row back in the worker's queue; it keeps its content-type icon
-- until the new tile is recorded. A row whose reference failure was an image or
-- a data file is retried too, and is recorded again if that file still fails.
UPDATE portal_assets
SET thumbnail_failure = '', thumbnail_failed_version = 0, thumbnail_attempts = 0
WHERE thumbnail_failure LIKE '%file(s) this document links to could not be loaded';

UPDATE resources
SET thumbnail_failure = '', thumbnail_failed_at = NULL, thumbnail_attempts = 0
WHERE thumbnail_failure LIKE '%file(s) this document links to could not be loaded';

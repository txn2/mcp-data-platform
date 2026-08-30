-- 000134: a resource's captured thumbnail (#1554).
--
-- A resource had no thumbnail. What the library drew in its place was the file
-- itself: the tile pointed at the content endpoint and the browser scaled a
-- full-size image down with CSS, capped by a cutoff past which the tile showed
-- nothing at all. A library of markdown, CSV and spreadsheets was a wall of
-- identical icons, an image library pulled megabytes to draw postage stamps,
-- and anything past the cutoff was blank.
--
-- An asset has not worked that way since 000020: it stores a captured PNG
-- beside the object and serves it from its own route. These are the same four
-- columns, so a resource is captured by the same browser queue and served the
-- same way.
--
-- Light and dark are separate because they are captured and uploaded
-- separately, exactly as for an asset: a dark pass that throws in the
-- rasterizer leaves the light variant current and the dark one behind, and one
-- column could not say so. A content type that carries its own colours (HTML,
-- SVG) stores only the light one and serves it in both modes.
ALTER TABLE resources
    ADD COLUMN IF NOT EXISTS thumbnail_s3_key TEXT NOT NULL DEFAULT '';

ALTER TABLE resources
    ADD COLUMN IF NOT EXISTS thumbnail_dark_s3_key TEXT NOT NULL DEFAULT '';

-- When each capture was taken, against the resource's own updated_at.
--
-- An asset compares versions instead, because an asset row carries its current
-- version. A resource does not: its versions live in resource_versions and the
-- head is MAX(version) there, so a version comparison would put a correlated
-- subquery in the predicate that finds work. The timestamp says the same thing
-- without the join -- a capture older than the row it came from is behind.
--
-- The cost of that choice is a metadata edit, which also moves updated_at and
-- so re-queues a capture of content that did not change. It costs one capture,
-- it corrects itself, and it is worth less than a join on every poll.
ALTER TABLE resources
    ADD COLUMN IF NOT EXISTS thumbnail_captured_at TIMESTAMPTZ;

ALTER TABLE resources
    ADD COLUMN IF NOT EXISTS thumbnail_dark_captured_at TIMESTAMPTZ;

-- Nothing to backfill: no resource has ever had a capture. Every row starts
-- pending, which is what the queue is for.

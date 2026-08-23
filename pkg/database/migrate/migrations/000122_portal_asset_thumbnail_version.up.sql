-- 000122: the version each of an asset's thumbnails was captured from (#1431).
--
-- A thumbnail is captured in a browser and there is no server that can render
-- one, so the only thing that can regenerate it is a portal tab that has been
-- told the one it holds is out of date. Until now nothing was told: a version
-- write blanked both pointers, and an asset a managed script rewrites on a
-- schedule went back to the placeholder icon every run and stayed there until
-- somebody opened the page it was listed on.
--
-- Recording the version a capture came from is what lets the pointers survive
-- the write. The asset keeps showing the image it has -- one version behind is
-- worth more than no image -- and the row itself says the capture has not
-- caught up, so the queue can find it without a person happening to be on the
-- right page.
--
-- Light and dark are stamped separately because they are captured and uploaded
-- separately: a dark pass that throws in the rasterizer leaves the light
-- variant current and the dark one behind, and one column could not say so.
ALTER TABLE portal_assets
    ADD COLUMN IF NOT EXISTS thumbnail_version INTEGER NOT NULL DEFAULT 0;

ALTER TABLE portal_assets
    ADD COLUMN IF NOT EXISTS thumbnail_dark_version INTEGER NOT NULL DEFAULT 0;

-- Every thumbnail that exists today was captured from the version the asset is
-- on now: the write path blanked the pointer on every version write, so a
-- non-empty key can only have been written after the last one. Stamping them
-- current is a statement of what the objects are, not an assumption, and it is
-- what keeps this migration from queueing an entire library for re-capture on
-- the deploy that adds the column.
UPDATE portal_assets SET thumbnail_version = current_version WHERE thumbnail_s3_key <> '';
UPDATE portal_assets SET thumbnail_dark_version = current_version WHERE thumbnail_dark_s3_key <> '';

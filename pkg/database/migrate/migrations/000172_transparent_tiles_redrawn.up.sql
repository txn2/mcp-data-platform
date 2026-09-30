-- 000172: SVG and image tiles are drawn again, transparent (#1991).
--
-- A tile of an SVG or a raster image was captured on the tile page's own
-- background, so a white logo meant for a dark page was stored as a blank
-- white rectangle. Those families are now captured with their transparent
-- areas left transparent, and every surface shows a checkerboard behind them.
--
-- The renderer generation rises from 2 to 3 (internal/platform/thumbworker
-- Renderer). Raising it alone would redraw every tile in the library; this
-- stamps every row that is NOT an SVG or an image with the new generation, so
-- only the tiles whose picture changes are owed. A row keeps serving its old
-- tile until the new one is recorded. The generation is part of a collection
-- mosaic's source, so a collection holding one of these assets is composed
-- again once its member is redrawn, and only then. Attempts are reset on the
-- rows owed a redraw, so a count left from an earlier draw cannot stop this
-- one.
WITH transparent(ct) AS (
    VALUES ('image/svg+xml'),
           ('image/png'), ('image/x-png'), ('image/jpeg'), ('image/jpg'), ('image/gif'), ('image/webp'),
           ('image/avif'), ('image/bmp'), ('image/x-icon'), ('image/vnd.microsoft.icon')
),
assets_kept AS (
    UPDATE portal_assets SET thumbnail_renderer = 3
    WHERE thumbnail_renderer = 2
      AND lower(btrim(split_part(content_type, ';', 1))) NOT IN (SELECT ct FROM transparent)
    RETURNING 1
),
assets_owed AS (
    UPDATE portal_assets SET thumbnail_attempts = 0
    WHERE thumbnail_renderer = 2 AND thumbnail_s3_key <> ''
      AND lower(btrim(split_part(content_type, ';', 1))) IN (SELECT ct FROM transparent)
    RETURNING 1
),
resources_kept AS (
    UPDATE resources SET thumbnail_renderer = 3
    WHERE thumbnail_renderer = 2
      AND lower(btrim(split_part(mime_type, ';', 1))) NOT IN (SELECT ct FROM transparent)
    RETURNING 1
)
UPDATE resources SET thumbnail_attempts = 0
WHERE thumbnail_renderer = 2 AND thumbnail_s3_key <> ''
  AND lower(btrim(split_part(mime_type, ';', 1))) IN (SELECT ct FROM transparent);

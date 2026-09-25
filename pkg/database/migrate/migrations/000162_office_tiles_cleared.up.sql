-- 000162: tiles stored for a type nothing draws are forgotten (#1882).
--
-- The rule deciding which content types get a tile matched a family's name
-- anywhere in the type, and every Office Open XML type contains "xml"
-- ("openxmlformats", "spreadsheetml"). A workbook, a Word document and a
-- presentation were offered to the renderer as XML and drawn as the text of
-- their zip bytes, and the card showed that picture instead of the file's
-- content-type icon. The rule now compares the whole type
-- (internal/thumbtypes), so no new tile is drawn for one, and this removes the
-- ones already recorded.
--
-- A type keeps its tile when the rule as of this migration draws it: every
-- text/ type, a +json or +xml dialect, and the types named below with the
-- spellings the platform settles on them. A tile of anything else was drawn by
-- the old match and is cleared, with any failure and attempt count recorded
-- against it. A cleared asset in a collection drops out of the collection's
-- mosaic source, so the mosaic is composed again without it. The image file
-- stays in the bucket beside the content until the asset or resource is
-- deleted.
WITH drawn(pattern) AS (
    VALUES ('text/%'), ('%+json'), ('%+xml'),
           ('application/pdf'), ('application/x-pdf'),
           ('application/json'), ('application/x-json'),
           ('application/x-ndjson'), ('application/ndjson'), ('application/jsonl'),
           ('application/yaml'), ('application/x-yaml'), ('application/x-yaml-stream'),
           ('application/xml'), ('application/x-xml'),
           ('application/sql'), ('application/x-sql'),
           ('application/csv'), ('application/markdown'),
           ('application/javascript'), ('application/x-javascript'),
           ('application/jsx'), ('application/x-python-code'),
           ('image/png'), ('image/x-png'), ('image/jpeg'), ('image/jpg'), ('image/gif'), ('image/webp'),
           ('image/avif'), ('image/bmp'), ('image/x-icon'), ('image/vnd.microsoft.icon')
),
assets AS (
    UPDATE portal_assets
    SET thumbnail_s3_key = '', thumbnail_dark_s3_key = '',
        thumbnail_version = 0, thumbnail_dark_version = 0,
        thumbnail_failure = '', thumbnail_failed_version = 0, thumbnail_attempts = 0
    WHERE (thumbnail_s3_key <> '' OR thumbnail_dark_s3_key <> '')
      AND NOT EXISTS (
          SELECT 1 FROM drawn WHERE lower(btrim(split_part(content_type, ';', 1))) LIKE drawn.pattern
      )
    RETURNING 1
)
UPDATE resources
SET thumbnail_s3_key = '', thumbnail_dark_s3_key = '',
    thumbnail_captured_at = NULL, thumbnail_dark_captured_at = NULL,
    thumbnail_failure = '', thumbnail_failed_at = NULL, thumbnail_attempts = 0
WHERE (thumbnail_s3_key <> '' OR thumbnail_dark_s3_key <> '')
  AND NOT EXISTS (
      SELECT 1 FROM drawn WHERE lower(btrim(split_part(mime_type, ';', 1))) LIKE drawn.pattern
  );

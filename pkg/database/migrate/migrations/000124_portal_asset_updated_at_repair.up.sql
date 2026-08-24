-- 000124: put updated_at back where a thumbnail capture moved it (#1466).
--
-- Until this release a thumbnail capture wrote the asset row through the same
-- update path an edit uses, and that path stamped updated_at unconditionally.
-- Any pass that re-captured a library's pending thumbnails therefore re-dated
-- every asset in it. The stamping is fixed in the same release; this repairs
-- the rows already carrying the date of a capture instead of the date of a
-- change.
--
-- The real last-changed time is not recoverable: the column that held it was
-- overwritten. The newest version's created_at is the durable record that
-- comes closest, so a row whose updated_at is later than its newest version is
-- reset to that version's date.
--
-- That rule is not exact. A version is written only when content is written,
-- so a rename, a description edit or a tag change after the last content write
-- also leaves updated_at later than the newest version, and this moves such a
-- row back to its last content date. That is the cost of the repair: on a
-- deployment that ran a capture pass every row is wrong today, and a date that
-- is early by one metadata edit is closer than one that reads as edited on the
-- day the pass ran.
UPDATE portal_assets AS a
SET updated_at = v.created_at
FROM (
    SELECT asset_id, MAX(created_at) AS created_at
    FROM portal_asset_versions
    GROUP BY asset_id
) AS v
WHERE v.asset_id = a.id
  AND a.updated_at > v.created_at;

-- 000172 stamped the tiles it did not redraw with renderer generation 3. The
-- binary it rolls back to draws as generation 2 and reads a higher stamp as
-- current, so returning them to 2 only keeps the column honest about which
-- generation drew them.
UPDATE portal_assets SET thumbnail_renderer = 2 WHERE thumbnail_renderer = 3;
UPDATE resources SET thumbnail_renderer = 2 WHERE thumbnail_renderer = 3;

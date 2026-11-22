-- #2751: removing or renaming a site environment left its site_tiles row
-- behind, and the storage reconcile counts every tile_key as a reference, so
-- the tile's object was never reclaimed. Removal and rename now delete or
-- move the row; this deletes the rows already left with no environment.
DELETE FROM site_tiles t
 WHERE NOT EXISTS (
       SELECT 1 FROM site_environments e
        WHERE e.site_id = t.site_id AND e.name = t.environment);

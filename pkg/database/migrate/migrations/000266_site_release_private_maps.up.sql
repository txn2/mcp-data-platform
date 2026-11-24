-- #2784: a release whose _build.json says "sourcemap": "private" holds its
-- source maps, and the gateway serves them only to the site's managers.
ALTER TABLE site_releases ADD COLUMN IF NOT EXISTS private_maps BOOLEAN NOT NULL DEFAULT FALSE;

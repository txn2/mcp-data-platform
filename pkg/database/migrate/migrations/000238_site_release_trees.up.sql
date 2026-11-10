-- 000238: a rendered release holds two trees (#2592). The output is what is
-- served, and the source is what is edited: a page's source and its output can
-- share a path and differ, so a file belongs to a tree. Every row before this is
-- output, and a release with no source tree serves its files as published.
ALTER TABLE site_release_files ADD COLUMN IF NOT EXISTS tree TEXT NOT NULL DEFAULT 'output'
    CHECK (tree IN ('output', 'source', 'render'));
ALTER TABLE site_release_files DROP CONSTRAINT IF EXISTS site_release_files_pkey;
ALTER TABLE site_release_files ADD PRIMARY KEY (release_id, tree, path);

ALTER TABLE site_releases ADD COLUMN IF NOT EXISTS rendered BOOLEAN NOT NULL DEFAULT FALSE;

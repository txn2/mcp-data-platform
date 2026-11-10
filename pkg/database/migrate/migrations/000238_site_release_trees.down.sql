DELETE FROM site_release_files WHERE tree <> 'output';
ALTER TABLE site_releases DROP COLUMN IF EXISTS rendered;
ALTER TABLE site_release_files DROP CONSTRAINT IF EXISTS site_release_files_pkey;
ALTER TABLE site_release_files ADD PRIMARY KEY (release_id, path);
ALTER TABLE site_release_files DROP COLUMN IF EXISTS tree;

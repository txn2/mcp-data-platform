DROP TABLE IF EXISTS site_retention_sweeps;
ALTER TABLE site_releases DROP COLUMN IF EXISTS pruned_at;
ALTER TABLE site_environments DROP COLUMN IF EXISTS keep_releases;

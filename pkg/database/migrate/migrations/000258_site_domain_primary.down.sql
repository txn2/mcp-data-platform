DROP INDEX IF EXISTS site_domains_one_primary;
ALTER TABLE site_domains DROP CONSTRAINT IF EXISTS site_domains_mode_check;
ALTER TABLE site_domains DROP COLUMN IF EXISTS mode;
ALTER TABLE site_domains DROP COLUMN IF EXISTS is_primary;

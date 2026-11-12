-- 000243: a release's _headers file (#2557).
--
-- Like _redirects, the file is read into the release rather than served: the
-- headers each path carries, from an allowed set of policies. It travels with
-- the release, so a rollback restores the headers that release declared.
ALTER TABLE site_releases ADD COLUMN IF NOT EXISTS headers JSONB NOT NULL DEFAULT '[]';

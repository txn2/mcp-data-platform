DROP TABLE IF EXISTS site_jobs;

CREATE TABLE IF NOT EXISTS site_certificates (
    key        TEXT        NOT NULL PRIMARY KEY,
    data       TEXT        NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE site_domains ADD COLUMN IF NOT EXISTS token TEXT NOT NULL DEFAULT '';
ALTER TABLE site_domains ADD COLUMN IF NOT EXISTS verified_at TIMESTAMPTZ;
ALTER TABLE site_domains ADD COLUMN IF NOT EXISTS verified_by TEXT NOT NULL DEFAULT '';
UPDATE site_domains SET verified_at = created_at, verified_by = created_by;

DELETE FROM site_environments WHERE name = 'default';

ALTER TABLE sites DROP COLUMN IF EXISTS internal_view;

DROP TABLE IF EXISTS site_grants;

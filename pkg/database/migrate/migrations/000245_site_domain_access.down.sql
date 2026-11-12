ALTER TABLE site_environments ADD COLUMN IF NOT EXISTS public BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE site_environments ADD COLUMN IF NOT EXISTS access_changed_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
UPDATE site_environments e SET public = TRUE
 WHERE e.name <> 'dev'
   AND EXISTS (SELECT 1 FROM site_domains d WHERE d.site_id = e.site_id AND d.environment = e.name AND d.public);
ALTER TABLE site_environments DROP CONSTRAINT IF EXISTS site_environments_dev_private;
ALTER TABLE site_environments ADD CONSTRAINT site_environments_dev_private CHECK (name <> 'dev' OR NOT public);
ALTER TABLE site_domains DROP COLUMN IF EXISTS access_changed_at;
ALTER TABLE site_domains DROP COLUMN IF EXISTS public;

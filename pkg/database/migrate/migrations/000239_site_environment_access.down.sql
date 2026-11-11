-- 000239 down: one access per site again, and 'dev' back to 'default'.
DROP INDEX IF EXISTS idx_approval_requests_one_pending_site_env;
DELETE FROM approval_requests WHERE subject_kind = 'site_environment';
ALTER TABLE approval_requests DROP CONSTRAINT IF EXISTS approval_requests_version_check;
ALTER TABLE approval_requests ADD CONSTRAINT approval_requests_version_check CHECK (version > 0);
ALTER TABLE approval_requests DROP CONSTRAINT IF EXISTS approval_requests_subject_present;
ALTER TABLE approval_requests ADD CONSTRAINT approval_requests_subject_present
    CHECK ((subject_kind = 'script_version' AND script_id IS NOT NULL)
        OR (subject_kind = 'site_release' AND site_release_id IS NOT NULL));
ALTER TABLE approval_requests DROP CONSTRAINT IF EXISTS approval_requests_purpose_check;
ALTER TABLE approval_requests ADD CONSTRAINT approval_requests_purpose_check
    CHECK (purpose IN ('form_public', 'site_promote'));
ALTER TABLE approval_requests DROP CONSTRAINT IF EXISTS approval_requests_subject_kind_check;
ALTER TABLE approval_requests ADD CONSTRAINT approval_requests_subject_kind_check
    CHECK (subject_kind IN ('script_version', 'site_release'));
ALTER TABLE approval_requests DROP COLUMN IF EXISTS site_id;

DELETE FROM site_grants WHERE persona <> '';
ALTER TABLE site_grants DROP CONSTRAINT IF EXISTS site_grants_one_principal;
ALTER TABLE site_grants DROP CONSTRAINT IF EXISTS site_grants_pkey;
ALTER TABLE site_grants ADD PRIMARY KEY (site_id, email);
ALTER TABLE site_grants DROP COLUMN IF EXISTS environments;
ALTER TABLE site_grants DROP COLUMN IF EXISTS persona;

ALTER TABLE sites ADD COLUMN IF NOT EXISTS internal_view BOOLEAN NOT NULL DEFAULT FALSE;
UPDATE sites s SET internal_view = TRUE
  FROM site_environments e WHERE e.site_id = s.id AND e.name = 'dev' AND e.internal_view;
ALTER TABLE sites DROP COLUMN IF EXISTS main_environment;

ALTER TABLE site_environments DROP CONSTRAINT IF EXISTS site_environments_dev_private;
ALTER TABLE site_environments DROP COLUMN IF EXISTS access_changed_at;
ALTER TABLE site_environments DROP COLUMN IF EXISTS internal_view;
ALTER TABLE site_environments DROP COLUMN IF EXISTS public;

-- 'dev' is 'default' again where no other environment took that name.
UPDATE site_deployments d SET environment = 'default'
 WHERE d.environment = 'dev' AND NOT EXISTS (SELECT 1 FROM site_environments x WHERE x.site_id = d.site_id AND x.name = 'default');
UPDATE site_domains d SET environment = 'default'
 WHERE d.environment = 'dev' AND NOT EXISTS (SELECT 1 FROM site_environments x WHERE x.site_id = d.site_id AND x.name = 'default');
UPDATE site_jobs j SET environment = 'default'
 WHERE j.environment = 'dev' AND NOT EXISTS (SELECT 1 FROM site_environments x WHERE x.site_id = j.site_id AND x.name = 'default');
UPDATE application_parts p SET part_id = split_part(p.part_id, '/', 1) || '/default'
 WHERE p.kind = 'site' AND split_part(p.part_id, '/', 2) = 'dev';
UPDATE application_release_pins p SET part_id = split_part(p.part_id, '/', 1) || '/default'
 WHERE p.kind = 'site' AND split_part(p.part_id, '/', 2) = 'dev';
UPDATE site_environments e SET name = 'default'
 WHERE e.name = 'dev' AND NOT EXISTS (SELECT 1 FROM site_environments x WHERE x.site_id = e.site_id AND x.name = 'default');

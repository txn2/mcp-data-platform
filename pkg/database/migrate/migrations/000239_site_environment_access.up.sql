-- 000239: agents work in dev, people use what was promoted (#2604).
--
-- The writable environment is renamed from 'default' to 'dev'. Every
-- environment is private or public on its own, answers at
-- <site>--<env>.<pages domain>, and one of them (or none) holds the site's
-- main address, <site>.<pages domain>. A view grant names a person or a
-- persona and the environments it opens.

-- Some sites still hold an environment named 'dev' from before 'default'
-- existed (000236 made 'default' beside it). It moves out of the way first:
-- to 'development', or to 'dev-previous' when that is taken too.
CREATE TEMP TABLE site_env_moves (site_id TEXT NOT NULL, site_name TEXT NOT NULL, old_name TEXT NOT NULL, new_name TEXT NOT NULL) ON COMMIT DROP;

INSERT INTO site_env_moves (site_id, site_name, old_name, new_name)
SELECT s.id, s.name, 'dev',
       CASE WHEN EXISTS (SELECT 1 FROM site_environments x WHERE x.site_id = s.id AND x.name = 'development')
            THEN 'dev-previous' ELSE 'development' END
  FROM sites s
 WHERE EXISTS (SELECT 1 FROM site_environments d WHERE d.site_id = s.id AND d.name = 'dev')
   AND EXISTS (SELECT 1 FROM site_environments d WHERE d.site_id = s.id AND d.name = 'default');

-- Then 'default' becomes 'dev' everywhere.
INSERT INTO site_env_moves (site_id, site_name, old_name, new_name)
SELECT s.id, s.name, 'default', 'dev'
  FROM sites s
 WHERE EXISTS (SELECT 1 FROM site_environments d WHERE d.site_id = s.id AND d.name = 'default');

-- Applied in two passes so the old 'dev' is gone before 'default' takes its
-- name.
DO $$
DECLARE pass TEXT;
BEGIN
    FOREACH pass IN ARRAY ARRAY['dev', 'default'] LOOP
        UPDATE site_environments e SET name = m.new_name
          FROM site_env_moves m WHERE m.old_name = pass AND e.site_id = m.site_id AND e.name = m.old_name;
        UPDATE site_deployments d SET environment = m.new_name
          FROM site_env_moves m WHERE m.old_name = pass AND d.site_id = m.site_id AND d.environment = m.old_name;
        UPDATE site_domains d SET environment = m.new_name
          FROM site_env_moves m WHERE m.old_name = pass AND d.site_id = m.site_id AND d.environment = m.old_name;
        UPDATE site_jobs j SET environment = m.new_name
          FROM site_env_moves m WHERE m.old_name = pass AND j.site_id = m.site_id AND j.environment = m.old_name;
        UPDATE application_parts p SET part_id = m.site_name || '/' || m.new_name
          FROM site_env_moves m WHERE m.old_name = pass AND p.kind = 'site' AND p.part_id = m.site_name || '/' || m.old_name;
        UPDATE application_release_pins p SET part_id = m.site_name || '/' || m.new_name
          FROM site_env_moves m WHERE m.old_name = pass AND p.kind = 'site' AND p.part_id = m.site_name || '/' || m.old_name;
        UPDATE approval_requests r SET target = m.new_name
          FROM site_env_moves m, site_releases sr
         WHERE m.old_name = pass AND r.site_release_id = sr.id AND sr.site_id = m.site_id AND r.target = m.old_name;
    END LOOP;
END $$;

-- Each environment's access. dev is never public.
ALTER TABLE site_environments ADD COLUMN IF NOT EXISTS public BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE site_environments ADD COLUMN IF NOT EXISTS internal_view BOOLEAN NOT NULL DEFAULT FALSE;
-- When public last changed: an approval to make an environment public is for
-- the access it was asked against, not for every later change.
ALTER TABLE site_environments ADD COLUMN IF NOT EXISTS access_changed_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE site_environments DROP CONSTRAINT IF EXISTS site_environments_dev_private;
ALTER TABLE site_environments ADD CONSTRAINT site_environments_dev_private CHECK (name <> 'dev' OR NOT public);

-- Every environment that answers on a domain today keeps answering to anyone.
UPDATE site_environments e SET public = TRUE
 WHERE e.name <> 'dev'
   AND EXISTS (SELECT 1 FROM site_domains d WHERE d.site_id = e.site_id AND d.environment = e.name);

-- "Everyone internal can view" opened the site's own address, which served
-- the environment now named dev.
UPDATE site_environments e SET internal_view = TRUE
  FROM sites s WHERE s.id = e.site_id AND s.internal_view AND e.name = 'dev';
ALTER TABLE sites DROP COLUMN IF EXISTS internal_view;

-- The environment the main address serves; NULL is none. Every site keeps its
-- address where it was: on dev.
ALTER TABLE sites ADD COLUMN IF NOT EXISTS main_environment TEXT;
UPDATE sites s SET main_environment = 'dev'
 WHERE EXISTS (SELECT 1 FROM site_environments e WHERE e.site_id = s.id AND e.name = 'dev');

-- A grant names a person by email or a persona by name, and the environments a
-- view grant opens; none listed is every private environment, which is what a
-- grant opened before.
ALTER TABLE site_grants ADD COLUMN IF NOT EXISTS persona TEXT NOT NULL DEFAULT '';
ALTER TABLE site_grants ADD COLUMN IF NOT EXISTS environments TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE site_grants DROP CONSTRAINT IF EXISTS site_grants_pkey;
ALTER TABLE site_grants ADD PRIMARY KEY (site_id, email, persona);
ALTER TABLE site_grants DROP CONSTRAINT IF EXISTS site_grants_one_principal;
ALTER TABLE site_grants ADD CONSTRAINT site_grants_one_principal
    CHECK ((email <> '' AND persona = '') OR (email = '' AND persona <> '' AND level = 'view'));

-- Making an environment public, where it requires approval, is a request in
-- the Approvals queue about the environment rather than a release.
ALTER TABLE approval_requests ADD COLUMN IF NOT EXISTS site_id TEXT REFERENCES sites(id) ON DELETE CASCADE;
ALTER TABLE approval_requests DROP CONSTRAINT IF EXISTS approval_requests_subject_kind_check;
ALTER TABLE approval_requests ADD CONSTRAINT approval_requests_subject_kind_check
    CHECK (subject_kind IN ('script_version', 'site_release', 'site_environment'));
ALTER TABLE approval_requests DROP CONSTRAINT IF EXISTS approval_requests_purpose_check;
ALTER TABLE approval_requests ADD CONSTRAINT approval_requests_purpose_check
    CHECK (purpose IN ('form_public', 'site_promote', 'site_public'));
ALTER TABLE approval_requests DROP CONSTRAINT IF EXISTS approval_requests_subject_present;
ALTER TABLE approval_requests ADD CONSTRAINT approval_requests_subject_present
    CHECK ((subject_kind = 'script_version' AND script_id IS NOT NULL)
        OR (subject_kind = 'site_release' AND site_release_id IS NOT NULL)
        OR (subject_kind = 'site_environment' AND site_id IS NOT NULL AND target <> ''));
-- Such a request names no version: the access it was asked against is in
-- source_sha256.
ALTER TABLE approval_requests DROP CONSTRAINT IF EXISTS approval_requests_version_check;
ALTER TABLE approval_requests ADD CONSTRAINT approval_requests_version_check
    CHECK (version > 0 OR subject_kind = 'site_environment');
CREATE UNIQUE INDEX IF NOT EXISTS idx_approval_requests_one_pending_site_env
    ON approval_requests (site_id, target) WHERE status = 'pending' AND subject_kind = 'site_environment';

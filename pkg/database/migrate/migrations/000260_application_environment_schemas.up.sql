-- 000260: each application environment its own schema (#2709).
--
-- An environment runs against a schema of its own on the application's
-- connection, so a dev run, a dev site and an agent acting in dev never read
-- or write prod's rows. Every environment that exists now keeps using the
-- application's schema, so nothing moves on upgrade; an environment added
-- from now on is given a schema of its own, built from the structure of the
-- last environment's.
--
-- An application keeps one run role per schema it runs in: the run role of
-- an environment's schema is the login role that environment's statements
-- run as, granted only on that schema.
ALTER TABLE application_environments ADD COLUMN IF NOT EXISTS schema_name TEXT NOT NULL DEFAULT '';

UPDATE application_environments e SET schema_name = a.schema_name
  FROM applications a
 WHERE a.id = e.application_id AND e.schema_name = '' AND a.schema_name <> '';

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'application_run_roles_pkey'
                AND conrelid = 'application_run_roles'::regclass
                AND array_length(conkey, 1) = 1) THEN
        ALTER TABLE application_run_roles DROP CONSTRAINT application_run_roles_pkey;
        ALTER TABLE application_run_roles ADD PRIMARY KEY (application_id, schema_name);
    END IF;
END $$;

-- A site part's pages call the application environment of the part's own
-- name from now on (#2709). Until now they called the last environment, so
-- each site keeps answering as it did:
--
-- 1. A site part whose environment the application lacks (a dev site on an
--    application with only prod) is given it, ahead of the others, running
--    the last environment's release in the application's schema. Added
--    environments take positions below the existing ones and every
--    application's positions are then numbered again from 0, so the first
--    environment is at 0 as add_environment expects.
-- 2. An environment a site part calls that runs no release, ahead of a last
--    environment that runs one, is given the last environment's release.
INSERT INTO application_environments (application_id, name, position, release, schema_name, updated_by, updated_at)
SELECT m.application_id, m.name,
       -1000000 - ROW_NUMBER() OVER (PARTITION BY m.application_id ORDER BY m.name DESC),
       l.release, l.schema_name, l.updated_by, NOW()
  FROM (SELECT DISTINCT application_id, split_part(part_id, '/', 2) AS name
          FROM application_parts WHERE kind = 'site') m
  JOIN (SELECT DISTINCT ON (application_id) application_id, release, schema_name, updated_by
          FROM application_environments
         ORDER BY application_id, position DESC) l ON l.application_id = m.application_id
 WHERE m.name ~ '^[a-z][a-z0-9-]{0,31}$'
   AND NOT EXISTS (SELECT 1 FROM application_environments e
                    WHERE e.application_id = m.application_id AND e.name = m.name)
ON CONFLICT (application_id, name) DO NOTHING;

UPDATE application_environments e SET position = r.n - 1
  FROM (SELECT application_id, name, ROW_NUMBER() OVER (PARTITION BY application_id ORDER BY position) AS n
          FROM application_environments) r
 WHERE e.application_id = r.application_id AND e.name = r.name AND e.position <> r.n - 1;

UPDATE application_environments e SET release = l.release, updated_at = NOW()
  FROM (SELECT DISTINCT ON (application_id) application_id, name, release
          FROM application_environments
         ORDER BY application_id, position DESC) l
 WHERE e.application_id = l.application_id AND e.name <> l.name
   AND e.release = 0 AND l.release > 0
   AND EXISTS (SELECT 1 FROM application_parts p
                WHERE p.application_id = e.application_id AND p.kind = 'site'
                  AND split_part(p.part_id, '/', 2) = e.name);

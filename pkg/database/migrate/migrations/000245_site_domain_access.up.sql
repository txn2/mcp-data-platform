-- 000245: only an added domain can be public (#2619).
--
-- Every address on the pages domain (a site's main address and each
-- <site>--<env>) answers only to someone signed in. Public is a setting of a
-- domain: added private unless asked for, and changed later on its own.

ALTER TABLE site_domains ADD COLUMN IF NOT EXISTS public BOOLEAN NOT NULL DEFAULT FALSE;
-- When public last changed: an approval to make a domain public is for the
-- access it was asked against.
ALTER TABLE site_domains ADD COLUMN IF NOT EXISTS access_changed_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

-- An environment public until now keeps answering to anyone at each of its
-- domains. Its pages addresses ask for a sign-in from here on; one with no
-- domain stops answering anonymously, which the platform warned of at startup
-- before this ran.
UPDATE site_domains d SET public = TRUE
  FROM site_environments e
 WHERE e.site_id = d.site_id AND e.name = d.environment AND e.public;

-- A request still waiting to make an environment public asks for what no
-- longer exists: it is withdrawn, and the owner asks again for a domain.
UPDATE approval_requests
   SET status = 'withdrawn', decided_by = 'platform', decided_at = NOW(),
       decision_comment = 'Public is now a setting of each domain: ask again to make a domain public.'
 WHERE subject_kind = 'site_environment' AND status = 'pending';

ALTER TABLE site_environments DROP CONSTRAINT IF EXISTS site_environments_dev_private;
ALTER TABLE site_environments DROP COLUMN IF EXISTS public;
ALTER TABLE site_environments DROP COLUMN IF EXISTS access_changed_at;

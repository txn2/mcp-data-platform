-- 000118: a saved script runs; approval and grants come out (#1403)
--
-- The approved-version pointer, the approval stamp, and the per-version
-- capability grant are removed. A run now executes the script's latest saved
-- version, presents the roles captured from the author at that save
-- (script_versions.author_roles, which the grant's roles were always copied
-- from), and resolves connections and destinations at run time through the
-- same persona filtering an interactive session gets. Bucket destinations are
-- declared in configuration (scripts.destinations) rather than pinned to a
-- version.
--
-- A pending draft version -- a proposal that was waiting for a review that no
-- longer exists -- becomes 'superseded': the live row kept serving without it,
-- and nothing may surface a queue of them. 'rejected' rows are left as they
-- are; they record a decision somebody made, and history stays true.
UPDATE script_versions SET status = 'superseded' WHERE status = 'draft';

-- A script in 'draft' status was one with no approved version, which is no
-- longer a state: a saved script runs. Every such script becomes active.
UPDATE scripts SET status = 'active' WHERE status = 'draft';
ALTER TABLE scripts ALTER COLUMN status SET DEFAULT 'active';
ALTER TABLE scripts DROP CONSTRAINT IF EXISTS scripts_status_check;
ALTER TABLE scripts
    ADD CONSTRAINT scripts_status_check
    CHECK (status IN ('active', 'deprecated', 'superseded'));

ALTER TABLE scripts DROP CONSTRAINT IF EXISTS scripts_approved_version_fk;
ALTER TABLE scripts DROP COLUMN IF EXISTS approved_version_id;

ALTER TABLE script_versions DROP COLUMN IF EXISTS approved_by;
ALTER TABLE script_versions DROP COLUMN IF EXISTS approved_at;
ALTER TABLE script_versions DROP COLUMN IF EXISTS auto_approved;
ALTER TABLE script_versions DROP COLUMN IF EXISTS grants;

-- 000249: what an environment answers besides its release, and the operations
-- that change it (#2648).
--
-- status (#2631, #2632): online serves the release; offline answers 503 with
-- a message on every address; redirect answers 302 to another https site.
-- dev is always online. The release pointer is untouched by either.
ALTER TABLE site_environments ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'online';
ALTER TABLE site_environments ADD COLUMN IF NOT EXISTS status_message TEXT NOT NULL DEFAULT '';
ALTER TABLE site_environments ADD COLUMN IF NOT EXISTS back_at TIMESTAMPTZ;
ALTER TABLE site_environments ADD COLUMN IF NOT EXISTS redirect_to TEXT NOT NULL DEFAULT '';
ALTER TABLE site_environments ADD COLUMN IF NOT EXISTS redirect_keep_path BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE site_environments ADD COLUMN IF NOT EXISTS status_changed_by TEXT NOT NULL DEFAULT '';
ALTER TABLE site_environments ADD COLUMN IF NOT EXISTS status_changed_at TIMESTAMPTZ;
ALTER TABLE site_environments DROP CONSTRAINT IF EXISTS site_environments_status_check;
ALTER TABLE site_environments ADD CONSTRAINT site_environments_status_check
    CHECK (status IN ('online', 'offline', 'redirect'));
ALTER TABLE site_environments DROP CONSTRAINT IF EXISTS site_environments_dev_online;
ALTER TABLE site_environments ADD CONSTRAINT site_environments_dev_online CHECK (name <> 'dev' OR status = 'online');
ALTER TABLE site_environments DROP CONSTRAINT IF EXISTS site_environments_redirect_target;
ALTER TABLE site_environments ADD CONSTRAINT site_environments_redirect_target
    CHECK ((status = 'redirect') = (redirect_to <> ''));

-- agents_may_promote (#2643): an agent's promote, rollback or public domain
-- here goes ahead without a person's decision. Off by default; only an
-- administrator turns it on, in the portal.
ALTER TABLE site_environments ADD COLUMN IF NOT EXISTS agents_may_promote BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE site_environments DROP CONSTRAINT IF EXISTS site_environments_dev_agents;
ALTER TABLE site_environments ADD CONSTRAINT site_environments_dev_agents CHECK (name <> 'dev' OR NOT agents_may_promote);

-- History: a status change is a move of its own, with what it said (the
-- message or the target) in detail; decided_by says whose decision moved an
-- environment an agent asked to move (#2643: elicitation, portal, or the
-- environment's setting); removed_at marks the moves of an environment that
-- was removed (#2630), which stop counting toward its retention and are not
-- the history of a later environment of the same name.
ALTER TABLE site_deployments ADD COLUMN IF NOT EXISTS detail TEXT NOT NULL DEFAULT '';
ALTER TABLE site_deployments ADD COLUMN IF NOT EXISTS decided_by TEXT NOT NULL DEFAULT '';
ALTER TABLE site_deployments ADD COLUMN IF NOT EXISTS removed_at TIMESTAMPTZ;
ALTER TABLE site_deployments DROP CONSTRAINT IF EXISTS site_deployments_action_check;
ALTER TABLE site_deployments ADD CONSTRAINT site_deployments_action_check
    CHECK (action IN ('publish', 'promote', 'rollback', 'application', 'offline', 'online', 'redirect', 'rename'));
CREATE INDEX IF NOT EXISTS idx_site_deployments_live ON site_deployments (site_id, environment, id DESC) WHERE removed_at IS NULL;

-- A redirect on an environment that requires approval waits in Approvals
-- (#2632), like making one of its domains public: subject site_environment,
-- target the environment, source_sha256 the redirect asked for.
ALTER TABLE approval_requests DROP CONSTRAINT IF EXISTS approval_requests_purpose_check;
ALTER TABLE approval_requests ADD CONSTRAINT approval_requests_purpose_check
    CHECK (purpose IN ('form_public', 'site_promote', 'site_public', 'site_redirect', 'call_handler', 'call_allowance'));

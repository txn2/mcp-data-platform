DELETE FROM approval_requests WHERE purpose = 'site_redirect';
ALTER TABLE approval_requests DROP CONSTRAINT IF EXISTS approval_requests_purpose_check;
ALTER TABLE approval_requests ADD CONSTRAINT approval_requests_purpose_check
    CHECK (purpose IN ('form_public', 'site_promote', 'site_public', 'call_handler', 'call_allowance'));

DROP INDEX IF EXISTS idx_site_deployments_live;
DELETE FROM site_deployments WHERE action IN ('offline', 'online', 'redirect', 'rename') OR removed_at IS NOT NULL;
ALTER TABLE site_deployments DROP CONSTRAINT IF EXISTS site_deployments_action_check;
ALTER TABLE site_deployments ADD CONSTRAINT site_deployments_action_check
    CHECK (action IN ('publish', 'promote', 'rollback', 'application'));
ALTER TABLE site_deployments DROP COLUMN IF EXISTS removed_at;
ALTER TABLE site_deployments DROP COLUMN IF EXISTS decided_by;
ALTER TABLE site_deployments DROP COLUMN IF EXISTS detail;

ALTER TABLE site_environments DROP CONSTRAINT IF EXISTS site_environments_dev_agents;
ALTER TABLE site_environments DROP COLUMN IF EXISTS agents_may_promote;
ALTER TABLE site_environments DROP CONSTRAINT IF EXISTS site_environments_redirect_target;
ALTER TABLE site_environments DROP CONSTRAINT IF EXISTS site_environments_dev_online;
ALTER TABLE site_environments DROP CONSTRAINT IF EXISTS site_environments_status_check;
ALTER TABLE site_environments DROP COLUMN IF EXISTS status_changed_at;
ALTER TABLE site_environments DROP COLUMN IF EXISTS status_changed_by;
ALTER TABLE site_environments DROP COLUMN IF EXISTS redirect_keep_path;
ALTER TABLE site_environments DROP COLUMN IF EXISTS redirect_to;
ALTER TABLE site_environments DROP COLUMN IF EXISTS back_at;
ALTER TABLE site_environments DROP COLUMN IF EXISTS status_message;
ALTER TABLE site_environments DROP COLUMN IF EXISTS status;

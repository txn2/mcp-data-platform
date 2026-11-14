CREATE OR REPLACE VIEW script_handlers AS
SELECT e.id, 'endpoint'::text AS kind,
       CASE WHEN s.is_form THEN 'form' ELSE 'endpoint' END AS type,
       e.script_id, e.name AS target, e.enabled, e.created_by, e.created_at
  FROM script_endpoints e JOIN scripts s ON s.id = e.script_id
UNION ALL
SELECT c.id, 'schedule'::text, 'schedule'::text,
       c.script_id, c.cron_spec || ' ' || c.timezone, c.enabled, c.created_by, c.created_at
  FROM script_schedules c
UNION ALL
SELECT w.id, 'webhook'::text, 'webhook'::text,
       w.script_id, w.source, w.enabled, w.created_by, w.created_at
  FROM script_webhook_handlers w;

DROP INDEX IF EXISTS idx_approval_requests_allowance;
DELETE FROM approval_requests WHERE subject_kind = 'call' OR purpose IN ('call_handler', 'call_allowance');
ALTER TABLE approval_requests DROP CONSTRAINT IF EXISTS approval_requests_calls_used_check;
ALTER TABLE approval_requests DROP CONSTRAINT IF EXISTS approval_requests_version_check;
ALTER TABLE approval_requests ADD CONSTRAINT approval_requests_version_check
    CHECK (version > 0 OR subject_kind = 'site_environment');
ALTER TABLE approval_requests DROP CONSTRAINT IF EXISTS approval_requests_subject_present;
ALTER TABLE approval_requests ADD CONSTRAINT approval_requests_subject_present
    CHECK ((subject_kind = 'script_version' AND script_id IS NOT NULL)
        OR (subject_kind = 'site_release' AND site_release_id IS NOT NULL)
        OR (subject_kind = 'site_environment' AND site_id IS NOT NULL AND target <> ''));
ALTER TABLE approval_requests DROP CONSTRAINT IF EXISTS approval_requests_purpose_check;
ALTER TABLE approval_requests ADD CONSTRAINT approval_requests_purpose_check
    CHECK (purpose IN ('form_public', 'site_promote', 'site_public'));
ALTER TABLE approval_requests DROP CONSTRAINT IF EXISTS approval_requests_subject_kind_check;
ALTER TABLE approval_requests ADD CONSTRAINT approval_requests_subject_kind_check
    CHECK (subject_kind IN ('script_version', 'site_release', 'site_environment'));
ALTER TABLE approval_requests DROP COLUMN IF EXISTS allowance_until;
ALTER TABLE approval_requests DROP COLUMN IF EXISTS calls_used;
ALTER TABLE approval_requests DROP COLUMN IF EXISTS calls_approved;
ALTER TABLE approval_requests DROP COLUMN IF EXISTS calls_asked;
ALTER TABLE approval_requests DROP COLUMN IF EXISTS approvers;
ALTER TABLE approval_requests DROP COLUMN IF EXISTS call_handlers;
ALTER TABLE approval_requests DROP COLUMN IF EXISTS call_rendering;
ALTER TABLE approval_requests DROP COLUMN IF EXISTS call_arguments;
ALTER TABLE approval_requests DROP COLUMN IF EXISTS call_operation;
ALTER TABLE approval_requests DROP COLUMN IF EXISTS call_connection;
ALTER TABLE approval_requests DROP COLUMN IF EXISTS call_tool;
ALTER TABLE approval_requests DROP COLUMN IF EXISTS decided_roles;

DROP INDEX IF EXISTS idx_script_runs_watch_backlog;
DELETE FROM script_runs WHERE trigger_kind = 'watch';
ALTER TABLE script_runs DROP CONSTRAINT IF EXISTS script_runs_trigger_kind_check;
ALTER TABLE script_runs ADD CONSTRAINT script_runs_trigger_kind_check
    CHECK (trigger_kind IN ('tool', 'schedule', 'portal', 'event'));
ALTER TABLE script_runs DROP COLUMN IF EXISTS call_handler_id;
ALTER TABLE script_runs DROP COLUMN IF EXISTS run_as;
ALTER TABLE script_runs DROP COLUMN IF EXISTS call;

ALTER TABLE audit_logs DROP COLUMN IF EXISTS handlers;

DROP TABLE IF EXISTS call_handler_failures;
DROP TABLE IF EXISTS call_handler_counts;
DROP TRIGGER IF EXISTS trg_call_handlers_changed ON call_handlers;
DROP FUNCTION IF EXISTS call_handlers_changed();
DROP TABLE IF EXISTS call_handlers;

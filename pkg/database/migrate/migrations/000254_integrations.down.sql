-- Reverses 000254: the script_handlers view without integrations, the
-- change triggers, the tables, and the integration run trigger.
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
  FROM script_webhook_handlers w
UNION ALL
SELECT h.id, 'policy'::text, 'policy'::text,
       h.script_id, h.connection || ' ' || array_to_string(h.operations, ', '), h.enabled, h.created_by, h.created_at
  FROM call_handlers h WHERE h.kind = 'policy'
UNION ALL
SELECT h.id, 'transform'::text, 'transform'::text,
       h.script_id, h.connection || ' ' || array_to_string(h.operations, ', '), h.enabled, h.created_by, h.created_at
  FROM call_handlers h WHERE h.kind = 'transform'
UNION ALL
SELECT h.id, 'watch'::text, 'watch'::text,
       h.script_id, h.connection || ' ' || array_to_string(h.operations, ', '), h.enabled, h.created_by, h.created_at
  FROM call_handlers h WHERE h.kind = 'watch';

DROP TRIGGER IF EXISTS script_runs_integration_finished ON script_runs;
DROP FUNCTION IF EXISTS integration_run_finished();
DROP TRIGGER IF EXISTS platform_settings_integration_changed ON platform_settings;
DROP FUNCTION IF EXISTS integration_settings_changed();
DROP TRIGGER IF EXISTS applications_integration_changed ON applications;
DROP TRIGGER IF EXISTS site_domains_integration_changed ON site_domains;
DROP TRIGGER IF EXISTS site_environments_integration_changed ON site_environments;
DROP TRIGGER IF EXISTS sites_integration_changed ON sites;
DROP FUNCTION IF EXISTS integration_items_changed();

DELETE FROM script_runs WHERE trigger_kind = 'integration';
ALTER TABLE script_runs DROP CONSTRAINT IF EXISTS script_runs_trigger_kind_check;
ALTER TABLE script_runs ADD CONSTRAINT script_runs_trigger_kind_check
    CHECK (trigger_kind IN ('tool', 'schedule', 'portal', 'event', 'watch'));

DROP TABLE IF EXISTS integration_items;
DROP TABLE IF EXISTS integration_bindings;

-- #2659: integrations. An operation the platform cannot know how to do in a
-- deployment's environment (routing a hostname through its ingress) is handed
-- to a script an administrator chooses. One script per operation, called when
-- the operation's items change and on a timer, one run at a time.

-- The script chosen for each operation. current_run_id is the run in flight,
-- NULL when none; retry_requested asks the next pass to call the script with
-- every item at once.
CREATE TABLE IF NOT EXISTS integration_bindings (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    operation        TEXT        NOT NULL UNIQUE,
    script_id        UUID        NOT NULL REFERENCES scripts(id) ON DELETE CASCADE,
    interval_seconds INT         NOT NULL DEFAULT 900 CHECK (interval_seconds BETWEEN 60 AND 86400),
    current_run_id   TEXT,
    retry_requested  BOOLEAN     NOT NULL DEFAULT FALSE,
    last_started_at  TIMESTAMPTZ,
    last_run_id      TEXT        NOT NULL DEFAULT '',
    last_status      TEXT        NOT NULL DEFAULT '',
    last_reason      TEXT        NOT NULL DEFAULT '',
    last_error       TEXT        NOT NULL DEFAULT '',
    last_finished_at TIMESTAMPTZ,
    created_by       TEXT        NOT NULL,
    updated_by       TEXT        NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Each item the script was last handed, with the status it last returned.
-- removing marks an item gone from the platform whose removal is in a run
-- still in flight; the row is deleted once that run finishes. failed_run is
-- the run that failed when the script itself failed (raised, timed out,
-- returned a malformed result), whose error only an administrator reads.
CREATE TABLE IF NOT EXISTS integration_items (
    operation  TEXT        NOT NULL,
    item_key   TEXT        NOT NULL,
    attrs      JSONB       NOT NULL DEFAULT '{}'::jsonb,
    status     TEXT        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'ready', 'failed')),
    message    TEXT        NOT NULL DEFAULT '',
    removing   BOOLEAN     NOT NULL DEFAULT FALSE,
    failed_run TEXT        NOT NULL DEFAULT '',
    checked_at TIMESTAMPTZ,
    PRIMARY KEY (operation, item_key)
);

-- An integration's run is a queued run of the chosen script, carrying its
-- operation and request in call, run as the administrator who approved the
-- version.
ALTER TABLE script_runs DROP CONSTRAINT IF EXISTS script_runs_trigger_kind_check;
ALTER TABLE script_runs ADD CONSTRAINT script_runs_trigger_kind_check
    CHECK (trigger_kind IN ('tool', 'schedule', 'portal', 'event', 'watch', 'integration'));

-- Every change that can move an operation's items, and the end of an
-- integration's run, wakes the integration loop on every replica; the one
-- holding the operation's lock computes what changed. No call site describes
-- its own change.
CREATE OR REPLACE FUNCTION integration_items_changed() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('integration_changed', TG_TABLE_NAME);
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS sites_integration_changed ON sites;
CREATE TRIGGER sites_integration_changed AFTER INSERT OR UPDATE OR DELETE ON sites
    FOR EACH STATEMENT EXECUTE FUNCTION integration_items_changed();
DROP TRIGGER IF EXISTS site_environments_integration_changed ON site_environments;
CREATE TRIGGER site_environments_integration_changed AFTER INSERT OR UPDATE OR DELETE ON site_environments
    FOR EACH STATEMENT EXECUTE FUNCTION integration_items_changed();
DROP TRIGGER IF EXISTS site_domains_integration_changed ON site_domains;
CREATE TRIGGER site_domains_integration_changed AFTER INSERT OR UPDATE OR DELETE ON site_domains
    FOR EACH STATEMENT EXECUTE FUNCTION integration_items_changed();
DROP TRIGGER IF EXISTS applications_integration_changed ON applications;
CREATE TRIGGER applications_integration_changed AFTER INSERT OR UPDATE OR DELETE ON applications
    FOR EACH STATEMENT EXECUTE FUNCTION integration_items_changed();

CREATE OR REPLACE FUNCTION integration_settings_changed() RETURNS trigger AS $$
BEGIN
    IF COALESCE(NEW.section, OLD.section) = 'sites' THEN
        PERFORM pg_notify('integration_changed', 'platform_settings');
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS platform_settings_integration_changed ON platform_settings;
CREATE TRIGGER platform_settings_integration_changed AFTER INSERT OR UPDATE OR DELETE ON platform_settings
    FOR EACH ROW EXECUTE FUNCTION integration_settings_changed();

CREATE OR REPLACE FUNCTION integration_run_finished() RETURNS trigger AS $$
BEGIN
    IF NEW.trigger_kind = 'integration' AND NEW.finished_at IS NOT NULL AND OLD.finished_at IS NULL THEN
        PERFORM pg_notify('integration_changed', 'script_runs');
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS script_runs_integration_finished ON script_runs;
CREATE TRIGGER script_runs_integration_finished AFTER UPDATE OF finished_at ON script_runs
    FOR EACH ROW EXECUTE FUNCTION integration_run_finished();

-- The script's page lists the operation among its handlers.
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
  FROM call_handlers h WHERE h.kind = 'watch'
UNION ALL
SELECT b.id, 'integration'::text, 'integration'::text,
       b.script_id, b.operation, TRUE, b.created_by, b.created_at
  FROM integration_bindings b;

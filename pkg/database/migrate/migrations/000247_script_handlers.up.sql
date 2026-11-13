-- 000247: script handlers (#2441, #2434, #2435).
--
-- A handler is what starts a script: an endpoint, a schedule or a webhook
-- source. Each kind keeps its own table, and a script may hold several
-- handlers of each kind. script_endpoints and script_schedules were already
-- one row per handler, with their own id, enabled and created_by, held to one
-- per script by UNIQUE(script_id); only that goes. The webhook source a script
-- runs for moves off scripts.event_source into a table of its own, one row
-- per (script, source).

ALTER TABLE script_schedules DROP CONSTRAINT IF EXISTS script_schedules_script_id_key;
CREATE INDEX IF NOT EXISTS idx_script_schedules_script ON script_schedules(script_id);

ALTER TABLE script_endpoints DROP CONSTRAINT IF EXISTS script_endpoints_script_id_key;
CREATE INDEX IF NOT EXISTS idx_script_endpoints_script ON script_endpoints(script_id);

-- Each event the source stores enqueues one run of each enabled handler's
-- script. script_runs.event_source and idx_script_runs_event_once are
-- unchanged: an event runs once per script, a redelivery included.
CREATE TABLE IF NOT EXISTS script_webhook_handlers (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    script_id   UUID        NOT NULL REFERENCES scripts(id) ON DELETE CASCADE,
    source      TEXT        NOT NULL CHECK (source <> ''),
    enabled     BOOLEAN     NOT NULL DEFAULT TRUE,
    created_by  TEXT        NOT NULL DEFAULT '',
    updated_by  TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (script_id, source)
);

CREATE INDEX IF NOT EXISTS idx_script_webhook_handlers_source
    ON script_webhook_handlers(source) WHERE enabled;

INSERT INTO script_webhook_handlers (script_id, source, created_by, updated_by, created_at, updated_at)
SELECT id, event_source, owner_email, owner_email, updated_at, updated_at
  FROM scripts
 WHERE event_source <> ''
ON CONFLICT (script_id, source) DO NOTHING;

-- Every replica keeps the source-to-scripts map in memory and drops it on
-- this channel (000230); the trigger moves with the binding.
DROP TRIGGER IF EXISTS trg_scripts_event_source_insert ON scripts;
DROP TRIGGER IF EXISTS trg_scripts_event_source_update ON scripts;
DROP TRIGGER IF EXISTS trg_scripts_event_source_delete ON scripts;

DROP TRIGGER IF EXISTS trg_script_webhook_handlers_changed ON script_webhook_handlers;
CREATE TRIGGER trg_script_webhook_handlers_changed
    AFTER INSERT OR UPDATE OR DELETE ON script_webhook_handlers
    FOR EACH STATEMENT
    EXECUTE FUNCTION scripts_event_source_changed();

DROP INDEX IF EXISTS idx_scripts_event_source;
ALTER TABLE scripts DROP COLUMN IF EXISTS event_source;

-- is_form is whether the latest saved version defines form_spec(req) and
-- form_submit(req, values): an endpoint on such a script is a form. Every
-- save writes it from the parsed source; this fills it once for the scripts
-- saved before, from the canonical layout every saved source has.
ALTER TABLE scripts ADD COLUMN IF NOT EXISTS is_form BOOLEAN NOT NULL DEFAULT FALSE;
UPDATE scripts
   SET is_form = TRUE
WHERE source_code ~ '(^|\n)def form_spec\(\s*[A-Za-z_][A-Za-z0-9_]*\s*\):'
   AND source_code ~ '(^|\n)def form_submit\(\s*[A-Za-z_][A-Za-z0-9_]*\s*,\s*[A-Za-z_][A-Za-z0-9_]*\s*\):';

-- Every handler of every kind, for the lists and the type filter. type is
-- the kind, except that an endpoint whose script is a form is a form. A
-- structure test holds the kinds named here to the kinds the platform
-- registers.
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

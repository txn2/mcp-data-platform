DROP VIEW IF EXISTS script_handlers;

ALTER TABLE scripts DROP COLUMN IF EXISTS is_form;

ALTER TABLE scripts ADD COLUMN IF NOT EXISTS event_source TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_scripts_event_source
    ON scripts(event_source)
    WHERE event_source <> '';

-- A script keeps one source: the first it was bound to.
UPDATE scripts s
   SET event_source = w.source
  FROM (SELECT DISTINCT ON (script_id) script_id, source
          FROM script_webhook_handlers
         ORDER BY script_id, created_at, source) w
 WHERE w.script_id = s.id;

DROP TABLE IF EXISTS script_webhook_handlers;

CREATE TRIGGER trg_scripts_event_source_insert
    AFTER INSERT ON scripts
    FOR EACH ROW WHEN (NEW.event_source <> '')
    EXECUTE FUNCTION scripts_event_source_changed();
CREATE TRIGGER trg_scripts_event_source_update
    AFTER UPDATE OF event_source ON scripts
    FOR EACH ROW WHEN (OLD.event_source IS DISTINCT FROM NEW.event_source)
    EXECUTE FUNCTION scripts_event_source_changed();
CREATE TRIGGER trg_scripts_event_source_delete
    AFTER DELETE ON scripts
    FOR EACH ROW WHEN (OLD.event_source <> '')
    EXECUTE FUNCTION scripts_event_source_changed();

-- A script keeps one endpoint and one schedule: the first of each.
DELETE FROM script_endpoints e
 USING script_endpoints older
 WHERE older.script_id = e.script_id
   AND (older.created_at, older.id) < (e.created_at, e.id);
DROP INDEX IF EXISTS idx_script_endpoints_script;
ALTER TABLE script_endpoints ADD CONSTRAINT script_endpoints_script_id_key UNIQUE (script_id);

UPDATE script_runs r SET schedule_id = keep.id
  FROM script_schedules c
  JOIN LATERAL (SELECT id FROM script_schedules k WHERE k.script_id = c.script_id
                 ORDER BY created_at, id LIMIT 1) keep ON TRUE
 WHERE r.schedule_id = c.id AND c.id <> keep.id;
DELETE FROM script_schedules c
 USING script_schedules older
 WHERE older.script_id = c.script_id
   AND (older.created_at, older.id) < (c.created_at, c.id);
DROP INDEX IF EXISTS idx_script_schedules_script;
ALTER TABLE script_schedules ADD CONSTRAINT script_schedules_script_id_key UNIQUE (script_id);

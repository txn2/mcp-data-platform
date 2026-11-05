-- 000230: tell every replica when a script's webhook source changes (#2480).
--
-- The webhook receiver hands each stored delivery to the event run enqueuer,
-- which used to ask scripts which automations name the delivery's source on
-- every delivery, inside the sender's request, even for a source no script
-- names. A burst from one sender held the shared pool on that query. Each
-- replica now keeps the source-to-scripts map in memory and drops it when it
-- hears this channel, re-reading at most every few seconds whatever it hears.
-- A trigger rather than each writer: a create, an edit, an application import
-- and a delete all change the map, and none of them can forget to say so.
CREATE OR REPLACE FUNCTION scripts_event_source_changed() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify('script_event_sources', '');
    RETURN NULL;
END
$$;

DROP TRIGGER IF EXISTS trg_scripts_event_source_insert ON scripts;
CREATE TRIGGER trg_scripts_event_source_insert
    AFTER INSERT ON scripts
    FOR EACH ROW WHEN (NEW.event_source <> '')
    EXECUTE FUNCTION scripts_event_source_changed();

DROP TRIGGER IF EXISTS trg_scripts_event_source_update ON scripts;
CREATE TRIGGER trg_scripts_event_source_update
    AFTER UPDATE OF event_source ON scripts
    FOR EACH ROW WHEN (OLD.event_source IS DISTINCT FROM NEW.event_source)
    EXECUTE FUNCTION scripts_event_source_changed();

DROP TRIGGER IF EXISTS trg_scripts_event_source_delete ON scripts;
CREATE TRIGGER trg_scripts_event_source_delete
    AFTER DELETE ON scripts
    FOR EACH ROW WHEN (OLD.event_source <> '')
    EXECUTE FUNCTION scripts_event_source_changed();

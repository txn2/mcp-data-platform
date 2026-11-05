DROP TRIGGER IF EXISTS trg_scripts_event_source_delete ON scripts;
DROP TRIGGER IF EXISTS trg_scripts_event_source_update ON scripts;
DROP TRIGGER IF EXISTS trg_scripts_event_source_insert ON scripts;
DROP FUNCTION IF EXISTS scripts_event_source_changed();

-- A persona's portal permissions, one setting per area (#2225). A field the
-- block leaves unset resolves at load to what the persona reached before the
-- setting existed (persona.PortalPermissions), so an empty block changes
-- nothing for any persona.
--
-- hide_automations (#2109) is folded in as portal.automations and dropped.
ALTER TABLE persona_definitions ADD COLUMN IF NOT EXISTS portal JSONB NOT NULL DEFAULT '{}'::jsonb;

UPDATE persona_definitions SET portal = jsonb_build_object('automations', false) WHERE hide_automations;

ALTER TABLE persona_definitions DROP COLUMN IF EXISTS hide_automations;

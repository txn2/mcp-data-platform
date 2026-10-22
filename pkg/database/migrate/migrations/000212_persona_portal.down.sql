ALTER TABLE persona_definitions ADD COLUMN IF NOT EXISTS hide_automations BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE persona_definitions SET hide_automations = TRUE WHERE portal->>'automations' = 'false';

ALTER TABLE persona_definitions DROP COLUMN IF EXISTS portal;

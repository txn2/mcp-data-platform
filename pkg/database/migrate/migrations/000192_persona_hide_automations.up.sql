-- A persona can take the portal's Automations section away from its people
-- (#2109). False shows it, which is what every persona stored before this did.
ALTER TABLE persona_definitions ADD COLUMN IF NOT EXISTS hide_automations BOOLEAN NOT NULL DEFAULT FALSE;

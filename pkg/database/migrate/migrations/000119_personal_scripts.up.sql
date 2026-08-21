-- 000119: a script is personal (#1404)
--
-- Scope came from prompts, where a shared prompt is read by many people. A
-- script is something one person writes and runs, so the scope column, the
-- personas it named, and the visibility rules built on them are removed: a
-- script belongs to its owner, and administrators see every script.
--
-- What was global or persona-scoped becomes that owner's. A row whose
-- owner_email is empty -- one authored by a principal carrying no address,
-- such as an API key -- stays empty and so belongs to nobody: it is visible
-- only to administrators, and the transfer action this ticket adds is how it
-- gets an owner.

-- Name uniqueness moves from "unique platform-wide unless personal" to "unique
-- within an owner", so two rows that were distinct under the old rule can
-- collide under the new one: a global "daily-sales" and its author's personal
-- "daily-sales" were both legal. The older row keeps the name and every other
-- row in the group is suffixed with the head of its id, which is unique by
-- construction. The name stays inside its 128-character bound and the
-- lowercase-alphanumeric pattern the domain validates, since a UUID's text
-- form is both.
WITH collisions AS (
    SELECT id,
           ROW_NUMBER() OVER (PARTITION BY owner_email, name ORDER BY created_at, id) AS n
      FROM scripts
)
UPDATE scripts s
   SET name = LEFT(s.name, 119) || '-' || LEFT(s.id::text, 8)
  FROM collisions c
 WHERE c.id = s.id AND c.n > 1;

DROP INDEX IF EXISTS idx_scripts_name_shared;
DROP INDEX IF EXISTS idx_scripts_name_personal;
CREATE UNIQUE INDEX IF NOT EXISTS idx_scripts_name_owner
    ON scripts(owner_email, name);

DROP INDEX IF EXISTS idx_scripts_scope;
DROP INDEX IF EXISTS idx_scripts_personas;

ALTER TABLE scripts DROP CONSTRAINT IF EXISTS scripts_scope_check;
ALTER TABLE scripts DROP COLUMN IF EXISTS scope;
ALTER TABLE scripts DROP COLUMN IF EXISTS personas;

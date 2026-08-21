-- Reverse of 000119. Every script comes back personal, which is what it is
-- under the post-1404 code: which scripts were once global or persona-scoped
-- was removed by the up migration and cannot be reconstructed. Names that were
-- suffixed to resolve a collision keep their suffixes, since restoring them
-- could re-create the collision.
ALTER TABLE scripts ADD COLUMN IF NOT EXISTS scope TEXT NOT NULL DEFAULT 'personal';
ALTER TABLE scripts DROP CONSTRAINT IF EXISTS scripts_scope_check;
ALTER TABLE scripts
    ADD CONSTRAINT scripts_scope_check
    CHECK (scope IN ('global', 'persona', 'personal'));
ALTER TABLE scripts ADD COLUMN IF NOT EXISTS personas TEXT[] NOT NULL DEFAULT '{}';

DROP INDEX IF EXISTS idx_scripts_name_owner;
CREATE UNIQUE INDEX IF NOT EXISTS idx_scripts_name_shared
    ON scripts(name) WHERE scope <> 'personal';
CREATE UNIQUE INDEX IF NOT EXISTS idx_scripts_name_personal
    ON scripts(owner_email, name) WHERE scope = 'personal';

CREATE INDEX IF NOT EXISTS idx_scripts_scope    ON scripts(scope);
CREATE INDEX IF NOT EXISTS idx_scripts_personas ON scripts USING GIN(personas);

DROP INDEX IF EXISTS portal_shares_persona_idx;
ALTER TABLE portal_shares DROP COLUMN IF EXISTS shared_with_persona;

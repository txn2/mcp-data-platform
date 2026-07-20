-- Per-share access mode (#999). Before this migration every share token was
-- anonymously usable, including shares created for a single named recipient.
--
-- Backfill rule:
--   share names a recipient -> 'restricted' (only that person may open it)
--   share names no recipient -> 'authenticated' (any signed-in platform user)
--
-- No existing row is left anonymously accessible. Links that were deliberately
-- public must be re-created with access_mode = 'public'.
ALTER TABLE portal_shares ADD COLUMN IF NOT EXISTS access_mode TEXT;

UPDATE portal_shares
SET access_mode = CASE
        WHEN COALESCE(shared_with_user_id, '') <> '' OR COALESCE(shared_with_email, '') <> ''
            THEN 'restricted'
        ELSE 'authenticated'
    END
WHERE access_mode IS NULL;

ALTER TABLE portal_shares ALTER COLUMN access_mode SET DEFAULT 'restricted';
ALTER TABLE portal_shares ALTER COLUMN access_mode SET NOT NULL;

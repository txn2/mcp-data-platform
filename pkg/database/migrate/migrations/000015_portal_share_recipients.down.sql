DROP INDEX IF EXISTS idx_portal_shares_shared_with_user_id;

ALTER TABLE portal_shares DROP COLUMN IF EXISTS shared_with_user_id;

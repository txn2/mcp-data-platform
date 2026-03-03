ALTER TABLE portal_shares ADD COLUMN IF NOT EXISTS shared_with_user_id TEXT;

CREATE INDEX IF NOT EXISTS idx_portal_shares_shared_with_user_id
    ON portal_shares(shared_with_user_id)
    WHERE shared_with_user_id IS NOT NULL;

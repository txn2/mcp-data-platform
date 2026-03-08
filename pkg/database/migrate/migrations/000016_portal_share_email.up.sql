ALTER TABLE portal_shares ADD COLUMN IF NOT EXISTS shared_with_email TEXT;

CREATE INDEX IF NOT EXISTS idx_portal_shares_shared_with_email
    ON portal_shares(shared_with_email)
    WHERE shared_with_email IS NOT NULL;

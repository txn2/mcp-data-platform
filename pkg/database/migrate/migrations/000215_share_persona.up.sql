-- A share can be addressed to a persona (#2089): everyone whose roles resolve
-- to it when they open the item. Empty for every other share.
ALTER TABLE portal_shares ADD COLUMN IF NOT EXISTS shared_with_persona TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS portal_shares_persona_idx ON portal_shares (shared_with_persona)
    WHERE shared_with_persona <> '' AND revoked = FALSE;

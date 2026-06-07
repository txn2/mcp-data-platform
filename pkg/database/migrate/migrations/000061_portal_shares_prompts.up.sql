-- Native prompt sharing (#556). Extend portal_shares to support prompt sharing
-- alongside assets and collections. Exactly one of asset_id, collection_id, or
-- prompt_id must be set.
-- prompts.id is UUID (see 000030), unlike portal_assets/collections whose ids
-- are TEXT, so this FK must be UUID. ON DELETE CASCADE so deleting a prompt
-- removes its shares rather than failing on the FK.
ALTER TABLE portal_shares ADD COLUMN IF NOT EXISTS prompt_id UUID REFERENCES prompts(id) ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS idx_portal_shares_prompt_id ON portal_shares(prompt_id)
    WHERE prompt_id IS NOT NULL;

ALTER TABLE portal_shares DROP CONSTRAINT IF EXISTS chk_shares_target;
ALTER TABLE portal_shares ADD CONSTRAINT chk_shares_target CHECK (
    (asset_id IS NOT NULL AND collection_id IS NULL AND prompt_id IS NULL) OR
    (asset_id IS NULL AND collection_id IS NOT NULL AND prompt_id IS NULL) OR
    (asset_id IS NULL AND collection_id IS NULL AND prompt_id IS NOT NULL)
);

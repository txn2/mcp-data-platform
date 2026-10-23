-- A managed resource can be shared like an asset (#2258): with a person, an
-- address or a persona, as viewer or editor. Deleting the resource removes
-- its shares.
ALTER TABLE portal_shares ADD COLUMN IF NOT EXISTS resource_id TEXT REFERENCES resources(id) ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS idx_portal_shares_resource_id ON portal_shares (resource_id)
    WHERE resource_id IS NOT NULL;

-- Every share has exactly one target.
ALTER TABLE portal_shares DROP CONSTRAINT IF EXISTS chk_shares_target;
ALTER TABLE portal_shares ADD CONSTRAINT chk_shares_target
    CHECK (num_nonnulls(asset_id, collection_id, prompt_id, resource_id) = 1);

-- A person who cannot open a file can ask its owner for access, as for an
-- asset (#2258).
ALTER TABLE access_requests DROP CONSTRAINT IF EXISTS access_requests_target_type_check;
ALTER TABLE access_requests ADD CONSTRAINT access_requests_target_type_check
    CHECK (target_type IN ('asset', 'collection', 'prompt', 'resource'));

-- Extend portal_shares to support collection sharing alongside asset sharing.
-- Exactly one of asset_id or collection_id must be set.
ALTER TABLE portal_shares ALTER COLUMN asset_id DROP NOT NULL;

ALTER TABLE portal_shares ADD COLUMN collection_id TEXT REFERENCES portal_collections(id);

CREATE INDEX idx_portal_shares_collection_id ON portal_shares(collection_id)
    WHERE collection_id IS NOT NULL;

ALTER TABLE portal_shares ADD CONSTRAINT chk_shares_target CHECK (
    (asset_id IS NOT NULL AND collection_id IS NULL) OR
    (asset_id IS NULL AND collection_id IS NOT NULL)
);

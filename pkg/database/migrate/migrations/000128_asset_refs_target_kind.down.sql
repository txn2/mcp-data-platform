-- Reverting drops the asset references outright. They cannot be represented in
-- the table this restores: it keys on a resource id, and an asset reference
-- left behind under that key would resolve to a managed resource that does not
-- exist. Dropping the row leaves the referencing asset's content naming a
-- reference that no longer resolves, which is the state a deleted target
-- already produces and which the page renders around.
DELETE FROM portal_asset_refs WHERE target_kind <> 'resource';

DROP INDEX IF EXISTS idx_portal_asset_refs_target;

ALTER TABLE portal_asset_refs DROP CONSTRAINT portal_asset_refs_pkey;
ALTER TABLE portal_asset_refs DROP COLUMN IF EXISTS target_kind;
ALTER TABLE portal_asset_refs ADD PRIMARY KEY (asset_id, target_id);

-- A constraint name does not follow a table rename, and the up migration drops
-- the primary key BY NAME. Without this rename a down-then-up cycle fails on a
-- constraint that does not exist and leaves the schema version dirty.
ALTER TABLE portal_asset_refs
    RENAME CONSTRAINT portal_asset_refs_pkey TO portal_asset_resource_refs_pkey;

ALTER INDEX portal_asset_refs_ref_token_key
    RENAME TO portal_asset_resource_refs_ref_token_key;

ALTER TABLE portal_asset_refs RENAME COLUMN target_id TO resource_id;
ALTER TABLE portal_asset_refs RENAME TO portal_asset_resource_refs;

CREATE INDEX IF NOT EXISTS idx_portal_asset_refs_resource
    ON portal_asset_resource_refs(resource_id);

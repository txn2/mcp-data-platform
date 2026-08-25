-- The reference table is now read from the resource's side as well as the
-- asset's (#1475): a resource's detail view lists the assets that reference it,
-- and a delete names them rather than silently breaking those assets.
--
-- That read filters on resource_id, which the declaring index
-- (asset_id, position) cannot serve at all: every such query would scan the
-- whole table. The question is asked once per resource page view and once
-- before each delete, on a table that grows with every reference every asset
-- declares, so it gets an index of its own.
CREATE INDEX IF NOT EXISTS idx_portal_asset_refs_resource
    ON portal_asset_resource_refs(resource_id);

-- An asset's content can now reference another asset as well as a managed
-- resource (#1488). The reference is the same mechanism either way -- one
-- token, one serving route, one rewrite -- so it stays one table, and the
-- table stops being named for one of the two kinds it holds.
--
-- target_kind is part of the primary key rather than a plain column. A
-- resource id and an asset id are separate id spaces, so the same string can
-- name both, and an asset referencing each of them is holding two references
-- that must not collide.
--
-- Existing rows are all resource references, which is what the DEFAULT
-- backfills. The default is then dropped: every writer states the kind, and a
-- writer that forgot would otherwise record an asset reference as a resource
-- one and serve the wrong thing.
ALTER TABLE portal_asset_resource_refs RENAME TO portal_asset_refs;
ALTER TABLE portal_asset_refs RENAME COLUMN resource_id TO target_id;

ALTER TABLE portal_asset_refs
    ADD COLUMN IF NOT EXISTS target_kind TEXT NOT NULL DEFAULT 'resource';
ALTER TABLE portal_asset_refs ALTER COLUMN target_kind DROP DEFAULT;

ALTER TABLE portal_asset_refs DROP CONSTRAINT portal_asset_resource_refs_pkey;
ALTER TABLE portal_asset_refs ADD PRIMARY KEY (asset_id, target_kind, target_id);

-- The token's uniqueness is what lets the serving route refuse a token pasted
-- onto another asset's path; only its name changes with the table's.
ALTER INDEX portal_asset_resource_refs_ref_token_key
    RENAME TO portal_asset_refs_ref_token_key;

-- The by-target read (what is holding this file up?) now asks about a kind as
-- well as an id, so its index leads with the kind. The declaring index
-- (asset_id, position) is unchanged and keeps its name.
DROP INDEX IF EXISTS idx_portal_asset_refs_resource;
CREATE INDEX IF NOT EXISTS idx_portal_asset_refs_target
    ON portal_asset_refs(target_kind, target_id);

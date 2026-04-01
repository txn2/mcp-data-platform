DROP INDEX IF EXISTS idx_collection_items_asset_id;
DROP INDEX IF EXISTS idx_collection_items_section_id;
DROP TABLE IF EXISTS portal_collection_items;

DROP INDEX IF EXISTS idx_collection_sections_collection_id;
DROP TABLE IF EXISTS portal_collection_sections;

DROP INDEX IF EXISTS idx_portal_collections_deleted_at;
DROP INDEX IF EXISTS idx_portal_collections_owner_id;
DROP TABLE IF EXISTS portal_collections;

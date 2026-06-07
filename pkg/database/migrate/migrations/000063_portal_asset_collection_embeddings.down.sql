-- Reverse 000063. Drop the asset + collection embedding indexes, FTS functions,
-- and columns (including the denormalized sections_text).
DROP INDEX IF EXISTS idx_portal_collections_search_fts;
DROP FUNCTION IF EXISTS portal_collection_fts(text, text, text);
DROP INDEX IF EXISTS idx_portal_collections_embedding_hnsw;

ALTER TABLE portal_collections
    DROP COLUMN IF EXISTS embedding_text_hash,
    DROP COLUMN IF EXISTS embedding_model,
    DROP COLUMN IF EXISTS embedding,
    DROP COLUMN IF EXISTS sections_text;

DROP INDEX IF EXISTS idx_portal_assets_search_fts;
DROP FUNCTION IF EXISTS portal_asset_fts(text, text, jsonb);
DROP INDEX IF EXISTS idx_portal_assets_embedding_hnsw;

ALTER TABLE portal_assets
    DROP COLUMN IF EXISTS embedding_text_hash,
    DROP COLUMN IF EXISTS embedding_model,
    DROP COLUMN IF EXISTS embedding;

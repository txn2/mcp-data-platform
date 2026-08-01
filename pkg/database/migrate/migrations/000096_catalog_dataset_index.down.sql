-- Reverse 000096. Drop the platform-side catalog index, its FTS function, and
-- the sync marker. The catalog itself is unaffected: these tables hold a
-- discardable copy of DataHub text, so dropping them costs only the ability to
-- rank catalog datasets locally (search falls back to DataHub's own keyword
-- search, which is what it used before this migration).
DROP INDEX IF EXISTS idx_catalog_datasets_search_fts;
DROP FUNCTION IF EXISTS catalog_dataset_fts(text, text, text[], text);
DROP INDEX IF EXISTS idx_catalog_datasets_embedding_hnsw;
DROP TABLE IF EXISTS catalog_dataset_sync;
DROP TABLE IF EXISTS catalog_datasets;

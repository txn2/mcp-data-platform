-- Reverse 000064.
DROP INDEX IF EXISTS idx_portal_threads_author_id;
DROP INDEX IF EXISTS idx_portal_threads_standalone;
DROP INDEX IF EXISTS idx_portal_threads_prompt_id;
DROP INDEX IF EXISTS idx_portal_threads_collection_id;
DROP INDEX IF EXISTS idx_portal_threads_asset_id;
DROP TABLE IF EXISTS portal_threads;

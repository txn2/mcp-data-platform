DROP TABLE IF EXISTS resource_reclassify_runs;
ALTER TABLE resource_folders
    DROP COLUMN IF EXISTS embedding_text_hash,
    DROP COLUMN IF EXISTS embedding_model,
    DROP COLUMN IF EXISTS embedding,
    DROP COLUMN IF EXISTS summary_text,
    DROP COLUMN IF EXISTS description;
DROP INDEX IF EXISTS idx_resources_unindexed_folder;
ALTER TABLE resources
    DROP COLUMN IF EXISTS index_policy_explicit,
    DROP COLUMN IF EXISTS index_policy;

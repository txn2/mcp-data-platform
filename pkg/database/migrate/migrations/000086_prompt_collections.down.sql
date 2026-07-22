DROP INDEX IF EXISTS idx_prompts_collection;
ALTER TABLE prompts DROP COLUMN IF EXISTS collection_id;
DROP INDEX IF EXISTS uq_prompt_collections_name;
DROP TABLE IF EXISTS prompt_collections;

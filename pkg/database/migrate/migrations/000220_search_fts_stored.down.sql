-- Dropping a column drops its index; the indexes on the expressions return.
ALTER TABLE resources DROP COLUMN IF EXISTS search_fts;
CREATE INDEX IF NOT EXISTS idx_resources_search_fts
    ON resources USING gin (resource_fts(display_name, description, path, filename, tags, content_text));

ALTER TABLE resource_folders DROP COLUMN IF EXISTS search_fts;

ALTER TABLE portal_assets DROP COLUMN IF EXISTS search_fts;
CREATE INDEX IF NOT EXISTS idx_portal_assets_search_fts
    ON portal_assets USING gin (portal_asset_fts(name, description, tags));

ALTER TABLE portal_collections DROP COLUMN IF EXISTS search_fts;
CREATE INDEX IF NOT EXISTS idx_portal_collections_search_fts
    ON portal_collections USING gin (portal_collection_fts(name, description, sections_text));

ALTER TABLE prompts DROP COLUMN IF EXISTS search_fts;
CREATE INDEX IF NOT EXISTS idx_prompts_search_fts
    ON prompts USING gin (prompt_fts(display_name, name, description, content, tags));

ALTER TABLE portal_knowledge_pages DROP COLUMN IF EXISTS search_fts;
CREATE INDEX IF NOT EXISTS idx_portal_knowledge_pages_search_fts
    ON portal_knowledge_pages USING gin (portal_knowledge_page_fts(title, body, tags));

ALTER TABLE scripts DROP COLUMN IF EXISTS search_fts;
CREATE INDEX IF NOT EXISTS idx_scripts_search_fts
    ON scripts USING gin (script_fts(display_name, name, description, category, tags, params, source_code));

ALTER TABLE catalog_datasets DROP COLUMN IF EXISTS search_fts;
CREATE INDEX IF NOT EXISTS idx_catalog_datasets_search_fts
    ON catalog_datasets USING gin (catalog_dataset_fts(name, description, tags, domain));

ALTER TABLE memory_records DROP COLUMN IF EXISTS search_fts;
CREATE INDEX IF NOT EXISTS idx_memory_records_content_fts
    ON memory_records USING gin (to_tsvector('english', fts_name_words(content)));

ALTER TABLE call_records DROP COLUMN IF EXISTS search_fts;
CREATE INDEX IF NOT EXISTS idx_call_records_fts
    ON call_records USING gin (to_tsvector('english', purpose || ' ' || statement || ' ' || method || ' ' || path || ' ' || operation_id));

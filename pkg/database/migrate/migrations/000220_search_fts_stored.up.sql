-- #2292: a ranked search stores the text it ranks.
--
-- Each ranked surface matched and ranked on an expression (resource_fts(...)
-- and its siblings) with a GIN index built on the same call. The index answers
-- the match, but it holds no tsvector to rank from, so ORDER BY ts_rank_cd(...)
-- computed the expression again for every matching row. A word in half of a
-- 30,000-file library computed it 15,000 times, over up to 32 KiB of content
-- each: 20.7 s against a 5 s search bound, of which the index scan was 5 ms.
--
-- search_fts is that expression, computed once when a row is written. The
-- statements match and rank on it, and its GIN index replaces the index on the
-- expression. The functions stay: each column is defined by the same call.
--
-- Adding a stored generated column rewrites its table and computes the
-- expression once per row, as the REINDEX in 000209 did.

ALTER TABLE resources ADD COLUMN search_fts tsvector
    GENERATED ALWAYS AS (resource_fts(display_name, description, path, filename, tags, content_text)) STORED;
CREATE INDEX idx_resources_search_fts_stored ON resources USING gin (search_fts);
DROP INDEX IF EXISTS idx_resources_search_fts;

ALTER TABLE resource_folders ADD COLUMN search_fts tsvector
    GENERATED ALWAYS AS (to_tsvector('english', fts_name_words(path) || ' ' || description || ' ' || summary_text)) STORED;
CREATE INDEX idx_resource_folders_search_fts ON resource_folders USING gin (search_fts);

ALTER TABLE portal_assets ADD COLUMN search_fts tsvector
    GENERATED ALWAYS AS (portal_asset_fts(name, description, tags)) STORED;
CREATE INDEX idx_portal_assets_search_fts_stored ON portal_assets USING gin (search_fts);
DROP INDEX IF EXISTS idx_portal_assets_search_fts;

ALTER TABLE portal_collections ADD COLUMN search_fts tsvector
    GENERATED ALWAYS AS (portal_collection_fts(name, description, sections_text)) STORED;
CREATE INDEX idx_portal_collections_search_fts_stored ON portal_collections USING gin (search_fts);
DROP INDEX IF EXISTS idx_portal_collections_search_fts;

ALTER TABLE prompts ADD COLUMN search_fts tsvector
    GENERATED ALWAYS AS (prompt_fts(display_name, name, description, content, tags)) STORED;
CREATE INDEX idx_prompts_search_fts_stored ON prompts USING gin (search_fts);
DROP INDEX IF EXISTS idx_prompts_search_fts;

ALTER TABLE portal_knowledge_pages ADD COLUMN search_fts tsvector
    GENERATED ALWAYS AS (portal_knowledge_page_fts(title, body, tags)) STORED;
CREATE INDEX idx_portal_knowledge_pages_search_fts_stored ON portal_knowledge_pages USING gin (search_fts);
DROP INDEX IF EXISTS idx_portal_knowledge_pages_search_fts;

ALTER TABLE scripts ADD COLUMN search_fts tsvector
    GENERATED ALWAYS AS (script_fts(display_name, name, description, category, tags, params, source_code)) STORED;
CREATE INDEX idx_scripts_search_fts_stored ON scripts USING gin (search_fts);
DROP INDEX IF EXISTS idx_scripts_search_fts;

ALTER TABLE catalog_datasets ADD COLUMN search_fts tsvector
    GENERATED ALWAYS AS (catalog_dataset_fts(name, description, tags, domain)) STORED;
CREATE INDEX idx_catalog_datasets_search_fts_stored ON catalog_datasets USING gin (search_fts);
DROP INDEX IF EXISTS idx_catalog_datasets_search_fts;

ALTER TABLE memory_records ADD COLUMN search_fts tsvector
    GENERATED ALWAYS AS (to_tsvector('english', fts_name_words(content))) STORED;
CREATE INDEX idx_memory_records_search_fts ON memory_records USING gin (search_fts);
DROP INDEX IF EXISTS idx_memory_records_content_fts;

ALTER TABLE call_records ADD COLUMN search_fts tsvector
    GENERATED ALWAYS AS (to_tsvector('english', purpose || ' ' || statement || ' ' || method || ' ' || path || ' ' || operation_id)) STORED;
CREATE INDEX idx_call_records_search_fts ON call_records USING gin (search_fts);
DROP INDEX IF EXISTS idx_call_records_fts;

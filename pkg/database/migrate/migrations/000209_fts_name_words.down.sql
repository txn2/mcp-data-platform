CREATE OR REPLACE FUNCTION resource_fts(display_name text, description text, path text, filename text, tags text[], content_text text)
    RETURNS tsvector LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT to_tsvector('english',
        coalesce(display_name, '') || ' ' ||
        coalesce(description, '')  || ' ' ||
        coalesce(path, '')         || ' ' ||
        coalesce(filename, '')     || ' ' ||
        coalesce(array_to_string(tags, ' '), '') || ' ' ||
        coalesce(content_text, ''));
$$;

CREATE OR REPLACE FUNCTION portal_asset_fts(name text, description text, tags jsonb)
    RETURNS tsvector LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT to_tsvector('english',
        coalesce(name, '')        || ' ' ||
        coalesce(description, '')  || ' ' ||
        coalesce(tags::text, ''));
$$;

CREATE OR REPLACE FUNCTION portal_collection_fts(name text, description text, sections_text text)
    RETURNS tsvector LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT to_tsvector('english',
        coalesce(name, '')          || ' ' ||
        coalesce(description, '')    || ' ' ||
        coalesce(sections_text, ''));
$$;

CREATE OR REPLACE FUNCTION prompt_fts(display_name text, name text, description text, content text, tags text[])
    RETURNS tsvector LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT to_tsvector('english',
        coalesce(nullif(display_name, ''), name) || ' ' ||
        coalesce(description, '')  || ' ' ||
        coalesce(content, '')      || ' ' ||
        coalesce(array_to_string(tags, ' '), ''));
$$;

CREATE OR REPLACE FUNCTION portal_knowledge_page_fts(title text, body text, tags jsonb)
    RETURNS tsvector LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT to_tsvector('english',
        coalesce(title, '') || ' ' ||
        coalesce(body, '')  || ' ' ||
        coalesce(tags::text, ''));
$$;

CREATE OR REPLACE FUNCTION script_fts(display_name text, name text, description text, category text, tags text[], params jsonb, source text)
    RETURNS tsvector LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT setweight(to_tsvector('english',
        coalesce(nullif(display_name, ''), name) || ' ' ||
        coalesce(description, '')                || ' ' ||
        coalesce(category, '')                   || ' ' ||
        coalesce(array_to_string(tags, ' '), '') || ' ' ||
        CASE WHEN jsonb_typeof(params) = 'array' THEN
            coalesce((
                SELECT string_agg(
                    coalesce(p->>'name', '') || ' ' || coalesce(p->>'description', ''), ' ')
                FROM jsonb_array_elements(params) AS p), '')
        ELSE '' END), 'A')
    || setweight(to_tsvector('english', coalesce(source, '')), 'D');
$$;

CREATE OR REPLACE FUNCTION catalog_dataset_fts(name text, description text, tags text[], domain text)
    RETURNS tsvector LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT to_tsvector('english',
        coalesce(name, '')        || ' ' ||
        coalesce(description, '') || ' ' ||
        coalesce(array_to_string(tags, ' '), '') || ' ' ||
        coalesce(domain, ''));
$$;

REINDEX INDEX idx_resources_search_fts;
REINDEX INDEX idx_portal_assets_search_fts;
REINDEX INDEX idx_portal_collections_search_fts;
REINDEX INDEX idx_prompts_search_fts;
REINDEX INDEX idx_portal_knowledge_pages_search_fts;
REINDEX INDEX idx_scripts_search_fts;
REINDEX INDEX idx_catalog_datasets_search_fts;

DROP INDEX IF EXISTS idx_memory_records_content_fts;
CREATE INDEX idx_memory_records_content_fts
    ON memory_records USING gin (to_tsvector('english', content));

DROP FUNCTION IF EXISTS fts_name_words(text);

-- #2230: a name is matched by its words. PostgreSQL's parser reads
-- "acceptance-1627-1792487495133911000" as the word "acceptance" and the
-- signed numbers -1627 and -1792487495133911000, and "q3-2024-report.csv" as
-- one file-name token, so a search for "acceptance 1627 1792487495133911000"
-- or "q3 2024 report" matched neither. fts_name_words keeps the text as it is
-- and adds it again with its separators read as spaces, so a name matches by
-- the words it is made of and still by itself. It is applied to the fields
-- that name a thing (names, titles, paths, file names, tags, categories), and
-- to a memory's content, which is short and is where an agent records an
-- identifier.
--
-- The search functions below spell the expression out rather than call
-- fts_name_words: PostgreSQL 17 runs an index's functions with search_path set
-- to pg_catalog, where an unqualified call to a function of this schema is not
-- found. fts_name_words itself calls only pg_catalog, so the memory index and
-- the folder search call it.
CREATE OR REPLACE FUNCTION fts_name_words(t text) RETURNS text
    LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT coalesce(t, '') || ' ' || regexp_replace(coalesce(t, ''), '[-_./:]+', ' ', 'g');
$$;

CREATE OR REPLACE FUNCTION resource_fts(display_name text, description text, path text, filename text, tags text[], content_text text)
    RETURNS tsvector LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT to_tsvector('english',
        (coalesce(display_name, '') || ' ' || regexp_replace(coalesce(display_name, ''), '[-_./:]+', ' ', 'g')) || ' ' ||
        coalesce(description, '')  || ' ' ||
        (coalesce(path, '') || ' ' || regexp_replace(coalesce(path, ''), '[-_./:]+', ' ', 'g'))       || ' ' ||
        (coalesce(filename, '') || ' ' || regexp_replace(coalesce(filename, ''), '[-_./:]+', ' ', 'g'))   || ' ' ||
        (coalesce(array_to_string(tags, ' '), '') || ' ' || regexp_replace(coalesce(array_to_string(tags, ' '), ''), '[-_./:]+', ' ', 'g')) || ' ' ||
        coalesce(content_text, ''));
$$;

CREATE OR REPLACE FUNCTION portal_asset_fts(name text, description text, tags jsonb)
    RETURNS tsvector LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT to_tsvector('english',
        (coalesce(name, '') || ' ' || regexp_replace(coalesce(name, ''), '[-_./:]+', ' ', 'g'))       || ' ' ||
        coalesce(description, '')  || ' ' ||
        (coalesce(tags::text, '') || ' ' || regexp_replace(coalesce(tags::text, ''), '[-_./:]+', ' ', 'g')));
$$;

CREATE OR REPLACE FUNCTION portal_collection_fts(name text, description text, sections_text text)
    RETURNS tsvector LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT to_tsvector('english',
        (coalesce(name, '') || ' ' || regexp_replace(coalesce(name, ''), '[-_./:]+', ' ', 'g'))         || ' ' ||
        coalesce(description, '')    || ' ' ||
        coalesce(sections_text, ''));
$$;

CREATE OR REPLACE FUNCTION prompt_fts(display_name text, name text, description text, content text, tags text[])
    RETURNS tsvector LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT to_tsvector('english',
        (coalesce(coalesce(nullif(display_name, ''), name), '') || ' ' || regexp_replace(coalesce(coalesce(nullif(display_name, ''), name), ''), '[-_./:]+', ' ', 'g')) || ' ' ||
        coalesce(description, '')  || ' ' ||
        coalesce(content, '')      || ' ' ||
        (coalesce(array_to_string(tags, ' '), '') || ' ' || regexp_replace(coalesce(array_to_string(tags, ' '), ''), '[-_./:]+', ' ', 'g')));
$$;

CREATE OR REPLACE FUNCTION portal_knowledge_page_fts(title text, body text, tags jsonb)
    RETURNS tsvector LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT to_tsvector('english',
        (coalesce(title, '') || ' ' || regexp_replace(coalesce(title, ''), '[-_./:]+', ' ', 'g')) || ' ' ||
        coalesce(body, '')    || ' ' ||
        (coalesce(tags::text, '') || ' ' || regexp_replace(coalesce(tags::text, ''), '[-_./:]+', ' ', 'g')));
$$;

CREATE OR REPLACE FUNCTION script_fts(display_name text, name text, description text, category text, tags text[], params jsonb, source text)
    RETURNS tsvector LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT setweight(to_tsvector('english',
        (coalesce(coalesce(nullif(display_name, ''), name), '') || ' ' || regexp_replace(coalesce(coalesce(nullif(display_name, ''), name), ''), '[-_./:]+', ' ', 'g')) || ' ' ||
        coalesce(description, '')                || ' ' ||
        (coalesce(category, '') || ' ' || regexp_replace(coalesce(category, ''), '[-_./:]+', ' ', 'g'))                 || ' ' ||
        (coalesce(array_to_string(tags, ' '), '') || ' ' || regexp_replace(coalesce(array_to_string(tags, ' '), ''), '[-_./:]+', ' ', 'g')) || ' ' ||
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
        (coalesce(name, '') || ' ' || regexp_replace(coalesce(name, ''), '[-_./:]+', ' ', 'g'))      || ' ' ||
        coalesce(description, '') || ' ' ||
        (coalesce(array_to_string(tags, ' '), '') || ' ' || regexp_replace(coalesce(array_to_string(tags, ' '), ''), '[-_./:]+', ' ', 'g')) || ' ' ||
        (coalesce(domain, '') || ' ' || regexp_replace(coalesce(domain, ''), '[-_./:]+', ' ', 'g')));
$$;

-- An expression index holds what its function returned when each row was
-- written, so each index on a function changed above is built again.
REINDEX INDEX idx_resources_search_fts;
REINDEX INDEX idx_portal_assets_search_fts;
REINDEX INDEX idx_portal_collections_search_fts;
REINDEX INDEX idx_prompts_search_fts;
REINDEX INDEX idx_portal_knowledge_pages_search_fts;
REINDEX INDEX idx_scripts_search_fts;
REINDEX INDEX idx_catalog_datasets_search_fts;

DROP INDEX IF EXISTS idx_memory_records_content_fts;
CREATE INDEX idx_memory_records_content_fts
    ON memory_records USING gin (to_tsvector('english', fts_name_words(content)));

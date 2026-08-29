DROP INDEX IF EXISTS idx_resources_search_fts;
DROP FUNCTION IF EXISTS resource_fts(text, text, text, text, text[], text);

DROP INDEX IF EXISTS idx_resources_path;
ALTER TABLE resources RENAME COLUMN path TO category;

CREATE INDEX IF NOT EXISTS idx_resources_category ON resources (category);

CREATE FUNCTION resource_fts(
    display_name text, description text, category text,
    filename text, tags text[], content_text text
) RETURNS tsvector LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT to_tsvector('english',
        coalesce(display_name, '') || ' ' ||
        coalesce(description, '')  || ' ' ||
        coalesce(category, '')     || ' ' ||
        coalesce(filename, '')     || ' ' ||
        coalesce(array_to_string(tags, ' '), '') || ' ' ||
        coalesce(content_text, ''));
$$;

CREATE INDEX IF NOT EXISTS idx_resources_search_fts
    ON resources USING gin (resource_fts(display_name, description, category, filename, tags, content_text));

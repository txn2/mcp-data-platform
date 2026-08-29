-- A managed resource was filed by exactly one flat label (#1529). `category`
-- was a single segment validated against ^[a-z][a-z0-9-]{0,30}$, which rejects
-- a slash, so the label could not nest: a library heading for thousands of
-- files was six unbounded lists with free-text search as the only way to narrow
-- one.
--
-- The address already had the shape the structure was missing. A resource URI
-- is <scheme>://<library>/<category>/<filename>, and ParseURI has always split
-- the library off the front and treated the rest as an opaque path. Only the
-- validation forbade the tree from having more than one level.
--
-- So the column becomes `path`: a slash-separated folder path inside a library.
-- Every segment keeps the old rule, which makes every existing value a legal
-- one-segment path. No row is rewritten here and no existing URI changes;
-- folders are derived from the paths in use rather than stored as rows of their
-- own, so there is no folder table to backfill either.
ALTER TABLE resources RENAME COLUMN category TO path;

-- The flat label was only ever looked up by equality. A path is looked up by
-- prefix as well -- listing a folder returns everything beneath it -- and the
-- default collation's btree cannot serve `path LIKE 'data/media-manager/%'`.
-- text_pattern_ops can, and still serves the equality lookup the old index did.
DROP INDEX IF EXISTS idx_resources_category;
CREATE INDEX IF NOT EXISTS idx_resources_path
    ON resources (path text_pattern_ops);

-- resource_fts composes the lexical document the GIN index is built on
-- (migration 000091), and its third parameter was named after the column. A
-- parameter cannot be renamed in place -- CREATE OR REPLACE refuses it -- and
-- the index expression depends on the function, so the rename is a drop and a
-- rebuild. The composition is unchanged: same fields, same order, so a stored
-- document and a query still meet in the same space.
DROP INDEX IF EXISTS idx_resources_search_fts;
DROP FUNCTION IF EXISTS resource_fts(text, text, text, text, text[], text);

CREATE FUNCTION resource_fts(
    display_name text, description text, path text,
    filename text, tags text[], content_text text
) RETURNS tsvector LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT to_tsvector('english',
        coalesce(display_name, '') || ' ' ||
        coalesce(description, '')  || ' ' ||
        coalesce(path, '')         || ' ' ||
        coalesce(filename, '')     || ' ' ||
        coalesce(array_to_string(tags, ' '), '') || ' ' ||
        coalesce(content_text, ''));
$$;

CREATE INDEX IF NOT EXISTS idx_resources_search_fts
    ON resources USING gin (resource_fts(display_name, description, path, filename, tags, content_text));

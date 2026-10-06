-- Reverse 000178: restore the inline embedding columns, their HNSW index and
-- the five- and six-argument script_fts overloads exactly as 000113, 000111 and
-- 000116 defined them, so 000177's down migration can rebuild its index on the
-- six-argument call. The columns come back empty: the vectors they held were
-- discarded by the up migration, and the chunk index is what search reads.

CREATE EXTENSION IF NOT EXISTS vector;

ALTER TABLE scripts
    ADD COLUMN IF NOT EXISTS embedding           vector(768),
    ADD COLUMN IF NOT EXISTS embedding_model     TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS embedding_text_hash BYTEA;

CREATE INDEX IF NOT EXISTS idx_scripts_embedding_hnsw
    ON scripts USING hnsw (embedding vector_cosine_ops);

CREATE OR REPLACE FUNCTION script_fts(
    display_name text, name text, description text, tags text[], params jsonb
) RETURNS tsvector LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT to_tsvector('english',
        coalesce(nullif(display_name, ''), name) || ' ' ||
        coalesce(description, '')                || ' ' ||
        coalesce(array_to_string(tags, ' '), '') || ' ' ||
        CASE WHEN jsonb_typeof(params) = 'array' THEN
            coalesce((
                SELECT string_agg(
                    coalesce(p->>'name', '') || ' ' || coalesce(p->>'description', ''), ' ')
                FROM jsonb_array_elements(params) AS p), '')
        ELSE '' END);
$$;

CREATE OR REPLACE FUNCTION script_fts(
    display_name text, name text, description text, category text, tags text[], params jsonb
) RETURNS tsvector LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT to_tsvector('english',
        coalesce(nullif(display_name, ''), name) || ' ' ||
        coalesce(description, '')                || ' ' ||
        coalesce(category, '')                   || ' ' ||
        coalesce(array_to_string(tags, ' '), '') || ' ' ||
        CASE WHEN jsonb_typeof(params) = 'array' THEN
            coalesce((
                SELECT string_agg(
                    coalesce(p->>'name', '') || ' ' || coalesce(p->>'description', ''), ' ')
                FROM jsonb_array_elements(params) AS p), '')
        ELSE '' END);
$$;

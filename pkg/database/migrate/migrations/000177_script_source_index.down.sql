DROP INDEX IF EXISTS idx_scripts_search_fts;

CREATE INDEX IF NOT EXISTS idx_scripts_search_fts
    ON scripts USING gin (script_fts(display_name, name, description, category, tags, params));

DROP FUNCTION IF EXISTS script_fts(text, text, text, text, text[], jsonb, text);

ALTER TABLE scripts
    DROP COLUMN IF EXISTS index_model,
    DROP COLUMN IF EXISTS index_embedded_hash,
    DROP COLUMN IF EXISTS index_text_hash;

DROP TABLE IF EXISTS script_embedding_chunks;

-- Reverse 000102: drop the scripts lexical index and its composing functions.
-- The index is dropped first: script_fts cannot be dropped while an index
-- expression depends on it.
DROP INDEX IF EXISTS idx_scripts_search_fts;
DROP FUNCTION IF EXISTS script_fts(text, text, text, text[], jsonb);
DROP FUNCTION IF EXISTS script_param_text(jsonb);

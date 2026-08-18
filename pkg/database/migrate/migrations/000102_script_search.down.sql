-- Reverse 000102: drop the scripts lexical index and its composing function.
-- The index is dropped first: script_fts cannot be dropped while an index
-- expression depends on it.
--
-- script_param_text is dropped IF EXISTS rather than unconditionally: it
-- belongs to the earlier shape of this migration, which composed script_fts
-- from a helper. That shape was only ever accepted by PostgreSQL 16 (see the
-- header of the up migration and 000111), so on most deployments there is
-- nothing here to drop.
DROP INDEX IF EXISTS idx_scripts_search_fts;
DROP FUNCTION IF EXISTS script_fts(text, text, text, text[], jsonb);
DROP FUNCTION IF EXISTS script_param_text(jsonb);

-- Reverse 000116. Rebuild the five-argument index, drop the six-argument
-- function, and drop the category column from both tables.
--
-- The five-argument function itself is not recreated: the up migration left it
-- in place (see the reasoning there), so it is still here. Only its index has to
-- come back, and it has to come back before the six-argument function goes, in
-- the same order the up migration used: an index cannot outlive the function its
-- expression calls.
--
-- Every category anybody filed is discarded, which is what reversing this
-- migration means. Nothing else is lost: a script's identity, its code, its
-- version history and its tags are untouched.
DROP INDEX IF EXISTS idx_scripts_search_fts;

CREATE INDEX IF NOT EXISTS idx_scripts_search_fts
    ON scripts USING gin (script_fts(display_name, name, description, tags, params));

DROP FUNCTION IF EXISTS script_fts(text, text, text, text, text[], jsonb);

DROP INDEX IF EXISTS idx_scripts_category;

ALTER TABLE script_versions
    DROP COLUMN IF EXISTS category;

ALTER TABLE scripts
    DROP COLUMN IF EXISTS category;

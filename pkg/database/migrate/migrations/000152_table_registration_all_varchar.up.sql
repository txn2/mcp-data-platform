-- A JSON-lines registration declares a type per column from the values its
-- file holds (#1833). Until now it declared every column VARCHAR, the rule of
-- the CSV reader, and a table registered under that rule has queries written
-- against it: a follow that retyped its columns when the next version of the
-- file arrived would break every one of them without a word.
--
-- all_varchar marks a registration made under the old rule. A follow keeps it,
-- declaring every column VARCHAR whatever the new version holds; registering
-- the file again under the same name replaces the row with one that is typed.
-- Every JSON-lines registration that exists now was made under the old rule.
ALTER TABLE table_registrations
    ADD COLUMN IF NOT EXISTS all_varchar BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE table_registrations SET all_varchar = TRUE WHERE format = 'jsonl';

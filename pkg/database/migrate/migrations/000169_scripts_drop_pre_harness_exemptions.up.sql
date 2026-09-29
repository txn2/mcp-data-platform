-- 000169: one rule for every script (#1965).
--
-- 000165, 000166 and 000167 marked the scripts that existed before the
-- authoring gates, tests, and read outputs so an edit to one was held to less.
-- A save of any script is now held to every rule; running a script was never
-- gated, so the scripts these columns marked keep running as they are.
ALTER TABLE scripts DROP COLUMN IF EXISTS legacy;
ALTER TABLE scripts DROP COLUMN IF EXISTS tests_optional;
ALTER TABLE scripts DROP COLUMN IF EXISTS outputs_read_optional;

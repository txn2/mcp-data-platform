-- 000165: mark the scripts saved before the authoring gates (#1913).
--
-- A script created from this release on is held to the whole harness on every
-- save: a main() entry point with nothing but definitions and constants at the
-- top level (#1944), and the structural lint and limits (#1938). A script that
-- already exists keeps running as it does today, and a new version of it is
-- refused only for a finding the version before it did not have.
--
-- The column is added TRUE, so every row that exists now is marked legacy, and
-- its default then becomes FALSE, so every script created afterwards is not.
-- Nothing ever sets it back to TRUE.

ALTER TABLE scripts ADD COLUMN IF NOT EXISTS legacy BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE scripts ALTER COLUMN legacy SET DEFAULT FALSE;

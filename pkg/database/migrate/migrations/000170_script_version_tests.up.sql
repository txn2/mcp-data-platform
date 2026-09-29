-- 000170: the test report a save produced, kept on the version it saved (#1972).
--
-- A save runs the script's tests and measures the statements they reach
-- (internal/platform/scriptsave) and refuses a version that fails them. The
-- report was only returned to whoever saved, so the script's page could not
-- show which tests a version has or how much of it they reach. It is kept here,
-- once per version, since a version never changes. NULL is a version saved
-- before this column existed; its tests were run at the save but not kept.
ALTER TABLE script_versions ADD COLUMN IF NOT EXISTS tests JSONB;

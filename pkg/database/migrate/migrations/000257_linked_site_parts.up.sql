-- #2679: being a part of an application and shipping with it are two
-- switches. A site environment part is linked (same-origin endpoints, the
-- application's cookies and its viewer, and nothing else) or coupled (every
-- application release moves it one step, every rollback returns it). Parts
-- added before this are coupled, so nothing changes for them.
ALTER TABLE application_parts ADD COLUMN IF NOT EXISTS release_mode TEXT NOT NULL DEFAULT '';
UPDATE application_parts SET release_mode = 'coupled' WHERE kind = 'site' AND release_mode = '';
ALTER TABLE application_parts DROP CONSTRAINT IF EXISTS application_parts_release_mode_check;
ALTER TABLE application_parts ADD CONSTRAINT application_parts_release_mode_check
    CHECK ((kind = 'site' AND release_mode IN ('linked', 'coupled')) OR (kind <> 'site' AND release_mode = ''));

-- The contract version an application declares, raised by hand when a release
-- breaks what its front ends call; 0 declares none.
ALTER TABLE applications ADD COLUMN IF NOT EXISTS contract_version INT NOT NULL DEFAULT 0;
ALTER TABLE applications DROP CONSTRAINT IF EXISTS applications_contract_version_check;
ALTER TABLE applications ADD CONSTRAINT applications_contract_version_check CHECK (contract_version >= 0);

-- What a site release's _application file declared: the application it is a
-- front end of and the contract versions it works with; '' for none.
ALTER TABLE site_releases ADD COLUMN IF NOT EXISTS contract TEXT NOT NULL DEFAULT '';

ALTER TABLE site_releases DROP COLUMN IF EXISTS contract;
ALTER TABLE applications DROP CONSTRAINT IF EXISTS applications_contract_version_check;
ALTER TABLE applications DROP COLUMN IF EXISTS contract_version;
ALTER TABLE application_parts DROP CONSTRAINT IF EXISTS application_parts_release_mode_check;
ALTER TABLE application_parts DROP COLUMN IF EXISTS release_mode;

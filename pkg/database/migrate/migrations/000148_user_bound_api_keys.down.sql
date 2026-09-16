DROP INDEX IF EXISTS idx_api_keys_user_email;
ALTER TABLE api_keys DROP COLUMN IF EXISTS user_email;
ALTER TABLE users DROP COLUMN IF EXISTS roles_seen_at;
ALTER TABLE users DROP COLUMN IF EXISTS roles;
ALTER TABLE identity_subjects DROP COLUMN IF EXISTS from_person;

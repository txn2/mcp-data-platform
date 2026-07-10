DROP INDEX IF EXISTS idx_oauth_clients_dcr_unused;
ALTER TABLE oauth_clients DROP COLUMN IF EXISTS last_used_at;
ALTER TABLE oauth_clients DROP COLUMN IF EXISTS dcr;

ALTER TABLE site_typecheck_state DROP CONSTRAINT IF EXISTS site_typecheck_state_site_id_fkey;
ALTER TABLE site_typechecks DROP CONSTRAINT IF EXISTS site_typechecks_site_id_fkey;

-- A deleted site's type checks and incremental state go with it (#2828):
-- 000268 gave them no foreign key, so a site's delete left both behind. Rows
-- whose site is already gone are removed first, then each table cascades from
-- sites like every other site table.
DELETE FROM site_typechecks t WHERE NOT EXISTS (SELECT 1 FROM sites s WHERE s.id = t.site_id);
DELETE FROM site_typecheck_state t WHERE NOT EXISTS (SELECT 1 FROM sites s WHERE s.id = t.site_id);

ALTER TABLE site_typechecks DROP CONSTRAINT IF EXISTS site_typechecks_site_id_fkey;
ALTER TABLE site_typechecks
    ADD CONSTRAINT site_typechecks_site_id_fkey FOREIGN KEY (site_id) REFERENCES sites(id) ON DELETE CASCADE;

ALTER TABLE site_typecheck_state DROP CONSTRAINT IF EXISTS site_typecheck_state_site_id_fkey;
ALTER TABLE site_typecheck_state
    ADD CONSTRAINT site_typecheck_state_site_id_fkey FOREIGN KEY (site_id) REFERENCES sites(id) ON DELETE CASCADE;

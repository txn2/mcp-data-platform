DELETE FROM application_run_roles r USING applications a
 WHERE a.id = r.application_id AND r.schema_name <> a.schema_name;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'application_run_roles_pkey'
                AND conrelid = 'application_run_roles'::regclass
                AND array_length(conkey, 1) = 2) THEN
        ALTER TABLE application_run_roles DROP CONSTRAINT application_run_roles_pkey;
        ALTER TABLE application_run_roles ADD PRIMARY KEY (application_id);
    END IF;
END $$;

ALTER TABLE application_environments DROP COLUMN IF EXISTS schema_name;

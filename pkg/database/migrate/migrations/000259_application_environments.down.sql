ALTER TABLE script_webhook_handlers DROP COLUMN IF EXISTS environment;
ALTER TABLE script_schedules DROP COLUMN IF EXISTS environment;
ALTER TABLE script_runs DROP COLUMN IF EXISTS environment;
DROP TABLE IF EXISTS application_variables;
DROP TABLE IF EXISTS application_environments;

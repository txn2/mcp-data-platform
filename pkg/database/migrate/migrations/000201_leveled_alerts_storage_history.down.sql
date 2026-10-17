ALTER TABLE webhook_spool_state DROP COLUMN IF EXISTS failing_since;
ALTER TABLE webhook_spool_state DROP COLUMN IF EXISTS consecutive_failures;
ALTER TABLE webhook_spool_state DROP COLUMN IF EXISTS fs_free_bytes;
ALTER TABLE webhook_spool_state DROP COLUMN IF EXISTS fs_total_bytes;
DROP TABLE IF EXISTS storage_samples;
DROP TABLE IF EXISTS alert_conditions;

ALTER TABLE webhook_windows DROP COLUMN IF EXISTS compacted_bytes;
ALTER TABLE webhook_windows DROP COLUMN IF EXISTS raw_segments;
ALTER TABLE webhook_windows DROP COLUMN IF EXISTS raw_bytes;
DROP TABLE IF EXISTS webhook_compactions;
DROP TABLE IF EXISTS webhook_replica_minutes;
ALTER TABLE webhook_ack_latency DROP COLUMN IF EXISTS put_ms_sum;
ALTER TABLE webhook_ack_latency DROP COLUMN IF EXISTS mark_ms_sum;
ALTER TABLE webhook_ack_latency DROP COLUMN IF EXISTS buffer_ms_sum;
DROP TABLE IF EXISTS webhook_status_counts;

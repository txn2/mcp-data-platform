DROP FUNCTION IF EXISTS webhook_ack_counts(BIGINT[], BIGINT[]);
ALTER TABLE webhook_ack_latency ALTER COLUMN buckets DROP DEFAULT;
ALTER TABLE webhook_ack_latency DROP COLUMN IF EXISTS ack_buckets;

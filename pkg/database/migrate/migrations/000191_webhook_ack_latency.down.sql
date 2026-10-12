ALTER TABLE webhook_rejections
    DROP COLUMN IF EXISTS event_ids,
    DROP COLUMN IF EXISTS request_hash;
DROP TABLE IF EXISTS webhook_ack_latency;

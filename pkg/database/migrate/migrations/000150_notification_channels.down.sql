DROP INDEX IF EXISTS idx_notifications_channel_created_at;
ALTER TABLE notifications DROP COLUMN IF EXISTS channel;
DROP TABLE IF EXISTS notification_channels;

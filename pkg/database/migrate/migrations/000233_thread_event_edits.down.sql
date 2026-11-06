DROP INDEX IF EXISTS idx_notifications_event;
DROP INDEX IF EXISTS idx_notifications_recipient_unread;
ALTER TABLE notifications DROP COLUMN IF EXISTS read_at;
DROP TABLE IF EXISTS portal_thread_event_revisions;
ALTER TABLE portal_thread_events
    DROP COLUMN IF EXISTS deleted_by,
    DROP COLUMN IF EXISTS deleted_at,
    DROP COLUMN IF EXISTS edited_at;

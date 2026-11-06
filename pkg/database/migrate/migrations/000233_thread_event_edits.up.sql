-- Editing and deleting a feedback message (#2514), and the in-portal unread
-- list of notifications (#2512).
--
-- A comment event can be edited by its author and deleted by its author, the
-- target's owner or an administrator. The row stays: deleted_at marks it and
-- every read returns it without its text, so later replies still read in
-- order. The text before each edit is kept in portal_thread_event_revisions,
-- and both go with the thread when it is purged (ON DELETE CASCADE from
-- portal_threads through portal_thread_events).
ALTER TABLE portal_thread_events
    ADD COLUMN IF NOT EXISTS edited_at  TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS deleted_by TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS portal_thread_event_revisions (
    id         BIGSERIAL   PRIMARY KEY,
    event_id   TEXT        NOT NULL REFERENCES portal_thread_events(id) ON DELETE CASCADE,
    body       TEXT        NOT NULL DEFAULT '',
    metadata   JSONB       NOT NULL DEFAULT '{}'::jsonb,
    edited_by  TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_portal_thread_event_revisions_event
    ON portal_thread_event_revisions(event_id, created_at);

-- A notification a person has read in the portal. NULL is unread. The rows
-- written before the portal listed them are read: nobody was shown them as
-- new, and an upgrade must not raise every recent notification at once.
ALTER TABLE notifications ADD COLUMN IF NOT EXISTS read_at TIMESTAMPTZ;
UPDATE notifications SET read_at = created_at WHERE read_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_notifications_recipient_unread
    ON notifications(recipient) WHERE read_at IS NULL;

-- The rows one thread message produced, found when the message is deleted so
-- its text leaves the queue too.
CREATE INDEX IF NOT EXISTS idx_notifications_event
    ON notifications((payload ->> 'event_id')) WHERE payload ? 'event_id';

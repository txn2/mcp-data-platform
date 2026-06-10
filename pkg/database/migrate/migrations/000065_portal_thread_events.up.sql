-- Typed event timeline for portal_threads (#601). A `comment` is one event type
-- among many; status changes, resolutions, ratings, approvals, and the
-- knowledge-link events (Phase 2) all share this timeline. parent_event_id
-- supports threaded replies. metadata carries event-specific payloads
-- (insight_id, changeset_id, old/new status).
CREATE TABLE IF NOT EXISTS portal_thread_events (
    id              TEXT PRIMARY KEY,
    thread_id       TEXT NOT NULL REFERENCES portal_threads(id) ON DELETE CASCADE,
    event_type      TEXT NOT NULL,  -- comment|status_change|resolution|rating|approval|rejection|validation_request|validation_result|insight_linked|changeset_linked
    author_id       TEXT NOT NULL,
    author_email    TEXT NOT NULL,
    body            TEXT,
    rating          INTEGER,
    parent_event_id TEXT REFERENCES portal_thread_events(id),
    metadata        JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_portal_thread_events_thread_id ON portal_thread_events(thread_id, created_at);

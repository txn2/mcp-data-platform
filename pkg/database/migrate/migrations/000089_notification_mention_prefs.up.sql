-- Per-user opt-out for @-mention notifications (#627).
--
-- Mentions are their own category rather than part of comments: someone who
-- muted general thread chatter should still hear when a comment addresses them
-- by name. Defaults TRUE, matching the platform convention that a user-facing
-- feature is on unless its owner turns it off, and matching the existing
-- shares/comments columns.
ALTER TABLE user_notification_prefs
    ADD COLUMN IF NOT EXISTS mentions_enabled BOOLEAN NOT NULL DEFAULT TRUE;

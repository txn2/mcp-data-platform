-- The notification history views (admin monitoring and the user's own
-- activity screen) page newest-first over the retention window. The worker's
-- existing indexes serve the claim predicate, not this ordering, so both
-- listings would sort the whole window on every page.
--
-- The recipient-scoped index also serves the self-scoped user view, which is
-- always filtered to one address; idx_notifications_recipient stays because
-- the digest claim query matches on recipient alone.
CREATE INDEX IF NOT EXISTS idx_notifications_created_at
    ON notifications(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_notifications_recipient_created_at
    ON notifications(recipient, created_at DESC);

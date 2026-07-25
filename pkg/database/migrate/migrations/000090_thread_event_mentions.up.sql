-- Index the @-mentions recorded on a thread event (#627).
--
-- A mention is stored as a lower-cased address in the event's existing metadata
-- column ({"mentions": ["marcus.johnson@example.com"]}) rather than in a table
-- of its own: a mention has no state beyond the event carrying it, so a join
-- table would add a row lifecycle with nothing to hold.
--
-- The index is on the mentions array itself, with jsonb_path_ops, because the
-- only query is the "mentions of me" inbox: metadata -> 'mentions' @> '["..."]'.
-- Indexing the whole metadata document would be larger and slower for exactly
-- one supported operator.
CREATE INDEX IF NOT EXISTS idx_portal_thread_events_mentions
    ON portal_thread_events USING GIN ((metadata -> 'mentions') jsonb_path_ops);

-- Reverses 000040. Drops the audit history table and its indexes.
-- Operators rolling back to migration <40 lose the auth-event history
-- they accumulated under 40+; the data has no other consumer so a
-- rollback drop is acceptable.

DROP INDEX IF EXISTS idx_connection_auth_events_occurred;
DROP INDEX IF EXISTS idx_connection_auth_events_conn_occurred;
DROP TABLE IF EXISTS connection_auth_events;

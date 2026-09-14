-- A refused jwt_bearer assertion, told the same way as a revoked credential
-- (#1734).
--
-- An oauth_grant jwt_bearer connection holds no stored credential to discard:
-- it signs an assertion with a key the operator registered and exchanges it at
-- the upstream's token endpoint on every expiry. When the upstream refuses it
-- (an unapproved key or integration user, or clock skew), every call through
-- the connection fails until the upstream is fixed, which is the same incident
-- this table exists to push. Two things differ, and both are columns so the
-- escalation, which reads the row rather than the event that opened it, says
-- the same thing the first alert did.
--
-- signed_assertion marks the row as a refused assertion rather than a revoked
-- authorization. Nobody authorized it, so authorized_by is empty and the first
-- alert goes to the operator's recipients; there is nothing to reconnect, so
-- the mail sends the reader to the upstream instead. The row is deleted by the
-- next exchange the upstream accepts rather than by the OAuth callback.
--
-- description is the upstream's error_description, bounded by the platform
-- before it is written. The refresh path's rows leave it empty.
ALTER TABLE connection_auth_alerts
    ADD COLUMN IF NOT EXISTS signed_assertion BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS description      TEXT    NOT NULL DEFAULT '';

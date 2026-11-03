-- 000225: the listener of each postgres webhook source (#2408).
--
-- A webhook source whose input is postgres reads an application database:
-- the notifications sent on its channels, or the rows of an outbox table.
-- One replica listens per source, under an advisory lock, and records here
-- that it does, how many events it filed and refused, and the last failure,
-- so the source's page on any replica shows who is listening and how it is
-- going. Nothing of a payload is kept here.
CREATE TABLE IF NOT EXISTS webhook_pg_listeners (
    source          TEXT        PRIMARY KEY REFERENCES webhook_sources(name) ON DELETE CASCADE,
    replica         TEXT        NOT NULL DEFAULT '',
    listening_since TIMESTAMPTZ,
    last_event_at   TIMESTAMPTZ,
    delivered       BIGINT      NOT NULL DEFAULT 0,
    rejected        BIGINT      NOT NULL DEFAULT 0,
    last_error      TEXT        NOT NULL DEFAULT '',
    last_error_at   TIMESTAMPTZ
);

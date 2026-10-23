-- 000218: where an imap webhook source has read each of its folders to (#2247).
--
-- A webhook source whose input is imap is a mailbox the platform polls, each
-- message one event. Each folder carries its own watermark: the UIDVALIDITY it
-- was read under and the highest UID read, so a poll asks only for newer mail
-- and a renumbered folder is read again from its start (an event's id is the
-- message's Message-ID, so a re-read files nothing twice). The counts are the
-- mail the sender rules withheld, which is never stored, and the last poll's
-- outcome, which the source's page shows.
CREATE TABLE IF NOT EXISTS webhook_imap_marks (
    source                 TEXT        NOT NULL REFERENCES webhook_sources(name) ON DELETE CASCADE,
    folder                 TEXT        NOT NULL,
    uid_validity           BIGINT      NOT NULL DEFAULT 0,
    last_uid               BIGINT      NOT NULL DEFAULT 0,
    stored                 BIGINT      NOT NULL DEFAULT 0,
    hidden_not_allowed     BIGINT      NOT NULL DEFAULT 0,
    hidden_unauthenticated BIGINT      NOT NULL DEFAULT 0,
    last_polled_at         TIMESTAMPTZ,
    last_error             TEXT        NOT NULL DEFAULT '',
    PRIMARY KEY (source, folder)
);

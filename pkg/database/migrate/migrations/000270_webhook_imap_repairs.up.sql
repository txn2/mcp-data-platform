-- 000270: the one-time repair of an imap source's attachments (#2836).
--
-- Before #2836 a message's attachments were saved in a folder named by the
-- server's numbering, so a renumbered folder filed a second copy of every
-- attachment. The repair moves each folder an event names to the message's
-- folder of the new form and deletes the later copies, once per source,
-- before its first poll after the upgrade. A row here is that it ran: when,
-- what it moved, what it deleted and what it kept, which the source's page
-- shows. The report holds folder paths only, which are the platform's own
-- (a day and a number or a hash), never a sender-written file name.
CREATE TABLE IF NOT EXISTS webhook_imap_repairs (
    source        TEXT        PRIMARY KEY REFERENCES webhook_sources(name) ON DELETE CASCADE,
    repaired_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    moved         INTEGER     NOT NULL DEFAULT 0,
    removed       INTEGER     NOT NULL DEFAULT 0,
    removed_files INTEGER     NOT NULL DEFAULT 0,
    kept          INTEGER     NOT NULL DEFAULT 0,
    report        JSONB       NOT NULL DEFAULT '{}'::jsonb
);

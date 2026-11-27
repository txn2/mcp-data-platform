-- 000271: the imap attachment repair's version and its collapsed copies (#2842).
--
-- 000270's repair removed a renumbered copy only when a kept event named one of
-- the copies; for a message whose event had expired every copy was kept. The
-- second version also collapses copies no event names, the same folder and UID
-- under different UIDVALIDITY values on one day holding the same files, and
-- counts them apart from the folders it keeps.
--
-- version is the repair that last ran. A row 000270 wrote is version 1, so a
-- source already repaired is repaired again, once, by version 2. A second pass
-- adds its moves, removals and collapses to the counts already recorded, and
-- replaces kept, which is what is left.
ALTER TABLE webhook_imap_repairs
    ADD COLUMN IF NOT EXISTS version         INTEGER NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS collapsed       INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS collapsed_files INTEGER NOT NULL DEFAULT 0;

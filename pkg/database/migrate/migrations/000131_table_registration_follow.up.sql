-- A registration pins a directory (#1536). Registration.Location is the
-- directory of the source's head key at the moment of registration, and every
-- revision of a managed resource and every version of a portal asset moves the
-- head to a new directory, so a table over a file something refreshes served
-- the file as it was on the day it was registered, from the first refresh on.
--
-- follow records that this registration is moved to the source's new head
-- directory when a revision or version is written. It defaults to true, for
-- the rows that exist and for a registration made without saying: a person
-- or an agent that replaces a file expects the table over it to read the new
-- contents, and a table that stays on the version it was registered over is
-- the choice, made by registering with follow off, for a report that must
-- keep returning the same rows until somebody decides otherwise.
--
-- follow_error is why the last follow of this registration did not move it,
-- and is empty while the registration is where the file is. A follow never
-- fails the write that triggered it -- the file changed, and that write
-- succeeded -- so the failure has to be kept on the registration for the
-- listing to say what is behind and why, rather than only in the log of the
-- write that met it. A successful follow clears it.
ALTER TABLE table_registrations
    ADD COLUMN IF NOT EXISTS follow BOOLEAN NOT NULL DEFAULT true,
    ADD COLUMN IF NOT EXISTS follow_error TEXT NOT NULL DEFAULT '';

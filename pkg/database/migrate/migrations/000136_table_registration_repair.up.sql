-- A registration made with repair saves a corrected version of a file that
-- cannot be read as a table the way it is stored, and registers that version
-- (#1441). The choice was made once and then forgotten, so a following
-- registration met the same defect on the next version of the file and stopped
-- there (#1577): a source that repeats a correctable defect on a schedule -- a
-- weekly spreadsheet export, a script writing a CSV whose text fields carry
-- paragraph breaks -- stranded its table on the day after it was registered,
-- and a producer that reads its own rows back through the table then regressed
-- its own file from the stale version.
--
-- repair records that this registration corrects its file. A follow that meets
-- a correctable defect on a new version saves the corrected bytes as the
-- file's next version, under the registrant, and moves the table onto that
-- version. It defaults to false, for the rows that exist and for a
-- registration made without asking: correcting somebody's file is not
-- something to do on the way to something else they asked for, which is the
-- same rule the registration itself applies.
ALTER TABLE table_registrations
    ADD COLUMN IF NOT EXISTS repair BOOLEAN NOT NULL DEFAULT false;

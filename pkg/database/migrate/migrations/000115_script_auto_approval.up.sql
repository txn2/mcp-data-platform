-- 000115: an approval nobody reviewed, marked as one (#1367)
--
-- A personal script -- one only its owner can see and only its owner can run --
-- is approved on save, with the grant minted from what a static read of its
-- source reaches rather than asked of a reviewer. The roles it presents are
-- still copied from the version's author, so nothing about the authority
-- ceiling changes; what changes is that no second person is involved.
--
-- That has to be visible in the history. approved_by keeps naming the owner,
-- because they are accountable for the script either way, and this column is
-- what separates their AUTHORSHIP from somebody's DECISION: an operator asking
-- "which of these did anyone actually look at" reads this and gets an answer.
-- The run's audit event carries the same fact, taken from this column.
--
-- FALSE for every existing row is correct rather than merely convenient: every
-- approval recorded before this migration was made by a person through the
-- review surface, which was the only path that existed.
ALTER TABLE script_versions
    ADD COLUMN IF NOT EXISTS auto_approved BOOLEAN NOT NULL DEFAULT FALSE;

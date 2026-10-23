-- 000218: whether a managed script that reads mail may act on it (#2247).
--
-- A message is written by whoever sent it, so a script that reads mail and
-- then writes something is a script a sender can steer. With mail_actions
-- off, a run that has called a mail_* tool is refused every later write and
-- every output; the owner turns it on knowingly, and the script's contract
-- shows it.
ALTER TABLE scripts
    ADD COLUMN IF NOT EXISTS mail_actions BOOLEAN NOT NULL DEFAULT FALSE;

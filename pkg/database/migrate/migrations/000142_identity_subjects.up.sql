-- The subject each address authenticates as (#1677).
--
-- A managed-resource user library is keyed by subject: a session files there
-- by its own subject, and so does the portal's upload. A managed-script run
-- acts for its version's author and knew that person only by address, so it
-- filed the same path in a second library keyed by the address, and one person
-- had two files at one path. This table is how a run learns the subject its
-- author's own session uses: the platform records the pair whenever a person
-- authenticates, and the run reads it when it opens its session.
--
-- One row per address, holding the subject most recently seen for it. A
-- person whose subject changes (an identity-provider migration) is followed to
-- the new one on their next authentication.
CREATE TABLE IF NOT EXISTS identity_subjects (
    address  TEXT        PRIMARY KEY,           -- normalized lowercase email
    subject  TEXT        NOT NULL,              -- the authenticated principal's id
    seen_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

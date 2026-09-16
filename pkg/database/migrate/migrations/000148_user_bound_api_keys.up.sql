-- A key can be issued against a person's account (#1759).
--
-- users.roles is the role set the platform last saw a person hold, recorded on
-- every real sign-in. A key bound to that person carries it, which is how a key
-- session and that person's own session reach one persona. The directory still
-- grants nothing: the identity provider decides, and this is the platform's
-- record of what it last said, alongside when it said it.
ALTER TABLE users ADD COLUMN IF NOT EXISTS roles JSONB NOT NULL DEFAULT '[]';
ALTER TABLE users ADD COLUMN IF NOT EXISTS roles_seen_at TIMESTAMPTZ;

-- api_keys.user_email is the person a key authenticates as, and '' is the
-- standalone service key this table has always held. A bound key with no roles
-- of its own carries whatever the person holds, read on every request; a
-- non-empty set is an administrator's narrower override, frozen on the key.
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS user_email TEXT NOT NULL DEFAULT '';

-- Bound keys are read by owner (a person's own key list, and revoking every
-- key a departing person holds); service keys are the rest of the table.
CREATE INDEX IF NOT EXISTS idx_api_keys_user_email ON api_keys(user_email) WHERE user_email <> '';

-- Whether a pair in identity_subjects was recorded from a person's own sign-in
-- (#1759). The table has held one row per address since #1677, upserted by
-- whatever last authenticated at that address -- including an API key, which is
-- recorded so a managed-script run files where its author's sessions file.
--
-- That was harmless while the pair only decided where a run filed its output.
-- It is not harmless now that a key issued against an account authenticates as
-- the subject recorded here: a service key configured with a person's address
-- would overwrite their subject with its own `apikey:<name>`, and that person's
-- bound key would then authenticate as the key rather than as them -- the exact
-- inverse of what this ticket exists to do.
--
-- So the pair now records which kind of principal wrote it. A person's pair is
-- never overwritten by a key's (the upsert refuses), and the resolver behind a
-- bound key answers only from a person's pair. The run path (#1677) still reads
-- either, so a deployment whose people authenticate only by key keeps the
-- behavior it has.
--
-- The default is FALSE, so every row written before this is distrusted by the
-- bound-key resolver until the person signs in again and rewrites it. A row
-- already holding an `apikey:<name>` subject for a human address is exactly
-- what must not be trusted, and there is nothing in the row to tell that case
-- from an honest one. Nothing is lost by being strict: no key was bound to an
-- account before this migration, so there is no behavior to preserve, and the
-- first sign-in after it repairs the row.
ALTER TABLE identity_subjects ADD COLUMN IF NOT EXISTS from_person BOOLEAN NOT NULL DEFAULT FALSE;

-- Distinguish explicitly-created shares from those derived when a viewer signs
-- in through a public link (#601). public_link_login shares are auto-created on
-- first authenticated public-link view (permission=viewer) so the object shows
-- up in the user's portal and they can leave feedback; the origin lets these
-- derived grants be identified and cascade-revoked with the public link later.
ALTER TABLE portal_shares ADD COLUMN IF NOT EXISTS origin TEXT NOT NULL DEFAULT 'explicit'; -- explicit|public_link_login

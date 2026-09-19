-- 000149: thumbnails the platform renders itself (#1787).
--
-- A tile was captured in whichever portal tab happened to be open, by a
-- JavaScript reimplementation of CSS painting, and was wrong wherever that
-- reimplementation fell short. The platform now renders every tile in a
-- headless browser beside it. These columns are what a server-side renderer
-- needs that a browser tab never kept.

-- thumbnail_renderer is the generation of the renderer that drew the tile.
-- Every tile that exists today was drawn by the old capturer (0), so each is
-- offered to the new renderer once while the image it has keeps being served:
-- an old picture until the new one lands, never an icon in between. A later
-- change to how tiles are drawn re-renders the library by raising the number
-- the store asks for.
--
-- thumbnail_failure and its date record a document the renderer could not
-- draw and why. The capturer kept that in the memory of one tab, so a document
-- no browser could draw was retried by every tab that opened, forever, and
-- nobody could see why it had no tile. A failure holds until the document
-- changes or someone asks for the tile again.
--
-- thumbnail_claimed_until is the lease a replica takes on a row while it
-- renders it, so two replicas never draw the same document twice.
ALTER TABLE portal_assets
    ADD COLUMN IF NOT EXISTS thumbnail_renderer SMALLINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS thumbnail_failure TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS thumbnail_failed_version INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS thumbnail_claimed_until TIMESTAMPTZ;

ALTER TABLE resources
    ADD COLUMN IF NOT EXISTS thumbnail_renderer SMALLINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS thumbnail_failure TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS thumbnail_failed_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS thumbnail_claimed_until TIMESTAMPTZ;

-- A collection's tile is composed from its members' tiles. thumbnail_source
-- names exactly which member tiles it was composed from, so it is composed
-- again when a member is added or removed or a member's tile is redrawn --
-- which the capturer never did: it drew a collection's tile once, when it had
-- none, and kept it however wrong it became.
ALTER TABLE portal_collections
    ADD COLUMN IF NOT EXISTS thumbnail_source TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS thumbnail_claimed_until TIMESTAMPTZ;

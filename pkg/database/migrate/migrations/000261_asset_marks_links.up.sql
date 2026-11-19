-- Asset status, the Verified badge and links between assets (#2502, #2503, #2432).
--
-- A status says how far along an asset is: draft, published or deprecated,
-- with an optional note a deprecated asset shows its readers. It is
-- informational and changes no access. An asset with no row has no status,
-- which is every asset until someone sets one.
CREATE TABLE IF NOT EXISTS portal_asset_status (
    asset_id TEXT PRIMARY KEY REFERENCES portal_assets(id) ON DELETE CASCADE,
    status   TEXT NOT NULL CHECK (status IN ('draft', 'published', 'deprecated')),
    note     TEXT NOT NULL DEFAULT '',
    set_by   TEXT NOT NULL,
    set_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_portal_asset_status_status ON portal_asset_status (status);

-- A verification records who checked an asset, the version they checked, an
-- optional note and an optional expiry. A row past its expiry reads as no
-- verification; reminded_at is when the reminder before the expiry was sent,
-- so it is sent once.
CREATE TABLE IF NOT EXISTS portal_asset_verifications (
    asset_id         TEXT PRIMARY KEY REFERENCES portal_assets(id) ON DELETE CASCADE,
    verified_by      TEXT NOT NULL,
    verified_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    verified_version INTEGER NOT NULL,
    note             TEXT NOT NULL DEFAULT '',
    expires_at       TIMESTAMPTZ,
    reminded_at      TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_portal_asset_verifications_expires
    ON portal_asset_verifications (expires_at) WHERE expires_at IS NOT NULL AND reminded_at IS NULL;

-- Every change to an asset's status or verification: who, when, from, to.
CREATE TABLE IF NOT EXISTS portal_asset_mark_events (
    id         BIGSERIAL PRIMARY KEY,
    asset_id   TEXT NOT NULL REFERENCES portal_assets(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL CHECK (kind IN ('status', 'verification')),
    from_value TEXT NOT NULL DEFAULT '',
    to_value   TEXT NOT NULL DEFAULT '',
    note       TEXT NOT NULL DEFAULT '',
    actor      TEXT NOT NULL,
    at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_portal_asset_mark_events_asset ON portal_asset_mark_events (asset_id, at DESC);

-- The assets each asset's CURRENT content links to (#2432), written every time
-- a version is recorded, so the target can list the assets linking to it. A
-- link grants nothing: the list is read through the reader's own reach, and
-- the target need not exist (to_asset_id has no foreign key).
CREATE TABLE IF NOT EXISTS portal_asset_links (
    from_asset_id TEXT NOT NULL REFERENCES portal_assets(id) ON DELETE CASCADE,
    to_asset_id   TEXT NOT NULL,
    PRIMARY KEY (from_asset_id, to_asset_id)
);

CREATE INDEX IF NOT EXISTS idx_portal_asset_links_to ON portal_asset_links (to_asset_id);

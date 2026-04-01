-- Collections: curated, ordered groups of assets with sections and descriptions.
CREATE TABLE portal_collections (
    id               TEXT        PRIMARY KEY,
    owner_id         TEXT        NOT NULL,
    owner_email      TEXT        NOT NULL DEFAULT '',
    name             TEXT        NOT NULL,
    description      TEXT        NOT NULL DEFAULT '',
    thumbnail_s3_key TEXT        NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at       TIMESTAMPTZ
);

CREATE INDEX idx_portal_collections_owner_id ON portal_collections(owner_id);
CREATE INDEX idx_portal_collections_deleted_at ON portal_collections(deleted_at) WHERE deleted_at IS NULL;

-- Sections within a collection, each with a title, description, and position.
CREATE TABLE portal_collection_sections (
    id            TEXT        PRIMARY KEY,
    collection_id TEXT        NOT NULL REFERENCES portal_collections(id) ON DELETE CASCADE,
    title         TEXT        NOT NULL DEFAULT '',
    description   TEXT        NOT NULL DEFAULT '',
    position      INTEGER     NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_collection_sections_collection_id ON portal_collection_sections(collection_id);

-- Items within a section, referencing assets with a position.
CREATE TABLE portal_collection_items (
    id         TEXT        PRIMARY KEY,
    section_id TEXT        NOT NULL REFERENCES portal_collection_sections(id) ON DELETE CASCADE,
    asset_id   TEXT        NOT NULL REFERENCES portal_assets(id),
    position   INTEGER     NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_collection_items_section_id ON portal_collection_items(section_id);
CREATE INDEX idx_collection_items_asset_id ON portal_collection_items(asset_id);

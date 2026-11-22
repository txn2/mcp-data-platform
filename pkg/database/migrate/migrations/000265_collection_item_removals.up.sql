-- #2752: the collection item sweep removes from a collection each item its
-- owner cannot open, since a collection's share opens every asset it holds.
-- Each removal is recorded here: the collection, the asset, how many of the
-- collection's sections held it, and when. No asset content is kept, and the
-- row outlives the collection and the asset, so neither is a foreign key.
CREATE TABLE IF NOT EXISTS portal_collection_item_removals (
    id            BIGSERIAL   PRIMARY KEY,
    collection_id TEXT        NOT NULL,
    asset_id      TEXT        NOT NULL,
    items         INT         NOT NULL,
    removed_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_collection_item_removals_collection
    ON portal_collection_item_removals (collection_id, removed_at);

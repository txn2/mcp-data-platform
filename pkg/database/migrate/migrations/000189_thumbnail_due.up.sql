-- 000189: the renderer's work as indexed state (#2084).
--
-- The thumbnail worker found the documents owed a tile by evaluating its whole
-- rule (content type against every spelling of every drawable family, the
-- source bound, the tile's age, the renderer generation) on every row of
-- resources, portal_assets and portal_collections, every five seconds, on
-- every replica, whether anything was owed or not. On 18,628 resources that
-- was 1.35 s per poll, and it grows with the library.
--
-- thumbnail_due_at is when a row next needs that rule evaluated: set when the
-- row is written, NULL once the rule has said it is owed nothing. The worker
-- reads only rows that are due, through a partial index that holds nothing
-- else, so a library with nothing owed costs an empty index probe however
-- large it is. A claimed or held row carries its lease or hold end here, so
-- it comes due again when that passes.
ALTER TABLE resources ADD COLUMN IF NOT EXISTS thumbnail_due_at TIMESTAMPTZ;
ALTER TABLE portal_assets ADD COLUMN IF NOT EXISTS thumbnail_due_at TIMESTAMPTZ;
ALTER TABLE portal_collections ADD COLUMN IF NOT EXISTS thumbnail_due_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_resources_thumbnail_due
    ON resources (thumbnail_due_at) WHERE thumbnail_due_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_portal_assets_thumbnail_due
    ON portal_assets (thumbnail_due_at) WHERE thumbnail_due_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_portal_collections_thumbnail_due
    ON portal_collections (thumbnail_due_at) WHERE thumbnail_due_at IS NOT NULL;

-- An insert, or an update of a column the owed rule reads that does not set
-- thumbnail_due_at itself, makes the row due now: a content change, a cleared
-- tile, a recorded tile or failure (which the rule then reads as owed
-- nothing). Doing it here rather than in each writer means no write path can
-- leave a changed document without a tile. The columns are named so a write
-- the rule never reads (a resource's last_read_at, stamped on every read; an
-- asset's embedding) neither marks the row nor wakes a worker. The worker's
-- own claim, hold and clear write no such column and set thumbnail_due_at
-- themselves. A row that becomes due wakes the worker on every replica.
CREATE OR REPLACE FUNCTION thumbnail_mark_due() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.thumbnail_due_at IS NULL THEN
            NEW.thumbnail_due_at := now();
            PERFORM pg_notify('thumbnail_due', TG_TABLE_NAME);
        END IF;
    ELSIF NEW.thumbnail_due_at IS NOT DISTINCT FROM OLD.thumbnail_due_at
          AND (OLD.thumbnail_due_at IS NULL OR OLD.thumbnail_due_at > now()) THEN
        NEW.thumbnail_due_at := now();
        PERFORM pg_notify('thumbnail_due', TG_TABLE_NAME);
    END IF;
    RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS trg_resources_thumbnail_due ON resources;
CREATE TRIGGER trg_resources_thumbnail_due
    BEFORE INSERT OR UPDATE OF mime_type, size_bytes, updated_at, thumbnail_s3_key, thumbnail_dark_s3_key,
        thumbnail_captured_at, thumbnail_dark_captured_at, thumbnail_renderer, thumbnail_failed_at
    ON resources
    FOR EACH ROW EXECUTE FUNCTION thumbnail_mark_due();

DROP TRIGGER IF EXISTS trg_portal_assets_thumbnail_due ON portal_assets;
CREATE TRIGGER trg_portal_assets_thumbnail_due
    BEFORE INSERT OR UPDATE OF content_type, size_bytes, current_version, deleted_at, thumbnail_s3_key,
        thumbnail_dark_s3_key, thumbnail_version, thumbnail_dark_version, thumbnail_renderer, thumbnail_failed_version
    ON portal_assets
    FOR EACH ROW EXECUTE FUNCTION thumbnail_mark_due();

DROP TRIGGER IF EXISTS trg_portal_collections_thumbnail_due ON portal_collections;
CREATE TRIGGER trg_portal_collections_thumbnail_due
    BEFORE INSERT OR UPDATE OF deleted_at, thumbnail_s3_key, thumbnail_source, thumbnail_failure, thumbnail_failed_source
    ON portal_collections
    FOR EACH ROW EXECUTE FUNCTION thumbnail_mark_due();

-- A collection's mosaic is drawn from its members' tiles, so it comes due
-- when a section or an item is added, moved or removed, and when a member's
-- tile is drawn again or the member is deleted.
CREATE OR REPLACE FUNCTION thumbnail_mark_collections_due(ids TEXT[]) RETURNS void
    LANGUAGE plpgsql AS $$
BEGIN
    UPDATE portal_collections SET thumbnail_due_at = now()
     WHERE id = ANY(ids) AND (thumbnail_due_at IS NULL OR thumbnail_due_at > now());
    IF FOUND THEN
        PERFORM pg_notify('thumbnail_due', 'portal_collections');
    END IF;
END
$$;

CREATE OR REPLACE FUNCTION thumbnail_section_changed() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        PERFORM thumbnail_mark_collections_due(ARRAY[NEW.collection_id]);
    ELSIF TG_OP = 'DELETE' THEN
        PERFORM thumbnail_mark_collections_due(ARRAY[OLD.collection_id]);
    ELSE
        PERFORM thumbnail_mark_collections_due(ARRAY[OLD.collection_id, NEW.collection_id]);
    END IF;
    RETURN NULL;
END
$$;

DROP TRIGGER IF EXISTS trg_collection_sections_thumbnail_due ON portal_collection_sections;
CREATE TRIGGER trg_collection_sections_thumbnail_due
    AFTER INSERT OR UPDATE OR DELETE ON portal_collection_sections
    FOR EACH ROW EXECUTE FUNCTION thumbnail_section_changed();

CREATE OR REPLACE FUNCTION thumbnail_item_changed() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        PERFORM thumbnail_mark_collections_due(ARRAY(
            SELECT collection_id FROM portal_collection_sections WHERE id = NEW.section_id));
    ELSIF TG_OP = 'DELETE' THEN
        PERFORM thumbnail_mark_collections_due(ARRAY(
            SELECT collection_id FROM portal_collection_sections WHERE id = OLD.section_id));
    ELSE
        PERFORM thumbnail_mark_collections_due(ARRAY(
            SELECT collection_id FROM portal_collection_sections WHERE id IN (OLD.section_id, NEW.section_id)));
    END IF;
    RETURN NULL;
END
$$;

DROP TRIGGER IF EXISTS trg_collection_items_thumbnail_due ON portal_collection_items;
CREATE TRIGGER trg_collection_items_thumbnail_due
    AFTER INSERT OR UPDATE OR DELETE ON portal_collection_items
    FOR EACH ROW EXECUTE FUNCTION thumbnail_item_changed();

CREATE OR REPLACE FUNCTION thumbnail_member_changed() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    PERFORM thumbnail_mark_collections_due(ARRAY(
        SELECT s.collection_id FROM portal_collection_items ci
          JOIN portal_collection_sections s ON s.id = ci.section_id
         WHERE ci.asset_id = NEW.id));
    RETURN NULL;
END
$$;

DROP TRIGGER IF EXISTS trg_portal_assets_member_tile ON portal_assets;
CREATE TRIGGER trg_portal_assets_member_tile
    AFTER UPDATE ON portal_assets
    FOR EACH ROW
    WHEN (OLD.thumbnail_s3_key IS DISTINCT FROM NEW.thumbnail_s3_key
       OR OLD.thumbnail_version IS DISTINCT FROM NEW.thumbnail_version
       OR OLD.thumbnail_dark_version IS DISTINCT FROM NEW.thumbnail_dark_version
       OR OLD.thumbnail_renderer IS DISTINCT FROM NEW.thumbnail_renderer
       OR OLD.deleted_at IS DISTINCT FROM NEW.deleted_at)
    EXECUTE FUNCTION thumbnail_member_changed();

-- When the rule itself changes (a new renderer generation, a changed source
-- bound, a newly drawable type), rows the old rule passed over may be owed.
-- The worker then marks every row of the table due once, in chunks, and
-- records here the rule it marked them under, so a restart does not do it
-- again. No row is recorded yet, so the first start marks every existing row.
CREATE TABLE IF NOT EXISTS thumbnail_sweeps (
    subject  TEXT        PRIMARY KEY,
    rule     TEXT        NOT NULL,
    swept_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

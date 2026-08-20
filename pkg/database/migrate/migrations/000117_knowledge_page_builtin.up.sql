-- 000117: built-in knowledge pages (#1390)
--
-- The platform ships its own knowledge pages: embedded in the binary and
-- reconciled into this table at startup, so a release that changes them updates
-- every deployment on its next start. Once reconciled they are ordinary rows —
-- the indexjobs consumer embeds them, search ranks them, fetch dereferences
-- them, the portal renders them — with one difference: builtin marks the row as
-- platform-owned, and the write paths refuse to modify a page carrying it. The
-- soft-delete stays available on a builtin page because deleting one IS the
-- operator's suppression mechanism: the startup reconcile treats a soft-deleted
-- builtin row as "hidden here" and never resurrects it.
--
-- A provenance column rather than a content hash, following prompts.source
-- ('system', 000030) and api_catalog_specs.source_kind ('embedded', 000058):
-- the reconcile detects release changes by comparing the shipped content to the
-- row directly, which is exact because nothing but the reconcile can write a
-- builtin row.

ALTER TABLE portal_knowledge_pages
    ADD COLUMN IF NOT EXISTS builtin BOOLEAN NOT NULL DEFAULT FALSE;

-- The startup reconcile resolves each shipped page by slug: the live row for
-- the slug, else the newest builtin tombstone for it (the hidden case). The
-- live arm is served by idx_portal_knowledge_pages_slug (000070); this partial
-- index serves the tombstone probe without scanning operator deletions.
CREATE INDEX IF NOT EXISTS idx_portal_knowledge_pages_builtin_deleted
    ON portal_knowledge_pages (slug)
    WHERE builtin AND deleted_at IS NOT NULL;

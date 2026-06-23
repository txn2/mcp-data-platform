-- 000071: knowledge page version history
--
-- Mirrors portal_asset_versions (000022): every edit to a page's body/title is
-- snapshotted so a page can be inspected over time and reverted. The body is
-- stored inline here too (pages are text-first and bounded), unlike asset
-- versions which reference S3 keys.

CREATE TABLE IF NOT EXISTS portal_knowledge_page_versions (
    id             TEXT        PRIMARY KEY,
    page_id        TEXT        NOT NULL REFERENCES portal_knowledge_pages(id) ON DELETE CASCADE,
    version        INT         NOT NULL,
    title          TEXT        NOT NULL,
    summary        TEXT        NOT NULL DEFAULT '',
    body           TEXT        NOT NULL DEFAULT '',
    tags           JSONB       NOT NULL DEFAULT '[]'::jsonb,
    created_by     TEXT        NOT NULL DEFAULT '',
    change_summary TEXT        NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (page_id, version)
);

CREATE INDEX IF NOT EXISTS idx_portal_knowledge_page_versions_page_id
    ON portal_knowledge_page_versions (page_id);

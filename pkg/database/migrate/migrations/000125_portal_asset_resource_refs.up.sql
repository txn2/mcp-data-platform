-- portal_asset_resource_refs records the managed resources an asset's content
-- references (#1474): the logo an HTML report shows, the photograph a markdown
-- page embeds, the design element a JSX dashboard draws.
--
-- Before this table the only way to put a graphic in an asset was to write the
-- bytes into the markup, which the agent paid for in output tokens, the asset
-- paid for in stored size, and every retained version paid for again. The row
-- stores a resource id, so the asset carries a reference rather than a copy and
-- replacing the uploaded file updates every asset that names it.
--
-- ref_token is the capability the reference is served under. An asset's content
-- is rendered inside a sandboxed iframe with an opaque origin, and on a public
-- share by a reader with no session at all, so the URL written into the served
-- content cannot depend on the reader's own credentials. The token is minted
-- per (asset, resource) at declaration time and is the whole authorization for
-- the serving route: possession of it is the grant, exactly as possession of a
-- share token is. It is UNIQUE so the route can refuse a token that names a
-- different asset than the path does.
--
-- There is deliberately NO foreign key to resources(id), for the reason
-- prompt_resource_attachments has none: deleting a resource must leave the
-- reference behind so the asset still renders with one image missing, rather
-- than silently losing the record that the report is now incomplete. asset_id
-- cascades, because a reference is meaningless without the asset that declares
-- it.
CREATE TABLE IF NOT EXISTS portal_asset_resource_refs (
    asset_id     TEXT        NOT NULL REFERENCES portal_assets(id) ON DELETE CASCADE,
    resource_id  TEXT        NOT NULL,
    uri          TEXT        NOT NULL,
    ref_token    TEXT        NOT NULL UNIQUE,
    position     INTEGER     NOT NULL DEFAULT 0,
    declared_by  TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (asset_id, resource_id)
);

-- Serving an asset reads its whole reference list on every content read, in the
-- order the author declared, to rewrite the URIs the content names.
CREATE INDEX IF NOT EXISTS idx_portal_asset_refs_order
    ON portal_asset_resource_refs(asset_id, position);

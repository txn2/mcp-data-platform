-- A managed resource can be moved to another library (#1502), and the move
-- rewrites the canonical URI: the URI names the library the file lives in, so a
-- file published to everyone whose URI still read mcp://user/<sub>/... would be
-- a URI that lies.
--
-- Rewriting it costs something, though. An asset or a prompt that already
-- declared the resource keeps rendering -- those rows key on resource_id and the
-- serve-time rewrite matches the URI string recorded on the reference -- but a
-- knowledge page, a script body, or a prompt's prose that hard-codes the old URI
-- as text resolves it through GetByURI, and after the move there is no row with
-- that URI.
--
-- This table is what keeps those resolving: every URI a resource has previously
-- answered to, pointing at the resource it belongs to. A live URI always wins,
-- so an alias can never shadow a resource that occupies the address now; the
-- alias is consulted only when nothing live matches.
CREATE TABLE IF NOT EXISTS resource_uri_aliases (
    -- The URI the resource used to answer to. Primary key because one address
    -- resolves to one resource: a later move onto the same address takes the
    -- alias over (ON CONFLICT DO UPDATE), which is the only reading that can be
    -- right when two resources have both vacated it.
    uri         TEXT PRIMARY KEY,
    resource_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Deleting a resource takes its aliases with it (ON DELETE CASCADE), which needs
-- the foreign-key column indexed or every resource delete scans this table.
CREATE INDEX IF NOT EXISTS idx_resource_uri_aliases_resource
    ON resource_uri_aliases(resource_id);

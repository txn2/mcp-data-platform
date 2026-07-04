-- search_gate_discovery records, per session, that a discovery tool (search or
-- a datahub_* tool) has been called, so the search-first gate
-- (middleware.MCPWorkflowGateMiddleware) can share that signal across replicas.
-- Without a shared store the in-memory tracker is per-pod, and a query
-- load-balanced to a replica that did not handle the search is wrongly refused
-- with SEARCH_REQUIRED even though discovery happened (#789).
CREATE TABLE IF NOT EXISTS search_gate_discovery (
    session_id    TEXT        PRIMARY KEY,
    discovered_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ NOT NULL
);

-- Supports the cleanup delete of expired rows.
CREATE INDEX IF NOT EXISTS idx_search_gate_discovery_expires_at
    ON search_gate_discovery (expires_at);

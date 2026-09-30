-- 000173: which replicas have an interactive embed in flight (#1988).
--
-- Every replica's index worker and every replica's searches share one
-- embedding server. A replica marks a row here while one of its request-path
-- embeds (a search, a discovery ranking, a knowledge-page dedup probe) is in
-- flight, and removes it when none is; every replica's worker waits while any
-- row is live before it sends the embedding server background work. until is
-- how long a mark holds without being cleared, so a replica that stops mid-
-- embed is not waited on past it.
CREATE TABLE IF NOT EXISTS embed_interactive (
    instance TEXT PRIMARY KEY,
    until    TIMESTAMPTZ NOT NULL
);

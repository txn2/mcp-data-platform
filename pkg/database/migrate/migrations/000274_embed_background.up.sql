-- 000274: the background input each replica has at the embedding server (#2846).
--
-- A CPU embedding server serves one input at a time and finishes the one it
-- has begun even when the request is canceled, so a search waits for the
-- background input already at the server however the embed gate orders the
-- rest. A replica marks a row here while its index worker has a call at the
-- server, saying how long that call's largest input is expected to take
-- (input_ms, from the rate the worker measured), and removes it when it has
-- none; a search on any replica waits that long past its own embedding
-- timeout before ranking by words alone. until is how long a mark holds
-- without being refreshed or cleared, so a replica that stops mid-embed is not
-- waited on past it.
CREATE TABLE IF NOT EXISTS embed_background (
    instance TEXT PRIMARY KEY,
    input_ms INTEGER NOT NULL,
    until    TIMESTAMPTZ NOT NULL
);

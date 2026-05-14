-- Drop the per-kind OAuth token tables now that every connection
-- kind reads and writes through the unified connection_oauth_tokens
-- (migration 000039). The per-kind tables were retained for one
-- release so 000039's backfill INSERTs had a source to copy from;
-- this migration completes the consolidation by removing them.
--
-- No data is preserved here: 000039 already migrated every row into
-- connection_oauth_tokens, and any rows written to the per-kind
-- tables after 000039 ran would belong to a process running code
-- older than this commit (impossible by construction). The down
-- migration recreates the empty tables for rollback symmetry, but
-- their content is not restored.

DROP TABLE IF EXISTS gateway_oauth_tokens;
DROP TABLE IF EXISTS apigateway_oauth_tokens;

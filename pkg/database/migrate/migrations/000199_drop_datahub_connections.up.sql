-- No toolkit serves a DataHub connection any more (#1925). A stored one (a row
-- the connection backfill seeded from the file configuration, which is where
-- the platform read DataHub from) would be listed as a connection nothing can
-- call, so it goes with its tokens and alerts. The DataHub a deployment
-- upgrades from is still read from its file configuration, once, by the
-- catalog import (#2124), which keeps its run log and its map of what each
-- URN it read became, and rewrites the knowledge page citations of DataHub
-- entities into catalog references.
DELETE FROM connection_instances WHERE kind = 'datahub';
DELETE FROM connection_oauth_tokens WHERE connection_kind = 'datahub';
DELETE FROM connection_auth_alerts WHERE connection_kind = 'datahub';

# Data Retention

Everything the platform writes to its database and its buckets is either removed
by something, bounded by a count, or kept on purpose. This page lists which, for
the tables that grow with use. Every retention below runs on its own; a
deployment sets nothing unless it wants a different age.

## Removed on age

| What | Kept for | Setting | Removed with it |
|---|---|---|---|
| Deleted assets, collections, feedback threads and knowledge pages | 30 days after the delete | `portal.deleted_retention_days` | Every version object, tile and collection mosaic the item names; its shares, its places in collections and its feedback threads |
| Archived (deleted) memory records | 90 days after the delete | `memory.archived_retention_days` | |
| Records of what produced an asset or managed resource whose file is gone | 90 days after the last write | `portal.orphaned_producer_retention_days` | |
| Failed indexing jobs nobody resolved | 90 days after they finished | `apigateway.embed_jobs.failed_retention_days` | |
| Finished indexing jobs | 14 days | `apigateway.embed_jobs.retention_days` | |
| Records of expired webhook windows | 90 days after expiry | `webhooks.compactor.expired_window_retention_days` | |
| Audit log | 90 days | `audit.retention_days` | |
| Call catalog records nothing came of | 90 days | `calls.retention_days` | |
| Managed-script runs | 365 days | `scripts.run_retention_days` | |

For each setting, `0` or unset takes the default shown and a negative value
keeps the rows forever.

A delete in the portal hides the item at once and removes it for good once the
retention has passed, so a mistaken delete can be taken up with an administrator
in the meantime. A built-in knowledge page an administrator hides is never
removed, because restoring the built-in pages brings it back.

The portal, memory, producer and GraphQL sweeps run once a day on every
replica; each takes a PostgreSQL advisory lock of its own, so only one replica
deletes at a time. The first run happens at startup. How often they run is
`retention.every`:

```yaml
retention:
  every: 24h   # default; zero or negative takes the default
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `retention.every` | duration | `24h` | How often the portal, memory, producer and GraphQL sweeps run. The ages they remove at are the `*_retention_days` settings above |
 An asset whose stored object cannot be
deleted keeps its row, and the next day's run tries again, so no object is left
behind with nothing naming it.

## Removed when superseded

GraphQL operation embeddings are kept for the schema a connection currently
holds. The embeddings of a schema the connection has since replaced are never
read again and are removed by the daily sweep. The schema itself is one row per
connection.

## Bounded by count

| What | Bound | Setting |
|---|---|---|
| Asset versions | 100 per asset | `portal.max_versions`, and per asset |
| Managed resource versions | 10 per resource | `resources.managed.max_versions` |
| Script draft runs | 20 per script and author | |

A pruned version's content and tiles are deleted with it.

## Kept on purpose

These grow with use and have no retention, by decision:

- **Script, prompt and knowledge-page version history.** It is product history,
  the way a script's run history is: a person reading an old version or
  restoring one relies on it being there. Each row is the text of one version.
  The history goes when its script, prompt or page is removed.
- **Configuration change log (`config_changelog`) and knowledge changesets
  (`knowledge_changesets`).** They record who changed the platform's
  configuration and its catalog, and they grow only with administrative
  changes.

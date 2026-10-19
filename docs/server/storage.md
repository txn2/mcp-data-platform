# Storage Capacity

**Admin > Storage** is every store the platform writes to, how much of each is
used and free, how fast it is growing, and when it would fill at that rate
(#2148). The same stores are alerted on at a warning and a critical level.
[Observability](observability.md#capacity-and-integrity) describes the metrics the
page builds on (#1899); this is the page, its settings and its alerts.

## The stores

| Store | Used | Capacity |
|---|---|---|
| **Database** | `pg_database_size` of the platform's database | The **Database limit** in the settings. The platform cannot read the database server's disk, so the limit is the operator's: the volume's size or the plan's quota. Unset is unknown. |
| **SeaweedFS cluster** | The disks the volume servers report, summed | The same disks. The platform reads the master's `/dir/status` for the volume servers and each one's `/status` for its disks. |
| **Bucket** *name* | The bytes the platform holds in the bucket, every purpose summed | The bucket's **budget**: set in the settings, or `MCP_PLATFORM_STORAGE_BUDGETS`, which a budget in the settings overrides bucket by bucket. For a store whose free space cannot be read, such as cloud S3. |
| **Webhook spool on** *replica* | The filesystem the spool directory is on, as the receiver reads it (`statfs`) | The same filesystem. A spool refuses deliveries at its own `max_bytes`, but a volume shared with anything else can fill first. |

A store with no capacity is listed with its usage and growth and no fill date; its **No capacity known** opens the Storage settings, where a database limit and bucket budgets are set.

## Growth and when a store fills

One replica at a time samples every store every 15 minutes
(`MCP_PLATFORM_STORAGE_HISTORY_INTERVAL` overrides it) and keeps each sample
for the settings' **History days** (default 180). Growth is the least-squares
slope of the samples over the last 7 and the last 30 days, in bytes per day.
The 7-day rate waits for a day of history and the 30-day rate for a week: a
new install's first hours are setup (migrations, the built-in knowledge pages
and their embeddings, the first catalog scan), not growth, and a 30-day rate
over the same few hours as the 7-day one would only repeat it. Until then the
page says **Not enough history** with how much there is and how much the rate
needs. A store holding one size grows at exactly zero. The fill date uses the
slower of the two that are growing, so a burst does not bring the date in, and
the page says which it used. A store that is not growing has no fill date.

Selecting a store's row shows its history over 7, 30, 90 or 180 days, used
against capacity.

Below the stores: each bucket's usage by purpose with the orphaned objects and
dangling references the last reconcile found and when the bucket was last
listed, each replica's spool against its volume, and the largest tables and
indexes.

## Settings

**Admin > Settings > Alerts > Storage**, or `GET|PUT /api/v1/admin/settings/storage`:

| Field | Default | Meaning |
|---|---|---|
| `database_limit_bytes` | 0 (unknown) | The size the database may reach. |
| `budgets` | none | Bytes per bucket. Overrides `MCP_PLATFORM_STORAGE_BUDGETS` for each bucket named, in the page and in the `storage_bucket_budget_bytes` gauge. |
| `seaweedfs.master_url` | none | The master's address, such as `http://seaweedfs-master:9333`. |
| `seaweedfs.volume_urls` | none | Volume servers read instead of the master's list, for a master that lists addresses the platform cannot reach. |
| `history_days` | 180 | How long samples are kept. |
| `used_percent` | 75 / 90 | Warning and critical, percent of capacity used. |
| `days_left` | 30 / 7 | Warning and critical, days until the store fills. |
| `channels` | none | The notification channels the alerts are posted to. |

A field left out of a `PUT` keeps its stored value. 0 turns a threshold off.

## Alerts

Each store over a threshold is posted at its level to the channels, under the
key `storage:<store>:used:<level>:<unix time>` or
`storage:<store>:exhaustion:<level>:<unix time>`, the time being when the
level began. The channel's repeat window holds back repeats of one episode; a
move from warning to critical, or a condition that clears and returns, posts
at once; a store back under its thresholds is posted once more as resolved. A
store the pass could not read (the cluster when its read fails, a bucket whose
capacity is the cluster's, a spool whose replica missed its reports in the
last 15 minutes) keeps its condition rather than reading as resolved. The post names the store, how full it is or when it fills, the rate,
the threshold, and links to the page.

## API

- `GET /api/v1/admin/storage`: every store now, with growth, fill date and
  level, and the buckets, reconcile, last listing, tables and spools.
- `GET /api/v1/admin/storage/history?store=<key>&days=<n>`: a store's samples.
  The key is `database`, `seaweedfs`, `bucket:<name>` or `spool:<replica>`.

## Related

- [Observability](observability.md)
- [Inbound Webhooks](webhooks.md)
- [Email Notifications](notifications.md)

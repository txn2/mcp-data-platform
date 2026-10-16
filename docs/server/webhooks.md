# Inbound Webhooks

An external system can post events to the platform. Each **webhook source** has
its own address, `POST /hooks/{source}`, and its own way of proving a request
came from the sender. The platform writes what it receives to object storage
within about a second and answers `202` only after that write. The events can
be queried through Trino moments after they are acknowledged, and each window
of them, an hour unless the source sets a shorter one, is compacted into one
Parquet file.

Ingestion does not depend on whatever reads the data. A source receives,
compacts and applies retention to its events whether or not anything reads
them, and readers come and go without the source changing: a script that reads
yesterday once a night, a script that picks up new events every minute, an
analyst running a query.

Events never go into the platform database. The database holds the source
definitions and the record of which windows are compacted.

## What a source needs

A source writes everything into the managed-resources bucket and creates its
table in the scratch catalog of a Trino connection. That catalog has to read the
managed-resources store and allow `register_partition`, and the connection's
Trino user needs a `procedures` rule to call the catalog's partition procedures;
the complete setup is on [Scratch Catalog](scratch-catalog.md). Creating a
source checks all three and refuses the source, naming the setting to add, when
one is missing. Testing the connection
([`POST /api/v1/admin/connection-instances/trino/{name}/test`](admin-api.md#test-a-connection-instance))
runs the same checks before any source exists.

## Creating a source

Administrators manage sources in the portal under **Admin > Webhooks**, and over
the admin API at `/api/v1/admin/webhooks/sources`:

```bash
curl -X POST https://platform.example.com/api/v1/admin/webhooks/sources \
  -H "Authorization: Bearer $ADMIN_KEY" -H "Content-Type: application/json" \
  -d '{
    "name": "email-events",
    "connection": "scratch",
    "auth": {
      "mode": "hmac",
      "secret": "whsec_example",
      "signature_header": "X-Signature",
      "prefix": "sha256=",
      "timestamp_header": "X-Timestamp",
      "signed": "timestamp.body"
    },
    "config": {
      "split": "$",
      "event_id_path": "$.event_id",
      "event_type_path": "$.event",
      "key_path": "$.email",
      "persona": "marketing"
    }
  }'
```

The sender is then given `https://platform.example.com/hooks/email-events`.

| Field | Meaning |
|---|---|
| `name` | The path segment: `/hooks/{name}`. Lowercase letters, digits and `-`, starting with a letter. The table is `webhook_{name}`, with each `-` as `_`. |
| `connection` | The Trino connection whose scratch catalog holds the table. |
| `enabled` | A disabled source answers `404`, the same answer as a name that is not a source. Default `true`. |
| `auth` | How a request proves it came from the sender. See [Authentication](#authentication). |
| `config.handshake` | `none` or `cloudevents`. See [The CloudEvents handshake](#the-cloudevents-handshake). |
| `config.max_body_bytes` | Default 1 MiB. A larger body gets `413` and is not stored. |
| `config.split` | Unset: the body is one event. A JSON path to an array: each element is its own event, for senders that batch. `$` is a body that is itself an array. |
| `config.event_id_path` | JSON path to the sender's event id, used to remove duplicates. Unset or absent from an event: the SHA-256 of the event's JSON (keys sorted) is used. |
| `config.event_type_path` | JSON path to the event type, stored as a column. |
| `config.key_path` | JSON path to the entity the event is about (a contact id, an email address), stored as a column so a reader can keep only the latest event per key before it spends API quota. |
| `config.persona` | The persona whose members see the source's compacted files in Resources and search. Unset: the administrator persona (`admin.persona`), so only administrators. Fixed when the source is created. |
| `config.compact_every_minutes` | The length of the window events are partitioned and compacted by. Default 60. One of 1, 2, 3, 4, 5, 6, 10, 12, 15, 20, 30 or 60, so windows start on the hour. See [Compaction](#compaction). |
| `config.flush_max_events` | Default 5,000. |
| `config.flush_max_interval_ms` | Default 1,000. |
| `config.flush_max_bytes` | Default 8 MiB. |
| `config.buffer_limit` | Events one replica holds in memory for the source before it answers `503`. Default 50,000. |
| `config.rate_limit_per_minute`, `config.rate_limit_burst` | Optional. The limit is on the source, not on a client address, and each replica holds it for itself: with N replicas behind a load balancer, a sender can be admitted up to N times the rate and the burst. Size it as what one replica should take for this source, as `buffer_limit` is. |
| `config.raw_retention_days` | How long a compacted window's raw segments are kept. Default 1 (7 before #2134). The segments are a safety margin only: a compacted window's events are all in its Parquet file, and a recompaction reads that file. Every day kept is objects the raw table and retention list. |
| `config.rollup_every_minutes` | How often the open window's segments are merged into one object (#2132). Default 5; at least 1. See [The open window's rollup](#the-open-windows-rollup). The portal's source editor shows it as **Merge recent raw files every (minutes)**. |
| `config.compacted_retention_days` | How long a compacted window is kept. Default 400; `0` keeps it forever. |
| `config.index` | How search indexes each compacted window's file: `full`, `metadata` or `none`. Absent leaves it to the deployment's `resources.index` rules, which can match these files with `origin: [webhook]` ([Search indexing](configuration.md#search-indexing)). Applies from the next compaction on. The portal's source editor shows it as **Search**. |

A JSON path is `$` for the document, `.name` or `['name']` for a member, and
`[n]` for an array element: `$.data.events`, `$.contact['id']`, `$[0].id`.

The name, the connection and the persona do not change after creation. The
table was created on that connection under that name, and the windows already
written are in that persona's library.

### Authentication

| `auth.mode` | What a request carries | Settings |
|---|---|---|
| `hmac` | A signature of the body in a header | `algorithm` (`sha256` default, `sha1`, `sha512`), `signature_header`, `encoding` (`hex` default, `base64`), `prefix` (such as `sha256=`), `timestamp_header` with `tolerance_seconds` (default 300), `signed` (`body` default; `timestamp.body`, which signs the timestamp, a `.`, and the body; or `id.timestamp.body`, the Standard Webhooks form, which signs the `id_header` value, a `.`, the timestamp, a `.`, and the body), `header_format` (empty, or `stripe` for `t=<timestamp>,v1=<signature>` with the timestamp inside the signature header). |
| `header_token` | The secret itself in a header | `header` |
| `basic` | HTTP Basic credentials, the secret as the password | `username` |
| `path_token` | The secret as a further path segment, `/hooks/{name}/{secret}`, for a sender that can set nothing but a URL | none |

With `timestamp_header` set, or `header_format: stripe`, a request whose
timestamp is further than the tolerance from the platform's clock is refused
even when its signature is right, which is what makes a replayed request fail.
A timestamp above 10^12 is read as milliseconds. A signature header may carry
several signatures separated by spaces, as a sender rotating its secret sends
them; the request verifies when one matches. A Standard Webhooks secret
(`whsec_` and base64) is used as that standard reads it when `signed` is
`id.timestamp.body`.

The secret is encrypted at rest and never returned. A view of the source says
whether one is set. Every comparison of a secret is constant-time.

**Rotating the secret.** Send a new `auth.secret` with
`rotation_overlap_seconds`. The previous secret is accepted alongside the new
one until the overlap ends, so the sender can be moved to the new one with no
window in which its requests fail. An update without `auth.secret` keeps the
stored secret.

### The CloudEvents handshake

A sender that implements the CloudEvents HTTP webhook abuse-protection
handshake sends an `OPTIONS` request carrying `WebHook-Request-Origin` before it
delivers anything, and delivers nothing until it is answered. With
`handshake: cloudevents` the source answers `200` with `WebHook-Allowed-Origin`
echoing the origin, and `WebHook-Allowed-Rate` when the source has a rate
limit. A source without the handshake answers `OPTIONS` with `405`.

### Sending to a webhook

The platform signs outbound requests too. An `api` connection with
[`auth_mode: hmac`](api-gateway.md#hmac-signing) signs every request with the
same settings, under `hmac_` names, and the two are built on one
implementation, so a script can deliver to Standard Webhooks, GitHub, Stripe
and other HMAC receivers, and to another deployment's sources. To deliver to a
source configured as in [Creating a source](#creating-a-source), give the
connection `hmac_preset: platform` and the source's secret:

```json
{
  "base_url": "https://other-platform.example.com",
  "auth_mode": "hmac",
  "credential": "whsec_example",
  "hmac_preset": "platform"
}
```

A script then posts with
`platform.call("api_invoke_endpoint", {"connection": "...", "method": "POST", "path": "/hooks/email-events", "body": {...}})`
and is answered `202`. A [notification channel](notifications.md) of the
webhook kind with `format: json` delivers through such a connection, signed.

## What a sender is answered

Each request is handled in this order, and stops at the first refusal:

| Step | Refusal |
|---|---|
| The source exists and is enabled | `404`, the same body for both |
| `OPTIONS` on a `cloudevents` source is answered here | |
| The body is at most `max_body_bytes` | `413` |
| The request authenticates | `401`; nothing is stored |
| The rate limit | `429`, `Retry-After` |
| The body is JSON (for a form post, its `payload` field is), and `split` names an array | `400` |
| The request holds no more events than `buffer_limit` | `413`; a batch the buffer can never hold is not worth retrying |
| The events fit in the source's buffer | `503`, `Retry-After: 1` |
| The segment holding the events is written to object storage | `503`, `Retry-After: 5` |
| | `202`, `{"accepted": <events>}` |

A `202` means the events are in object storage. A replica that stops while it
holds events it has not written answered nobody for them, so their senders send
them again. Backpressure is the sender's own retry queue, told to wait: the
platform keeps no backlog beyond each source's buffer limit.

### A local spool

By default a `202` waits for object storage, so a sender's wait is object
storage's latency at that moment. Many senders give up within a few seconds,
and an object store slows down for ordinary reasons: a bulk copy or a backup
reading the same cluster, healing, a slow credential refresh. A receiver given
a spool directory (`webhooks.receiver.spool.dir`, #2121) answers once the events
are synced to its own disk instead, and drains them to object storage behind
the answer:

| Step | Refusal |
|---|---|
| ... the steps above, through `buffer_limit` ... | |
| The spool holds less than `max_bytes`, and its oldest event waiting is younger than `max_age` | `503`, `Retry-After: 5`, outcome `spool_full` |
| The events are appended to the spool and synced | `503`, `Retry-After: 5`, outcome `write_failed` |
| | `202`, `{"accepted": <events>}` |

A `202` then means the events are on this replica's disk, synced. Each source
has one file being appended to, sealed by the same rules a buffer is flushed by
(`flush_max_events`, `flush_max_bytes`, `flush_max_interval_ms`), and a drainer
writes each sealed file to object storage through the same windows and layout,
retrying with backoff (one second, doubling, at most a minute) until the write
succeeds, then removes the file. A file is written under its own name, so a
file drained again after a crash, between its write and its removal, overwrites
what the first drain wrote instead of adding to it: every acknowledged event
lands once. The spool holds the events the request was read into, after it
authenticated, not the raw request, so a drain never checks a signature again:
a timestamped signature past its tolerance, or a secret rotated since, cannot
refuse a request that was already acknowledged.

A replica that is stopped seals what it holds and drains for as long as
`write_timeout` allows; one that is killed, or stopped while object storage
does not answer, drains what it holds when it starts again, before it serves
its first request. A record whose sync did not finish was never acknowledged,
and the next start cuts it off. Past `max_bytes` or `max_age` the receiver
answers `503` with `Retry-After` rather than accept a request it cannot keep.
A source deleted while its events wait in a spool has had its tables and
objects removed with it, so its spooled file is discarded at its next drain
with a `WARN` line, `webhooks: spooled events of a deleted source discarded`,
rather than failing every drain. The Webhooks page shows each replica's spool.

### How long a sender waits

A sender should expect its answer within the source's flush interval
(`flush_max_interval_ms`, one second by default) plus one write to object
storage and one database write that records the segment against its window;
with a spool, within one write to local disk.
The first segment a replica writes into a window records the window once more
before the write. Nothing else is done before the answer: registering a window's
partition with the query engine runs on the replica's own loop. For a source
that wrote into the current window, the loop registers the next one a minute
before it starts (half the window ahead for a window shorter than two minutes),
so its first event is queryable as soon as it is acknowledged. A window nobody
saw coming, such as a new source's first, is registered right after its first
`202`, so its events become queryable moments later. A registration that fails
is retried every 15 seconds, never while a sender waits, and the compactor's
partition sync, every 10 minutes, registers any window still missing. The source's page
shows how long requests waited (see [The source's page](#the-sources-page)).

### A sender that gives up

A sender whose own timeout passes before its answer disconnects. The platform
counts that request `sender_gone`, not `write_failed`, because its write goes
on and usually stores the events. The request is logged at `WARN`
(`webhooks: request not accepted`, `outcome=sender_gone`) and kept in the
rejections with its event ids and body hash. When the write finishes, a second
`WARN` line, `webhooks: write for a disconnected sender finished`, says whether
the events were stored (`stored=true` or `stored=false`, with the error) and how
long each stage took. A sender that retries delivers the same events again;
compaction removes the duplicate.

### What is logged

Every request not answered `202` is logged at `WARN` as `webhooks: request not
accepted`, with the source, the replica, the outcome and reason, how long since
the request arrived (`elapsed_ms`), and the number of events. Once the body has
been read into events the line also names the windows they fall in, the
SHA-256 of the body (`request_hash`) and the ids of the first 20 events
(`event_ids`). A request whose write ran adds how long its events waited in the
buffer (`buffer_ms`), recorded their window (`mark_ms`) and took to write
(`put_ms`). A `write_failed` line carries the storage error, which the sender's
answer never does.

A source created on any replica is served by every replica from its first
request. A replica asked for a name it does not hold reads the source list
again before answering `404`, and every such request waits for a read that
started after it arrived. Those reads are shared and at least a second apart,
so a caller inventing names costs the database at most one read a second, and
waits up to a second for its `404` (#2108).

The rate limit, the buffer and the `503` they answer are each replica's own.
They protect the replica holding the source's buffer, not the sender's quota,
so they take no shared state on the path a request takes. A sender spread over
N replicas can be admitted up to N times the configured rate and burst.

Delivery is at least once, because a sender retries anything it did not see
acknowledged. Duplicates are removed at compaction.

A form post, `application/x-www-form-urlencoded`, carries its JSON in a field
named `payload`. The signature is checked over the raw body as sent.

## Where events land

A replica collects a source's events for up to `flush_max_interval_ms`,
`flush_max_events` or `flush_max_bytes`, whichever comes first, and writes them
as one gzipped JSON-lines **segment**, one per window the events were received
in. A window is `compact_every_minutes` long and named by the UTC date, hour and
minute it starts at:

```
{managed-resources bucket}/webhooks/{source}/raw/dt=YYYY-MM-DD/hour=HH/minute=MM/{replica}-{start}-{sequence}.jsonl.gz
```

Every replica writes its own segments, named by itself, so replicas never
coordinate on a write.

### The open window's rollup

Until a window is compacted, a reader reads it from its raw segments, one
object each, and a busy source writes thousands an hour: 70 events a second
written every 200 ms by three replicas is about 3,700 segments an hour, and a
count over one such hour took 130 s against 20 s for the same hour compacted.
Every reader of recent events pays that, and pays more when the object store is
slow (#2132).

So the open window is rolled up. Every `rollup_every_minutes` (default 5), one
replica merges the segments written into the open window since the last rollup
into one object in the same directory and format, `rollup-{time}.jsonl.gz`,
which the raw table reads like any segment. A reader of the open window then
reads one object per rollup plus the segments written since the last one: at
most twelve rollups for an hour's window.

The merged segments are not deleted at once. A query lists a directory and then
reads what it listed, so a segment deleted between the two fails that query.
They are deleted by a later pass, at least `webhooks.compactor.rollup_delete_delay`
(default 2 minutes) after the rollup. Until then an event is in a segment and in
the rollup, and the raw side returns it twice; the raw side already holds a
sender's retries until compaction, which keeps one event per event id. A hidden
`.rollup-{time}.keys` object beside each rollup lists what it merged; the query
engine skips names beginning with `.` and compaction skips it by name.

Only the open window is rolled up: a window that has ended is compaction's. A
window that closes before a rollup's segments could be deleted is not rolled up
at all, since the rollup would add an object and a second copy of every event
and remove nothing: with the defaults, a `compact_every_minutes` of 2 or less
is never rolled up, and needs none. One
replica rolls a window up at a time, through a short claim on the window's
record that holds back its compaction no longer than a minute.
`webhooks.compactor.rollup_check_every` (default 1 minute) is how often the
open windows are looked at.

### Compaction

A window is compacted once it has ended and `webhooks.compactor.grace` has
passed (default two minutes, for segments still being written). A window events
are still landing in is never compacted. One replica compacts it:

1. It reads every segment of the window, and the window's previous Parquet file
   if there is one.
2. It keeps one event per event id, the earliest received.
3. It writes the result as the window's Parquet file, a managed resource in the
   folder `webhooks/{source}/{dt}` named `{HH}-{MM}.parquet`, in the library of
   the source's persona. A later compaction of the window is a new version of
   the same resource.
4. It registers the window's partition at that resource's directory.

The window length is a trade between how soon duplicates are removed and how
many files a source keeps. Every event is queryable as soon as it is
acknowledged whatever the setting, so a short window is only worth it when
readers need duplicates removed, or the Parquet file, within minutes. An hour
gives 24 files a day; a minute gives 1,440.

Changing `compact_every_minutes` takes effect for events received after the
change. A window that already holds segments keeps the longer of its old and
new lengths, so raising the setting never compacts a window early.

The file opens in the portal's Parquet viewer and is found by `search` like any
resource the reader can see, with the source's table attached. Its contents are
not indexed for search.

A segment written for a window after it was compacted, by a replica that was
slow or that recovered, makes the window owed another compaction, which starts
again from everything the window holds. Rewriting a window is idempotent. A
window that fails to compact is retried with a growing delay and shown as failing on
the source's page.

## The table

Readers name one table, `webhook_{source}`, in the scratch schema of the
source's connection. It is a view over two tables that are storage, not
something a reader chooses between:

- `webhook_{source}_compacted`: Parquet, one partition per compacted window.
- `webhook_{source}_raw`: the raw segments.

For each window, the view reads the compacted table once the window's partition
is registered, and the raw segments until then. An event is queryable once its
window's raw partition is registered: ahead of the window for a source receiving
steadily, otherwise moments after the window's first `202` (see
[How long a sender waits](#how-long-a-sender-waits)). No window is missing or
served twice. Rows read
from the raw side have not had duplicates removed yet.

| Column | Type |
|---|---|
| `received_at` | `timestamp(6)`, UTC |
| `landed_at` | `timestamp(6)`, UTC: when the segment holding the event was written |
| `event_id` | `varchar` |
| `event_type` | `varchar` |
| `key` | `varchar` |
| `content_hash` | `varchar`: SHA-256 of the event's JSON |
| `replica` | `varchar`: the replica that received it |
| `payload` | `varchar`: the event's JSON |
| `dt` | `varchar`: `YYYY-MM-DD` |
| `hour` | `varchar`: `HH` |
| `minute` | `varchar`: `MM`, the minute the event's window starts at; `00` for a source compacting by the hour |

The schema is fixed. A sender adding a field never breaks anything, and a
reader takes what it needs with `json_extract_scalar(payload, '$.field')`.

The table is recorded as a registered table, so it is listed under Scratch
Tables. It is removed with its source, not unregistered on its own. Who can
query it is decided by who is granted the connection, as for every table in the
scratch schema.

## Reading the data

The receiving side knows nothing of its readers. A [managed
script](../scripts/running.md) reads the view like any other table, on
whatever schedule it has.

The examples name the view without its catalog and schema. That resolves
through the connection's `catalog` and `schema` settings, which are the Trino
session's defaults for every query on it; on a connection without them, write
the full name, `<catalog>.<schema>.webhook_email_events`.

**Once a day.** Read yesterday's events:

```python
day = run.params["day"]
rows = platform.query(connection="scratch",
    sql="SELECT event_type, count(*) AS n FROM webhook_email_events WHERE dt = :day GROUP BY 1",
    params={"day": day})["rows"]
```

**Every few minutes.** Keep a `landed_at` watermark in the script's
[state](../scripts/running.md#state-what-a-run-carries-to-the-next), read from
a little before it, and remove duplicates on `event_id`:

```python
since = run.state.get("landed_through", "1970-01-01 00:00:00.000000")
rows = platform.query(connection="scratch", sql="""
    SELECT event_id, landed_at, payload FROM webhook_email_events
     WHERE landed_at > CAST(:since AS timestamp(6)) - INTERVAL '5' MINUTE
     ORDER BY landed_at""", params={"since": since})["rows"]
seen = set(run.state.get("recent_ids", []))
new = [r for r in rows if r["event_id"] not in seen]
# ... process new ...
if rows:
    platform.save_state({
        "landed_through": rows[-1]["landed_at"],
        "recent_ids": [r["event_id"] for r in rows],
    })
```

State is saved only when a run succeeds, so a run that fails reads the same
events again. The overlap covers clock differences between replicas and a
segment written late; the `event_id` check makes reading an event twice
harmless. A watermark on `received_at` would be wrong: a segment written late
carries `received_at` values earlier than events already read.

## Retention

Retention runs in the compactor, per source.

- **Raw segments** of a compacted window are deleted once the window's last
  segment is older than `raw_retention_days`. A window owed a compaction is never
  touched, so retention never deletes an event that is not yet in a Parquet
  file. The window's raw partition is unregistered once its segments are
  deleted.
- **Raw partitions** are kept by name from the window records (#2133): a pass
  registers the raw partition of every window a segment landed in during the
  last two retention intervals, which covers a window a
  receiver could not register and one a late segment reopened. A pass never
  lists the raw table's location, so what it costs is the windows that changed,
  not every segment raw retention keeps. `POST
  /api/v1/admin/webhooks/sources/{name}/sync-partitions` (the **Resync
  partitions** action on the source's page) runs the full listing
  (`sync_partition_metadata` FULL) as a repair, for example after a restore.
- **A whole window** older than `compacted_retention_days` is removed: its
  partition is unregistered first, so the view stops serving it, then its
  resource and any raw segments are deleted. Each step is recorded in the
  platform database, so a pass that stops part-way is finished by the next.

Payloads usually carry personal data. The source's page shows both settings and
the oldest window still held, and a shorter setting takes effect on the next
pass. A bucket lifecycle rule is not the mechanism: it would delete objects the
metastore still lists. An operator may add one as a backstop, set longer than
`compacted_retention_days`.

## The Webhooks page

**Admin > Webhooks** is the status of every source, read in one request
(#1979):

- **Requests**: request volume by outcome, stacked, for all sources or one,
  over the last hour (one bar a minute) or the last 24 hours (one bar every
  fifteen minutes), with the total of each outcome beneath.
- **Sources**: one row per source with its health, when it last received an
  event, the requests accepted and rejected in the last hour and the last day,
  how long accepted requests waited for their answer (Response time: the 99th
  percentile, with the slowest beneath, over the last hour or, when the hour
  had none, the last day), the windows owed a compaction (Pending), and the
  windows whose last compaction failed (Failing) with the newest one's error,
  and the raw files recorded against the open window (Raw files (current
  window); hover for the 95th percentile per window over the last day).
  Clicking a row opens the source's page.
- **Spool**, where a receiver has one (#2121): one row per replica with the
  events it acknowledged and has not yet drained (Waiting), their size, how
  long the oldest has waited, the events drained and the drains that failed
  since the replica started, and the newest drain error. A replica that has not
  reported for two minutes is marked Not reporting: it stopped, and drains what
  it holds when it starts again.
- **Recent rejections**: the rejected requests across every source within the
  range, the newest 50 rows of each outcome, each with its source, time,
  outcome, reason, the events it refused (Events) and how many requests it
  stands for. Never the body. Choosing a source in the Requests chart reads that
  source's rejections alone. An outcome filter narrows the list.

Rejections are kept per outcome, so a burst of `rate_limited` refusals never
pushes out the `unauthorized` rows that show someone sending forged requests.
A rejection with the same outcome and reason as that outcome's newest row,
within a minute of it, is added to the row rather than written as a new one:
the row's `count` says how many requests it stands for, `first_at` the first
of them and `at` the latest. A burst is one row.

A rejected request whose body was read into events names them: `request_hash`
is the SHA-256 of its body and `event_ids` the ids of its first 20 events, the
ids the source's table holds in `event_id`. Such a row is one request and is
never added to a run. A request refused before its events were read (`401`,
`413` for the body size, `429`, `400`) has an empty `request_hash` and
`event_ids: []`.

A source's health is one of:

| Health | When |
|--------|------|
| Disabled | An administrator turned the source off. |
| Failing | One or more of its windows failed their last compaction. |
| Silent | Enabled, and no event received in the last 24 hours, including a source that never received one. |
| Receiving | Enabled, an event received in the last 24 hours, and no window failing. |

When more than one applies, the first in that order is reported: a disabled
source receives nothing by choice, and a failing one is losing what it did
receive, which outranks silence. A sender that stops produces no error on the
receiving side, so Silent is how a stopped sender shows.

The page reads `GET /api/v1/admin/webhooks/status?range=hour|day`, and
`&source=<name>` to read one source (a name no source has is `404`):

```json
{
  "generated_at": "2026-09-29T12:00:00Z",
  "range": "hour",
  "from": "2026-09-29T11:00:00Z",
  "bucket_seconds": 60,
  "silent_after_seconds": 86400,
  "sources": [
    {
      "name": "esp-events", "enabled": true, "health": "failing",
      "auth_mode": "hmac", "connection": "acme-scratch-resources",
      "table": "webhook_esp_events", "last_event_at": "2026-09-29T11:59:58Z",
      "last_hour": {"accepted": 1832, "unauthorized": 3},
      "last_day": {"accepted": 40211, "unauthorized": 12},
      "pending": 2, "failing": 1,
      "last_error": "segment webhooks/esp-events/...: gzip: invalid header",
      "ack_last_hour": {
        "count": 1832, "p50_ms": 1000, "p99_ms": 2500, "max_ms": 2380,
        "slowest": {"at": "2026-09-29T11:00:21Z", "total_ms": 2380, "buffer_ms": 990, "mark_ms": 1310, "put_ms": 80}
      },
      "ack_last_day": {"count": 40211, "p50_ms": 1000, "p99_ms": 2500, "max_ms": 2380, "slowest": {"at": "2026-09-29T11:00:21Z", "total_ms": 2380, "buffer_ms": 990, "mark_ms": 1310, "put_ms": 80}},
      "segments_open_window": 412,
      "segments_per_window_p95": 3650
    }
  ],
  "volume": [
    {"at": "2026-09-29T11:59:00Z", "source": "esp-events", "outcome": "accepted", "count": 31}
  ],
  "rejections": [
    {"source": "esp-events", "first_at": "2026-09-29T11:58:40Z", "at": "2026-09-29T11:58:40Z", "count": 1, "outcome": "sender_gone", "reason": "the sender disconnected after 30s, before its events were written", "request_hash": "9f2c4e7a...", "event_ids": ["evt_01J9Z6Q4"]},
    {"source": "esp-events", "first_at": "2026-09-29T11:58:12Z", "at": "2026-09-29T11:58:12Z", "count": 1, "outcome": "unauthorized", "reason": "the signature does not match", "request_hash": "", "event_ids": []},
    {"source": "esp-events", "first_at": "2026-09-29T11:57:01Z", "at": "2026-09-29T11:57:03Z", "count": 42, "outcome": "rate_limited", "reason": "the source's rate limit was reached", "request_hash": "", "event_ids": []}
  ]
}
```

`volume` is read from the per-minute request counts at or after `from`, the
bound each source's `last_hour` (range `hour`) or `last_day` (range `day`) is
read with, so the series sums to those counts. A bucket starts at a multiple of
`bucket_seconds` since the Unix epoch, and one with no requests has no point.
Per-minute counts are kept for 48 hours, which is why the longest range is a
day. `rejections` holds the rows whose latest rejection (`at`) is at or after
`from`, so a range of an hour shows the last hour's. With no sources, every
list is `[]`.

`ack_last_hour` and `ack_last_day` are how long the requests answered `202`
waited for that answer. Each replica counts them per minute in fixed buckets
(10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000 and 30000 ms, and one for
anything slower). `p50_ms` and `p99_ms` are the upper bound of the bucket the
percentile falls in, capped at `max_ms`, so each is the most it could be.
`slowest` is the slowest request of the span with how long its events waited in
the buffer (`buffer_ms`), recorded their window (`mark_ms`) and took to write to
object storage (`put_ms`); it is `null` when nothing was accepted. A response
time creeping toward a sender's timeout shows here before requests start to
fail. The counts are kept for 48 hours, like the request counts.

`segments_open_window` is how many segments were recorded against the source's
open window, and `segments_per_window_p95` the 95th percentile of that count
over the windows that started in the last day (#2132): the objects a reader of
a window reads before a rollup merges them, which is the read cost to watch
before readers time out. A receiver records its first write of a window twice,
so a count can exceed the segments written by one per replica. The source's
own `GET /api/v1/admin/webhooks/sources/{name}` carries both in its `status`.

A create or a change of a source is answered with the source and, when there
is something to say, `warnings` (#2132, #2134): the read of the raw table's
partitions the save makes took 5 seconds or more, which an uncached file
metastore causes ([Scratch Catalog](scratch-catalog.md#the-catalog)), or
failed (the save waits at most 15 seconds for it and reports a read still
running then as slow); or the settings imply more than 1,000 segments between two rollups of
the open window (`rollup_every_minutes` over `flush_max_interval_ms`, times the
replicas whose spool reported in the last two minutes, at least one). A
warning never refuses the save. The portal shows them as **Saved, with
warnings** before it opens the source.

## The source's page

A source's page shows:

- the URL to give the sender, and the table readers query;
- request counts by outcome for the last hour and the last day;
- how long accepted requests waited for their answer over the last hour and
  the last 24 hours (median, 99th percentile, slowest), and the slowest
  request's time split into Buffer, Recording and Writing;
- when the source last received an event (a sender that stops produces no
  error on the receiving side, so this is how a quiet source shows);
- the window length, the last compacted window, the windows owed a compaction,
  and the last failure;
- the oldest window held, beside the retention settings;
- the raw files in the current window, and the 95th percentile per window over
  the last day, with **Resync partitions**, which runs the full listing of the
  raw table's location as a repair (see [Retention](#retention));
- the rejected requests, the newest 50 rows of each outcome: when, the
  outcome, why, the events refused (their ids, with the body's SHA-256 on hover
  and a copy button), and how many requests a row stands for. Never the body.

## Deployment

```yaml
webhooks:
  receiver:
    enabled: true          # default; serve /hooks/ on this replica
    address: ""            # optional: also serve /hooks/ on a listener of its own
    write_timeout: 30s     # a segment not written by then is answered 503
    spool:
      dir: ""              # set to answer from local disk and drain behind the answer (#2121)
      max_bytes: 1073741824
      max_age: 1h
  compactor:
    enabled: true          # default; compact and apply retention on this replica
    grace: 2m              # wait after a window ends for segments still in flight
    poll: 30s
    lease: 10m
    batch: 4
    retry_backoff: 1m
    retention_every: 10m
    rollup_check_every: 1m   # how often the open windows are looked at for a rollup (#2132)
    rollup_delete_delay: 2m  # least time between a rollup and deleting what it merged
```

Every source is served on one port and routed by path, so a deployment needs one
Ingress rule for all of them. A sender whose configured URL cannot change is
moved with an Ingress path rewrite from its old path to `/hooks/{source}`.

**Serve `/hooks/` from an Ingress without CORS.** An Ingress with CORS enabled
(ingress-nginx's `nginx.ingress.kubernetes.io/enable-cors: "true"`, which a
platform serving browser MCP clients often has) answers every `OPTIONS`
request itself with `204`, and the request never reaches the platform. A
`cloudevents` source then never answers the handshake, and a sender that
requires it never delivers; a source without the handshake never answers its
`405`. Annotations apply per Ingress and the longest path prefix wins, so a
second Ingress for `/hooks/` on the same host, with no CORS annotations and the
same TLS secret, takes those requests while the rest of the host keeps its
CORS. Senders are servers, so `/hooks/` never needs CORS:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: mcp-data-platform-hooks
  annotations:
    nginx.ingress.kubernetes.io/proxy-body-size: "10m"
spec:
  ingressClassName: nginx
  rules:
    - host: platform.example.com
      http:
        paths:
          - path: /hooks/
            pathType: Prefix
            backend:
              service:
                name: mcp-data-platform
                port:
                  number: 8080
  tls:
    - hosts: [platform.example.com]
      secretName: platform-tls   # the main Ingress's certificate; no cert-manager annotation here
```

A deployment that wants bursts kept off the replicas serving MCP and the portal
turns `webhooks.receiver.enabled` off there and runs a receiver-only
deployment of the same image behind the webhook Ingress, with `address` set if
it should listen on a port of its own.

### Where the spool lives

The spool is a directory on the replica, one per replica: a second process
opening a directory another holds is refused at start. Its volume decides what
survives:

- An `emptyDir` survives a container restart, not the pod being rescheduled:
  what was acknowledged and not yet drained when the pod is deleted is lost.
- A volume of the replica's own keeps it across rollouts, evictions and node
  drains: run the receiver as a StatefulSet with a `volumeClaimTemplates` entry
  mounted at the spool directory. A ReadWriteOnce volume is tied to its node;
  if the node is lost, that replica's events wait until the volume is attached
  again. They are delayed, not lost.

A replica drains only its own spool. Another replica does not take over a
stopped replica's volume, so its events wait for it to start. The Webhooks page
shows such a replica as not reporting, with what it holds.

```yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: mcp-data-platform-hooks
spec:
  serviceName: mcp-data-platform-hooks
  template:
    spec:
      containers:
        - name: platform
          volumeMounts:
            - name: spool
              mountPath: /var/lib/mcp-data-platform/spool
  volumeClaimTemplates:
    - metadata:
        name: spool
      spec:
        accessModes: [ReadWriteOnce]
        resources:
          requests:
            storage: 2Gi
```

with `webhooks.receiver.spool.dir: /var/lib/mcp-data-platform/spool`. A spool
directory that cannot be opened (not writable, or held by another process)
stops the platform at start, naming it: an operator who asked for acknowledged
events to be kept on disk is told then, not answered without it.

## Metrics

| Series | Labels |
|---|---|
| `webhook_requests_total` | `source`, `outcome`: `accepted`, `unauthorized`, `too_large`, `rate_limited`, `buffer_full`, `spool_full`, `write_failed`, `sender_gone`, `unknown_source`, `invalid_body` |
| `webhook_events_total` | `source` |
| `webhook_segments_written_total` | `source` |
| `webhook_compactions_total` | `source`, `result`: `compacted`, `failed` |
| `webhook_duplicates_dropped_total` | `source` |
| `webhook_buffer_events` | `source`: events held in memory on this replica |
| `webhook_ack_seconds` | `source`: request to acknowledgement |
| `webhook_spool_bytes`, `webhook_spool_events`, `webhook_spool_files` | none: what this replica's spool holds |
| `webhook_spool_oldest_age_seconds` | none: age of the oldest event waiting; rising means the spool is not draining |
| `webhook_spool_drained_events_total` | `source`: events drained to object storage |
| `webhook_spool_drain_failures_total` | `source`: drains that failed and are retried |

A request to a name that is not a source is counted with an empty `source`
label, so a caller cannot create series by inventing names.

## Related

- [Scratch Catalog](scratch-catalog.md)
- [Registered Tables](registered-tables.md)
- [Managed Scripts](../scripts/running.md)
- [Threat Model](../security/threat-model.md#inbound-webhooks)

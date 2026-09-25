# Inbound Webhooks

An external system can post events to the platform. Each **webhook source** has
its own address, `POST /hooks/{source}`, and its own way of proving a request
came from the sender. The platform writes what it receives to object storage
within about a second and answers `202` only after that write. The events can
be queried through Trino as soon as they are acknowledged, and each window of
them, an hour unless the source sets a shorter one, is compacted into one
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
managed-resources store and allow `register_partition`; every requirement is on
[Scratch Catalog](scratch-catalog.md). Creating a source checks both and refuses
the source, with the reason, when either is missing.

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
| `config.rate_limit_per_minute`, `config.rate_limit_burst` | Optional. The limit is on the source, not on a client address. |
| `config.raw_retention_days` | How long a compacted window's raw segments are kept. Default 7. |
| `config.compacted_retention_days` | How long a compacted window is kept. Default 400; `0` keeps it forever. |

A JSON path is `$` for the document, `.name` or `['name']` for a member, and
`[n]` for an array element: `$.data.events`, `$.contact['id']`, `$[0].id`.

The name, the connection and the persona do not change after creation. The
table was created on that connection under that name, and the windows already
written are in that persona's library.

### Authentication

| `auth.mode` | What a request carries | Settings |
|---|---|---|
| `hmac` | A signature of the body in a header | `algorithm` (`sha256` default, `sha1`), `signature_header`, `encoding` (`hex` default, `base64`), `prefix` (such as `sha256=`), `timestamp_header` with `tolerance_seconds` (default 300), `signed` (`body` default, or `timestamp.body`, which signs the timestamp, a `.`, and the body). |
| `header_token` | The secret itself in a header | `header` |
| `basic` | HTTP Basic credentials, the secret as the password | `username` |
| `path_token` | The secret as a further path segment, `/hooks/{name}/{secret}`, for a sender that can set nothing but a URL | none |

With `timestamp_header` set, a request whose timestamp is further than the
tolerance from the platform's clock is refused even when its signature is
right, which is what makes a replayed request fail. A timestamp above 10^12 is
read as milliseconds.

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
is registered, and the raw segments until then. An event is therefore queryable
as soon as its `202` is sent, and no window is missing or served twice. Rows read
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
  file.
- **A whole window** older than `compacted_retention_days` is removed: its
  partition is unregistered first, so the view stops serving it, then its
  resource and any raw segments are deleted. Each step is recorded in the
  platform database, so a pass that stops part-way is finished by the next.

Payloads usually carry personal data. The source's page shows both settings and
the oldest window still held, and a shorter setting takes effect on the next
pass. A bucket lifecycle rule is not the mechanism: it would delete objects the
metastore still lists. An operator may add one as a backstop, set longer than
`compacted_retention_days`.

## The source's page

**Admin > Webhooks** lists every source. A source's page shows:

- the URL to give the sender, and the table readers query;
- request counts by outcome for the last hour and the last day;
- when the source last received an event (a sender that stops produces no
  error on the receiving side, so this is how a quiet source shows);
- the window length, the last compacted window, the windows owed a compaction,
  and the last failure;
- the oldest window held, beside the retention settings;
- the last 50 rejected requests: when, the outcome, and why. Never the body.

## Deployment

```yaml
webhooks:
  receiver:
    enabled: true          # default; serve /hooks/ on this replica
    address: ""            # optional: also serve /hooks/ on a listener of its own
    write_timeout: 30s     # a segment not written by then is answered 503
  compactor:
    enabled: true          # default; compact and apply retention on this replica
    grace: 2m              # wait after a window ends for segments still in flight
    poll: 30s
    lease: 10m
    batch: 4
    retry_backoff: 1m
    retention_every: 10m
```

Every source is served on one port and routed by path, so a deployment needs one
Ingress rule for all of them. A sender whose configured URL cannot change is
moved with an Ingress path rewrite from its old path to `/hooks/{source}`.

A deployment that wants bursts kept off the replicas serving MCP and the portal
turns `webhooks.receiver.enabled` off there and runs a receiver-only
deployment of the same image behind the webhook Ingress, with `address` set if
it should listen on a port of its own.

## Metrics

| Series | Labels |
|---|---|
| `webhook_requests_total` | `source`, `outcome`: `accepted`, `unauthorized`, `too_large`, `rate_limited`, `buffer_full`, `write_failed`, `unknown_source`, `invalid_body` |
| `webhook_events_total` | `source` |
| `webhook_segments_written_total` | `source` |
| `webhook_compactions_total` | `source`, `result`: `compacted`, `failed` |
| `webhook_duplicates_dropped_total` | `source` |
| `webhook_buffer_events` | `source`: events held in memory on this replica |
| `webhook_ack_seconds` | `source`: request to acknowledgement |

A request to a name that is not a source is counted with an empty `source`
label, so a caller cannot create series by inventing names.

## Related

- [Scratch Catalog](scratch-catalog.md)
- [Registered Tables](registered-tables.md)
- [Managed Scripts](../scripts/running.md)
- [Threat Model](../security/threat-model.md#inbound-webhooks)

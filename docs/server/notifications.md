# Email Notifications

The platform emails users when something needs their attention: a teammate
shares an asset, collection, or prompt with them, or comments on something
they own. Delivery is durable (a database-backed queue with retries), never
blocks the originating request, and respects per-user preferences including
a daily digest mode.

Email notifications require a database-backed deployment. With no database
the feature is absent; everything else works unchanged.

## How it works

```mermaid
graph LR
    subgraph Triggers
        Share[Share created]
        Comment[Thread comment / feedback]
    end
    subgraph Queue
        Prefs[(user preferences)]
        Rows[(notifications queue)]
    end
    subgraph Delivery
        Worker[Send worker]
        SMTP[SMTP server]
    end
    Share --> Prefs
    Comment --> Prefs
    Prefs -->|off| Drop[Dropped]
    Prefs -->|immediate or daily| Rows
    Rows --> Worker --> SMTP
```

1. When a direct share is created or a feedback thread event is written, the
   platform consults the recipient's preferences and queues a notification
   row. The queue insert is cheap and failures are logged, never surfaced:
   a share or comment always succeeds regardless of notification state.
2. A background send worker claims due rows (immediately via Postgres
   LISTEN/NOTIFY, or on a poll interval), renders a branded HTML email with
   a plaintext alternative, and delivers it over SMTP. Failed sends retry
   with exponential backoff before being marked failed; queued rows survive
   pod restarts and expired delivery leases are reclaimed automatically.
3. Daily-digest users get one email per day summarizing that window's
   events instead of one email per event.

## Admin SMTP settings

Admins configure the mail server in the portal under **Admin, then
Settings**, or via the REST API. Settings are stored in the database
(`platform_settings`), so no config file edit or restart is needed. The
SMTP password is encrypted at rest with the platform's `ENCRYPTION_KEY`
(the same field encryption used for connection credentials) and is
write-only: no API response ever includes it.

| Field | Description |
|-------|-------------|
| `enabled` | Master switch for outbound email. |
| `host`, `port` | SMTP server address. Port 587 for STARTTLS, 465 for implicit TLS. |
| `username`, `password` | SMTP AUTH credentials. Leave username empty for unauthenticated relays. An empty password on update keeps the stored one. |
| `from`, `from_name` | Sender address and optional display name. |
| `tls_mode` | `starttls` (default), `implicit`, or `none` (closed-network relays only). |

The **Send test** action delivers a test email through the stored settings
so the configuration can be verified end to end before users depend on it.
It requires an enabled, saved configuration; a disabled or unconfigured
setup gets a 409 rather than sending around the master switch.

```
GET  /api/v1/admin/settings/smtp        read settings (password_set only, never the password)
PUT  /api/v1/admin/settings/smtp        update settings
POST /api/v1/admin/settings/smtp/test   send a test email  {"to": "admin@example.com"}
```

Like other admin configuration, writes require database config mode; in
file mode the endpoints respond 405.

## User notification preferences

Each user manages their own preferences in the portal under **Settings**
(user section, not admin), or via the self-scoped REST API. A user can only
ever read or write their own preferences.

- **Delivery mode**: `immediate` (one email per event, the default),
  `daily` (one digest email per day), or `off`.
- **Category toggles**: shares, and comments/feedback, each individually
  switchable.

Users with no stored preferences get the defaults: immediate delivery with
all categories enabled. Turning notifications off drops events at enqueue
time; nothing is queued.

```
GET /api/v1/portal/notification-prefs
PUT /api/v1/portal/notification-prefs   {"mode": "daily", "shares_enabled": true, "comments_enabled": false}
```

## Branded emails

Emails are responsive, table-based HTML (broad email-client compatibility)
with a plaintext alternative part. They carry the deployment's brand name
linked to the portal, the implementor footer when configured, deep links to
the shared or discussed item (`portal.public_base_url` must be set for
links to render), and a link to the recipient's notification preferences.
Share links for assets and collections use the token viewer, which works
for the recipient without signing in; prompt shares link to the in-app
prompt page.

## Configuration

Notifications are enabled by default whenever a database is configured. The
YAML section controls only the enqueue/delivery machinery; the SMTP
connection itself is admin-configured at runtime as described above.

```yaml
notifications:
  enabled: false        # opt out of email notifications entirely
  digest_hour_utc: 13   # UTC hour (0-23) daily digests are sent (default: 13)
```

## Delivery semantics

- Immediate rows are picked up as soon as they are queued (LISTEN/NOTIFY)
  or within the worker's 30 second poll fallback.
- A delivery attempt is bounded by a 2 minute lease; if a worker dies
  mid-send, the row returns to claimable state and another replica picks it
  up.
- Failed sends retry up to 5 times with exponential backoff (30s doubling
  to a 32 minute cap), then are marked failed with the error recorded on
  the row.
- When SMTP is unconfigured or disabled, queued rows simply wait without
  burning retry attempts; configuring SMTP later delivers the recent
  backlog.
- Retention bounds the queue table: delivered and failed rows are purged
  after 30 days, and undelivered rows older than 7 days are dropped as
  stale (so enabling SMTP months into a deployment does not deliver an
  ancient backlog).
- A per-actor rate limit (burst of 30, 6 per minute sustained, per
  replica) bounds how much outbound email one account can generate;
  excess events are dropped with a log line, never a request failure.

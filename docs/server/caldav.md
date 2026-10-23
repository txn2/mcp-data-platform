# CalDAV Calendars

A `caldav` connection reads one calendar account over CalDAV (RFC 4791), so the platform can answer which meetings happened, when, who attended and what they were about. Most mail servers that are not Google or Microsoft serve calendars only this way: Fastmail, iCloud, Nextcloud, Radicale, SOGo, Zimbra, Synology, and the CalDAV endpoint of most hosted mail.

It is read-only by design. The kind sends PROPFIND, REPORT and GET and nothing else; there is no PUT, DELETE or MKCALENDAR for a model or a managed script to reach. Every caldav connection reports `read_only: true` in `list_connections`.

## Tools

| Tool | Does |
|---|---|
| `calendar_list` | The calendars on the account: name, href, color and the component types each holds |
| `calendar_events` | The events in a time range on one calendar, or on every calendar that holds events, sorted by start, recurring events expanded into their occurrences |
| `calendar_event` | One event in full by the href `calendar_events` reported: the event, and for a recurring one its master and every instance written apart from its rule |

`calendar_events` takes `start` and `end` as an RFC 3339 time or a `YYYY-MM-DD` date (midnight in the connection's `time_zone`). A missing start is now, a missing end seven days after the start, and a range longer than `max_range_days` is refused. A calendar is named by its display name (any case) or its href.

## What an event says

Each event carries the platform's reading of it beside what its author wrote:

- `start` and `end` in UTC, `time_zone` (the IANA zone the start was written in, `UTC`, or `floating` for a time with no zone, read in the connection's zone), `all_day` with `start_date` and `end_date` (exclusive, as iCalendar writes it), `status` (`confirmed`, `tentative`, `canceled`), `recurring` and `recurrence_id` (the original start of the occurrence, UTC), `last_modified`, `uid`, `calendar`, `href`.
- Under `external`: `summary`, `description`, `location`, `url`, `organizer` and `attendees` (each with `email`, `name`, `role`, and `participation`: accepted, declined, tentative, delegated or needs-action).

Recurrence is expanded by the platform rather than by the server, so the original zone is kept: RRULE, RDATE and EXDATE are applied in the zone the event was written in, across daylight-saving changes, and an instance the organizer moved or edited (a VEVENT with RECURRENCE-ID) replaces the occurrence it stands for, including one moved into the range from outside it. A zone name that is not an IANA name is mapped: Windows names as Exchange writes them (`W. Europe Standard Time`), a path ending in one (`/mozilla.org/.../Europe/Berlin`), or the VTIMEZONE's `X-LIC-LOCATION`. A name none of those resolves is read as UTC and named in `time_zone_unresolved`. `include_raw` returns each event's iCalendar text as well.

## Untrusted text

Anyone can send an invitation, so an event's title, description, location and attendee names are third-party text. They come back under `external`, never beside the platform's own fields, with zero-width, bidirectional and other invisible formatting characters and control characters removed, and every result carries a `notice` saying what `external` is. The separation is structural rather than a delimiter in a string, because a sender can write any delimiter.

## Configuration

```yaml
toolkits:
  caldav:
    instances:
      team:
        url: "https://mail.example.com/dav/"
        auth_mode: basic
        username: "platform@example.com"
        password: "{{secret:team-calendar}}"
        time_zone: "Europe/Berlin"
```

| Key | Meaning |
|---|---|
| `url` | The account's address: the server root, its principal, or its calendar home. Discovery (`current-user-principal`, then `calendar-home-set`, then `/.well-known/caldav` from a bare root) finds the calendars from any of the three, once, and keeps them five minutes. Required |
| `time_zone` | IANA zone a floating time and a date's midnight are read in. Default `UTC` |
| `max_events` | Most occurrences one call returns (1-5000). Default 500; past it the result says `truncated` |
| `max_range_days` | Longest range one call may ask about (1-3660). Default 366 |
| `auth_mode`, `username`, `password`, `credential`, ... | The shared HTTP credential set (see [API Gateway](api-gateway.md)): basic with an app password for most servers, bearer, or the OAuth client_credentials grant. The authorization_code grant is not offered on this kind. `{{secret:<name>}}` names a stored secret and is read at every request |
| `connect_timeout`, `call_timeout`, `tls_ca_bundle_pem`, `max_concurrent_calls`, `static_headers` | The shared transport settings |

Connections are usually saved in the portal under Admin > Connections (kind CalDAV) or through `PUT /api/v1/admin/connection-instances/caldav/{name}`; the kind is enabled automatically. The connection test (`POST .../caldav/{name}/test`) runs discovery and reports the calendars it found.

## Reading calendars through an API connection

An `api` connection can also reach a CalDAV server: `api_invoke_endpoint` accepts `REPORT` beside `PROPFIND` (#2246), forwarded unchanged with its XML body and `Depth` header, honored on a catalog's `x-webdav-method` carrier operation, and classified as a read. `decode: "xml"` parses the multistatus. The `caldav` kind is what to use for events: it expands recurrence and time zones and separates third-party text, which a raw `calendar-query` leaves to the model.

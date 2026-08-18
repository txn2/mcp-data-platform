---
title: Session-Start Notices
description: How platform_info tells a user that feedback is waiting on their work and what has newly been shared with them.
---

# Session-Start Notices

Email reaches a person who reads email; the portal reaches a person who opens
the portal. Someone who works entirely through an agent does neither, and until
they opened the portal they had no way to learn that a colleague had left a
correction on an asset they own, or had shared a collection with them.

`platform_info` closes that gap. The first call of every session carries a
`notices` block addressed to the person behind the agent, and the agent
instructions in the same response tell the agent to relay it before starting on
the request. It needs no configuration: wherever the portal and a database are
present, every authenticated caller gets it.

## What is in it

```json
{
  "notices": {
    "since": "2026-08-10T09:00:00Z",
    "feedback": [
      {
        "thread_id": "thr_01HK7R8Z",
        "kind": "correction",
        "status": "open",
        "title": "wrong currency",
        "author_email": "sme@example.com",
        "asset_id": "asset_01HK7R8Z",
        "asset_name": "Q3 revenue",
        "asset_reference": "mcp:asset:asset_01HK7R8Z",
        "last_activity_at": "2026-08-14T16:20:00Z"
      }
    ],
    "feedback_total": 3,
    "new_shares": [
      {
        "kind": "collection",
        "id": "col_01HK7R9B",
        "name": "Board pack",
        "reference": "mcp:collection:col_01HK7R9B",
        "shared_by": "lead@example.com",
        "shared_at": "2026-08-15T11:02:00Z",
        "permission": "viewer"
      }
    ],
    "new_shares_truncated": false
  }
}
```

**`feedback`** holds unresolved feedback threads on assets the caller owns.
Threads the caller opened themselves, and replies the caller wrote, are
excluded: your own comment on your own asset is not feedback awaiting you. The
list is capped at ten; `feedback_total` is the whole count, so an agent says
"three threads, here are the first two" rather than implying the list is
complete. Each entry carries the asset's `mcp:asset:` reference, which `fetch`
dereferences, and `manage_feedback` answers or resolves the thread.

**`new_shares`** holds assets, collections, and prompts granted to the caller
by name, newest first. A public link nobody was named on is not a share with
anyone and never appears. Each entry carries the reference that reads the
artifact in full, and names the person who made the grant rather than the
artifact's owner: an editor may share someone else's asset, and naming the owner
would credit a person who did nothing. This list is capped at ten too, and
`new_shares_truncated` marks a page that did not fit, so the count reads as a
floor rather than as the whole set.

Both lists are a briefing rather than an inbox. The watermark advances past what
did not fit, so what a capped list left out is not re-offered next session: the
portal's [activity feed](portal-user.md#activity) and
[Shared With Me](portal-user.md#shared-with-me) remain the complete views, and
the agent instructions say so.

The block is absent entirely when there is nothing to report, and for an
anonymous caller.

## Delivered once

`since` is the caller's **notice watermark**: the instant they were last
briefed. Everything reported arrived after it, and delivering the digest
advances it, so the next session is told only what is new since this one. That
is why the agent instructions say to relay the notices rather than act on them
silently — a notice the agent keeps to itself is not repeated.

Two details follow from that:

- A caller who has never been briefed has no watermark, and is briefed on the
  last 30 days rather than their whole history. Without a watermark the
  platform cannot tell what they have already seen in the portal, and
  announcing a two-year-old share as new would be false.
- If one half of the digest cannot be loaded, the watermark does **not**
  advance. A database hiccup delays a notice to the next session instead of
  swallowing it.

The watermark is stored per user in `user_notice_watermarks`, keyed by the
caller's email address (or their user id when they have no email). It is
deliberately not `users.last_seen_at`, which the directory refreshes
asynchronously during the very session whose digest is being computed.

## Relationship to the other surfaces

| Surface | Reaches | Timing |
|---------|---------|--------|
| [Email notifications](notifications.md) | People who read email | On the event, or in a daily digest |
| [Portal activity feed](portal-user.md#activity) | People who open the portal | Whenever they look |
| Session-start notices | People working through an agent | First call of a session |

The three are independent: turning email off does not affect notices, and a
notice relayed in a session does not mark anything read in the portal.

## Ownership scope

Feedback notices cover assets the caller **owns**, resolved by user id, which
is how the portal scopes every other owned-artifact view. Feedback on an asset
merely shared with the caller belongs to its owner's briefing, not theirs; the
portal activity feed is the cross-artifact view that spans everything the caller
can see. A caller whose identity carries no user id owns no assets and so gets no
feedback notices, though shares still resolve for them by email. A service
account authenticating with an API key does have a user id, so an asset it saved
is an asset it owns, and feedback on it reaches that principal's briefing.

---
description: "Home in the portal: what you opened, what you starred, your drafts and what is waiting on you, and the Unused, Possible duplicates and Trash views you tidy your assets from."
---

# Home

Home is the portal's landing page, at `/`. It brings back what you were working on: the assets and collections you opened recently, the ones you starred, your drafts, and what is waiting on you. The Assets gallery has its own page at `/assets`; every asset and collection address that worked before still opens the same page.

Home has four sections, one column on a phone:

- **Recent** — The assets and collections you opened in the portal, newest first. Opening an asset or collection page records it; an open is counted once even if the page reports it twice while it loads.
- **Favorites** — What you starred. The same list sits at the bottom of the sidebar (and of the menu drawer on a phone).
- **My drafts** — Your assets whose status is Draft, with a link to all of them in Assets.
- **Waiting on you** — The feedback threads that need your answer or your sign-off, from the same lists as the Inbox.

Each section says what belongs in it when it is empty.

## Favorites

The star is the same control everywhere an asset or collection is shown: the round button on an asset card, **Star** in the asset viewer's action row, and **Star** in a collection's header. A filled amber star means the item is in your favorites; select it again to remove it.

Favorites and Recent are yours alone and grant nothing. Every read is made under your own access, so an item you can no longer open, because its share was revoked or it was deleted, is simply left out: no title, no placeholder, no count. If the share comes back, so does the entry. You cannot star or record an item you cannot open; the portal answers as if it did not exist.

## Unused, Possible duplicates and Trash

Three tabs sit beside Assets and Collections for tidying your library. Each lists only your own assets; an administrator also gets an **Everyone's** switch, which spans every owner and adds an Owner column. Anyone else who asks for everyone's is refused.

### Unused

Your assets nobody has opened in the portal in the last 30, 90 or 180 days (90 by default), never opened first. The table shows each asset's status, how many times it was opened and when last, never who opened it. Opening an asset takes it off the list. Select assets to **Deprecate** them or **Delete** them to the trash. Opens are counted from the release that started recording them, so an older asset reads "Never" until someone opens it. Low use can also mean an asset is hard to find.

### Possible duplicates

Groups of your assets whose name, description and tags are nearly the same, read from the embedding search already stores for each asset, by the same closeness `save_asset` reports as `similar_assets`. Each group shows how alike its closest pair is, and each asset its last update, version and opens.

- **Keep** marks the one to keep and changes nothing.
- **Deprecate** sets an asset's status to Deprecated with the note "Duplicate of" the kept asset.
- **Delete** sends an asset to the trash.
- **Dismiss group** hides the group until one of its assets gets a new version, or the group gains or loses a member. **Undo** brings it back.

A group with one asset left disappears.

### Trash

The assets and collections you deleted, most recently deleted first, each with the day it is purged for good: its deletion plus `portal.deleted_retention_days` (30 by default). A date within a week is marked.

- **Restore** brings an item back with its versions, shares and collection memberships, so the people it was shared with open it again.
- **Delete permanently** asks first, then removes the item now, with its versions, stored files, shares and comments, as the retention sweep would after the grace period. It cannot be undone.

Only the item's owner or an administrator can restore it or delete it for good; to anyone else it does not exist. A deleted collection is in the trash on its own: the assets it held stay in Assets.

## For agents

`manage_asset` reads the same library as the person calling it:

| Action | What it returns |
|---|---|
| `list_recents` | What the caller opened in the portal, newest first (`limit`) |
| `list_favorites` | What the caller starred |
| `list_trash` | What the caller deleted, with each item's purge date and the retention in days |
| `restore` | Takes `asset_id` or `collection_id` out of the caller's trash, with its shares and collection places |

An item the caller can no longer open is never listed, and a restore of an item that is not in the caller's trash reads as not found.

## REST routes

All under `/api/v1/portal`, behind portal sign-in:

| Route | Purpose |
|---|---|
| `GET /recents`, `POST /recents` | List what you opened; record an open (`{kind, id}`) |
| `GET /favorites`, `PUT` and `DELETE /favorites/{kind}/{id}` | List, star and remove a favorite |
| `GET /trash` | Your trash (`everyone=true` for an administrator) |
| `POST /trash/{kind}/{id}/restore`, `DELETE /trash/{kind}/{id}` | Restore; delete permanently |
| `GET /unused?days=` | Your unused assets (30, 90 or 180) |
| `GET /duplicates` | Your possible-duplicates groups |
| `POST /duplicates/dismissals`, `DELETE /duplicates/dismissals/{digest}` | Dismiss a group; undo |

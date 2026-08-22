---
description: User portal guide covering assets, collections, resources, sharing, knowledge, prompts, and activity tracking with screenshots.
---

# User Portal

The user portal is the day-to-day interface for analysts, engineers, and other data consumers. It provides access to AI-generated assets, curated collections, managed resources, shared content, knowledge capture, prompt templates, and personal activity analytics.

The sidebar is divided into **User** pages (described here) and **Admin** pages (see [Admin Portal](admin-portal.md)).

Every page here is addressable, so a link to one can be bookmarked or handed to a
colleague. An address that names a page which has moved, or that carries a stray
trailing slash, lands on the page it meant. An address with no page behind it says so
and offers the way back, rather than rendering an empty section — an empty page would be
indistinguishable from being told you have none of whatever you asked for, and nothing
was looked up to say that.

![No page at this address](../images/screenshots/light/user-not-found-light.webp#only-light)![No page at this address](../images/screenshots/dark/user-not-found-dark.webp#only-dark)

## Activity

Activity has three tabs: **Overview**, the aggregates; **My Sessions**, the individual sessions those aggregates are made of; and **My Calls**, the queries and API calls those sessions ran.

### Overview

The Overview tab shows your personal tool usage analytics across configurable time ranges (1h, 6h, 24h, 7d).

![Activity](../images/screenshots/light/user-activity-light.webp#only-light)![Activity](../images/screenshots/dark/user-activity-dark.webp#only-dark)

The tab includes:

- **Summary cards** — Total calls, average duration, and tools used in the selected window
- **My Activity chart** — Timeseries of your tool call volume (green) with errors highlighted (red)
- **Top Tools** — Horizontal bar chart of your most-used tools

### My Sessions

The My Sessions tab lists the sessions you ran, most recently active first. A session is every tool call sharing one session id, so it can be read back long after it ended — as far back as this deployment keeps audit history, and after the platform's own session record has expired.

![My Sessions](../images/screenshots/light/user-my-sessions-light.webp#only-light)![My Sessions](../images/screenshots/dark/user-my-sessions-dark.webp#only-dark)

Each row carries the session's kind, the persona you ran it as, how many calls it made, how many of them failed, and what it left behind. The facets narrow the list:

- **Time window** — How far back the list reads (24 hours, 7 days, 30 days, or all time; 7 days by default). The list rolls up every event in range, so a wider window is a heavier query — which is why the window is a visible control rather than a hidden default.
- **Session kind** — Where the id came from: an **Agent** handle threaded across calls, one **Portal run**, one **Script run**, or a **Transport** session
- **Outcome** — Only sessions with at least one failed call
- **Output** — Only sessions that saved at least one asset

There is no user facet, and no user column: this list is always your own. A session id belonging to someone else is answered as not found, the same answer an id that was never used gets. Administrators read every session from [Admin > Sessions](admin-portal.md) instead.

Click a row to open the session.

![My Session](../images/screenshots/light/user-my-session-detail-light.webp#only-light)![My Session](../images/screenshots/dark/user-my-session-detail-dark.webp#only-dark)

The detail shows:

- **The session at a glance** — Calls, failures, wall-clock duration, and the assets and insights it produced
- **What it produced** — The assets it saved, each opening in the asset viewer, and the insights it captured with the review status each is sitting at
- **Timeline** — Its calls in the order they were made, each with the purpose the agent stated for it, the connection it went to, whether it succeeded, and how long it took

The session detail is addressable, so you can bookmark it or hand it to a colleague — though a colleague who is not an administrator will get a not-found, since a session opens only for the person who ran it.

An agent recalls the same sessions the same way you read them: `search` finds them by what their calls said they were for, and `fetch mcp:session:<id>` opens one. The scope is the same one this list applies, so an agent recalls the sessions of the person it is acting for and no one else's. See [Recalling a session](../knowledge/overview.md#recalling-a-session).

### My Calls

The My Calls tab is the catalog of data-access calls you have made: every query against a query engine and every invocation through the API gateway, kept with the reason stated for it and what came of the result.

![My Calls](../images/screenshots/light/user-my-calls-light.webp#only-light)![My Calls](../images/screenshots/dark/user-my-calls-dark.webp#only-dark)

Each row leads with the purpose, because it is the only line about the call a person wrote, and carries the statement under it. The **Outcome** column is what the catalog exists for, and it is derived on every read rather than stored:

| Outcome | What it means |
|---------|---------------|
| `satisfied` | Something was built from the call and **named** it: an asset or a capture whose `sources` cite it, or an export citing the statement it streamed |
| `failed` | The call returned an error |
| `superseded` | A later **read** in the same session addressed the same resource, and nothing was built from this one |
| `ran` | The call succeeded and nothing has come of it yet |

Deriving the outcome rather than storing it means it can never be stale with respect to the asset or the capture that gives it meaning: save an asset citing a query and the query reads satisfied on the next read, with no backfill and nothing to recompute.

Two limits on the rule are worth stating, because their absence produced wrong outcomes:

- **Naming, not proximity.** An asset saved without a `sources` argument still records every call the session made in its [provenance](provenance.md), and that record is not a claim about any one of them. Those calls read `ran`. What makes a call `satisfied` is an artifact naming it.
- **Supersession is read-shaped, over a resolved resource.** A later read of the same thing is a better answer to the same question; a mutation is not a better version of an earlier mutation. And the resource a call addressed includes the values it resolved its path with, so a call against one script is never reported as having been replaced by the same call against another. A call whose target cannot tell it apart from a different call is never declared superseded.

**Reuse** is the other column worth reading. It counts the later sessions that found the record and then ran what it holds: the same statement, or the same API resource, over the same connection. A session running its own query again does not count, and neither does an identical query written independently: without the sighting, nothing says this record led to it. It is the one signal on a record that a stranger, and not its author, found it worth running.

The facets narrow by kind (SQL or API), connection, outcome, and free text over the purpose and the statement. **Awaiting review** keeps the records that answered something and have not been published or declined, most re-run first. There is no user facet and no user column: this list is always your own, and another person's record id is answered as not found.

Click a row to open the record.

![My Call](../images/screenshots/light/user-my-call-detail-light.webp#only-light)![My Call](../images/screenshots/dark/user-my-call-detail-dark.webp#only-dark)

The detail shows the statement or request line, the datasets it addressed, what was built from it (each asset opening in the viewer), the session that ran it, and the `mcp:call:<id>` reference an agent cites it by.

A satisfied record can be **published**. A query becomes a Query entity in the data catalog, associated with every dataset it reads; an API call becomes a saved example on its endpoint, shown to whoever reads that endpoint's schema next. A record you decide is not worth publishing can be **declined** with a note, which stops it being offered.

## Assets

The Assets page displays interactive dashboards, reports, and visualizations generated by AI agents during sessions. Assets are saved via the `save_asset` tool and support multiple content types. A file you wrote yourself and want the agent to use as-is is a [resource](#resources), not an asset; the empty state says so.

![My Assets](../images/screenshots/light/user-my-assets-light.webp#only-light)![My Assets](../images/screenshots/dark/user-my-assets-dark.webp#only-dark)

Features:

- **Search** — Full-text search by name or description
- **Filters** — Content type dropdown (HTML, JSX, SVG, Markdown, CSV) and tag filter
- **Sort** — Column dropdown (updated, created, name, size) and a direction toggle. The list opens on most recently updated, so an asset revised today sits above one created yesterday and never touched since; the date shown on each card and row is the one the list is ordered by, so the visible dates always run in the order the rows do. Sorting is server-side over the whole library, not just the page already loaded. A relevance search is ranked rather than sorted, and the control reads as inert while one is running.
- **View toggle** — Switch between grid (card thumbnails) and table view; preference persisted to localStorage
- **Grid cards** — 4:3 thumbnail previews with content type icon, tags, collection badges, file size, and sharing indicators
- **Theme-aware thumbnails** — Markdown and CSV previews are captured in both light and dark variants, and the grid shows the one matching your active theme. Self-themed content (HTML, JSX, SVG) carries its own colors, so a single preview is used in both modes. Public shares always use the light variant.
- **When a preview is captured** — A missing preview is produced in your browser, by rendering the asset off-screen and rasterizing it. That is a long piece of work on the same thread the page runs on, so it waits: it starts only once the browser has gone idle with the tab in front, runs one asset at a time, stops after eight assets on a visit, and skips any asset over 1 MB, which keeps its placeholder icon. The rest are picked up the next time you open the list, so a large library fills in over a few visits rather than stalling one.
- **Table rows** — Columns for name, type, tags, collections, size, sharing, and the ordering date. Name, size, and the date header sort the list; clicking the active column reverses it.

### Asset Viewer

Click any asset to open the full-screen viewer. The viewer renders content natively based on type: HTML and JSX as interactive components, SVG as vector graphics, Markdown with full formatting, CSV and TSV as sortable tables, JSON as a searchable collapsible tree, images with zoom and pan, audio and video with working seek, and PDFs in an embedded viewer. Anything with no viewer shows a metadata card and a download action rather than raw bytes. See [Content Types and Viewers](content-viewers.md) for the full family list and for how a mislabeled content type is detected and corrected at write time.

![Asset Viewer — HTML](../images/screenshots/light/user-asset-html-light.webp#only-light)![Asset Viewer — HTML](../images/screenshots/dark/user-asset-html-dark.webp#only-dark)

The viewer provides:

- **Preview / Source toggle** — Switch between rendered output and raw source code
- **Actions** — Delete, Download, and Share buttons
- **Owner display** — Shows the asset owner's email address
- **Metadata sidebar** — Type, size, created and updated timestamps, tags, version history, and the calls this asset was built from

The **Provenance** panel groups those calls by capture — one per time the asset was written, so a revised asset shows what fed each of its versions. Each call names its kind (a SQL statement, an API request, or another data call), the connection it ran against, the purpose the agent stated for it, how long it took, and whether it failed; a failed call is shown, not hidden, because it is part of how the answer was reached. Opening a call shows the full statement or request with a copy action and the `mcp:call:` reference that names it in the audit log. A capture marked **Cited** is one where the agent named its sources itself rather than the platform taking the session's recent calls. See [Provenance](provenance.md).

![Provenance panel](../images/screenshots/light/user-asset-provenance-light.webp#only-light)![Provenance panel](../images/screenshots/dark/user-asset-provenance-dark.webp#only-dark)

#### Version retention

Every write to an asset records a version, and until a cap applies that history grows without end. A dashboard a scheduled script refreshes hourly writes twenty-four versions a day, each with its own stored content, for as long as the schedule runs.

An asset keeps its most recent **100** versions by default. The deployment sets that default with [`portal.max_versions`](configuration.md#portal-configuration), and the asset's owner (or an administrator) can override it from the metadata sidebar's edit mode, under **Version history**:

- **Deployment default** — the asset has no opinion and follows whatever the deployment is set to. This is where every asset starts.
- **Keep the newest N** — the asset keeps N versions, however many the deployment keeps.
- **Keep every version** — nothing is ever pruned from this asset.

![Asset metadata edit with version retention](../images/screenshots/light/user-asset-metadata-edit-light.webp#only-light)![Asset metadata edit with version retention](../images/screenshots/dark/user-asset-metadata-edit-dark.webp#only-dark)

A version pushed past the cap is deleted along with its stored content and its thumbnails, so the cap bounds storage and not only the list. The version the asset currently points at is never pruned, whatever the cap, so the content stays readable and the newest entry stays revertible-to.

Retention applies when a version is written, not on a schedule. An asset that already carries more history than the cap is left alone until it is next written, and is trimmed to the cap then. Changing the setting never deletes anything by itself.

Retention is the owner's to set. An editor share carries every other field on that form — name, description, tags, the content itself — but not this one: lowering the cap deletes the owner's history and its stored content at the next write, and nothing brings it back. The control is not shown to an editor, and the API refuses the field from one.

Agents set the same thing through `manage_asset` action `update` with `max_versions` (owner or administrator, as with every other update through that tool), and administrators through the admin asset route; all three write the same value.

The sidebar's **Session** row and the **Open session** action beneath the captured calls both open [the session that made this asset](#my-sessions): the panel shows only the calls captured at the moment the asset was saved, while the session holds every call that session made, before and after. Both appear only on your own assets — a session opens for the person who ran it, so on an asset shared with you there is nothing to link to. Administrators get the same walk on the admin asset viewer, into the admin sessions surface.

The Share action opens a dialog to mint a link or a user-scoped share, with a copy-once token and a per-link access count. A public link takes an expiration, and must have one; every other share is now minted without one and grants access until the owner revokes it. Shares created before that rule keep the expiry they were given, whatever their mode, so an older link may still show a countdown. Every share carries an **access mode** that decides who the link opens for:

| Mode | Who can open the link |
|---|---|
| `restricted` | Only the named recipient (and the person who created the share) |
| `authenticated` | Any signed-in platform user |
| `public` | Anyone with the link, without signing in |

Sharing with a person makes the share `restricted`: the link resolves only for that recipient, signed in, so forwarding the email or the URL grants nothing. The **Share by Link** section mints a link for any signed-in user by default, with no lifetime control, because such a link resolves against who the viewer is and ends on revocation. Choosing **Anyone with the link** makes it `public`, shows a warning that the link opens without sign-in, and reveals the lifetime control: possession of a public URL is the whole of its access check, so it expires on a clock as well. A signed-in user who is not the recipient of a restricted share sees a branded page naming the account they are signed in as, with a sign-out-and-switch action, rather than a generic not-found.

![Share asset](../images/screenshots/light/user-asset-share-light.webp#only-light)![Share asset](../images/screenshots/dark/user-asset-share-dark.webp#only-dark)

The same dialog with the link switched to **Anyone with the link**:

![Share a public link](../images/screenshots/light/user-asset-share-public-light.webp#only-light)![Share a public link](../images/screenshots/dark/user-asset-share-public-dark.webp#only-dark)

Naming a recipient reveals two more controls. **Notify by email** is checked by default and can be cleared to share quietly — the share is created either way, only the email is suppressed. With notification on, an optional **Message** box attaches a short plain-text note to that email, quoted and attributed to the sharer; it travels only in the notification and is stored nowhere. The note takes text, not markup or links: a link inside a trusted platform email is a phishing vector, so one is refused rather than delivered. Addresses pasted in the `Example User <user@example.com>` form mail clients copy are reduced to the bare address as the field loses focus, so what is stored and mailed is what the sharer sees. See [Email Notifications](notifications.md) for what the recipient receives and how they control it.

![Share with a recipient](../images/screenshots/light/user-asset-share-recipient-light.webp#only-light)![Share with a recipient](../images/screenshots/dark/user-asset-share-recipient-dark.webp#only-dark)

A recipient who opens a non-public share while signed out lands on a branded page with a **Sign in** action that returns them to the shared item after authenticating. When the share names an email address, the same page also offers **Email me a one-time view link** for recipients who have no platform account: a single-use link is emailed to the address the share names (never to an address the visitor types), expires in 15 minutes, and opens a view-only guest session scoped to that one share for the current browsing visit. Guests see the shared item (and, for collection shares, its items) with a "Viewing as guest" indicator; they can download but not edit, even when the share grants Editor, and they never gain portal access. A forwarded or replayed link is dead after its first use, and revoking the share cuts off existing guest sessions immediately. The recipient can request a fresh link for each viewing session, which keeps an email share strictly safer than a public URL. When the address the share names has opted out of notification emails, the same landing page shows a notice with a **Resume notification emails** action, so an opted-out recipient has a way back in without asking the sharer; opting back in takes one deliberate click and restores immediate delivery.

All content types are rendered inline:

=== "HTML"

    Interactive dashboards with KPI cards, charts, and tables rendered as live HTML.

    ![HTML Asset](../images/screenshots/light/user-asset-html-light.webp#only-light)![HTML Asset](../images/screenshots/dark/user-asset-html-dark.webp#only-dark)

=== "JSX"

    React components rendered with live state and interactivity (tabbed views, filters).

    ![JSX Asset](../images/screenshots/light/user-asset-jsx-light.webp#only-light)![JSX Asset](../images/screenshots/dark/user-asset-jsx-dark.webp#only-dark)

=== "SVG"

    Vector graphics — charts, diagrams, and data visualizations rendered at full resolution.

    ![SVG Asset](../images/screenshots/light/user-asset-svg-light.webp#only-light)![SVG Asset](../images/screenshots/dark/user-asset-svg-dark.webp#only-dark)

=== "Markdown"

    Formatted text with headings, tables, lists, code blocks, and mermaid diagrams.

    ![Markdown Asset](../images/screenshots/light/user-asset-markdown-light.webp#only-light)![Markdown Asset](../images/screenshots/dark/user-asset-markdown-dark.webp#only-dark)

=== "CSV"

    Tabular data rendered as a sortable, searchable table.

    ![CSV Asset](../images/screenshots/light/user-asset-csv-light.webp#only-light)![CSV Asset](../images/screenshots/dark/user-asset-csv-dark.webp#only-dark)

## Collections

Collections let you organize related assets into curated, shareable groups with rich descriptions and ordered sections.

![Collections](../images/screenshots/light/user-collections-light.webp#only-light)![Collections](../images/screenshots/dark/user-collections-dark.webp#only-dark)

The collections list shows:

- **Search** — Filter collections by name or description
- **New Collection** button — Creates a collection and opens the editor
- **Sort** — The same control as Assets, minus size, which a collection does not have. Collections also open on most recently updated.
- **View toggle** — Grid or table view
- **Grid cards** — Thumbnail mosaic of contained assets, collection name, description, tags, sharing indicators, and the ordering date

### Viewing a Collection

Click a collection to open the viewer, which renders the full collection with sections, markdown descriptions, and asset cards.

![Collection Viewer](../images/screenshots/light/user-collection-view-light.webp#only-light)![Collection Viewer](../images/screenshots/dark/user-collection-view-dark.webp#only-dark)

The editor arranges a collection into drag-and-drop sections of assets, with a markdown description and thumbnail-size settings.

![Collection editor](../images/screenshots/light/user-collection-edit-light.webp#only-light)![Collection editor](../images/screenshots/dark/user-collection-edit-dark.webp#only-dark)

- **Section navigation** — Each section has a title, markdown description, and ordered asset list
- **Asset cards** — Thumbnail previews with name, description, content type badge, and file size
- **Thumbnail size** — Configurable per collection (Large, Medium, Small, None) via Settings
- **Actions** — Back, Edit, Share, and Delete, offered according to what you may actually do with this collection (see [Sharing Collections](#sharing-collections)). A collection you did not create carries a badge naming your access, the same way a shared asset does.

Click any asset card to open it in the asset viewer:

![Collection Asset](../images/screenshots/light/user-collection-asset-light.webp#only-light)![Collection Asset](../images/screenshots/dark/user-collection-asset-dark.webp#only-dark)

### Sharing Collections

Collections use the same sharing system as individual assets:

- **Links**: time-limited token URL, opening for signed-in users or, by explicit choice, for anyone
- **User shares**: share with specific email addresses, restricted to that recipient, with Viewer or Editor permission. The email field suggests known teammates as you type (name + email, with an "Invited" badge for people an admin pre-added who have not signed in yet); you can still type any email that is not in the directory
- **Share management**: view active shares with their access mode, copy links, revoke access

An **Editor** share on a collection grants the collection itself, not only the assets in it: the recipient can rename it, rewrite its description, add and reorder sections, change the thumbnail size, and replace the thumbnail image. What an Editor never gets is owner authority — deleting the collection, sharing it onward, and reading its list of shares stay with the owner, so a person you trusted to edit cannot hand your collection to someone else or destroy it. A **Viewer** share reads and nothing more; the viewer is offered no edit control at all rather than one that fails on save.

Platform administrators hold owner authority over every asset, collection, and personal prompt, including sharing one they do not own. That is not an extra power: an admin can already read, edit, and delete any asset from the admin portal, so sharing is the weaker right of the two. Every share still records who created it, so an admin-created share is attributed to the admin rather than to the owner.

## Resources

Resources are human-uploaded inputs an agent uses as-is: report templates, brand files, data dictionaries, sample payloads, and reference documents. Assets are AI-generated outputs. Knowledge pages are curated facts to search and synthesize. Memory is per-user recall. If it existed before the conversation and the agent should use it verbatim, it is a resource.

That is the whole test for which surface a file belongs on, and [Content Model](../concepts/content-model.md) covers the four layers in full, including the operator rule that makes an approved template mandatory. The page states the same split in its empty state and its upload dialog, so the choice is in front of you at the moment you make it. Agents reach a resource during a session through the MCP `resources/read` protocol method.

An uploaded resource is also **discoverable through `search`**, the front door agents are steered to. A background indexer embeds each resource's metadata and, for text-family files, a bounded prefix of its contents, so a data dictionary is found by a column name that appears only inside the file. Search results carry an `mcp:resource:<id>` reference that `fetch` reads in full (text inline, binary as metadata plus its URI), plus a resource link a client with native resource support can attach directly. Visibility is the same as everywhere else: global resources reach every caller, persona resources only their members, and personal resources only their owner. Indexing runs off the request path, but the upload enqueues its own indexing job rather than waiting for a periodic sweep, so a just-uploaded file is findable by its name and description immediately and by its contents seconds later, once the indexer has read it. A replacement upload and a metadata edit enqueue the same way. Content indexing needs the background index queue, which requires a database and a configured embedding provider; without one, resources are still searchable by their metadata. Files larger than 8 MB are indexed on metadata alone.

![Resources](../images/screenshots/light/user-resources-light.webp#only-light)![Resources](../images/screenshots/dark/user-resources-dark.webp#only-dark)

Uploading opens a modal for the file plus its category, display name, description, and tags. The category says how the agent should treat the file and the dialog spells each one out as you pick it: `templates` are layouts a deliverable must be produced in, `playbooks` are procedures to follow rather than summarize, `samples` are examples to pattern-match against, and `references` are documents to consult. A custom category is accepted for anything that fits none of the four. The file itself may be any format the library needs — documents, spreadsheets, images, media, archives, CAD exports — apart from executables, which are refused by both extension and MIME type ([Accepted types](content-viewers.md#accepted-types)).

![Upload resource](../images/screenshots/light/user-resource-upload-light.webp#only-light)![Upload resource](../images/screenshots/dark/user-resource-upload-dark.webp#only-dark)

The Resources page provides:

- **Scope tabs** — My Resources, admin, and Global tabs for filtering by visibility scope
- **Search and filter** — Text search and category dropdown
- **Upload** button — Upload new resources with name, description, category, and tags
- **Resource table** — Name, category, MIME type, tags, file size, uploader email, and last updated date
- **Delete** — Trash icon to remove owned resources

Administrators can open, edit, and delete any resource by id, including persona material they do not belong to — otherwise an admin could upload a persona resource and then be unable to manage or remove it. Listing and agent-facing reads stay membership-scoped.

Opening a resource shows which prompts attach it as reference material. Deleting a resource that prompts depend on does not break them: they keep serving and report the material as missing, and the prompt viewer flags the broken link so its author can repair it.

The dialog opens on what the resource is — its scope, category, metadata, canonical URI, and an inline preview. Its name and its Download, Edit and Delete actions stay in place while everything between them scrolls, so neither is ever pushed off the screen by a long preview or a deep revision trail.

![Resource detail](../images/screenshots/light/admin-resource-detail-light.webp#only-light)![Resource detail](../images/screenshots/dark/admin-resource-detail-dark.webp#only-dark)

### Revising a resource's content

Scrolling the dialog reaches its lifecycle surfaces: the read-activity rollup, the version history described below, and the prompts attaching the resource.

![Resource lifecycle surfaces](../images/screenshots/light/admin-resource-lifecycle-light.webp#only-light)![Resource lifecycle surfaces](../images/screenshots/dark/admin-resource-lifecycle-dark.webp#only-dark)

**Replace content** on the detail view uploads a new file for an existing resource. The resource keeps its id, its canonical `mcp://` URI, and its file name, so every `mcp:resource:<id>` citation and prompt attachment pointing at it keeps resolving — which delete-plus-re-upload does not, since that mints a new id and breaks them all. The uploaded file's own name is ignored for that reason; only the bytes, type, and size change. Agents connected at the time are told the resource list changed, so a client re-reads the new content rather than serving the old.

Every revision is recorded in **Version history** with its number, who uploaded it, when, and how large it was. Any version can be downloaded, and any prior version can be **restored** — which re-promotes that version's exact bytes as a new head revision rather than rewinding, so the trail stays append-only and the restored content is itself restorable. A restored revision is labeled with the version it came from.

History is bounded: a resource keeps its most recent 10 revisions by default ([`resources.managed.max_versions`](configuration.md#managed-resources)), and a revision past the cap deletes the oldest version's stored file. The live content is never pruned.

### Seeing what is actually used

The detail view shows **Usage**: reads over the last 30 and 90 days, broken down by which door served the content — an agent's `resources/read`, a `search` fetch, or a portal download — plus when it was last read. The admin resources table adds a **Last read** column and a *Recently read* sort, so a curator can order the library by recency and find material nothing has touched; a resource never read since it was uploaded over 30 days ago is flagged.

These counts come from the read audit trail, so they are bounded by the deployment's audit retention window, and a deployment with `audit.enabled: false` records no reads and shows no usage (reads themselves are unaffected). Listing resources is not a read: only content actually served counts.

### Attaching resources to a prompt

A prompt is a procedure, and a procedure usually depends on material: the template it fills, the checklist it follows, the brand header it embeds. The prompt viewer has an **Attached materials** panel where the prompt's owner (or an admin, for shared prompts) attaches resources from a searchable picker, orders them, and detaches them. The order is authored, not incidental, because it is the order the agent receives them in.

Every agent that runs the prompt receives the attached material as authoritative: text files inline, larger or binary files as links it can read on demand.

An attachment must be at least as widely visible as the prompt. A private resource belongs only on your own personal prompts, and a persona resource only on prompts for that same persona. Attaching something narrower is refused with a message naming the resource, and so is requesting promotion of a prompt that still carries it, because a shared prompt whose materials most readers cannot open is worse than one with no materials at all.

## Shared With Me

Items that other users share with you appear in the corresponding pages, filtered by ownership scope:

- **Assets**: the Assets page has a Mine / Shared / All scope control. Shared assets show content type badges, tags, sharer email, permission level (Viewer/Editor), file size, and share date
- **Collections**: the Collections page has the same Mine / Shared / All scope control, listing shared collections with sharer and access level
- **Prompts**: prompts shared with you appear in the Prompts page's **My Prompts** bucket with a "Shared by" attribution. These are real runnable prompts: your agent can invoke a shared prompt over MCP as `shared-<name>`, or resolve it by name with `manage_prompt use`

![Shared Assets](../images/screenshots/light/user-assets-shared-light.webp#only-light)![Shared Assets](../images/screenshots/dark/user-assets-shared-dark.webp#only-dark)

Click any shared asset to open it in the viewer:

![Shared Asset](../images/screenshots/light/user-shared-asset-light.webp#only-light)![Shared Asset](../images/screenshots/dark/user-shared-asset-dark.webp#only-dark)

## Feedback

Feedback lets the people who review your work, including subject-matter experts and stakeholders who do not use an agent, leave structured corrections and questions on the things you share with them, instead of relaying that feedback over email.

Feedback is organized into **threads**. A thread targets one asset, collection, prompt, or knowledge page, or it lives on a **standalone channel** for general feedback not tied to a single object. Each thread has a kind (comment, question, correction, rating, approval, rejection, or suggestion), a status (open, answered, resolved, won't fix, acknowledged), an optional `requires_resolution` flag, and a timeline of events (the opening message plus replies and status changes). A thread can be anchored to a specific selection within the target so a correction like "we don't use that term" stays pinned to the place it refers to, along with the version it was raised against. Standalone-channel threads are visible to every signed-in user; feedback on an asset, collection, or prompt is visible to people who can already view that object; knowledge pages are org-shared, so any signed-in user can read and add feedback on them.

Feedback you have not seen also reaches you outside the portal: an
[email notification](notifications.md) when a thread event lands, and a
[session-start notice](session-notices.md) on the first `platform_info` call of
an agent session, which lists the unresolved threads other people left on assets
you own.

### The feedback panel

Open the **Feedback** button in an asset, collection, prompt, or knowledge-page viewer to slide out the feedback panel. It lists the threads on that item with their kind, status, and activity, and a header counts how many are open and how many still need resolution. Selecting a text passage in markdown or plain-text content (an asset, a prompt, or a knowledge page) before opening **New** lets you anchor your feedback to that selection. In the Knowledge hub, each knowledge-page card shows an open-thread badge so you can see where feedback is waiting.

![Asset feedback panel](../images/screenshots/light/user-asset-feedback-light.webp#only-light)![Asset feedback panel](../images/screenshots/dark/user-asset-feedback-dark.webp#only-dark)

Opening a thread shows its full timeline. Anyone can reply; the item's owner, an editor, or an admin can change the status (for example resolve it) or delete the thread. The status change is recorded on the timeline.

![Feedback thread detail](../images/screenshots/light/user-asset-feedback-detail-light.webp#only-light)![Feedback thread detail](../images/screenshots/dark/user-asset-feedback-detail-dark.webp#only-dark)

### Mentioning a teammate

Type `@` anywhere in a feedback message or reply to address someone directly. The composer suggests people as you type and inserts the person as `@marcus.johnson(example.com)`, which reads as a name in the thread and is stored as an address so it keeps working when someone's display name changes.

![Mentioning a teammate](../images/screenshots/light/user-asset-feedback-mention-light.webp#only-light)![Mentioning a teammate](../images/screenshots/dark/user-asset-feedback-mention-dark.webp#only-dark)

The suggestions are the people who can already open the item being discussed: its owner and everyone it is shared with, directly or through a collection. Knowledge pages and the standalone channel are open to every signed-in user, so any known user may be mentioned there. This is deliberate. A mention sends the item's title and an excerpt of the comment by email, so it may only go to someone who could open the item anyway.

You can still type an address by hand. If it belongs to someone without access, the composer says so while you are writing and the mention posts as ordinary text: it is not recorded, not rendered as a chip, and delivers nothing. Share the item with them first, then mention them.

Being mentioned is its own notification category, separate from general comment activity, so someone who muted thread chatter still hears when a comment names them. The **Mentions of me** tab in the feedback inbox lists every thread where a comment addressed you.

Agents leave feedback through the same path: a reply written with the `manage_feedback` tool carries mentions and fires the same notifications as one written in the portal, on deployments that run the HTTP server. Email delivery lives with that server, so a stdio-only deployment stores the reply but sends nothing.

### Turning feedback into knowledge

A correction or suggestion is only useful if it can change something. When you have **apply_knowledge** access, an unresolved correction or suggestion thread shows a **Capture as insight** action in its detail view. Capturing it creates a pending insight from the thread (its title and first comment) that enters the review queue alongside insights captured by agents, and resolves the thread with a link to that insight. From there the normal apply_knowledge review and promote/apply pipeline takes over: once the insight is promoted to a knowledge page or applied to the catalog, the thread's knowledge chain shows the resulting change, closing the loop for both the reviewer and the person who raised the feedback. This is how feedback on any content becomes durable, reviewed knowledge rather than a dead-end comment.

The **Feedback** page in the sidebar is the standalone channel for general feedback. The My Assets and Collections lists show an open-thread badge on items you own so you can see at a glance where feedback is waiting.

![Feedback channel](../images/screenshots/light/user-feedback-light.webp#only-light)![Feedback channel](../images/screenshots/dark/user-feedback-dark.webp#only-dark)

### Leaving feedback through a public link

When you share an asset or collection with a public link, an anonymous visitor can view it and sees a **Sign in to leave feedback** prompt. Signing in through that link, when the visitor has no prior share for the item, grants them a viewer share automatically so the item appears in their portal and they can leave feedback. An existing editor is never downgraded to a viewer by this flow.

## Knowledge

The Knowledge page is the single home for the **Memory to Insight to Knowledge** lifecycle. A short header teaches the model so a first-time reader can state what each stage is and how one becomes the next:

> Everything the platform learns is a **Memory**. Most memories are personal or operational and stay yours. When a memory asserts something true about the business or the data that others would benefit from, it becomes an **Insight**, a proposal awaiting review. Whoever holds the `apply_knowledge` capability reviews insights and promotes the good ones into **Knowledge**: shared, trusted, and canonical. Business and domain facts become knowledge pages; technical and entity facts go to the DataHub catalog.

The page has three tabs. Review and promote affordances appear only when your persona grants the `apply_knowledge` tool (a capability check, not an admin role).

Knowledge pages are facts to be searched and synthesized into new answers. A file you wrote and want reproduced verbatim belongs in [Resources](#resources) instead, where its bytes and formatting survive; the knowledge pages empty state says so.

### Knowledge (default)

- **Unified search** - One query fans across every source you can access (the DataHub catalog, canonical knowledge pages, your memory, captured insights, saved assets, uploaded resources, prompts, managed scripts, API endpoints, and connections) and returns results grouped by source with a coverage summary. It is the same federation behind the `search` tool, exposed over `GET /api/v1/portal/search`. It ranks semantically when an embedding provider is configured and degrades to lexical search otherwise
- **Browse** - With the search box empty, the tab browses the canonical knowledge pages. Personas with `apply_knowledge` can create, edit, and remove pages
- **Changesets** (`apply_knowledge` holders) - The record of insights promoted into knowledge: the catalog and knowledge-page changes applied when your agent runs `apply_knowledge`, with rollback to undo a changeset's writes. They live here, with the promoted knowledge, rather than with the unpromoted insights in the review pipeline

One query returns results grouped by source (catalog, knowledge pages, insights, memory, assets, prompts) with a per-source coverage summary and source filter chips.

![Unified search](../images/screenshots/light/user-knowledge-knowledge-light.webp#only-light)![Unified search](../images/screenshots/dark/user-knowledge-knowledge-dark.webp#only-dark)

#### Built-in pages

The platform ships a set of its own knowledge pages — the rationale behind the advanced features that is not readable from tool schemas: writing managed scripts (the Starlark dialect's deliberate absences, DECIMAL columns arriving as strings, the save being the version that runs), script outputs and export identity (a stable name is a refresh, a dated name is an archive), the semi-dynamic dashboard pattern (`platform.publish_data` and the data region), and provenance, call references, and the capture loop. They are embedded in the binary and reconciled into the knowledge-page store at startup, so a release that changes them updates every deployment on its next start. Once reconciled they are ordinary pages: `search` ranks them, `fetch` dereferences them, the portal renders them, and feedback threads work on them.

Each carries a **Built-in** badge and is read-only where people edit: the portal offers no Edit, and the update paths refuse a change that the next start would overwrite anyway. A deployment that wants its own version of a topic hides the built-in page (**Hide**, the same action Remove is on an ordinary page) and writes its own — the startup reconcile respects the hide instead of resurrecting the page, and a deployment page holding the topic's slug is left alone. Hiding is not a one-way door: **Restore built-in** on the Knowledge list (apply_knowledge access, also `POST /api/v1/portal/knowledge-pages/restore-builtin`) brings hidden built-in pages back, refreshed to the running release; a hidden page whose topic a deployment page has since taken stays hidden, since that page owns the slug. An unchanged release touches nothing, so a page's version history records exactly the releases that changed it, and a page a release stops shipping is retired everywhere — which, unlike a hide, does not survive the page being shipped again later.

#### Cards and graph

The **Knowledge Pages** sub-tab offers two layouts of the same corpus, switched with the Cards/Graph toggle. Cards is the browse list. **Graph** draws the corpus as its reference network: every page is a node, so is every entity the pages reference (assets, prompts, collections, connections, catalog URNs, and other pages), and every edge is a stored reference, drawn as an arrow from the page to what it cites.

**It opens on one node, not the whole corpus.** A whole-corpus force layout is a hairball that answers nothing, so the view starts at the corpus's strongest *bridge* — the node the most shortest paths run through — and shows its neighbourhood. **Hops** widens that neighbourhood one step at a time, and **Whole corpus** drops back to the overview when you want the shape of the entire knowledgebase.

The structure is measured, not just drawn. The corpus is partitioned into clusters (Louvain community detection) and each node is scored for how much of the graph it bridges (betweenness centrality). Node size is that bridge score, so the entities holding otherwise separate topics together are the largest marks on screen; node shape and colour stay with the type. In the whole-corpus overview each substantial cluster is tinted as a region behind its members, and the layout pulls a cluster's members together so those regions are actually distinct. The summary line states how many clusters were found and the partition's modularity, so you can tell whether the corpus really has topic structure rather than inferring it from a picture.

Clicking a node **inspects** it rather than navigating away. The inspector reports how many references run each way, the node's bridge score and its rank among everything that bridges anything, and its cluster; it lists both directions of references, each selectable in place so you can walk the corpus without a single page load. For anything that is not a knowledge page it also shows the reference's **URN** — the identifier the node's label is derived from, and the only string you can search or act on elsewhere. Its actions are **Focus** (re-centre the view here), **Expand** (pull in this node's neighbours), **Path from** (then click any other node to trace the shortest chain of references between them, highlighted on the canvas and listed hop by hop), and **Open**, which is the only thing that leaves the graph. A connection has no per-instance portal page, and a reference whose target has been removed has nothing left to open, so neither offers Open.

Selecting a **catalog** node looks the dataset up in the DataHub catalog and shows what is actually there — its description, domain, owners, and tags — naming the connection it queried. When the catalog does not have it, the inspector says so: a knowledge page citing a dataset that is not in your catalog is a real gap, not an ordinary node, and the graph states it rather than drawing it as if it resolved. **Open** deep-links the entity in the Knowledge > Catalog tab (`/knowledge/catalog?urn=...`), which is also what a catalog reference chip links to anywhere else in the portal.

Hovering a node lights its immediate neighbourhood and dims the rest; dragging pulls a cluster apart and it stays where you drop it (**Reset layout** releases every pin). Scroll to zoom and drag the canvas to pan. The **Show** chips filter by node type — knowledge pages are always shown, since they are the corpus the view is of — the tag facet narrows which pages are in the graph at all, and the search box focuses matching nodes rather than removing the rest. Switching between Cards and Graph preserves both the search text and the tag filter.

Entities you cannot access are absent from the graph entirely — neither node nor edge — which is the same visibility rule the per-page reference list applies. A very large corpus is capped, and the cap is always stated in a notice above the canvas rather than applied silently.

![Knowledge graph](../images/screenshots/light/user-knowledge-graph-light.webp#only-light)![Knowledge graph](../images/screenshots/dark/user-knowledge-graph-dark.webp#only-dark)

The whole-corpus overview, with the detected clusters drawn as regions:

![Knowledge graph, whole corpus](../images/screenshots/light/user-knowledge-graph-corpus-light.webp#only-light)![Knowledge graph, whole corpus](../images/screenshots/dark/user-knowledge-graph-corpus-dark.webp#only-dark)

#### What a page links to

A knowledge page's **Manual references** panel is where an editor states what the page is about. It searches the portal's own entities — assets, collections, pages, prompts — and, when a DataHub connection is configured, the catalog's governance vocabularies: **glossary terms**, **tags**, and **domains**. Every type is searched by display name, so attaching the term *Net Revenue* never means typing the URN DataHub generated for it; picking a catalog type also asks which connection to search, since a governance entity belongs to one catalog and the portal's own entities belong to none. A deployment with no DataHub connection is offered only the four portal types, rather than three that could never return a match.

An attached reference renders as a named chip in the **Related** panel and links to where that entity is managed: a glossary term to the Glossary tab, a tag to Tags, a domain to Domains, a table to Tables. The names are resolved from the catalog itself, because the key inside a governance URN is not a name — DataHub generates one for anything created without an explicit id — so a chip built from the URN alone would read as `8f3c1a94` where the page meant *Net Revenue*. When the catalog cannot be reached, the chip falls back to that key rather than failing the page. Resolving a name is a catalog read, so it is gated the same way the Catalog tab is: a persona granted no DataHub tool sees the URN-derived label, since a name it could not look up there should not arrive here instead.

The link runs both ways: each governance detail page lists the knowledge pages that reference its entity, so a steward reading a term sees what has been written about it. References you cannot access are omitted from both directions.

### Catalog

The **Catalog** sub-tab is the whole of your data catalog inside the portal, and the second of the two knowledge sinks. **Everything under Catalog is DataHub**; anything the portal's own database backs — knowledge pages, changesets — stays outside it. That rule is what keeps the top row at four tabs while the catalog surfaces grow underneath.

Catalog holds its own inner tabs: **Tables**, **Context Docs**, **Tags**, **Domains**, and **Glossary** — the described things first, the vocabularies that describe them second. The DataHub connection is picked once, at the top of the section, and applies to every inner tab; switching it returns each tab to its list, since an open table, document, tag, domain, or glossary entity belongs to the connection it was read from. The section is URL-addressable at `/knowledge/catalog`, and the inner tab is carried in the hash (`/knowledge/catalog#tags`), so a refresh and browser back/forward land where you were. A single entity stays addressable at `/knowledge/catalog?urn=...`, which is what a catalog reference links to from anywhere in the portal; the link names the tab that manages that kind of entity (`?urn=urn:li:glossaryTerm:...#glossary`), because Tables cannot show a term at all. Each tab claims only its own kinds, so a stale or hand-edited link opens the list rather than a read that could not succeed. Going back from a deep-linked entity drops the `?urn=` so a refresh does not reopen what you just left.

![Catalog](../images/screenshots/light/user-knowledge-catalog-light.webp#only-light)![Catalog](../images/screenshots/dark/user-knowledge-catalog-dark.webp#only-dark)

#### Tables

The **Tables** tab browses or searches the tables the connection catalogs, and opens one to see its description, tags, owners, glossary terms, domain, and columns. When your persona grants `datahub_update` and the connection is write-enabled, each metadata facet is editable inline (description, tags, owners, glossary terms, domain); otherwise the view is read-only with no edit controls. Tags, glossary terms, and domains are chosen through name-search pickers: type a display name (e.g. `Reven`) and select **Revenue**, and the URN is resolved for you. Table and column descriptions are markdown: the entity view renders them formatted, and the description editor is the same split source/preview markdown editor used for knowledge pages and prompts. Owners are entered as a DataHub user or group URN (`urn:li:corpuser:<name>` or `urn:li:corpGroup:<name>`); an invalid value is rejected with a clearly visible inline error rather than silently failing. DataHub has no table create or delete (tables originate in source systems), so this is metadata editing, not table lifecycle.

#### Context Docs

The **Context Docs** tab manages DataHub context documents: markdown notes attached to a dataset, glossary term, glossary node, or container. Browse or search, open a document to read its rendered markdown, and, with the matching `datahub_create` / `datahub_update` / `datahub_delete` grant on a write-enabled connection, create, edit, and delete documents through a markdown editor. A document can attach only to the supported entity types; the create form rejects any other type.

#### Tags

The **Tags** tab manages the tag vocabulary itself, rather than the tags carried by one table. List the connection's tags with their descriptions, filter by name, and open a tag to see what it means and which tables carry it; each of those tables links straight into the Tables tab's entity editor. With the matching grant on a write-enabled connection you can create a tag (`datahub_create`), edit its description (`datahub_update`), and retire one (`datahub_delete`); without those grants the same read surfaces appear with no editing controls. A tag description is plain text, not markdown, and this is the one deliberate exception among the Catalog vocabularies: DataHub's own tag page renders the field as plain text, so formatting authored here would show as raw source everywhere else in the catalog. An open tag also lists the **knowledge pages that reference it**, so the prose written about a tag is one click from the tag itself; a tag no page cites shows no such list. Deleting states its blast radius first — how many tables in the connection carry the tag — so retiring an unused tag and retiring one the warehouse depends on do not look identical. DataHub indexes new tags asynchronously, so a tag you just created may take a moment to appear in the list.

#### Domains

The **Domains** tab manages the business areas the catalog is grouped into, rather than the domain carried by one table. List the connection's domains with their descriptions, filter by name, and open a domain to see what it covers and which tables are in it; each of those tables links straight into the Tables tab's entity editor. With the matching grant on a write-enabled connection you can create a domain (`datahub_create`), edit its description (`datahub_update`), retire one (`datahub_delete`), and move tables in and out of it (`datahub_update`); without those grants the same read surfaces appear with no editing controls. Domain descriptions are markdown: the domain view renders them formatted, and the description editor is the same split source/preview markdown editor used for table descriptions, knowledge pages, and prompts.

An open domain also lists the **knowledge pages that reference it**, the same reverse lookup the tag and term views offer.

Two limits the tab states rather than hides. The domain list is capped at 100 by DataHub itself — its `listDomains` query asks for that many and the lookup endpoint takes no limit — so a full list means there are domains this page cannot reach, and it says so. A table has at most one domain, so adding a table that is already in another domain **moves** it rather than giving it a second; the add form says so before you pick.

Deleting states its blast radius first — how many tables in the connection are in the domain — and what it does with them: the delete removes the domain definition from DataHub and leaves those tables without a domain, since it touches no table. As with tags, DataHub indexes new domains asynchronously, so a domain you just created may take a moment to appear in the list.

#### Glossary

The **Glossary** tab manages the business glossary itself: the terms your organization defines and the nodes that organize them, rather than the terms carried by one table. It is a tree, so it is walked one branch at a time — the root shows the nodes and the terms with no parent, and opening a node shows what is inside it. A node is both a place in the tree and an entity of its own, so browsing into one *is* its detail view: its definition, its attached documents, and its children on the one screen.

Opening a term shows five things: its definition, where it sits (a breadcrumb built from DataHub's parent chain, so it is the same wherever you reached the term from), the context documents attached to it, the knowledge pages that reference it, and the tables annotated with it. Each of those tables links straight into the Tables tab's entity editor, and the ones where a **column** rather than the table carries the term are marked. That distinction takes two reads: DataHub's `glossaryTerms` search filter matches a table annotated at either level, and only `fieldGlossaryTerms` isolates the column-level ones, so listing the first alone would report every carrier as a column carrier.

With the matching grant on a write-enabled connection you can create a term or a node (`datahub_create`), edit either's definition (`datahub_update`), and retire a term or an **empty** node (`datahub_delete`); without those grants the same read surfaces appear with no editing controls. Term and node definitions are markdown: both views render them formatted, and the definition editor is the same split source/preview markdown editor used for table descriptions, knowledge pages, and prompts — so a definition can carry a heading, a list of the cases it includes and excludes, and a worked example. A new term or node lands in the branch you have open, and the form says which one that is before you submit.

A node that still holds entries is not offered a delete at all. DataHub takes the node without taking what is inside it, so the honest options are to empty it first or leave it, and the tab says which — a confirmation that cannot state the outcome would be worse than no button. Deleting a term does state its blast radius: how many tables in the connection are annotated with it, and that the delete removes the term definition without removing the annotation from those tables, since it touches no table. As with tags and domains, DataHub indexes the glossary asynchronously, so what you create may take a moment to appear in the branch.

One glossary backs both surfaces: a term defined here is immediately what the Tables tab's glossary picker offers, as it is in DataHub.

These tabs are backed by the portal DataHub REST API at `/api/v1/portal/datahub/{connection}/...`. Reads require DataHub access on your persona; a write is permitted only when your persona grants the matching MCP tool **and** the target connection is write-enabled (`read_only: false`). Both checks are enforced server-side regardless of what the UI shows, and every write is recorded in the audit log. Tag and glossary-term edits are applied as batched add/remove sets so concurrent edits do not clobber one another. The pickers are backed by name-search lookup endpoints (`catalog/lookup/tags`, `catalog/lookup/glossary-terms`, `catalog/lookup/domains`). A malformed metadata value is rejected with `400 Bad Request`; `502 Bad Gateway` is reserved for genuine upstream DataHub failures.

#### Tag endpoints

Managing the tag vocabulary adds two routes, because the reads it needs already exist. Listing and name-filtering tags is `GET catalog/lookup/tags`, the same read behind the tag picker; the datasets carrying a tag are `GET catalog/search?q=*&tags=<urn>`, through the catalog search's tag filter; and a tag's description is edited with `PUT catalog/entity/description`, which takes any entity URN.

| Endpoint | Behavior |
| --- | --- |
| `POST catalog/tags` | Creates a tag from `{name, description}` and returns the URN DataHub assigned it, `201`. Gated on `datahub_create` and a write-enabled connection. |
| `DELETE catalog/tags?urn=` | Retires a tag definition. Gated on `datahub_delete` and a write-enabled connection. The URN is a query parameter rather than a path segment because a tag URN is itself colon-delimited. |

A URN that is not a tag is a `400` before the call reaches DataHub, rather than a forwarded call surfacing as a misleading `502`. A newly created tag is not immediately listable: the list read is served from DataHub's search index, which is populated asynchronously, so the returned URN is authoritative until the index catches up.

#### Domain endpoints

Managing domains adds two routes, for the same reason tags did: everything else it needs already exists. Listing domains with their descriptions is `GET catalog/lookup/domains`, the same read behind the domain picker; the tables in a domain are `GET catalog/search?q=*&domain=<urn>`, through the catalog search's domain filter; a domain's description is edited with `PUT catalog/entity/description`; and moving a table into or out of a domain is `PUT catalog/entity/domain`, the same write the per-table entity editor makes, aimed at the table rather than at the domain.

| Endpoint | Behavior |
| --- | --- |
| `POST catalog/domains` | Creates a domain from `{name, description}` and returns the URN DataHub assigned it, `201`. Gated on `datahub_create` and a write-enabled connection. |
| `DELETE catalog/domains?urn=` | Retires a domain definition. Gated on `datahub_delete` and a write-enabled connection. The URN is a query parameter rather than a path segment because a domain URN is itself colon-delimited. |

A URN that is not a domain is a `400` before the call reaches DataHub. `GET catalog/lookup/domains` returns at most 100 domains because the upstream `listDomains` query is fixed at that count; the portal reports a full list as capped rather than as complete.

#### Glossary endpoints

The business glossary is a tree — nodes containing sub-nodes and terms — and the name-search picker above flattens it. These endpoints expose the structure itself, so a client can browse and maintain the glossary rather than only search it. They require mcp-datahub v1.15.0 or later.

| Endpoint | Behavior |
| --- | --- |
| `GET catalog/glossary/roots` | The top of the tree: the nodes and the terms with no parent. Nodes and terms carry separate totals (`nodes_total`, `terms_total`) because DataHub pages the two independently. |
| `GET catalog/glossary/children?urn=` | One page of the nodes and terms directly under a glossary node. DataHub pages a node's children as one mixed collection, so `start`, `count`, and `total` describe the combined page rather than either slice. |
| `GET catalog/glossary/parents?urn=` | The ancestor nodes of a glossary term or node, direct parent first, so a client can render a breadcrumb without walking the tree. Each node carries its own parent URN, the next link up. |
| `GET catalog/glossary/term?urn=` | One term by URN: its name and its definition. It is what opens a term a knowledge page cites — a citation carries only the URN, which neither the name-search picker nor the hierarchy reads can start from. There is no node counterpart because upstream has no by-URN node read; a node is reached by browsing the tree. A term the catalog does not hold is a `404`. |
| `POST catalog/glossary/nodes` | Creates a node from `{name, definition, parent_node}` and returns its URN. An empty `parent_node` creates it at the root. Gated on `datahub_create` and a write-enabled connection. |
| `POST catalog/glossary/terms` | Creates a term from the same body, and is the same handler: a term and a node differ only in which upstream call runs. Gated on `datahub_create` and a write-enabled connection. |
| `DELETE catalog/glossary/entity?urn=` | Retires a glossary term **or** node. One route for both kinds because upstream is one call. Gated on `datahub_delete` and a write-enabled connection. |
| `GET catalog/entity/documents?urn=` | The context documents attached to one catalog entity — a dataset, a glossary term, or a glossary node. The corpus-wide `documents/browse` and `documents/search` cannot express it: neither is scoped to what a given entity carries. |

Two things the glossary needs are not routes of their own, because they already exist. A term's or node's **definition** is edited with `PUT catalog/entity/description`, which takes any entity URN: DataHub stores a glossary entity's text in the `glossaryTermInfo` / `glossaryNodeInfo` aspect's `definition` field, and the platform routes the write there by entity type. And the tables a term is applied to come from the catalog search's glossary filters: `GET catalog/search?q=*&glossary_term=<urn>` for every table annotated with it, and `&column_glossary_term=<urn>` for those where a column carries it. The two are distinct because DataHub's `glossaryTerms` index folds column-level annotations into the table's, and only `fieldGlossaryTerms` isolates them; there is no table-level-only filter field.

Each node in a read carries `terms_count` and `nodes_count`, DataHub's own tally of its direct children, so a browser can render an expandable branch without first fetching it.

A URN of the wrong kind is a `400` — children hang off a node only, while a parent chain and a delete accept either kind of glossary entity — and a node DataHub does not know is a `404`, not a `502`.

Deleting a node does not delete what is inside it, and deleting a term does not remove the term from the tables annotated with it: upstream `DeleteGlossaryEntity` touches only the entity named. That is why the portal shows a node's children and a term's usage before offering the delete.

A node's children come from DataHub's graph index, which is populated asynchronously: a term or node created moments earlier may not appear under its parent yet. The parent chain reads the entity itself and is immediately consistent, so it is the reliable way to confirm a just-written parent.

### Insights

The review pipeline for insights, which are the only memories that cross between users. A pending-review count is badged on the sidebar Knowledge item and the Insights tab so reviewers notice work without opening it.

- **Your insights** - The insights captured from your sessions, with status (pending, approved, applied, rejected) and relevance search
- **Review queue** (`apply_knowledge` holders) - Every user's captured insights. Approving and rejecting curates which insights are worth promoting; the actual promotion into durable knowledge happens when you ask your agent to run `apply_knowledge`, whose synthesize step gathers the approved insights and writes business and domain facts to knowledge pages and technical and entity facts to the DataHub catalog

![Insights](../images/screenshots/light/user-knowledge-insights-light.webp#only-light)![Insights](../images/screenshots/dark/user-knowledge-insights-dark.webp#only-dark)

### Memory

Memory is personal: this tab is scoped to your own records. The only memory that crosses to other users is an insight, reviewed in the Insights tab, and it crosses when it is applied: applying an insight writes it to a canonical sink and makes it findable by everyone through `search`, attributed to whoever captured it. An insight that is still pending or approved stays yours alone.

- **Your memory** - The raw substrate captured from your sessions, classified by lifecycle **class** (`sink_class`): Preference, Event, Business knowledge, Operational rule, and Schema/entity. The class is why something is "just memory" versus a candidate for promotion

![Memory](../images/screenshots/light/user-knowledge-memory-light.webp#only-light)![Memory](../images/screenshots/dark/user-knowledge-memory-dark.webp#only-dark)

The former Knowledge Pages, Knowledge & Memory, and admin Knowledge & Memory routes now redirect into this one page.

See [Knowledge Capture](../knowledge/overview.md) and [Memory Layer](../memory/overview.md) for how these are created during sessions.

## Prompts

Prompts are reusable templates that guide AI agent behavior: the organization's SOP manual for agent-run procedures. The library presents two buckets: **My Prompts** (every prompt you own, whatever its scope — shared-scope prompts carry a scope badge — plus prompts shared with you, each attributed to its sharer) and **Library** (the approved team prompts visible to you). Scope and persona mechanics appear only inside the promote and admin flows.

![Prompts](../images/screenshots/light/user-prompts-light.webp#only-light)![Prompts](../images/screenshots/dark/user-prompts-dark.webp#only-dark)

Both buckets group prompts into **collections**: named groups organized by team, domain, or workflow. Each group heads its table with the collection name, its prompt count, and the collection description beneath. Uncollected prompts list under a default General group. Search results are the exception: they hold their relevance order in one flat list.

![Prompt library](../images/screenshots/light/user-prompts-library-light.webp#only-light)![Prompt library](../images/screenshots/dark/user-prompts-library-dark.webp#only-dark)

The **Collections** button opens the manager for creating, renaming, and deleting collections.

![Manage collections](../images/screenshots/light/user-prompt-collections-light.webp#only-light)![Manage collections](../images/screenshots/dark/user-prompt-collections-dark.webp#only-dark)

Creating a prompt uses an inline markdown editor that auto-extracts `{argument}` placeholders into a typed arguments table.

![Create prompt](../images/screenshots/light/user-prompt-create-light.webp#only-light)![Create prompt](../images/screenshots/dark/user-prompt-create-dark.webp#only-dark)

Opening a prompt shows its rendered content, arguments, actions (copy, save-as-asset, share, request promotion, edit, delete), point-of-use invocation help, and the version history described below.

![Prompt viewer](../images/screenshots/light/user-prompt-view-light.webp#only-light)![Prompt viewer](../images/screenshots/dark/user-prompt-view-dark.webp#only-dark)

Features:

- **Search**: Type a phrase to rank prompts by relevance to what you mean, not just literal substrings. Results span your prompts and the Library, ranked best-first: your own prompts match at any status, shared prompts once approved; prompts shared with you are matched by name and description.
- **Collections**: Group prompts by team, domain, or workflow. Any user can create collections (the **Collections** button opens the manager); renaming and deleting are limited to the collection's creator or an admin. A prompt belongs to at most one collection: owners assign their own prompts, admins assign shared prompts, from the picker on the prompt page. Deleting a collection releases its prompts to the General group.
- **Facets**: Narrow the list by collection, tag, status (My Prompts), owner (Library), and usage (recently used / never or long unused).
- **Usage columns and sorting**: Every row shows its run count and last-run age, aggregated from prompt-serve audit events. Sort by name, runs, or last run; usage sorts default to most-active-first. Dead prompts are flagged with a badge naming the exact condition — **never run**, or **unused 60d+** — and a prompt created within the last week carries no flag while it is still too new to judge.
- **Status badges**: Lifecycle state on your own prompts: draft (gray), approved (emerald), deprecated (amber), superseded (rose)
- **Tags**: Free-form, comma-separated labels for organizing prompts, set on create and edit
- **New Prompt** — Create prompts with name, display name, description, content (supports `{arg}` placeholders), category, and tags
- **Request Promotion** — On your own personal prompt, ask an admin to promote it to a persona (you choose which) or to global scope. The prompt stays personal and shows a "Promotion requested" badge until an admin approves or rejects it in the admin review queue.
- **Share** — Share your prompt directly with another user by email. The recipient gets a real, runnable prompt (with its arguments intact), not a markdown snapshot. "Save as Asset" remains a separate action for exporting the content as a markdown asset.

### Version history and diffs

The prompt page renders the full version history with per-version approval provenance: each version's author, timestamp, status (applied, draft, superseded, rejected), and, bound to that specific version, who approved it and when. A pending draft on an approved shared prompt is flagged with a banner: readers keep being served the approved version until an admin approves the draft. Any version can be diffed against the current content as a line diff.

![Library prompt with versions](../images/screenshots/light/user-prompt-view-library-light.webp#only-light)![Library prompt with versions](../images/screenshots/dark/user-prompt-view-library-dark.webp#only-dark)

![Version diff](../images/screenshots/light/user-prompt-version-diff-light.webp#only-light)![Version diff](../images/screenshots/dark/user-prompt-version-diff-dark.webp#only-dark)

Version history is visible to anyone who can view the prompt: your own prompts, and enabled Library prompts. Library readers see the served history: applied snapshots in full, and a pending draft as an author/date stub whose content stays private until an admin approves it; rejected and superseded drafts (never served) appear only to admins. A prompt shared with you person-to-person shows only its served content.

### Run from chat

The prompt page includes a copyable natural-language invocation ("Run the `<name>` prompt with ..."), built from the prompt's stable name and required arguments. Paste it into any connected chat client; the agent resolves the name against the prompt library with `manage_prompt use`.

### Sharing a prompt

Open your prompt and choose **Share**, then enter a recipient's email. The recipient sees it in the Prompts page's **My Prompts** bucket, attributed to you, and their agent can run it over MCP as `shared-<name>` (auto-deduplicated if names collide). Sharing is owner-initiated and does not require admin approval; revoke a share any time from the Share dialog. Markdown export ("Save as Asset") is a distinct action for documentation or external sharing.

### Requesting promotion

A personal prompt is yours alone. To make it available to your team or the whole organization, open it and choose **Request Promotion**, then pick a target: one or more personas, or global. An admin reviews the request and, on approval, the prompt moves to the requested scope and becomes a real shared prompt. Scope promotion is admin-only; requesting it is the self-service path.

### Personal naming and scope prefixes

Personal prompt names are unique per owner, so two users can each have a prompt named `report` without colliding. When prompts are served to an AI agent over MCP, names are prefixed by scope so they never clash across users or personas:

- Personal prompts appear as `personal-<name>` (for example, `personal-report`)
- Persona prompts appear as `<persona>-<name>` (one entry per persona you belong to, for example `analyst-report`)
- Global prompts appear as `global-<name>`
- Prompts shared with you appear as `shared-<name>`

These prefixes are computed at serve time; the stored name stays bare. To make a personal prompt visible at the persona or global scope, rename it if a prompt with that name already exists at the target scope.

You never need to type these names. Ask your agent to run a prompt by whatever handle you know: its name, its display name ("run the Daily Sales Report"), or a description of it. The agent resolves it against the prompt library with the `manage_prompt` `use` command.

## Scripts

A script is a program the platform runs for you: an agent writes one once, and from then
on it produces the same report, dashboard refresh, or export on a schedule or on request.
A script runs as soon as it is saved, under the access its author holds. The Scripts page
is where you see what you have, what is scheduled, and how it has been going, over two
tabs: **Scripts** and **Runs**.

![Scripts](../images/screenshots/light/user-scripts-light.webp#only-light)![Scripts](../images/screenshots/dark/user-scripts-dark.webp#only-dark)

Above the table are three numbers, and each of them is also the control that shows what
it counted: **Scripts**, **Scheduled** (anything with a schedule, paused or not), and
**Failing** — the scripts whose last run failed, which is the number most people open
this page for. Pressing a tile narrows the table to the scripts it counted; pressing it
again, or pressing **Scripts**, shows all of them.

Every script here is yours: a script is one person's, so this page needs no owner column
and shows nobody else's. An administrator can move a script to another owner, which is
how one arrives here that you did not write.

Each row states what is worth knowing at a glance: what the script is called, its
schedule and next fire, and how its most recent run ended. A script that will execute
nothing carries a badge beside its name — **disabled**, or its lifecycle status — because
that is the exception you scan a list for; the version a run executes is true of every
healthy script and is stated on the script's own page. Opening a row opens the script, the
way every other list in the portal opens a record. A script with no schedule runs on
demand; a paused schedule says so rather than showing a next fire that will not happen.

Each row also shows how the script is filed: the category it belongs to and the tags it
carries. Under the tiles are a search box and a chip per category, with the tags on a
second row. The search matches what a script is called and what it says about itself, and
pressing an active chip again clears it. All three are applied by the server, so they
cover every script you can see rather than only the ones already on screen.

The schedule is stated in words, always — "Every weekday at 7:00 AM,
America/Los_Angeles", "Every 30 minutes, UTC" — because this is the column you scan to
answer what is running and when, and a cron expression is not an answer to that. A
schedule with no phrase for it is named as a custom schedule; the expression itself is in
the schedule editor on the script's own page, which is where one is read and written.

Before an agent has written anything for you, the page says so rather than showing an
empty table.

![No scripts yet](../images/screenshots/light/user-scripts-empty-light.webp#only-light)![No scripts yet](../images/screenshots/dark/user-scripts-empty-dark.webp#only-dark)

### Every run, across your scripts

The **Runs** tab answers the question the run history on one script cannot: not how is
this report going, but how are your scripts going, all of them. Every run of every script
you own, newest first, with what triggered it, how it ended, how long it took, and — when
it failed — the reason, in the row rather than behind it.

![Runs across your scripts](../images/screenshots/light/user-scripts-runs-light.webp#only-light)![Runs across your scripts](../images/screenshots/dark/user-scripts-runs-dark.webp#only-dark)

Opening a row opens that run: the script's page, with the run's log, its parameters and
what it produced already open. The script's name in the row opens the script itself. A
listing that fills its cap says so, and each script's own page carries its full history.

### One script

Opening a script shows its **Details**: who owns it, which version runs, the schedule it
fires on, when it fires next, and the parameters a run binds against. These are the same
facts an agent gets when it resolves a reference to the script, so the page and your
agent describe the script identically.

The page is ordered the way a script is debugged. Details first, then the schedule, then
what the script says about itself, then the code — and the run history directly under the
code, so an error in the history is answered by the text above it.

The schedule is folded, and says what the script runs without being opened: "Runs: Every
weekday at 7:00 AM, America/Los_Angeles", or "Not scheduled". Open it to change the
cadence; pausing and resuming are on the header either way. **About** starts open, because
what a script is for is what you came to read, and folds away when the document is long
enough to be in the way.

![Script detail](../images/screenshots/light/user-script-detail-light.webp#only-light)![Script detail](../images/screenshots/dark/user-script-detail-dark.webp#only-dark)

When a run would be refused — the script disabled or retired — the page carries the
platform's own reason for the refusal rather than leaving you to work it out from the
status.

### The schedule

Below the details, on a script you own, is when it runs — folded, with what it does now in
the header. Open it and pick how often — hourly, daily,
weekdays, chosen days of the week, or a day of the month — set the time and the timezone
it is read in, bind the value every fire passes, and pause or resume the whole thing.

You do not have to know cron. The page states what it will save in words ("Every weekday
at 7:00 AM, America/Los_Angeles") and shows the expression it produces underneath, and
there is a **Custom** choice for a schedule the builder cannot express. A schedule an
agent wrote through `manage_script` that the builder cannot express opens there, as
itself, rather than being rewritten into something near it.

The time is read in the zone beside it, so a report keeps its wall clock across a
daylight-saving change, and the floor is one fire a minute. A monthly schedule past the
28th says plainly that the months without that day are skipped rather than moved.

A date parameter usually wants `${fire_date}`, which expands to the day the schedule
fires rather than to the day you typed it — that is what makes a scheduled run
reproducible, because the run records the date it was computing for.

Pausing is its own control rather than a schedule you have to clear and retype. A paused
schedule resumes on the fire it was parked on, and there is no way to delete one: the
schedule is part of the explanation of the runs it produced. Fires that came due while
the platform was not running them are counted and stated rather than caught up on, so a
gap in a script's schedule is visible instead of turning into a burst of stale reports.

![Paused schedule](../images/screenshots/light/user-script-schedule-paused-light.webp#only-light)![Paused schedule](../images/screenshots/dark/user-script-schedule-paused-dark.webp#only-dark)

A schedule on a disabled or retired script saves, and fires nothing until the script is
back in service, which the page says plainly rather than leaving you waiting on a run
that was never going to happen.

### About

What the script says about itself, written as a document rather than a caption:
markdown, rendered the way an asset's description and a knowledge page are, with the
category and tags it is filed under above it. On a script you own, **Edit** opens the
four fields together — display name, category, tags, and the description, with a live
preview beside what you type.

![Documenting a script](../images/screenshots/light/user-script-documentation-light.webp#only-light)![Documenting a script](../images/screenshots/dark/user-script-documentation-dark.webp#only-dark)

None of the four changes what the script does, so saving them applies at once: nothing is
sent for review, and the version that is running is untouched. Write what the script
produces, what each parameter means, and what it assumes about the data — this is what
somebody reading the script in six months has instead of the code, and it is part of what
search matches the script on. A description long enough to be a document in its own right
is still saved, with a suggestion that the background might belong in a knowledge page you
link to.

### The code, and running it

On a script you own, the source is editable in place, with Starlark highlighted as the
Python dialect it is. Saving makes the edit the version that runs: `run_script` executes
it, any schedule fires it, and it runs under the access you hold when you save.

![Script source](../images/screenshots/light/user-script-source-light.webp#only-light)![Script source](../images/screenshots/dark/user-script-source-dark.webp#only-dark)

Source that does not parse is refused when you save it, naming what to fix, rather than
failing at the next run with nobody watching.

**Run** and **Dry run** sit side by side above the editor, because they are the same
question asked of two texts: Run executes the saved version, a dry run executes what is
on screen. One parameter form below the editor supplies the values for both.

Run produces fresh output without waiting for the next scheduled fire, and queues exactly
what an agent's `run_script` queues: the platform executes it the same way a scheduled
fire is executed, and it appears in the run history directly below and updates as it
goes.

Where a value comes from a set the platform already knows, the form offers the set
rather than asking you to remember the spelling. A parameter naming a connection is a
list of the connections your access reaches, each with what it is; a parameter with
declared choices is those choices. A box is for a value the platform genuinely cannot
enumerate.

A script nothing would execute has no Run control at all, for the reason stated at the
top of the page, rather than a button that fails when you press it. Editing, checking and
saving stay available, because fixing the script is how it comes back into service.

### Checking a change before you send it

Beside Run are the two things you would otherwise have had to ask an agent for.

**Validate** parses what is on screen and tells you what it would reach — which
capabilities, which connections, where it writes — and, if it does not parse, what to fix
and where. Nothing runs and nothing is saved.

**Dry run** actually executes it, as you: your identity, your access, tighter limits, and
nothing kept. Outputs are measured rather than written, so you see how many rows and how
big each one would be without a dashboard being refreshed or a file leaving the platform.
You get the log the script printed, which is usually the whole reason to have run it, and
a failure is reported with the same detail a success is.

![Dry-run a change](../images/screenshots/light/user-script-dry-run-light.webp#only-light)![Dry-run a change](../images/screenshots/dark/user-script-dry-run-dark.webp#only-dark)

Because a dry run is you running it, it reaches exactly what you reach and nothing
more. The record of the run is kept with the script, so anyone reading a version later
can see that its exact code was executed, by whom, and what it produced — and a version
nobody has dry-run says so.

### Version history

Folded into the Source section is every version of the script, each with its author and
the roles they held at the save, which are the roles a run of that version presents. It
opens on a reveal rather than standing as a section of its own: the editor above it
already holds the version that runs, so what the history adds is the versions before
that one.

![Version history](../images/screenshots/light/user-script-versions-light.webp#only-light)![Version history](../images/screenshots/dark/user-script-versions-dark.webp#only-dark)

### Run history

The run history is the refresh record of one script: what triggered each run, which
version it executed, how long it took, what it produced, and how it ended. A failure
states its reason in the list. A fire that arrived while the previous run was still
going is recorded as skipped rather than silently dropped, because a report that stopped
producing is exactly what this history has to show.

How a run ended and when it ran are read as one fact and are set as one. What triggered
it and which version executed qualify that fact rather than standing beside it — the
trigger is a short enumeration and the version is the same number down the whole history
— so they sit under it in the row rather than each holding a column open.

The section header carries what the history adds up to — the share that succeeded, how
many failed or were skipped, and the median duration — over the runs actually loaded,
which the sentence names rather than implying it covers all time.

It sits directly under the code, because an error here is answered by the text above it,
and nothing in it holds the page open sideways: a failure message wraps to as many lines
as it needs rather than running off the edge.

![Run history](../images/screenshots/light/user-script-runs-light.webp#only-light)![Run history](../images/screenshots/dark/user-script-runs-dark.webp#only-dark)

Opening a run shows what it was given, what it cost, what it wrote, and the log it
printed while working. A run has an address of its own, which is what the Runs tab links
to: following one lands on this page with that run open.

![Run log](../images/screenshots/light/user-script-run-log-light.webp#only-light)![Run log](../images/screenshots/dark/user-script-run-log-dark.webp#only-dark)

An output that went to the portal links to the asset version it produced. A recurring
script writes new versions of the same asset rather than a new asset each time, so that
asset's version history is the history of what the dashboard has been showing. An output
delivered to a bucket names where it was written and is not a link: those bytes left the
platform, and nothing here will serve them back.

The schedule controls, the source, and the run history of a script belong to its owner
and to administrators. A script you can see but do not own shows its details and what it
says about itself, and nothing else.

### Asking for the pages

Ask your agent to show you your scripts — "show me my scripts", "what scripts do I
have", "did the daily report run" — and it opens this page with the `show_scripts` tool.
That tool only opens the pages; every script operation an agent performs for its own
work uses `manage_script`, which renders nothing.

## Settings

The Settings page (user section of the sidebar) holds per-user preferences.
The **Notifications** section controls [email notifications](notifications.md):
a delivery mode (Off, Immediate, or Daily digest) and per-category toggles
for shares, comments/feedback, and mentions. Defaults are immediate delivery
with all categories enabled; changes save as they are made.

**Recent notifications** sits directly below and shows what the platform has
actually sent you: the subject, category, and delivery status of each
notification addressed to your account, newest first. It pairs with the
preferences above because the two answer one question together — what should
I be told, and what was I told. It shows recent activity rather than a full
record: notifications are removed on a retention schedule, and the effective
window is stated on the panel. A notification that never went out reads
"Not delivered"; the reason belongs to the platform's mail configuration and
is shown to admins, not here.

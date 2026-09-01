---
description: "Managed resources in the portal: libraries, folders, uploads, revisions, and registering a CSV as a table."
---

# Resources

Resources are human-uploaded inputs an agent uses as-is: report templates, brand files, data dictionaries, sample payloads, and reference documents. Assets are AI-generated outputs. Knowledge pages are curated facts to search and synthesize. Memory is per-user recall. If it existed before the conversation and the agent should use it verbatim, it is a resource.

That is the whole test for which surface a file belongs on, and [Content Model](../concepts/content-model.md) covers the four layers in full, including the operator rule that makes an approved template mandatory. The page states the same split in its empty state and its upload dialog, so the choice is in front of you at the moment you make it. Agents reach a resource during a session through the MCP `resources/read` protocol method.

An uploaded resource is also **discoverable through `search`**, the front door agents are steered to. A background indexer embeds each resource's metadata and, for text-family files, a bounded prefix of its contents, so a data dictionary is found by a column name that appears only inside the file. Search results carry an `mcp:resource:<id>` reference that `fetch` reads in full (text inline, binary as metadata plus its URI), plus a resource link a client with native resource support can attach directly. Visibility is the same as everywhere else: global resources reach every caller, persona resources only their members, and personal resources only their owner. Indexing runs off the request path, but the upload enqueues its own indexing job rather than waiting for a periodic sweep, so a just-uploaded file is findable by its name and description immediately and by its contents seconds later, once the indexer has read it. A replacement upload and a metadata edit enqueue the same way. Content indexing needs the background index queue, which requires a database and a configured embedding provider; without one, resources are still searchable by their metadata. Files larger than 8 MB are indexed on metadata alone.

![Resources](../images/screenshots/light/user-resources-light.webp#only-light)![Resources](../images/screenshots/dark/user-resources-dark.webp#only-dark)

One **Library** dropdown at the head of the filter bar says which library is open: **All**, **Mine**, one entry per persona, then **Global**. All is where the page opens, because a reader's libraries are few and mostly hold other people's material, so the useful first view is all of it at once; narrowing to one is the deliberate act. A reader's persona entries are the personas they belong to and administer. A platform administrator's are every persona the deployment defines, and each of them lists that persona's material whether or not the administrator is a member — the authority that lets an administrator upload into a persona is the authority to read it back.

![The Resources library as it opens](../images/screenshots/light/user-resources-recent-light.webp#only-light)![The Resources library as it opens](../images/screenshots/dark/user-resources-recent-dark.webp#only-dark)

The library in view is the one an upload lands in, and the dialog states that destination and who will be able to see the file before you choose one. On All, which names no single library, the dialog asks instead, offering the libraries you may add to with your own first. Upload is offered wherever you may add to something: your own library always, a persona's library when you hold that persona's `persona-admin:{name}` role, and every library including the global one when you are a platform administrator. That is the same rule the server applies to the request, read from who you are rather than from which page you are on, so a control is never offered where the upload would be refused and never withheld where it would be accepted. Where Upload is not offered, the bar says who publishes that library instead, so a read-only library reads as read-only rather than as an empty page.

![The global library on the Resources page](../images/screenshots/light/user-resources-global-light.webp#only-light)![The global library on the Resources page](../images/screenshots/dark/user-resources-global-dark.webp#only-dark)

Uploading opens a modal for the file plus its folder, display name, description, and tags. The folder is a dropdown carrying the folders the library already has, and it defaults to the one you are standing in, so a file is never filed somewhere you did not expect and the folders that exist are on screen at the moment you file into one. Its last entry, **New folder...**, swaps the control for a text field and accepts any path you type; a link beside it leads back to the list. A folder is created by filing something into it, so a folder that does not exist yet has to stay typeable — what changed is that guessing is no longer the first thing asked of you. Six folder names are suggested for the first level and each is spelled out as you pick it: `data` are records to read as fact rather than as an example (rosters, mappings, rate tables), `visual` are logos, photographs, diagrams, and design elements meant to be displayed, `templates` are layouts a deliverable must be produced in, `playbooks` are procedures to follow rather than summarize, `samples` are examples to pattern-match against, and `references` are documents to consult. `data` and `samples` are the pair most easily confused: the same CSV is a sample when the agent should copy its shape and data when the agent should read its rows. They are suggestions and not a closed set — any path you type is accepted. The file itself may be any format the library needs — documents, spreadsheets, images, media, archives, CAD exports — apart from executables, which are refused by both extension and MIME type ([Accepted types](../server/content-viewers.md#accepted-types)).

![Upload resource](../images/screenshots/light/user-resource-upload-light.webp#only-light)![Upload resource](../images/screenshots/dark/user-resource-upload-dark.webp#only-dark)

A library this page does not upload to says who fills it instead, in the control's place, so a reader who opens the Global library is told the material there is published by platform administrators rather than being left at a page with a missing button.

## Folders

A resource is filed under a **folder path** inside its library: a slash-separated chain like `data/media-manager/shows`. Each folder name is lowercase letters, digits and hyphens starting with a letter, a path is at most 8 folders deep and 200 characters, and a refusal names the rule you broke rather than restating the whole grammar.

A folder exists because a resource is filed under it, and stops existing when the last one leaves. There is nothing to create and nothing to clean up, and the consequence is stated rather than worked around: an empty folder cannot be made and left waiting for a file, so the destination is picked, or typed, while filing the file.

![The resource library as a folder tree](../images/screenshots/light/admin-resource-tree-light.webp#only-light)![The resource library as a folder tree](../images/screenshots/dark/admin-resource-tree-dark.webp#only-dark)

Opening a library shows its folders, with each folder's count of everything beneath it at every depth. The counts are exact and come from the server in one request: they used to be derived in the browser from a page of the listing, so a folder read `12+` until the last page arrived and the root offered a Load-more control whose only visible effect was to firm that number up. A library's root lists no files — every resource is filed under a folder — so there is nothing to page there. Opening a folder navigates into it and lists what it holds. Each level is an address of its own — `/resources/lib/global/data/media-manager` — so a folder survives a reload, can be linked to, and Back steps out one level rather than out of the library. Breadcrumbs name the whole path from the library down and every crumb navigates to that level. A folder longer than one page loads as you scroll.

![Two folders in](../images/screenshots/light/admin-resource-folder-light.webp#only-light)![Two folders in](../images/screenshots/dark/admin-resource-folder-dark.webp#only-dark)

Search spans the **whole library**, not the folder you are standing in, and each hit shows the path it was found at with a control that reveals it in the tree. A search that only looked in the open folder would make the tree worse than the flat list it replaced.

![A library-wide search, each hit naming the folder it was found in](../images/screenshots/light/admin-resource-search-light.webp#only-light)![A library-wide search, each hit naming the folder it was found in](../images/screenshots/dark/admin-resource-search-dark.webp#only-dark)

A **grid/table switch** beside the filters decides how the files are drawn, and the choice is remembered for the next visit. It applies to the folders and the recently-updated section as well as the files, so the whole page answers it. The grid is the default and gives every file a card the way the Assets gallery does: its thumbnail, with the name, description, type, tags, size and date beneath it — plus the library badge and the never-read flag in the administrator's section, which is what those columns say in a table. It used to be the folder that decided — tiles only where every file in it was an image — so a folder holding one PDF among fifty photographs was fifty rows of filename and size.

A resource's **thumbnail is a captured image**, stored beside the file and served from its own route ([Thumbnails](#thumbnails)).

### Thumbnails

A resource's tile is a **captured PNG**, taken by a portal tab and stored beside the resource's own object. Nothing on a server can rasterize a document, so the image is made in a browser: any open portal tab asks the platform what still needs one and works through the list while the browser is idle and the tab is in front. That is the same arrangement portal assets have.

Markdown, CSV, JSON, plain text, HTML, JSX and SVG are rendered and captured. A raster image is **downscaled** onto the tile rather than displayed at full size, for the formats a browser can decode: PNG, JPEG, GIF, WebP, AVIF, BMP and ICO. A TIFF, HEIC or PSD is not offered a capture at all, because the browser that would have to decode it cannot, and a file that can never be captured would sit at the head of the queue in front of ones that can. Files rendered on a plain background — markdown, CSV, JSON, plain text — are captured twice so the library looks right in both colour schemes, and the tile shows the one matching the colour scheme you are in; the rest carry their own colours and store a single image, which is used in both. Anything larger than 1 MB, and anything nothing can rasterize (PDFs, spreadsheets, archives), keeps its content-type icon and is never queued.

That list is the same one portal assets are captured under. It is written once per language — one Go definition and one browser definition, held to each other by a test — because it used to be written out four times and the four had stopped agreeing.

Before this the tile *was* the file: an image was fetched at full size and scaled down by the browser, anything past a size cutoff drew nothing at all, and a library of documents was a wall of identical icons. Reading a stored capture costs a few kilobytes instead of a few megabytes.

A capture records the moment it was taken. When a resource's content is replaced the capture is older than the file it came from, which is what puts the resource back on the queue; the tile keeps showing the image it has until a new one lands, because one revision behind is worth more than no image.

A tile that is wrong can be cleared, which asks for another. The **Thumbnail** panel in a resource's sidebar shows the stored image and offers **Recapture**: it discards both captures and puts the file back on the queue, and the next tab that goes idle over it takes the picture again. It is there for whoever may change the file — its uploader, and anyone who may add to the library it is in — and for a type something can rasterize; there is no tile for anyone else to be wrong about. Use it when a tile shows something the file no longer looks like without the file itself having changed.

![The Thumbnail panel on a resource, with its Recapture control](../images/screenshots/light/admin-resource-thumbnail-light.webp#only-light)![The Thumbnail panel on a resource, with its Recapture control](../images/screenshots/dark/admin-resource-thumbnail-dark.webp#only-dark)

Drawing an image tile is a real read of the bytes, and it is audited as one — under its own `portal_preview` surface, the only one that does not stamp the resource's last-read time. Browsing a library of photographs therefore cannot clear the never-read flag on every image in it or reorder the *Recently read* sort ([Resource reads](../server/audit.md#resource-reads)).

**Recently updated** heads a library at its root: the ten files that changed last, each naming the folder it is in. A library opens on a list of folder names, and the folder somebody wants is usually the one somebody else has just changed, which the folder names do not say. It follows the Library dropdown, and it is left off inside a folder and while a search or a tag filter is running — those views are already an answer to what is relevant here, and a second, differently ordered answer above them competes with the one that was asked for.

A tag filter sits beside the search box, offering the tags the resources in view carry. Tags are orthogonal to the tree: the same tag reaches files in any folder. A filter that matched nothing says so rather than reading as an empty library.

## Acting on several files, and on a folder

Rows are multi-selectable. A selection can be moved to another folder, tagged, or deleted as one action, and the result is reported per file: each file is its own request, so the ones that could move do and the ones that were refused stay where they were with the reason beside them. Tagging adds to whatever each file already carries rather than replacing it. Re-filing forty resources used to mean opening forty Edit dialogs.

![Moving several files at once](../images/screenshots/light/admin-resource-multi-select-move-light.webp#only-light)![Moving several files at once](../images/screenshots/dark/admin-resource-multi-select-move-dark.webp#only-dark)

A file can be dragged onto a folder to move it, and a folder onto another folder to nest it. Both open the same confirmation the menu action does, because dragging is easy to do by accident and either one rewrites an address.

Renaming a folder, or nesting it under another, rewrites the path of every resource beneath it at every depth, in one transaction: a half-renamed folder is not a state anyone can observe. Each of those resources records the address it left, so a citation written against the old address keeps resolving. The whole move is refused if any destination address is already taken, if you cannot change one of the files beneath the folder, or if it would put a folder inside itself — and a refusal moves nothing. One rename covers at most 500 resources; a larger subtree is refused with its true count rather than moved in part, and is moved a subfolder at a time.

The Resources page provides:

- **Library** dropdown — All, Mine, each persona, and Global; All is the default and shows every library you can read at once
- **Breadcrumbs** — The path from the library down, every crumb navigating to that level
- **Search and filter** — Text search over the whole library, and a tag dropdown
- **Layout switch** — Tiles or rows, applying to folders, recents and files alike, remembered for the next visit
- **Upload** button — Wherever you may add to something: uploads a new resource with name, description, folder, and tags, into the library in view and the folder you are standing in, asking which library when the view names none
- **Recently updated** — At a library's root: the ten files that changed last, each naming its folder
- **Folders and files** — The folders at this level with their exact counts, and inside a folder the files it holds, as tiles or as a table of name, MIME type, tags, file size, uploader email, and last updated date
- **Open** — Clicking a folder row opens the folder; clicking a file row or a tile opens that resource at its own address
- **Select** — Checkboxes on the rows and the tiles, and one Move, Tag or Delete over everything picked
- **Rename or move** — On a folder row, on a view naming one library: rewrites the path of everything beneath it
- **Library** — On the Edit dialog: moves the resource to another library you may put it in ([Moving a resource to another library](#moving-a-resource-to-another-library))

Administrators can open, edit, and delete any resource by id, including persona material they do not belong to — otherwise an admin could upload a persona resource and then be unable to manage or remove it. Listing follows the same rule: a library an administrator may write is a library they may list, and their unnarrowed listing spans every library in the deployment. Anything less made the material they had just uploaded unfindable. What `search` ranks stays membership-scoped whoever asks: an administrator's discovery is not widened to every persona's library, because a search hands a caller material they did not ask for by name. Dereferencing a file the caller did name is a different question, and `fetch` answers an `mcp:resource:` reference on the same rule the by-id routes use, so an administrator is not told a file does not exist while the same session can download and replace it. `resources/read` remains membership-scoped. Every other caller's listing is unchanged: their own library, the personas they belong to, and the global one.

Opening a resource shows which prompts attach it as reference material. Deleting a resource that prompts depend on does not break them: they keep serving and report the material as missing, and the prompt viewer flags the broken link so its author can repair it.

**Used by** lists the assets whose content [references](../server/asset-references.md) the resource, and flags any of them carrying a public share link — a reference gives the file that asset's audience, so an asset anyone can open makes the file readable by anyone holding the link. Referencing assets you cannot open are counted but not named. Deleting a resource assets reference warns and names them first; the assets keep rendering, with that one file missing.

![Assets referencing a resource](../images/screenshots/light/admin-resource-used-by-assets-light.webp#only-light)![Assets referencing a resource](../images/screenshots/dark/admin-resource-used-by-assets-dark.webp#only-dark)

**Written by** lists what has written the file: every managed script, agent session and person that created or modified it, most recent writer first, each marked as having **created** the file or having only **modified** it. A script and a session open on their own page.

Until this existed a resource recorded only its uploader, and for a managed-script run that was the script's *name* — so renaming the script severed the link, and a script that replaced the content of a file somebody else uploaded left no trace at all. Both are now recorded: a script is identified by its id, so a rename changes only what the row displays, and a file with two writers lists two.

![What produced a resource](../images/screenshots/light/admin-resource-producers-light.webp#only-light)![What produced a resource](../images/screenshots/dark/admin-resource-producers-dark.webp#only-dark)

A file uploaded before this shipped shows no panel rather than a guessed writer. The one case the platform can derive without guessing is a resource a managed script uploaded, where exactly one script bears the name it was filed under.

Clicking a row opens the resource at `/resources/{id}` — `/admin/resources/{id}` in the administrator's section — so a resource can be linked to, bookmarked, reloaded and opened in a second tab, and Back returns to the library on the scope, the folder and the filters it was left on. The page takes the same shape a portal asset takes, because a resource is the same kind of object: the content at the full width of the page, and what the resource is beside it — its library, its folder path as a clickable trail back into the library, metadata, canonical URI, tags, read activity, revision trail, table registration, and the prompts that attach it. Download, Edit and Delete are in the page header, so a long document or a deep revision trail never pushes them off the screen. Editing and deleting are still dialogs; they are bounded forms.

![Resource detail](../images/screenshots/light/admin-resource-detail-light.webp#only-light)![Resource detail](../images/screenshots/dark/admin-resource-detail-dark.webp#only-dark)

## Moving a resource to another library

A resource's library used to be chosen once, on the upload form, and never again. The only route from a personal library to a shared one was to upload the file a second time, which mints a second id, a second URI and a second blob, leaves the original in place, and gives the two copies separate version trails, while every asset and prompt that already referenced the first one keeps referencing it.

**Library** on the Edit dialog moves the file instead. It offers the libraries you may put it in and nothing else, and it is absent when there are none:

- Your own library, always.
- A persona you belong to. This is looser than uploading, which needs that persona's `persona-admin:{name}` role: putting new material in front of a persona's members is the persona administrator's call, while moving in a file you already own and will read yourself is not.
- Every persona, the global library, and a named person's library, for a platform administrator. These are offered wherever the dialog is opened from, on your own Resources page and in [Admin > Resources](admin-content.md#resources-admin) alike: the authority is the administrator's, not the page's, and the server grants every one of these targets whichever route the request arrives on.

![Moving a resource to another library](../images/screenshots/light/admin-resource-move-light.webp#only-light)![Moving a resource to another library](../images/screenshots/dark/admin-resource-move-dark.webp#only-dark)

The move rewrites the resource's row, not its content: the id, the stored file, the version trail, the table registration and the read history are all unchanged, and the blob is not copied. What does change is who can see the file and what it is called. The canonical `mcp://` URI names the library the file lives in, so it is rewritten to match — a file published to everyone whose URI still read `mcp://user/<sub>/...` would be a URI that lies.

Nothing that already points at the file breaks, and nothing that writes it stops. An asset that [references](../server/asset-references.md) it and a prompt that attaches it both record the resource's id, and the reference keeps the URI exactly as its author wrote it, so both keep rendering. A scheduled script that refreshes the file goes on refreshing it: the move rewrites where the file is filed and never who uploaded it, so whoever may replace its content before the move may after it, and so may their scripts ([Managed scripts](../scripts/security.md)). Text that hard-codes the old URI — a knowledge page, a script, a prompt's prose — resolves it by address rather than by id, and that keeps working too: every address a resource has answered to stays resolvable, and a live address always wins over a vacated one, so a file uploaded into the address you left is reached by its own URI and not by the alias.

A move into a library that already holds a file at that folder and name is refused, naming the file it collides with, and changes nothing. The move is recorded in the audit trail with who moved what, out of which library and folder and into which ([Resource moves](../server/audit.md#resource-moves)).

Changing the **folder** works the same way and is the other half of the same address. Editing it rewrites the URI's path exactly as a library move rewrites its prefix, records the address vacated, and refuses a collision by name; a library and a folder changed in the same save produce one URI carrying both, one alias for the one address left, and one audit event. Before this, editing the folder changed where the portal filed the file and left the URI alone, so a resource's own page printed two different paths for it — the breadcrumb from one column and the Details panel from the other.

Agents do not move resources. `manage_resource` creates and replaces content; deciding that a file becomes a persona's or the whole platform's is a human act, and nothing an agent does needs it.

## Revising a resource's content

Scrolling the sidebar reaches the rest of the lifecycle surfaces: the read-activity rollup, the version history described below, the prompts attaching the resource, and the assets referencing it.

![Resource lifecycle surfaces](../images/screenshots/light/admin-resource-lifecycle-light.webp#only-light)![Resource lifecycle surfaces](../images/screenshots/dark/admin-resource-lifecycle-dark.webp#only-dark)

**Replace content** on the resource page uploads a new file for an existing resource. The resource keeps its id, its canonical `mcp://` URI, and its file name, so every `mcp:resource:<id>` citation and prompt attachment pointing at it keeps resolving — which delete-plus-re-upload does not, since that mints a new id and breaks them all. The uploaded file's own name is ignored for that reason; only the bytes, type, and size change. Agents connected at the time are told the resource list changed, so a client re-reads the new content rather than serving the old.

Every revision is recorded in **Version history** with its number, who uploaded it, when, and how large it was. Any version can be downloaded, and any prior version can be **restored** — which re-promotes that version's exact bytes as a new head revision rather than rewinding, so the trail stays append-only and the restored content is itself restorable. A restored revision is labeled with the version it came from. A revision the platform wrote on your behalf — a [table registration](../server/registered-tables.md) that had to correct the file before it could read it — carries a line beneath it saying what changed, so a revision nobody uploaded is not mistaken for one somebody did.

History is bounded: a resource keeps its most recent 10 revisions by default ([`resources.managed.max_versions`](../server/configuration.md#managed-resources)), and a revision past the cap deletes the oldest version's stored file. The live content is never pruned.

An agent revises through the same path, without the portal step. `manage_resource action=replace_content` writes new bytes over an existing resource under your own permissions, and the result lands in this Version history like any other revision — same number, same author, same restore — with its `change_summary` shown beneath it. `manage_resource action=create` files a new resource the same way. A [managed script](../scripts/running.md) reaches both, which is what lets a scheduled run refresh the file a dashboard reads without anybody uploading it again. See [manage_resource](../server/tools.md#manage_resource).

## Querying a CSV resource as a table

A CSV resource carries the same **Query as a table** panel the asset viewer does. Registering asks for two things: the connection the table is created on, and what to call it. The name is optional and defaults to a slug of the file name; either way your persona is added as a prefix, because the schema it lands in is shared with everyone else who has that connection.

![Registering a resource as a table](../images/screenshots/light/admin-resource-table-register-light.webp#only-light)![Registering a resource as a table](../images/screenshots/dark/admin-resource-table-register-dark.webp#only-dark)

Registering a resource is the uploader's call, the same way registering an asset is the owner's — or an administrator's, including the administrator of the persona a persona-scoped resource belongs to. An agent you are working with can do it for you without the portal step, with `manage_table`, under the same rule.

Uploading a new revision moves the file, and the table keeps serving the revision it was registered against. The panel says so, and registering again moves the table to the current revision.

![A registration the file has moved on from](../images/screenshots/light/admin-resource-table-light.webp#only-light)![A registration the file has moved on from](../images/screenshots/dark/admin-resource-table-dark.webp#only-dark)

A spreadsheet export often has a line break inside a cell — a multi-line address in one column. A query engine splits records on newlines before it looks at the quotes, so each of those rows would come back torn into fragments with every later field in the wrong column. Registering such a file is refused, and the refusal says how many rows are affected and which columns they are in. A file whose lines end in a carriage return rather than a newline is refused for the same reason: the engine does not split on one, so the records that end in one are run together into a single row.

![A CSV that has to be corrected first](../images/screenshots/light/admin-resource-table-repair-offer-light.webp#only-light)![A CSV that has to be corrected first](../images/screenshots/dark/admin-resource-table-repair-offer-dark.webp#only-dark)

**Save a corrected copy and register that** does the correction for you: every record gets its own line, every cell goes back onto one line, and the text is converted to UTF-8 if it was not already. The result is a new version of the file itself, so the bytes you uploaded stay as the version before it and the correction can be undone from Version history like any other. The panel then says what changed, and so does the new version's row in Version history.

![What the correction changed](../images/screenshots/light/admin-resource-table-repaired-light.webp#only-light)![What the correction changed](../images/screenshots/dark/admin-resource-table-repaired-dark.webp#only-dark)

The same description is recorded on the version the correction wrote, so Version history still says why the file changed after this answer is gone. The version below it has none: those are the bytes you uploaded.

A table registered this way goes on correcting the file. If the next revision you upload has the same problem — the same weekly export from the same spreadsheet — the correction is made again, as a new version above the one you uploaded, and the table moves onto it. The revision you uploaded is still there and still restorable, and the reply to the upload says both what happened to the table and what the correction changed. The panel labels such a table *Corrects the file*. Registering the same name again without asking for the correction turns it off.

![The corrected version in Version history](../images/screenshots/light/admin-resource-corrected-version-light.webp#only-light)![The corrected version in Version history](../images/screenshots/dark/admin-resource-corrected-version-dark.webp#only-dark)

See [Registered Tables](../server/registered-tables.md).

## Seeing what is actually used

The detail view shows **Usage**: reads over the last 30 and 90 days, broken down by which door served the content — an agent's `resources/read`, a `search` fetch, or a portal download — plus when it was last read. The admin resources table adds a **Last read** column and a *Recently read* sort, so a curator can order the library by recency and find material nothing has touched; a resource never read since it was uploaded over 30 days ago is flagged.

These counts come from the read audit trail, so they are bounded by the deployment's audit retention window, and a deployment with `audit.enabled: false` records no reads and shows no usage (reads themselves are unaffected). Listing resources is not a read: only content actually served counts.

## Attaching resources to a prompt

A prompt is a procedure, and a procedure usually depends on material: the template it fills, the checklist it follows, the brand header it embeds. The prompt viewer has an **Attached materials** panel where the prompt's owner (or an admin, for shared prompts) attaches resources from a searchable picker, orders them, and detaches them. The order is authored, not incidental, because it is the order the agent receives them in.

Every agent that runs the prompt receives the attached material as authoritative: text files inline, larger or binary files as links it can read on demand.

An attachment must be at least as widely visible as the prompt. A private resource belongs only on your own personal prompts, and a persona resource only on prompts for that same persona. Attaching something narrower is refused with a message naming the resource, and so is requesting promotion of a prompt that still carries it, because a shared prompt whose materials most readers cannot open is worse than one with no materials at all.


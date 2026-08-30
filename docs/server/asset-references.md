---
description: How an asset references a managed resource or another asset instead of embedding it - declaring a reference on save, managing one from the portal, the URL every viewing surface rewrites, what the grant gives away, and what happens when the target is deleted or the asset is copied.
---

# Asset References

An asset's content can name a managed resource by its `mcp://` URI, or another asset by its `mcp:asset:<id>` reference, instead of carrying the bytes. The stored content keeps the reference; every surface that serves the asset to a reader rewrites it into a URL that resolves to the target.

!!! quote "The rule"
    Write the reference where the file belongs in your markup and declare it in the same call. The asset stores a reference, not a copy.

## Why an asset should not carry the bytes

Before references, the only way to put a logo, a photograph, or a design element in a report was to write it into the markup: an SVG inlined in full, an image as a `data:` URI. The cost is paid four times. The agent spends output tokens emitting bytes it read from somewhere else, the asset stores them, every retained version stores them again, and every read carries them. A report refreshed hourly by a [scheduled script](../scripts/running.md) re-emits the same logo on every run and keeps a copy in every version it retains.

The material is already in the platform. A [managed resource](../concepts/content-model.md) is a stored file with a content type, a scope, a permission model, and a stable URI. Another asset is a stored file too, with content that changes when its author or a scheduled job rewrites it. A reference is how an asset names either.

## What each kind is for

A **resource reference** names material a person uploaded: a logo, a photograph, a design element, a data dictionary. It changes when somebody replaces the file.

An **asset reference** names something the platform produces. It resolves to the referenced asset's **current** content on every read, which is what makes a refresh loop expressible: a [scheduled script](../scripts/running.md) rewrites a CSV or JSON asset hourly, and a dashboard asset that names it renders the new numbers without being re-saved and without an agent spending output tokens carrying the data across.

Both kinds are one mechanism. They share a declaration, a token, a serving route, and a rewrite; what differs is which store the target is read from and which permission admitted the declaration.

## Declaring a reference

`save_asset` and `manage_asset` (the `update` and `patch` actions) take a `references` list. Each entry is a managed resource's `mcp://` URI or an asset's `mcp:asset:<id>` reference — the same reference string a `search` hit carries and `fetch` dereferences.

```json
{
  "name": "Q4 Revenue Report",
  "content_type": "text/html",
  "content": "<h1>Q4 Revenue</h1><img src=\"mcp://global/brand/logo.png\" alt=\"ACME\"><script>fetch(\"mcp:asset:ast_7c1e\").then((r) => r.text())</script>",
  "references": ["mcp://global/brand/logo.png", "mcp:asset:ast_7c1e"]
}
```

The reference appears twice on purpose. In the markup it is what the reader's browser will follow; in `references` it is the declaration, and only a declared reference is ever rewritten. A reference string that appears in the content but was never declared is served exactly as written and resolves to nothing, so the grant is always the declaration and never a string that happens to appear in the body.

The rewrite is a whole-document replacement over textual content rather than an attribute rewrite, so a reference resolves wherever it is written: an `img` `src`, a `fetch()` inside a `<script>` block, a markdown link.

On `manage_asset`, `references` replaces whatever the asset referenced before:

| `references` | Effect |
|--------------|--------|
| absent | the asset's references are left alone |
| `["mcp://...", "mcp:asset:..."]` | the asset now references exactly these |
| `[]` | every reference is removed |

At most **20** references per asset, of both kinds together. A save above the cap is refused and the refusal states the number.

## What declaring one gives away

A declaration is checked once, against the author, at the moment of the save: they must be able to read the target, and a save naming one they cannot is refused with the reference named and nothing created. An agent reads a resource through its scope claims and an asset through ownership and shares, which are the same two checks those surfaces apply everywhere else.

From then on the reference carries the **referencing asset's** audience. Anyone who can open that asset can load the target through it, including an anonymous viewer of a [public share link](../portal/index.md). This is the grant model a [managed script](../scripts/security.md) already uses, where a run acts as its version author rather than as its caller. The tool response states it when the reference is made:

> Anyone this asset is shared with can load these files through it, including anyone holding a public link, now and later.

The rule is the same for a referenced asset as for a resource, and is deliberately **not** the referenced asset's own shares. The reference is served to a reader with no session — inside a sandboxed frame, or on a public link — so there is no reader identity to resolve those shares against. What protects the referenced asset is that the author had to be able to read it, and that the notice states the consequence before the reference is made.

A resource or an asset that must not reach a wider audience does not belong in an asset that has one.

## The URL a reader follows

Each viewing surface rewrites the declared URIs in the content it serves into an absolute URL under `/portal/refs/{asset_id}/{reference_token}`. The rewrite runs on the portal's asset and version content reads, the public share and collection-item reads, and the admin console's content reads. Every one of them produces the same URL.

The route takes no session. That is not a convenience: an HTML or JSX asset renders inside an iframe with an opaque origin, and a public share is read by someone with no account at all, so a URL that depended on the reader's own credentials would resolve for neither. The token in the path is the whole authorization. It is 256 random bits, it is minted per (asset, target) pair, it reaches a reader only inside content they were already allowed to open, and it resolves to nothing on any other asset's path — the same capability model a public share link already uses.

A referenced asset is served through its **own** reference list, so a referenced page renders with its pictures rather than with dead reference strings. That is one level and never a walk: the rewrite writes URLs, and following one is the reader's next request rather than the platform's recursion. A cycle — two assets referencing each other, or an asset referencing itself — is therefore answered with content each time instead of being followed. The portal refuses a self-reference at the moment it is made, because it resolves to the very content it was written in.

A reference that survives a save keeps its token, so a URL already rendered into a reader's open page does not break every time the author saves.

## The tile a referencing artifact gets

An asset's thumbnail is captured in the reader's browser by rendering the asset a second time in an off-screen frame. That frame runs the artifact under the same policy the viewer's frame does, from the same definition, so a reference resolves during a capture exactly as it resolves for a reader. A referencing artifact captured with its references blocked would render the branch it draws when a file is missing, and that picture — a valid image of an error — is what would be stored and shown on every card.

The frame reports what it could not load, so a capture in which a referenced file was refused or answered an error is discarded rather than uploaded, and the asset stays on the queue for another try. An owner can also ask for the picture to be taken again: the **Thumbnail** panel in the metadata sidebar shows the stored image and offers **Recapture**, which discards it and re-queues the asset without waiting for its version to move.

## Managing references from the portal

An asset's viewer sidebar carries a **References** panel listing what the asset depends on. A resource row names the file, its scope and its content type, with a thumbnail where it is an image; an asset row is marked as one and names the asset, its content type and its owner. A thumbnail loads through the reference's own URL rather than through the target's own route, so it renders for a reader who was only ever shown the asset.

![The References panel on an asset](../images/screenshots/light/user-asset-refs-light.webp#only-light)![The References panel on an asset](../images/screenshots/dark/user-asset-refs-dark.webp#only-dark)

Every row carries the reference string with a copy control. That is the point of the panel rather than a detail of it: adding a reference does not change the asset's content, and the markup has to name the reference for the target to load.

An owner, an editor on a shared asset, and an administrator can add a reference through a picker with a tab for each kind — the resources they can read, and the assets they can open — and remove one. The picker states what the reference gives away and names the asset's current audience before anything is added — a public link, people it is shared with, or neither yet. A reader with no edit authority sees the list and is offered neither control.

![The reference picker, on its resources tab](../images/screenshots/light/user-asset-ref-picker-light.webp#only-light)![The reference picker, on its resources tab](../images/screenshots/dark/user-asset-ref-picker-dark.webp#only-dark)

![The reference picker, on its assets tab](../images/screenshots/light/user-asset-ref-picker-assets-light.webp#only-light)![The reference picker, on its assets tab](../images/screenshots/dark/user-asset-ref-picker-assets-dark.webp#only-dark)

A row whose target has been deleted is flagged rather than dropped. It names the target id, which is what the owner needs in order to clean it up, and it is the only place they learn the report is now serving without it.

### Removing one the content still names

The panel reads the asset's stored content and reports which lines write each reference's URI. Removing a reference the content still names warns first, naming those lines, and proceeds on confirmation: the declaration and the markup are two things the author keeps in step, and being unable to withdraw a grant until a document had been edited would be worse than leaving one URI resolving to nothing.

The check does not always run. A binary asset, one larger than 2 MB, or a storage fault leaves it with nothing to report, and the response says which it was. That distinction is the point rather than a detail: "the content does not name this file" makes a removal safe, "we could not look" does not, and the two produce an identical empty list. A removal is confirmed whenever the check did not run, and the confirmation says the content could not be checked rather than claiming the URI is absent from it.

Each reference made through the panel is logged as `asset_reference.granted` and each removal as `asset_reference.revoked`, carrying the target's kind as well as its id, and matching the record a save writes when an agent declares one. An operator's log covers both doors or it covers half the grants and looks complete.

### The reverse view

A resource's own detail page carries a **Used by** section beside the one listing the prompts that attach it, naming the assets whose content references it. An asset's viewer sidebar carries the same section, answering the same question about the asset itself: which reports read this one's content. An asset carrying a public link is flagged in both, because that is the reference that widens the target's audience furthest. Referencing assets the reader cannot open are counted but not named — someone deciding whether to delete something has to know the list is not the whole of what would break.

![A resource's Used by section](../images/screenshots/light/admin-resource-used-by-assets-light.webp#only-light)![A resource's Used by section](../images/screenshots/dark/admin-resource-used-by-assets-dark.webp#only-dark)

![An asset's Used by section](../images/screenshots/light/user-asset-used-by-light.webp#only-light)![An asset's Used by section](../images/screenshots/dark/user-asset-used-by-dark.webp#only-dark)

The asset's own section is refused to a reader who cannot open the asset: who reads an asset is part of the asset, not public knowledge about it.

The answer is bounded at 50, and says so when the bound cut it. Narrowing the list to the assets a given reader may open costs a share lookup per asset, so an unbounded read of a file every asset in a deployment references would turn one page view into a query per asset. A cut answer reads as "at least *n*" rather than as a total, on the same principle the hidden count follows.

Deleting a resource that assets reference warns and names them first, on the same terms. The delete button stays disabled until that check answers, and says so if the check itself fails — a dialog that armed its destructive control while the answer was in flight would let a fast click skip the warning it exists for.

## What the agent reads

`manage_asset get_content` is **not** rewritten. It returns the stored content with its declared references intact, because that is what the agent reads before it patches: an agent handed a rewritten URL would write a platform-internal path back into the asset on its next patch, and the reference would be gone. A `patch` round trip through `get_content` leaves references intact.

## Refreshing what the asset reads

The referenced target is not fixed at the moment the asset was written, in either kind, and the reference URL a rendered page fetches is unchanged by a refresh. The route's response carries `Cache-Control: private` with no `max-age`, so a page refetching on a timer revalidates rather than being served a pinned copy.

**A resource** is refreshed by `manage_resource action=replace_content`, which writes new bytes over an existing managed resource, keeping its id, its canonical `mcp://` URI and its file name. Every replacement lands in the resource's version history with its author and the reason it changed, and the version before it stays restorable. See [manage_resource](tools.md#manage_resource).

**An asset** is refreshed by writing it: `manage_asset action=update` or a `patch`, from an agent or from a [scheduled script](../scripts/running.md) through `platform.call`. The referencing asset is untouched and the next read of the reference URL serves the new content.

That is what makes a referencing asset's data half refreshable by the platform rather than only by a person at an upload form. A script rewrites the CSV a dashboard reads, on a schedule, under its version author's permissions, and the dashboard is never re-saved.

## When things change

**The target is deleted.** The reference row survives, the URL answers 404, and the asset still renders with that one thing missing. This is the rule [prompt attachments](../concepts/content-model.md) already follow: losing the evidence that a report is now incomplete is worse than a broken image. A soft-deleted asset is treated as absent, so a reference cannot resurrect deleted content into a page that outlives it.

**The reference store is unreachable.** Content is served exactly as stored, with its references unresolved. The images go off the page; the page does not.

**The asset is copied.** A copy carries only the references its new owner can read for themselves, each under a fresh token — the two assets are separate grants from that point on. A resource is re-checked against the copier's own claims and an asset against their own view of it. A reference the copier cannot read is dropped rather than refusing the copy, so they get the report with that one thing missing. A copy never carries a grant its new owner did not earn.

**A version is read.** Version history is rewritten against the asset's **current** references, because the references belong to the asset rather than to any one version. An old version naming a resource the asset no longer references renders that image missing.

## Deployment

Nothing configures this. References are available wherever there is a database; a resource reference additionally needs a managed-resource layer, and a deployment without one refuses an `mcp://` URI with that reason rather than accepting it and recording nothing, while still recording asset references. The route carries its own rate limit sized so one page load can fetch everything the asset declared.

`/portal/refs/` is mounted at the composition root as a prefix of its own, beside `/portal/view/`. The portal UI claims `/portal/`, which matches the reference path as well, so without a mount of its own every reference URL is answered by the SPA's `index.html` and an image element receives a document. A deployment with no managed-resource layer answers the prefix as not-found rather than letting it reach the authenticated routes, since the path takes no session by design.

The portal panel and the resource-side view are registered on the same condition and answer nothing where the reference store is absent.

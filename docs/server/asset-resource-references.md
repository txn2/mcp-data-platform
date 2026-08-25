---
description: How an asset references a managed resource instead of embedding it - declaring a reference on save, managing one from the portal, the URL every viewing surface rewrites, what the grant gives away, and what happens when the file is deleted or the asset is copied.
---

# Asset Resource References

An asset's content can name a managed resource by its `mcp://` URI instead of carrying the file's bytes. The stored content keeps the URI; every surface that serves the asset to a reader rewrites it into a URL that resolves to the file.

!!! quote "The rule"
    Write the resource's URI as the `src` in your markup and declare it in the same call. The asset stores a reference, not a copy.

## Why an asset should not carry the bytes

Before references, the only way to put a logo, a photograph, or a design element in a report was to write it into the markup: an SVG inlined in full, an image as a `data:` URI. The cost is paid four times. The agent spends output tokens emitting bytes it read from somewhere else, the asset stores them, every retained version stores them again, and every read carries them. A report refreshed hourly by a [scheduled script](../scripts/running.md) re-emits the same logo on every run and keeps a copy in every version it retains.

The material is already in the platform. A [managed resource](../concepts/content-model.md) is a stored file with a content type, a scope, a permission model, and a stable URI. A reference is how an asset names one.

## Declaring a reference

`save_asset` and `manage_asset` (the `update` and `patch` actions) take a `resources` list of `mcp://` URIs.

```json
{
  "name": "Q4 Revenue Report",
  "content_type": "text/html",
  "content": "<h1>Q4 Revenue</h1><img src=\"mcp://global/brand/logo.png\" alt=\"ACME\">",
  "resources": ["mcp://global/brand/logo.png"]
}
```

The URI appears twice on purpose. In the markup it is what the reader's browser will follow; in `resources` it is the declaration, and only a declared URI is ever rewritten. An `mcp://` URI that appears in the content but was never declared is served exactly as written and resolves to nothing, so the grant is always the declaration and never a string that happens to appear in the body.

On `manage_asset`, `resources` replaces whatever the asset referenced before:

| `resources` | Effect |
|-------------|--------|
| absent | the asset's references are left alone |
| `["mcp://..."]` | the asset now references exactly these |
| `[]` | every reference is removed |

At most **20** resources per asset. A save above the cap is refused and the refusal states the number.

## What declaring one gives away

A declaration is checked once, against the author, at the moment of the save: they must be able to read the resource, and a save naming one they cannot is refused with the URI named and nothing created.

From then on the reference carries the **asset's** audience. Anyone who can open the asset can load the referenced file through it, including an anonymous viewer of a [public share link](portal-user.md). This is the grant model a [managed script](../scripts/security.md) already uses, where a run acts as its version author rather than as its caller. The tool response states it when the reference is made:

> Anyone this asset is shared with can load these files through it, including anyone holding a public link, now and later.

A resource that must not reach a wider audience does not belong in an asset that has one.

## The URL a reader follows

Each viewing surface rewrites the declared URIs in the content it serves into an absolute URL under `/portal/refs/{asset_id}/{reference_token}`. The rewrite runs on the portal's asset and version content reads, the public share and collection-item reads, and the admin console's content reads. Every one of them produces the same URL.

The route takes no session. That is not a convenience: an HTML or JSX asset renders inside an iframe with an opaque origin, and a public share is read by someone with no account at all, so a URL that depended on the reader's own credentials would resolve for neither. The token in the path is the whole authorization. It is 256 random bits, it is minted per (asset, resource) pair, it reaches a reader only inside content they were already allowed to open, and it resolves to nothing on any other asset's path — the same capability model a public share link already uses.

A reference that survives a save keeps its token, so a URL already rendered into a reader's open page does not break every time the author saves.

## Managing references from the portal

An asset's viewer sidebar carries a **Referenced files** panel listing what the asset depends on: each file's name, scope, content type, and, where the file is an image, a thumbnail of it. The thumbnail loads through the reference's own URL rather than through the resource route, so it renders for a reader who was only ever shown the asset.

Every row carries the `mcp://` URI with a copy control. That is the point of the panel rather than a detail of it: adding a reference does not change the asset's content, and the markup has to name the URI for the picture to render.

An owner, an editor on a shared asset, and an administrator can add a reference through a picker over the resources they can read, and remove one. The picker states what the reference gives away and names the asset's current audience before anything is added — a public link, people it is shared with, or neither yet. A reader with no edit authority sees the list and is offered neither control.

A row whose resource has been deleted is flagged rather than dropped. It names the resource id, which is what the owner needs in order to clean it up, and it is the only place they learn the report is now serving without that file.

### Removing one the content still names

The panel reads the asset's stored content and reports which lines write each reference's URI. Removing a reference the content still names warns first, naming those lines, and proceeds on confirmation: the declaration and the markup are two things the author keeps in step, and being unable to withdraw a grant until a document had been edited would be worse than leaving one URI resolving to nothing.

The check does not always run. A binary asset, one larger than 2 MB, or a storage fault leaves it with nothing to report, and the response says which it was. That distinction is the point rather than a detail: "the content does not name this file" makes a removal safe, "we could not look" does not, and the two produce an identical empty list. A removal is confirmed whenever the check did not run, and the confirmation says the content could not be checked rather than claiming the URI is absent from it.

Each reference made through the panel is logged as `asset_resource_reference.granted` and each removal as `asset_resource_reference.revoked`, the same record a save writes when an agent declares one. An operator's log covers both doors or it covers half the grants and looks complete.

### The reverse view

A resource's own detail page carries a **Used by** section beside the one listing the prompts that attach it, naming the assets whose content references it. An asset carrying a public link is flagged there, because that is the reference that widens the file's audience furthest. Referencing assets the reader cannot open are counted but not named — someone deciding whether to delete a file has to know the list is not the whole of what would break.

The answer is bounded at 50, and says so when the bound cut it. Narrowing the list to the assets a given reader may open costs a share lookup per asset, so an unbounded read of a file every asset in a deployment references would turn one page view into a query per asset. A cut answer reads as "at least *n*" rather than as a total, on the same principle the hidden count follows.

Deleting a resource that assets reference warns and names them first, on the same terms. The delete button stays disabled until that check answers, and says so if the check itself fails — a dialog that armed its destructive control while the answer was in flight would let a fast click skip the warning it exists for.

## What the agent reads

`manage_asset get_content` is **not** rewritten. It returns the stored content with the `mcp://` URIs intact, because that is what the agent reads before it patches: an agent handed a rewritten URL would write a platform-internal path back into the asset on its next patch, and the reference would be gone. A `patch` round trip through `get_content` leaves references intact.

## When things change

**The resource is deleted.** The reference row survives, the URL answers 404, and the asset still renders with that one image missing. This is the rule [prompt attachments](../concepts/content-model.md) already follow: losing the evidence that a report is now incomplete is worse than a broken image.

**The reference store is unreachable.** Content is served exactly as stored, with the `mcp://` URIs unresolved. The images go off the page; the page does not.

**The asset is copied.** A copy carries only the references its new owner can read for themselves, each under a fresh token — the two assets are separate grants from that point on. A reference the copier cannot read is dropped rather than refusing the copy, so they get the report with that image missing. A copy never carries a grant its new owner did not earn.

**A version is read.** Version history is rewritten against the asset's **current** references, because the references belong to the asset rather than to any one version. An old version naming a resource the asset no longer references renders that image missing.

## Deployment

Nothing configures this. References are available wherever there is a database and a managed-resource layer; a deployment with neither refuses a declaration with that reason rather than accepting it and recording nothing. The reference route is registered only where it can serve, and it carries its own rate limit sized so one page load can fetch everything the asset declared.

The portal panel and the resource-side view are registered on the same condition and answer nothing where the reference store is absent.

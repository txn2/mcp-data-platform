---
description: How an asset references a managed resource instead of embedding it - declaring a reference on save, the URL every viewing surface rewrites, what the grant gives away, and what happens when the file is deleted or the asset is copied.
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

## What the agent reads

`manage_asset get_content` is **not** rewritten. It returns the stored content with the `mcp://` URIs intact, because that is what the agent reads before it patches: an agent handed a rewritten URL would write a platform-internal path back into the asset on its next patch, and the reference would be gone. A `patch` round trip through `get_content` leaves references intact.

## When things change

**The resource is deleted.** The reference row survives, the URL answers 404, and the asset still renders with that one image missing. This is the rule [prompt attachments](../concepts/content-model.md) already follow: losing the evidence that a report is now incomplete is worse than a broken image.

**The reference store is unreachable.** Content is served exactly as stored, with the `mcp://` URIs unresolved. The images go off the page; the page does not.

**The asset is copied.** A copy carries only the references its new owner can read for themselves, each under a fresh token — the two assets are separate grants from that point on. A reference the copier cannot read is dropped rather than refusing the copy, so they get the report with that image missing. A copy never carries a grant its new owner did not earn.

**A version is read.** Version history is rewritten against the asset's **current** references, because the references belong to the asset rather than to any one version. An old version naming a resource the asset no longer references renders that image missing.

## Deployment

Nothing configures this. References are available wherever there is a database and a managed-resource layer; a deployment with neither refuses a declaration with that reason rather than accepting it and recording nothing. The reference route is registered only where it can serve, and it carries its own rate limit sized so one page load can fetch everything the asset declared.

The portal surface for managing an asset's references, and the reverse view on the resource side, are not part of this. See [`#1475`](https://github.com/txn2/mcp-data-platform/issues/1475).

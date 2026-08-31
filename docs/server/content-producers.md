---
description: The record of what wrote a portal asset or a managed resource - the scripts, sessions and people that created or modified it, read from the file and from the script, what is recorded at each write funnel, and what the migration derives from history and what it declines to guess.
---

# What Produced a File

Every portal asset and every managed resource records what wrote it: each managed script, agent session and person that created or modified it, when they first and last wrote, how many times, and which version they last produced.

!!! quote "The rule"
    A producer is recorded by **id**, never by name. Renaming a script changes what a surface displays and nothing else; deleting one leaves behind the record that it wrote this file.

## The question nothing else answered

Three records come close and each answers something different.

An asset's [provenance](provenance.md) records the data calls its content was built from — which queries fed this report. It is not who wrote the report, it cannot be read backwards without scanning every asset's stored JSON, and it says nothing at all about a managed resource.

A [managed script](../scripts/running.md) linked to its own outputs through the asset's idempotency key, one string per `(script, declared output)` pair. That key is what makes a re-run land on the same asset and it does that job well, but it is one-to-one, it covers only the portal destination of a *declared* output, and nothing joins on it: a script that modifies an asset it never declared leaves no trace.

A resource recorded an uploader. A run authenticates as `script:<name>`, so a resource a script wrote carried the script's **name**, by string — severed by a rename, and absent entirely for a script that replaced the content of a file somebody else uploaded.

The relation is many-to-many by nature. One script writes many files; one file is written by many producers over its life. A person editing a report a script also refreshes is the ordinary case.

## What is recorded

One row per `(file kind, file id, producer kind, producer id)`, holding whether that producer created the file or has only modified it, the first and last write, the number of writes, and the last version written.

| Producer kind | What it is |
| --- | --- |
| `script` | A managed script, by its id. A run stamps this itself, so it survives a rename. |
| `session` | The agent session a tool call was made in, which is the unit a reader can open and follow. |
| `person` | Somebody writing through the portal's own pages. |

Exactly one producer is recorded per write. An agent's `save_asset` is filed under its session, not also under the person behind it; the session's own page names that person.

The file kind is part of the key because asset ids and resource ids are separate id spaces and the same string can name one of each.

## Where it is recorded

At the write funnels every write already passes through, not at each call site:

- **Assets** — the asset store's insert (which `save_asset`, `manage_asset`, both exports and the managed-script output writer all reach) and the version store's `CreateVersion`. Version 1 is not counted separately: it is the content half of the create, so one save is one write.
- **Resources** — `CreateResource` and `ReviseContent`, which the upload route, `manage_resource`, and a [registration's corrective revision](registered-tables.md) all pass through.

Recording is **best effort and never fails the write**, on the same reasoning the [audit](audit.md) path uses: losing the note that a write happened must not lose the write. A failure is logged and the write stands.

## Reading it from both ends

| Surface | What it answers |
| --- | --- |
| An asset's **Written by** panel ([Assets](../portal/assets.md)) | What has written this report, beside the provenance panel that answers what its content was built from |
| A resource's **Written by** panel ([Resources](../portal/resources.md)) | What has written this file |
| A script's **Files written** section ([Scripts](../portal/scripts.md)) | Every asset and resource this script has created or modified, across every run |

The REST routes behind them are `GET /api/v1/portal/assets/{id}/producers`, `GET /api/v1/portal/resources/{id}/producers`, and `GET /api/v1/portal/scripts/{id}/produced`. The first two require the same access the file's own page requires; the third is the script's owner and administrators, which is the rule every other script route applies.

The per-run output list on a script's run history is unchanged. It answers what one run did, which the aggregate does not.

## A deleted script, and a deleted file

The relation deliberately keeps no foreign key to `scripts`, `portal_assets` or `resources`. Deleting a script must leave behind the record that it wrote this file rather than silently erasing it, which is the same reasoning [asset references](asset-references.md) apply to a deleted target.

The producer's name at the time it wrote is stored beside its id, so a surface can still say *which* script that was after the script is gone. A file's own producer panel then reports that the script no longer exists rather than linking to a page that would answer not-found, and a script's produced list keeps a deleted file listed, named by its id.

## What the migration derives, and what it does not

Existing deployments gain rows for the two signals already in the schema that are unambiguous:

- an asset whose idempotency key is `script:<script id>:<output name>` was created by that script's output writer, and by nothing else;
- a resource whose uploader is `script:<name>`, where exactly one script bears that name.

Everything else is left out. A resource somebody uploaded lists no producer rather than a wrong one, and where two owners each keep a script of the same name the link is genuinely ambiguous and neither is recorded. A history that was never recorded is not reconstructed by guessing.

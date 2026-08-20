# Script outputs: export identity, stable names, and dated series

`platform.export(name, rows, format=...)` is how a managed script writes. Where
the output lands and what identity it keeps are decided by two things you
choose: the destination and the name.

## Why your export is not a new asset every day

Output identity at the portal is stable: the pair of **(script, output name)**
maps to **one** asset, and each run writes a new version of it. Two patterns
follow, and both are legitimate — choose by what the output is for:

- **A stable name is a refresh.** `platform.export(name="weekly-sales", ...)`
  keeps one asset with its URL, its shares, and its whole version history; a
  year of runs leaves one asset with a year of versions. Use it for the living
  report or dashboard people bookmark and share.
- **A dated series is an archive.** Composing the period into the name —
  `"sales-{}".format(run.params["report_date"])` — produces one asset per
  period, each frozen as of its run. Use it when each period's output is a
  separate deliverable (a monthly close, a statement), not a newer copy of the
  same thing.

If you see a new asset appearing every morning and expected one asset gaining
versions, the name is changing per run; make it a literal.

## rows: a table or a document, decided by format

`rows` carries the content in one of two shapes, and the declared format
decides which are valid:

- **A list of dicts**, serialized in the declared format. `csv` and `json`
  accept **only** this shape — passing a string body to `format="csv"` or
  `"json"` is refused, so a data feed another system parses stays well-formed
  by construction.
- **A string body, written verbatim**, so a script can compose a document: an
  HTML or JSX dashboard, a prose report, a hand-assembled markdown page. `html`
  and `jsx` accept **only** a string body; `markdown` and `text` accept either.

So: tabular data goes out as `csv` or `json` from a list of dicts; a composed
document goes out as `html`, `jsx`, `markdown`, or `text` from a string. An
empty document body is refused rather than published, so a conditionally
assembled document that ends up blank fails the run loudly instead of silently
replacing the current version of a shared dashboard.

## The zero-data guard

The zero-rows case is yours to decide, and deciding it explicitly is the idiom:

```python
rows = platform.query(connection="warehouse", sql="SELECT ...")["rows"]
if not rows:
    fail("no rows for {}; refusing to overwrite the current version".format(run.params["day"]))
platform.export(name="weekly-sales", rows=rows, format="csv")
```

Publish the empty structure when empty is a true answer; `fail("why")` when it
means the upstream data is missing and the current version should stand. A
failed run is recorded with its reason; a silently empty publish is not.

## Destinations

`destination` defaults to `portal` (the versioned asset). A version approved
with a bucket destination delivers the same bytes to an external system
instead; the script names only the destination — the connection, bucket, and
prefix come from the approval. `destination` and `key` must be passed **by
name**, not positionally. One output name may be written once per destination
in a run, so sending one result to the portal and to a bucket is two calls with
one name.

In a draft run nothing is written anywhere: each export reports the shape and
size it would have.

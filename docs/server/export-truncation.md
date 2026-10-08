# Exports cut at a limit

Every export runs under a bound. `trino_export` reads at most a row limit: the caller's `limit`, or the deployment's cap, `portal.export.max_rows` (default 100,000). `api_export` and `graphql_export` with `paginate` walk at most a page bound: the caller's `paginate.max_pages`, or the tool's default (100 pages for `api_export`, 10 for `graphql_export`).

When the source has more than the bound lets through, the export was cut. The platform detects this from the engine's own signal and never by counting the query a second time. `trino_export` reads one row past the limit. A page walk stops at its bound only when the page it fetched last named another page. A result that ends exactly at the bound is complete.

A cut export is never reported as a clean success. What happens depends on who set the bound:

| Who set the bound | Default | With `on_truncation: "warn"` | With `on_truncation: "fail"` |
|---|---|---|---|
| The caller (`limit`, `paginate.max_pages`) | Written and flagged | Written and flagged | Refused, nothing written |
| The deployment (`portal.export.max_rows`, the default page bound) | Refused, nothing written | Written and flagged | Refused, nothing written |

A refusal names the bound, the key or parameter that set it, the evidence that the source had more, and what to do instead. For example:

```text
the result was cut at the deployment cap of 100000 rows (portal.export.max_rows): the query returned more rows, so nothing was written. Set limit to export a chosen subset, or split the query by key range (one export per range) or aggregate it in SQL so each result fits under the cap. To write the first 100000 rows anyway, marked incomplete, set on_truncation to "warn".
```

`api_export` and `graphql_export` have no row limit and refuse a result over `portal.export.max_bytes` whole, so a single response is never cut. Only a page walk can be.

## What the response says

Every `trino_export` response, and every `api_export` or `graphql_export` response that walked pages, carries:

| Field | Meaning |
|---|---|
| `truncated` | `true` when the bound cut the result |
| `limit_applied` | The bound's value |
| `limit_source` | `request` (the caller set it) or `deployment` |
| `limit_unit` | `rows` or `pages` |
| `arbitrary_subset` | `true` when a `trino_export` result was cut and its SQL has no top-level `ORDER BY`, so which rows were kept is the engine's choice and can differ on the next run |
| `expect_mismatch` | Under `on_truncation: "warn"`, the count expectation the export missed |

The `message` says it in words: `Exported 100000 rows as csv. Truncated at the deployment cap of 100000 rows (portal.export.max_rows); the query returned more rows. This file is incomplete.`

An idempotency hit on an asset a cut export wrote reports the same fields.

## Where the file is marked

The response is read once. The file is opened later by people who never saw it, so a cut file is marked where it lives:

- **Portal assets** carry the reserved tag `_sys-truncated` while their current version is a cut export, and the version records `truncated`, `limit_applied`, `limit_source` and `limit_unit` in its metadata. The asset list shows an **Incomplete** badge, the asset page states `Incomplete: truncated at 100,000 rows`, and the version history marks each cut version. A later version that is complete, including a file uploaded in the portal, removes the tag. A revert to a cut version brings the mark back with its content. Search for incomplete assets with the tag.
- **Managed resources** record the cut on the version the export landed. The resource page and its version history show it. A resource is not tagged, because a replacement leaves a resource's tags alone and the tag would outlive the cut.
- **Managed script runs** record the cut on the run's output, and the run's page marks that output **Incomplete**. A cut a run did not accept fails the `platform.call` that made it, which fails the run unless the script passed `on_error="return"`.

## Count expectations

`expect_rows` (an exact count) and `expect_min_rows` (a floor) guard a recurring export, where a sudden change in count usually means an upstream feed broke. A count outside the expectation refuses the export, or is flagged under `on_truncation: "warn"`. For `trino_export` the count is rows. For `api_export` and `graphql_export` it is the items a page walk merged, and an expectation on a call without `paginate` is refused.

## Reading the bounds ahead of time

`platform_info` reports the bounds the caller's tools run under in its `limits` block, each with the key that sets it where a key does:

```json
"limits": {
  "export": {
    "max_rows": 100000, "max_rows_key": "portal.export.max_rows",
    "max_bytes": 104857600, "max_bytes_key": "portal.export.max_bytes",
    "default_timeout_seconds": 300, "max_timeout_seconds": 600, "max_timeout_key": "portal.export.max_timeout"
  },
  "query": { "default_rows": 1000, "max_rows": 10000, "timeout_seconds": 120 },
  "page_walk": {
    "api": { "default_max_pages": 100, "max_pages": 10000 },
    "graphql": { "default_max_pages": 10, "max_pages": 1000 }
  }
}
```

A part is present only when the caller's persona reaches a tool it bounds. `trino_export`'s `limit` parameter states the configured cap in its description.

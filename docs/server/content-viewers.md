# Content Types and Viewers

Every asset and managed resource is stored under a media type, and that type
decides how the portal renders it. This page covers where the type comes from,
which viewer each family gets, and how raw content is served.

## Content-type detection

The platform does not take a declared content type on faith. An upstream API
that answers a JSON endpoint with `Content-Type: text/plain`, a browser that
sends `application/octet-stream` for an extension it does not recognize, and an
agent that saves a payload under a catch-all type would all otherwise produce an
asset the viewer can only show as raw text.

Detection runs on every write path that accepts outside content:

| Write path | Declared type comes from |
|---|---|
| `save_asset` | the tool's `content_type` argument |
| `manage_asset action=update` | the tool's `content_type`, or the asset's existing type |
| `api_export` | the upstream response's `Content-Type` header |
| Resource upload | the multipart part's `Content-Type` header |

`trino_export` sets an exact type from its own formatter and is not sniffed.

### Rules

- **A specific declaration wins.** Detection only runs when the declaration is
  absent, `application/octet-stream`, or `text/plain`.
- **Binary families come from magic bytes.** Images, audio, video, PDF and
  archives are recognized from the first 512 bytes.
- **Structured text is layered on top.** JSON, NDJSON, XML, YAML, CSV and TSV
  all look like plain text to a byte sniffer, so each has its own heuristic over
  a bounded prefix (8 KB).
- **Detection reads a prefix, never the whole payload.** A streaming export
  stays streaming: the prefix is replayed ahead of the untouched remainder.
- **Aliases are normalized.** `text/json` and `application/json` both store as
  `application/json`, `text/xml` and `application/xml` as `application/xml`, and
  so on, so one family means one type string everywhere downstream.
- **The stored object key follows the detected type**, so a bucket listing shows
  `.json` rather than `.bin`.

When detection replaces a declaration, the original is recorded in the asset's
provenance as `declared_content_type`, so a stored type that disagrees with its
source is explainable after the fact.

### The active-type rule

Detection may reclassify content **into passive families only**: JSON, images,
audio, video, PDF, CSV, XML, YAML, plain text. It can never promote content to
`text/html`, `text/jsx`, `image/svg+xml` or JavaScript. Those types render as
executing markup, and they render only when an author declared them
deliberately.

A payload that sniffs as HTML but was declared `text/plain` is stored and
rendered as plain text. A payload with no declaration at all that sniffs as HTML
becomes `text/plain`. This keeps a mislabeled upload from turning itself into
script-bearing content.

### Existing assets

Assets stored before detection existed still carry whatever generic type they
were saved with. The portal applies the same rules on the client when it opens
one, so an older `application/octet-stream` asset holding JSON still reaches the
JSON viewer. No data migration is involved, and the client-side rules honor the
same active-type restriction.

## Viewers

One shared renderer registry drives every surface: the portal asset viewer, the
public and guest viewers, collection items, and the resources detail view. A
content type renders identically wherever it is opened.

| Family | Types | Viewer | Editor |
|---|---|---|---|
| JSON | `application/json` | Collapsible tree with search across keys and values, match count and jump-to-match, JSONPath breadcrumb with copy-path and copy-value, type-aware values, and raw/formatted/tree views. Virtualized. | CodeMirror with JSON mode and a parse-error gutter |
| JSON Lines | `application/x-ndjson` | One expandable row per record, each opening into the JSON viewer | CodeMirror |
| Tabular | `text/csv`, `text/tab-separated-values` | Sortable, searchable table | CodeMirror |
| Images | `image/png`, `image/jpeg`, `image/gif`, `image/webp`, `image/avif`, ... | Zoom and pan, checkerboard backing for transparency, dimensions and size readout, fit/actual-size toggle | None |
| SVG | `image/svg+xml` | Sanitized inline render | Source editor |
| Audio | `audio/mpeg`, `audio/wav`, `audio/ogg`, `audio/mp4`, `audio/flac` | Native player with seek | None |
| Video | `video/mp4`, `video/webm`, `video/ogg` | Native player with seek | None |
| PDF | `application/pdf` | Embedded viewer (`<object>`) over the content URL, with a download fallback | None |
| Markup | `text/html`, `text/jsx`, `text/markdown` | Sandboxed / sanitized renderers | Source editor |
| Structured text | `application/xml`, `application/yaml` | CodeMirror, read-only, with folding and a wrap toggle | CodeMirror |
| Code and logs | `application/sql`, `text/x-python`, `text/javascript`, `text/plain` | CodeMirror, read-only, with line numbers and a wrap toggle | CodeMirror |
| Anything else | | Metadata card naming the type and size, with a download action | None |

Media types are never edited: the platform stores audio, video and images, it
does not transcode them.

### Size limits

The inline preview limit is per family, not global:

- Media and PDF stream from the content endpoint and have **no** limit.
- JSON and tabular viewers virtualize, so they hold a much higher limit
  (32 MB), so a multi-megabyte JSON document opens in the tree rather than being
  refused.
- Families rendered as one continuous block of text keep a 2 MB cutoff, above
  which the viewer offers a download.

## Serving raw content

Every raw-content endpoint (portal assets, asset versions, thumbnails, public
share content, managed resources) writes through one shared code path, so the
guarantees below hold on all of them:

- **`X-Content-Type-Options: nosniff`**, so a browser cannot decide for itself
  that a `text/plain` response is really HTML and run it.
- **A parsed, parameter-free `Content-Type`**, so a stored value cannot smuggle
  anything into the header.
- **`Content-Disposition: attachment` for active types** (HTML, JSX, SVG,
  JavaScript), which never render inline on the platform's own origin, and
  `inline` for the passive families a viewer embeds.
- **Byte-range support**, so audio and video elements seek by requesting the
  range they need instead of downloading the whole object first.

The PDF viewer deliberately carries no iframe `sandbox`: Chrome refuses to
instantiate its PDF plugin inside any sandboxed frame, with or without
`allow-scripts`, so a sandboxed PDF frame shows a broken-plugin icon rather than
the document. Containment for PDFs comes from the serving guarantees above plus
the `object-src 'self'` directive in the public viewer's CSP.

Binary content is served from these endpoints rather than embedded in the
viewer page. The public viewer embeds text content in the page as a JSON string,
which cannot carry arbitrary bytes; images, audio, video and PDFs are handed a
content URL instead.

The public viewer's Content-Security-Policy carries `media-src` and `object-src`
for the audio, video and PDF sources. Active types keep their existing sandboxed
iframe and DOMPurify treatment.

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
- **Except where the filename and the content both contradict it.** A resource
  upload carries a filename, and a declaration that disagrees with both the
  extension and the bytes is wrong about what it labels: a `.csv` whose content
  parses as CSV is stored as `text/csv` even when the uploading machine declared
  `application/vnd.ms-excel`, which is what Windows sends for `.csv` when Excel
  is installed. Neither signal counts alone -- a `.csv` holding a PNG keeps its
  declaration, because the bytes do not back the name -- and an extension whose
  family renders as executing markup (`.html`, `.js`, `.svg`) never wins,
  because a filename is not a declaration. The other write paths carry no
  filename and are unaffected.
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

### Accepted types

Detection settles what a payload *is*. What the platform will *store* is a
separate question, and the answer differs by what the door carries.

**Doors that carry content as a string** — `POST /api/v1/portal/assets` (inline
create), `save_asset`, and `manage_asset action=update` — accept one list:
`text/markdown`, `text/plain`, `text/html`, `text/jsx`, `text/csv`,
`text/tab-separated-values`, `image/svg+xml`, `application/json`,
`application/x-ndjson`, `application/xml`, `application/yaml`,
`text/javascript`, `text/css`, `text/x-python`, `application/sql` and
`application/octet-stream`. Aliases normalize first, so declaring `text/json` or
`text/xml` works, and membership is exact: `text/*` is not a wildcard, because
it would admit every `text/x-*` type a caller cares to invent. A refused write
names the accepted types rather than failing silently.

The list is textual because these doors are. Content reaches them inside a JSON
document, so a binary family has no way through in the first place; the list
costs no capability. `application/xhtml+xml` is deliberately absent — a browser
renders XHTML natively and runs the script inside it.

A content update may keep whatever type its asset already carries, including one
no door would accept today: an asset written by `api_export` from an upstream
response, or one that predates the list. Changing the type is a new declaration
and goes through the same check as a create.

**The resource upload door carries bytes and keeps a denylist**, refusing the
executable MIME types and `application/xhtml+xml`, alongside a denylist of
executable file extensions. A resource is human-uploaded reference material —
report templates, brand files, CAD exports, sample documents — so the long tail
of formats has to get through, and an allowlist there would refuse the library's
purpose to buy little: every stored byte is already served under the sandbox CSP
and disposition rules below, whatever its type.

`api_export` is not gated either: it stores the upstream response's own type,
which is neither caller-declared nor carried as a string. `trino_export` sets its
type from its own formatter.

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
guarantees below hold on all of them. The API gateway's raw passthrough route
answers under the same contract: it does not store the bytes it serves, but it
does reproduce an upstream's bytes on the platform's own origin, so the type,
disposition, `nosniff` and CSP decisions are made in the same place rather than
forwarded from the upstream. Byte-range support and the cache default are the
two items scoped differently there: the passthrough serves no ranges of its own
(it relays the upstream's partial response, `Content-Range` included, when the
caller asks the upstream for one), and an upstream that states its own
`Cache-Control` keeps it.

- **`Content-Security-Policy: default-src 'none'; sandbox`**, on every response
  regardless of type. `default-src 'none'` denies the document every fetch it
  could make and, because `script-src` falls back to it, kills inline script,
  event handler attributes and `javascript:` URLs; `sandbox` with no `allow-`
  tokens puts the document in an opaque origin with scripting off. The header is
  unconditional on purpose: stored content has no legitimate need for
  same-origin script here, and a conditional header would make the guarantee
  depend on the type classification below staying complete.
- **`X-Content-Type-Options: nosniff`**, so a browser cannot decide for itself
  that a `text/plain` response is really HTML and run it.
- **A parsed, parameter-free `Content-Type`**, so a stored value cannot smuggle
  anything into the header.
- **`Content-Disposition: attachment` for scriptable document types** (HTML,
  XHTML, JSX, SVG, JavaScript, XML and any `+xml` dialect), which never render
  inline on the platform's own origin, and `inline` for the passive families a
  viewer embeds.
- **Byte-range support**, so audio and video elements seek by requesting the
  range they need instead of downloading the whole object first.
- **`Cache-Control: private` by default**, so an endpoint that authorized its
  caller does not hand the bytes to a shared cache by saying nothing: a response
  carrying no directive at all is heuristically storable, and a CDN or ingress
  cache in front of the platform would then answer later requests for the same
  URL from one authorized fetch. This one is a default rather than an override —
  a fully public share's thumbnail is genuinely anonymous and sets `public,
  max-age=3600` deliberately.

The scriptable set is wider than the set of types the sniffer refuses to
promote. XML is safe to name from content, and a viewer shows it as inert text,
but a browser navigating to `application/xml` builds a document and honors an
`<?xml-stylesheet?>` processing instruction, so it is served as a download.
`nosniff` is no help for XHTML or XML: it enforces the declared type, and the
declared type genuinely is a document type.

The PDF viewer deliberately carries no iframe `sandbox`: Chrome refuses to
instantiate its PDF plugin inside any sandboxed frame, with or without
`allow-scripts`, so a sandboxed PDF frame shows a broken-plugin icon rather than
the document. The response-level sandbox CSP above is a different mechanism and
does not have that effect — a PDF served under it still renders in Chrome's
viewer through `<object>`. Containment for PDFs comes from the serving
guarantees above plus the `object-src 'self'` directive in the public viewer's
CSP.

Binary content is served from these endpoints rather than embedded in the
viewer page. The public viewer embeds text content in the page as a JSON string,
which cannot carry arbitrary bytes; images, audio, video and PDFs are handed a
content URL instead.

The public viewer's Content-Security-Policy carries `media-src` and `object-src`
for the audio, video and PDF sources. Active types keep their existing sandboxed
iframe and DOMPurify treatment.

### What the public share page loads

The page carries its own chrome — the theme toggle, the expiry countdown, the
modal handlers, the asset's content as JSON — and its stylesheet inline. It does
not carry the renderer. The viewer is referenced as a module:

```html
<script type="module" src="/portal/view/_assets/content-viewer-entry-<hash>.js"></script>
```

and each family's viewer is a chunk the browser fetches only if the asset it is
showing needs it. A markdown document loads the markdown renderer; it does not
load CodeMirror, the JSX transformer, the CSV parser or the diagram engine, and
a document with no ```mermaid fence does not load the diagram engine either.

The chunks are served from `/portal/view/_assets/{file}` by the portal handler,
outside both the share access gate and the viewer rate limiter. The gate has
nothing to gate on: no token is in the path, no share is looked up, and the same
bytes are served to every viewer, so gating it would leave every public share
page blank. The limiter is wrong for a different reason — it is sized for share
page loads (60/min, burst 10), and one cold view of a markdown document with a
diagram in it legitimately fetches around thirty chunks at once, which that
bucket would answer 429. Nothing here justifies the bucket either: a request
costs a map lookup and a read from memory, the names are content hashes with
nothing to enumerate, and this matches how the portal's own SPA bundle is
already served. Filenames are content-hashed and served
`Cache-Control: public, max-age=31536000, immutable`, so the second share
someone opens costs no JavaScript at all.

A chunk that does not arrive — a tab left open across a deploy asks a replica
for a hash it does not have — is caught by an error boundary rather than
blanking the page: `<Suspense>` does not catch a rejected `import()`, and
React's own answer to one is to unmount the document. The share page keeps its
metadata and download link and says the preview could not be loaded; the portal
keeps its chrome and offers a reload, which is the fix when the cause is a
deploy that moved the chunk names.

The stylesheet is inline rather than a second request because it is small: it is
compiled against the viewer's own bundle (`ui/src/content-viewer.css` declares
the emitted chunks as its only Tailwind source), not copied from the portal SPA.
That ordering is why `make content-viewer-embed` builds the JavaScript before
the stylesheet.

### The public viewer's policy

One policy governs two documents, which is what bounds how narrow it can be: the
viewer page, and the untrusted HTML and JSX assets it renders in `blob:` URL
iframes, which inherit the creating document's policy. The page is served with:

```
default-src 'none'; script-src 'self' 'unsafe-inline' blob: https:; style-src 'unsafe-inline' https:;
img-src * data: blob:; media-src 'self' blob: data:; object-src 'self';
font-src * data:; connect-src 'self' https:;
```

plus `frame-src blob: data: 'self'` for a single asset, or `frame-src 'self'
blob: data:` for a collection, whose items open in a same-origin iframe.

- `script-src 'unsafe-inline'` is required by both documents: the page's theme,
  expiry and modal handlers are inline, and a stored HTML asset's own
  `<script>` blocks are the artifact. A per-response nonce would cover the page
  and blank every HTML asset, since the inherited policy would reject script the
  server never saw. What isolates an artifact is the frame —
  `sandbox="allow-scripts"` without `allow-same-origin`, so artifact script runs
  in an opaque origin.
- `script-src 'self'` is what lets the page load the viewer bundle from
  `/portal/view/_assets/` and fetch the rest of its chunks. On an https
  deployment `https:` already covered it; `'self'` is what makes a plaintext
  deployment work too. It grants the server's own origin, which is where the
  page came from, and resolves to nothing inside an artifact frame, whose origin
  is opaque.
- `script-src https:` is required because assets legitimately load third-party
  script: the JSX renderer resolves react, react-dom, recharts and lucide-react
  from esm.sh through an import map, and stored HTML artifacts reference CDN
  libraries directly. Plain `http:` is not permitted — an https page blocks it
  as mixed content anyway, so allowing it only widened the policy for plaintext
  deployments.
- `script-src blob:` stays because `worker-src` falls back through `child-src`
  to it, so dropping it would refuse an artifact its own web worker. It widens
  nothing: a blob URL can only be minted by script that is already running,
  which `'unsafe-inline'` has already permitted.
- `'unsafe-eval'` is not granted. Sucrase transforms JSX in the parent page and
  the frame runs the result as a module, so no viewer path evaluates source at
  runtime. An artifact that calls `eval` or `new Function` is refused.
- `style-src`, `img-src` and `font-src` stay permissive for artifacts that style
  themselves inline and pull images and webfonts from arbitrary hosts. All three
  are passive.
- `connect-src` is there for artifacts. No viewer path issues a request it
  governs — content URLs are handed to elements, which answer to `img-src`,
  `media-src` and `object-src` instead.

The policy is enforced by the browser and by nothing else, so a change to it is
verified by rendering each family under it rather than by reading the header.
`make frontend-e2e-public-viewer` drives HTML, JSX, markdown, SVG and a
collection item against a live stack and fails on any blocked resource. It is
not part of `make verify`: it needs the content-viewer bundle built
(`make frontend-build`, which the binary embeds at compile time), a running
server (`make dev`) and network egress to esm.sh, which the JSX family resolves
its imports from. The families served from a content URL — image, audio, video,
PDF, the ones `media-src` and `object-src` exist for — have no public share in
the dev seed and are covered by the Go tests instead.

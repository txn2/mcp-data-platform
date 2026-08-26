# Public viewer end-to-end tests

Coverage of the portal's public share viewer: the server-rendered page at
`/portal/view/{token}`, the content families it renders client-side, and the
Content-Security-Policy those renders run under.

This suite needs a live backend. The page's markup, its embedded content-viewer
bundle and its CSP header all come from the platform binary, so MSW cannot stand
in for it the way it does for the admin suites.

## Running

```bash
make frontend-build            # the content-viewer bundle the binary embeds
make dev                       # platform binary + Postgres + SeaweedFS, seeded
make frontend-e2e-public-viewer
```

`make frontend-build` comes first and is not optional: `internal/contentviewer/dist`
is a gitignored build artifact embedded into the binary at compile time, and
`make dev` does not produce it. A binary built from an empty dist serves a page
with an empty `<script>` and renders nothing. The suite checks for that before
its first case and says so rather than timing out on every family.

The suite reads `PUBLIC_VIEWER_BASE_URL`, falling back to
`http://localhost:${DEV_API_PORT:-8080}`. `make dev` relocates its ports when
8080 is taken, so pass the URL explicitly against a relocated stack:

```bash
PUBLIC_VIEWER_BASE_URL=http://localhost:28080 npm run test:public-viewer
```

## What it depends on

- The public shares in `dev/seed.sql` — one per client-rendered family
  (`tok-revenue-dash-public`, `tok-store-compare-public`,
  `tok-pipeline-arch-public`, `tok-regional-heatmap-public`) plus the collection
  (`tok-q3-exec-review-public`). Each is `access_mode = 'public'`, which is what
  lets an anonymous browser open it.
- Network egress to esm.sh: the JSX case resolves react, react-dom and the
  charting libraries through the artifact frame's import map, exactly as a
  shared JSX asset does in production.
- The asset reference `dev/seed-asset-refs.sh` declares: the seeded HTML and
  JSX assets name the global brand mark by its `mcp://` URI instead of carrying
  it, and the reference cases assert it renders.

## What it asserts

Each of the four families the page renders client-side — HTML, JSX, markdown,
SVG — renders, and no console message reports a resource the policy refused.
The families served from a content URL instead (image, audio, video, PDF) are
not covered here: the dev seed has no public share for one. They are what
`media-src` and `object-src` exist for, so those two directives are pinned by
`pkg/portal/publicviewer`'s Go tests alone. The policy cases assert the served header denies plaintext script and
runtime eval, then prove it by rendering an artifact that tries both — in a
`blob:` frame created inside the live page, so it runs under the same inherited
policy a stored asset does.

The reference cases cover the other direction, what an artifact must be ABLE to
load: an HTML and a JSX share each render a referenced image through
`/portal/refs/`, and a JSX artifact is shown to reach that route and no other
path on the platform origin. A JSX artifact carries a policy of its own on top
of the inherited one, which is the half a header assertion cannot check.

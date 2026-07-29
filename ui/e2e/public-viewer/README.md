# Public viewer end-to-end tests

Coverage of the portal's public share viewer: the server-rendered page at
`/portal/view/{token}`, the content families it renders client-side, and the
Content-Security-Policy those renders run under.

This suite needs a live backend. The page's markup, its embedded content-viewer
bundle and its CSP header all come from the platform binary, so MSW cannot stand
in for it the way it does for the admin suites.

## Running

```bash
make dev                       # platform binary + Postgres + SeaweedFS, seeded
cd ui && npm run test:public-viewer
```

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

## What it asserts

Each family renders, and no console message reports a resource the policy
refused. The policy cases assert the served header denies plaintext script and
runtime eval, then prove it by rendering an artifact that tries both — in a
`blob:` frame created inside the live page, so it runs under the same inherited
policy a stored asset does.

import { http, HttpResponse } from "msw";

// A site's files (#2590) over module state: dev's tree, one file's text, and
// the saves, deletes and uploads that each make a release of dev from the one
// the editor read. A save against an older release answers 409, as the server
// does. `bumpSiteFile` lets a test stand in for someone else saving first.

const base = "/api/v1/portal/sites";
const at = "2026-11-08T13:52:00Z";

const PRICING = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Pricing | Example</title>
</head>
<body>
  <main>
    <h1>Plans for every team</h1>
    <section class="plan">
      <h2>Starter</h2>
      <p class="price">$12 per user, per month</p>
      <a class="btn" href="/signup">Start free</a>
    </section>
  </main>
</body>
</html>
`;

const LAYOUT = `<!doctype html>
<html lang="en">
<head><title>{{ page.title }} | {{ site.title }}</title></head>
<body>
  {% include "nav.html" %}
  <main>{{ content }}</main>
  {% include "footer.html" %}
</body>
</html>
`;

interface MockFile {
  content_type: string;
  body?: string;
  size: number;
}

function text(body: string, type = "text/html; charset=utf-8"): MockFile {
  return { content_type: type, body, size: body.length };
}

function makeTree(): Record<string, MockFile> {
  return {
    "index.html": text("<h1>Example</h1>\n"),
    "about/index.html": text("<h1>About us</h1>\n"),
    "pricing/index.html": text(PRICING),
    "pricing/old-plans.html": text("<h1>Old plans</h1>\n"),
    "css/site.css": text("body { color: #123; }\n", "text/css; charset=utf-8"),
    "images/hero.8b1d44f0.webp": { content_type: "image/webp", size: 1_200_000 },
    "images/logo.svg": text("<svg xmlns=\"http://www.w3.org/2000/svg\"/>\n", "image/svg+xml"),
    _redirects: text("/old /new 301\n", "text/plain; charset=utf-8"),
    "_layouts/base.html": text(LAYOUT),
  };
}

let tree = makeTree();
let release = 12;

export function resetSiteFiles() {
  tree = makeTree();
  release = 12;
}

/** Someone else saves first: dev moves on, with or without this file changed. */
export function bumpSiteFile(path?: string, body?: string) {
  release += 1;
  if (path && body !== undefined) tree[path] = text(body);
}

const missing = (path: string) => HttpResponse.json({ detail: `release ${release} has no file "${path}"` }, { status: 422 });
const stale = (b: number) => HttpResponse.json({ detail: `base_version ${b} does not match the current version ${release}` }, { status: 409 });

function published(note: string) {
  release += 1;
  return {
    environment: "dev",
    address: "https://marketing.pages.example.net",
    release: { id: `srel_${release}`, number: release, base: release - 1, note, source: "edit", file_count: Object.keys(tree).length, total_bytes: 1, redirects: [], created_by: "admin@example.com", created_at: at, served_in: ["dev"], kept_by: ["dev"] },
  };
}

function listing(prefix: string) {
  const files = Object.entries(tree)
    .filter(([p]) => p.startsWith(prefix))
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([path, f]) => ({ path, content_type: f.content_type, size: f.size, sha256: `sum-${path}` }));
  return { environment: "dev", version: release, files, total: files.length, truncated: false };
}

function diffOf(from: number, to: number) {
  return `--- r${from}\n+++ r${to}\n@@ -11,4 +11,4 @@\n       <h2>Starter</h2>\n-      <p class="price">$12 per user, per month</p>\n+      <p class="price">$15 per user, per month</p>\n       <a class="btn" href="/signup">Start free</a>\n`;
}

export const siteFileHandlers = [
  http.get(`${base}/:site/files`, ({ request }) => {
    const q = new URL(request.url).searchParams;
    if (q.get("state") === "error") return HttpResponse.json({ detail: "this could not be done; try again" }, { status: 500 });
    return HttpResponse.json(listing(q.get("path") ?? ""));
  }),
  http.get(`${base}/:site/file`, ({ request }) => {
    const path = new URL(request.url).searchParams.get("path") ?? "";
    const f = tree[path];
    if (!f) return missing(path);
    if (f.body === undefined) return HttpResponse.json({ detail: `the content is ${f.content_type}, not text` }, { status: 422 });
    return HttpResponse.json({ environment: "dev", release, path, content_type: f.content_type, body: f.body });
  }),
  http.put(`${base}/:site/file`, async ({ request }) => {
    const b = (await request.json()) as { path: string; content: string; base: number; note?: string; dry_run?: boolean };
    if (b.base !== release) return stale(b.base);
    if (b.content.includes("| datefmt")) {
      return HttpResponse.json({ detail: `${b.path} line 10: unknown filter "datefmt". It failed in all 6,118 pages that use this layout.` }, { status: 422 });
    }
    if (b.dry_run) return HttpResponse.json({ environment: "dev", address: "", release: { number: release }, outputs_changed: b.path.startsWith("_layouts/") ? 6118 : 1 });
    tree[b.path] = text(b.content, tree[b.path]?.content_type);
    return HttpResponse.json(published(b.note ?? ""));
  }),
  http.delete(`${base}/:site/file`, ({ request }) => {
    const q = new URL(request.url).searchParams;
    const path = q.get("path") ?? "";
    if (Number(q.get("base")) !== release) return stale(Number(q.get("base")));
    if (!tree[path]) return missing(path);
    delete tree[path];
    return HttpResponse.json(published(q.get("note") ?? ""));
  }),
  http.post(`${base}/:site/files`, async ({ request }) => {
    const form = await request.formData();
    if (Number(form.get("base")) !== release) return stale(Number(form.get("base")));
    const folder = String(form.get("folder") ?? "");
    for (const f of form.getAll("files")) {
      if (typeof f === "string") continue;
      tree[folder ? `${folder}/${f.name}` : f.name] = { content_type: f.type || "application/octet-stream", size: f.size };
    }
    return HttpResponse.json(published(String(form.get("note") ?? "")));
  }),
  http.get(`${base}/:site/file/diff`, ({ request }) => {
    const q = new URL(request.url).searchParams;
    const from = Number(q.get("from") ?? 0) || release - 1;
    const to = Number(q.get("to") ?? 0) || release;
    const path = q.get("path") ?? "";
    return HttpResponse.json({ from, to, path, diff: diffOf(from, to) });
  }),
];

import { http, HttpResponse } from "msw";
import type { Site, SiteEnvironment, SiteGrant, SiteJob, SiteRelease } from "@/api/portal/hooks/sites";
import { siteFileHandlers, resetSiteFiles } from "./siteFiles";

// Sites (#2559, #2604): the routes the portal's Sites section reads and writes,
// over module state so a write in one mock session is what the page reads back.
// `?state=` on the document's URL selects a state of the list for a screenshot.

const at = "2026-11-08T13:52:00Z";
const DEFAULT_KEEP = 50;
/** An environment; keep is its own release limit, undefined for the deployment's default. */
const env = (name: string, release: number, requires_approval = false, isPublic = false, internal_view = false, keep?: number, kept?: number): SiteEnvironment => ({
  name, release, requires_approval, public: isPublic, internal_view, updated_by: "jane.doe@example.com", updated_at: at,
  keep_releases: keep ?? DEFAULT_KEEP, keep_releases_default: keep === undefined, releases_kept: kept ?? Math.min(release, keep || DEFAULT_KEEP),
});
const PAGES = "pages.example.net";
const PERSONAS = ["admin", "analyst", "finance-team", "management-team", "web-team"];

/** Every environment's pages address, the main one at the bare name; none for a two-character name, whose <site>--<env> DNS reserves (#2611). */
function pagesAddressesOf(s: Pick<Site, "name" | "main_environment" | "environments">, pages: string): Record<string, string> {
  const at = (env: string) => (!pages ? "" : env === s.main_environment ? `https://${s.name}.${pages}` : s.name.length === 2 ? "" : `https://${s.name}--${env}.${pages}`);
  return Object.fromEntries(s.environments.map((e) => [e.name, at(e.name)]));
}

/** The addresses the server answers with (#2607): each environment's first domain, else its pages address. */
function withAddresses(s: Site, pages = PAGES): Site {
  const own = pagesAddressesOf(s, pages);
  const domains = [...s.domains].sort((a, b) => a.hostname.localeCompare(b.hostname));
  const addresses = Object.fromEntries(
    s.environments.map((e) => {
      const d = domains.find((x) => x.environment === e.name);
      return [e.name, d ? `https://${d.hostname}` : own[e.name]!];
    }),
  );
  const note = pages && s.name.length === 2 ? addressNote(s.name, pages) : undefined;
  return { ...s, address: pages && s.main_environment ? `https://${s.name}.${pages}` : "", addresses, pages_addresses: own, ...(note ? { address_note: note } : {}) };
}

/** What the server tells a two-character site (#2611), word for word. */
function addressNote(name: string, pages: string): string {
  return (
    `A two-character site name puts "--" in the 3rd and 4th characters of an address like ${name}--prod.${pages}. ` +
    "DNS reserves that form, so certificate authorities refuse it. The main address still works; give each other environment people use a domain."
  );
}
const domain = (hostname: string, site: string, environment: string) => ({
  hostname, site, environment, created_by: "jane.doe@example.com", created_at: at,
});

function makeSites(): Site[] {
  const site = (name: string, description: string, owner: string, environments: Site["environments"], domains: Site["domains"], permissions: Site["permissions"], main = "dev"): Site =>
    withAddresses({
      id: `site_${name}`, name, description, owner_email: owner, main_environment: main, environments, domains, permissions,
      created_by: owner, created_at: "2026-09-14T18:20:00Z", updated_at: at, address: "", addresses: {}, pages_addresses: {},
    });
  const admin = { manage: true, own: true, delete: true };
  return [
    site("annual-report", "2026 annual report", "priya.nair@example.com", [env("dev", 2)], [], admin),
    // The caller only views docs, so the server sends only the environments that open for them.
    site("docs", "Product documentation", "sam.lee@example.com", [env("prod", 30, true)], [domain("docs.example.com", "docs", "prod")], { manage: false, own: false, delete: false }, ""),
    site(
      "marketing", "Company website", "jane.doe@example.com",
      // dev keeps its last 3, stage the deployment's default, prod every release it served (#2602).
      [env("dev", 12, false, false, false, 3, 3), env("stage", 11, false, false, true, undefined, 1), env("prod", 9, true, true, false, 0, 2)],
      [domain("go.example.com", "marketing", "stage"), domain("example.com", "marketing", "prod"), domain("www.example.com", "marketing", "prod")],
      admin,
    ),
  ].map((st) => ({ ...st, media: st.name === "marketing" ? [media("/media/", "sites/marketing/media"), media("/downloads/", "sites/marketing/downloads")] : [] }));
}

// crowdedSite is the row that ran the Sites list off its card (#2612): a long
// name and description, a long owner address, an address of about 60
// characters, four environments and two domains on each. `?state=crowded`
// adds it to the list.
function crowdedSite(): Site {
  const name = "marketing-launch-fall-2026-regional-pages";
  const envs = [env("dev", 7), env("stage", 6), env("preview", 0), env("prod", 5, true, true)];
  return withAddresses({
    id: `site_${name}`, name, owner_email: "priya.ramanathan-oconnell@example.com", main_environment: "dev",
    description:
      "Landing pages for the fall launch: the pricing table, the signup form, the regional variants the field team asked for in September, the partner pages, the press kit and the event schedule, kept apart from the company website until the launch date so the regional teams can review every page before it goes out.",
    environments: envs,
    domains: envs.flatMap((e) => [domain(`${e.name}.fall-launch.example.com`, name, e.name), domain(`${e.name}.launch.example.org`, name, e.name)]),
    permissions: { manage: true, own: true, delete: true },
    created_by: "priya.ramanathan-oconnell@example.com", created_at: at, updated_at: at, address: "", addresses: {}, pages_addresses: {}, media: [],
  });
}

// twoCharSite is a site named before two-character names were refused (#2611):
// dev holds the main address, staging has no address and prod answers at its
// domain. It is not in the list; /portal/sites/ab opens it.
function twoCharSite(): Site {
  const owner = "mina.kowalski@example.com";
  return withAddresses({
    id: "site_ab", name: "ab", description: "Annual benefits guide", owner_email: owner, main_environment: "dev",
    environments: [env("dev", 15), env("staging", 14), env("prod", 13, false, true)],
    domains: [domain("ab.example.com", "ab", "prod")], permissions: { manage: true, own: true, delete: true },
    created_by: owner, created_at: at, updated_at: at, address: "", addresses: {}, pages_addresses: {}, media: [],
  });
}

function media(prefix: string, folder: string) {
  return { prefix, scope: "global" as const, scope_id: "", folder, created_by: "jane.doe@example.com", created_at: at };
}

/** A media address as the server stores it, or why it cannot be one. */
function mediaPrefix(raw: string): string | { detail: string } {
  const p = `/${raw.trim().replace(/^\/+|\/+$/g, "")}/`;
  if (p === "//" || p.startsWith("/_") || p.startsWith("/.well-known/")) return { detail: `media prefix ${raw}: "/", a path under /_ and /.well-known/ are the platform's` };
  return p;
}

let sites = makeSites();
const grant = (g: Partial<SiteGrant> & Pick<SiteGrant, "level">): SiteGrant => ({ email: "", persona: "", environments: [], granted_by: "jane.doe@example.com", created_at: at, ...g });
const makeGrants = (): Record<string, SiteGrant[]> => ({
  marketing: [
    grant({ email: "david.park@example.com", level: "manage" }),
    grant({ email: "pat.rivera@client.example", level: "view", environments: ["stage"], granted_by: "david.park@example.com" }),
    grant({ persona: "web-team", level: "view", environments: ["dev"] }),
  ],
});
let grants = makeGrants();
const jobs = new Map<string, SiteJob>();

const release = (number: number, note: string, served_in: string[], kept_by: string[], base = number - 1, source = `patch index.html`): SiteRelease => ({
  id: `srel_${number}`, number, source, note, file_count: 418, total_bytes: 61_200_000, base,
  redirects: [{ from: "/old", to: "/new", status: 301 }], created_by: "jane.doe@example.com", created_at: at, served_in, kept_by,
});
const removed = (number: number, note: string): SiteRelease => ({ ...release(number, note, [], []), pruned: true, pruned_at: "2026-11-10T03:00:00Z" });
const releases: SiteRelease[] = [
  release(12, "New pricing page", ["dev"], ["dev"], 11, "edit pricing/index.html"),
  release(11, "Footer links, new logo", ["stage"], ["dev", "stage"]),
  release(10, "Header spacing", [], ["dev"]),
  release(9, "Fall homepage", ["prod"], ["prod", "application shop"]),
  removed(8, "Try a new header"),
  removed(7, "Fix a typo"),
  release(5, "Launch", [], ["prod"], 0, "site-5.zip"),
];

const page = () => new URLSearchParams(window.location.search).get("state");
const find = (name: string) => sites.find((s) => s.name === name);
const missing = () => HttpResponse.json({ detail: "no such site" }, { status: 404 });
const LIMITS = { max_release_files: 20000, max_file_bytes: 67108864, max_release_bytes: 2147483648, crawl_max_pages: 2000, crawl_max_bytes: 536870912 };

export function resetSitesMock() {
  sites = makeSites();
  grants = makeGrants();
  jobs.clear();
  resetSiteFiles();
}

const REFUSED =
  "nothing was published: 2 files can't be served: .env and .git/HEAD have a segment starting with a dot. A release is public, so only .well-known may. Leave them out of the archive or the prefix.";

/**
 * publishJob starts a publish (#2593): running, in its second phase. In mock
 * mode a note naming "done", "refuse" or "stop" ends it that way at once, so
 * each ending can be looked at; a test ends one with endSiteJob.
 */
function publishJob(source: string, note: string): SiteJob {
  const j: SiteJob = {
    id: `sjob_${jobs.size + 1}`, kind: "publish", status: "running", phase: "Writing files", environment: "dev", source,
    done: 3020, total: 5214, pages: 0, bytes: 1_181_116_006, skipped_count: 0, redirects: 0, skipped: [], release: 0, created_by: "admin@example.com", created_at: at,
  };
  jobs.set(j.id, j);
  if (/\bdone\b/i.test(note)) endSiteJob(j.id, "done");
  else if (/\brefuse\b/i.test(note)) endSiteJob(j.id, "failed", REFUSED);
  else if (/\bstop\b/i.test(note)) endSiteJob(j.id, "stopped");
  return j;
}

/** endSiteJob ends a mocked job: published as release 13 with 6,118 pages, refused with error, or stopped. */
export function endSiteJob(id: string, status: "done" | "failed" | "stopped", error?: string) {
  const j = jobs.get(id);
  if (!j) return;
  Object.assign(j, { status, phase: "", ...(status === "done" ? { release: 13, done: j.total, pages: 6118 } : {}), ...(error ? { error } : {}) });
}

/** advanceSiteJob moves a running mocked job to another phase. */
export function advanceSiteJob(id: string, phase: string, done: number) {
  const j = jobs.get(id);
  if (j) Object.assign(j, { phase, done });
}

function job(kind: SiteJob["kind"], environment: string): SiteJob {
  const j: SiteJob = {
    id: `sjob_${jobs.size + 1}`, kind, status: "done", phase: "", environment, source: "site.zip", done: 418, total: 418, pages: kind === "crawl" ? 42 : 0, bytes: 1048576, skipped_count: kind === "crawl" ? 1 : 0,
    redirects: 3, skipped: kind === "crawl" ? [{ address: "/members/login", reason: "answered 403" }] : [], release: 13, created_by: "admin@example.com", created_at: at,
  };
  jobs.set(j.id, j);
  return j;
}

const base = "/api/v1/portal/sites";

export const siteHandlers = [
  http.get("/api/v1/portal/application-addresses/:name", () => HttpResponse.json({ page_origin: "https://shop.pages.example.net", domains: ["shop.example.com"] })),
  http.get(base, () => {
    const state = page();
    const list = state === "empty" ? [] : state === "crowded" ? [...sites, crowdedSite()] : sites;
    const off = state === "previews-off";
    return HttpResponse.json({
      sites: list.map((s) => withAddresses(s, off ? "" : PAGES)), total: list.length, pages_domain: off ? "" : PAGES, limits: LIMITS, can_create: true, admin: true,
    });
  }),
  http.post(base, async ({ request }) => {
    const b = (await request.json()) as { name: string; description: string };
    if (b.name.length === 2) {
      return HttpResponse.json(
        {
          detail: `address ${b.name}--dev has "--" in its 3rd and 4th characters, the form DNS reserves for an encoded international domain name (RFC 5891), so certificate authorities refuse it: a site name cannot be two characters`,
        },
        { status: 422 },
      );
    }
    const s: Site = withAddresses({
      id: `site_${b.name}`, name: b.name, description: b.description, owner_email: "admin@example.com", main_environment: "dev", environments: [env("dev", 0)], domains: [],
      permissions: { manage: true, own: true, delete: true }, created_by: "admin@example.com", created_at: at, updated_at: at, address: "", addresses: {}, pages_addresses: {},
    });
    sites = [...sites, s];
    return HttpResponse.json(s, { status: 201 });
  }),
  http.get(`${base}/:site`, ({ params }) => {
    const s = find(params.site as string) ?? (params.site === "ab" ? twoCharSite() : undefined);
    return s ? HttpResponse.json(withAddresses(s)) : missing();
  }),
  http.patch(`${base}/:site`, async ({ params, request }) => {
    const s = find(params.site as string);
    if (!s) return missing();
    const b = (await request.json()) as Partial<Site> & { media?: { prefix: string; scope: string; scope_id: string; folder: string }[] };
    if (b.media) {
      if (!s.permissions.manage) return HttpResponse.json({ detail: "you do not have permission to do this on this site" }, { status: 403 });
      const next = [];
      for (const m of b.media) {
        const prefix = mediaPrefix(m.prefix);
        if (typeof prefix !== "string") return HttpResponse.json(prefix, { status: 422 });
        if (m.folder.startsWith("reports/")) return HttpResponse.json({ detail: `there is no folder ${m.folder} in that library that you can read` }, { status: 422 });
        const kept = s.media?.find((x) => x.prefix === prefix && x.folder === m.folder);
        next.push(kept ?? { ...media(prefix, m.folder), scope: m.scope as "global", scope_id: m.scope_id, created_by: "admin@example.com" });
      }
      s.media = next;
      delete b.media;
    }
    Object.assign(s, b);
    return HttpResponse.json(s);
  }),
  http.delete(`${base}/:site`, ({ params }) => {
    sites = sites.filter((s) => s.name !== params.site);
    return new HttpResponse(null, { status: 204 });
  }),
  http.get(`${base}/:site/releases`, () => HttpResponse.json({ releases })),
  http.get(`${base}/:site/releases/:n`, ({ params }) => {
    const r = releases.find((x) => x.number === Number(params.n));
    if (!r) return HttpResponse.json({ detail: "no such release" }, { status: 404 });
    if (r.pruned) return HttpResponse.json({ ...r, files: [] });
    const files = [
      { path: "index.html", content_type: "text/html", size: 38000, gzip_size: 9000, brotli_size: 7000 },
      { path: "404.html", content_type: "text/html", size: 6000 },
    ];
    return HttpResponse.json({ ...r, files });
  }),
  http.get(`${base}/:site/compare`, ({ request }) => {
    const q = new URL(request.url).searchParams;
    const from = Number(q.get("from") ?? 0);
    const to = Number(q.get("to") ?? 0);
    if (releases.find((r) => r.number === from || r.number === to)?.pruned) {
      return HttpResponse.json({ detail: "release 8 was removed by the release limit (dev keeps the last 3, prod keeps every release, keep_releases)" }, { status: 422 });
    }
    return HttpResponse.json({ from, to, changed: ["pricing/index.html", "css/site.css"], added: ["images/logo-2026.svg"], removed: ["images/logo.svg"], redirects_added: [{ from: "/a", to: "/b", status: 301 }], redirects_removed: [] });
  }),
  http.get(`${base}/:site/deployments`, ({ request }) => {
    const all = [
      { environment: "dev", action: "publish", from: 11, to: 12, actor: "jane.doe@example.com", created_at: at },
      { environment: "dev", action: "publish", from: 10, to: 11, actor: "jane.doe@example.com", created_at: at },
      { environment: "dev", action: "publish", from: 8, to: 10, actor: "jane.doe@example.com", created_at: at },
      { environment: "dev", action: "publish", from: 7, to: 8, actor: "jane.doe@example.com", created_at: at },
      { environment: "stage", action: "promote", from: 0, to: 11, actor: "jane.doe@example.com", created_at: at },
      { environment: "prod", action: "promote", from: 5, to: 9, actor: "sarah.chen@example.com", created_at: at },
      { environment: "prod", action: "promote", from: 0, to: 5, actor: "sarah.chen@example.com", created_at: at },
    ];
    const environment = new URL(request.url).searchParams.get("environment");
    return HttpResponse.json({ deployments: environment ? all.filter((d) => d.environment === environment) : all });
  }),
  http.post(`${base}/:site/publish`, async ({ request }) => {
    const b = (await request.json()) as { note?: string; archive?: string; folder?: string; source?: string; from_bucket?: { connection: string; bucket: string; prefix?: string } };
    if ([b.archive, b.folder, b.from_bucket].filter(Boolean).length !== 1) {
      return HttpResponse.json({ detail: "publish names one source: an archive, a folder or from_bucket" }, { status: 422 });
    }
    const from = b.from_bucket;
    if (from?.connection === "locked-s3") {
      return HttpResponse.json(
        { detail: `nothing was published: ${from.connection} refused to list ${from.bucket}/${from.prefix ?? ""}: access denied. The connection's credentials can't read that prefix.` },
        { status: 422 },
      );
    }
    const label = b.source ?? (from ? `s3://${from.bucket}/${from.prefix ?? ""} on connection ${from.connection}` : (b.archive ?? b.folder ?? ""));
    return HttpResponse.json(publishJob(label, b.note ?? ""), { status: 202 });
  }),
  http.post(`${base}/:site/copy`, () => HttpResponse.json(job("crawl", "dev"), { status: 202 })),
  http.get(`${base}/:site/forms`, () => HttpResponse.json({ forms: ["contact-us", "newsletter"] })),
  http.get(`${base}/:site/jobs`, () => HttpResponse.json({ jobs: [...jobs.values()].filter((j) => j.status === "running") })),
  http.get(`${base}/:site/jobs/:id`, ({ params }) => HttpResponse.json(jobs.get(params.id as string))),
  http.post(`${base}/:site/jobs/:id/stop`, ({ params }) => {
    endSiteJob(params.id as string, "stopped");
    return new HttpResponse(null, { status: 204 });
  }),
  http.post(`${base}/:site/promote`, async ({ request }) => {
    const b = (await request.json()) as { to: string };
    return HttpResponse.json({ environment: b.to, release: 11, status: "promoted" });
  }),
  http.post(`${base}/:site/rollback`, async ({ request }) => {
    const b = (await request.json()) as { environment: string; release: number };
    return HttpResponse.json({ environment: b.environment, release: b.release, status: "rollback" });
  }),
  http.post(`${base}/:site/domains`, async ({ params, request }) => {
    const b = (await request.json()) as { hostname: string; environment: string; requires_approval?: boolean; public?: boolean };
    const s = find(params.site as string);
    if (!s) return missing();
    if (b.environment === "dev") return HttpResponse.json({ detail: "dev is the working copy and can't have a domain" }, { status: 422 });
    if (s.domains.some((d) => d.hostname === b.hostname)) return HttpResponse.json({ detail: "that domain is already in use" }, { status: 409 });
    if (!s.environments.some((e) => e.name === b.environment)) {
      s.environments.push(env(b.environment, 0, !!b.requires_approval, !!b.public));
      Object.assign(s, withAddresses(s));
    }
    const d = domain(b.hostname, s.name, b.environment);
    s.domains.push(d);
    return HttpResponse.json(d, { status: 201 });
  }),
  http.delete(`${base}/:site/domains/:hostname`, ({ params }) => {
    const s = find(params.site as string);
    if (s) s.domains = s.domains.filter((d) => d.hostname !== params.hostname);
    return new HttpResponse(null, { status: 204 });
  }),
  http.get(`${base}/:site/access`, ({ params }) => {
    const s = find(params.site as string);
    if (!s) return missing();
    return HttpResponse.json({ owner_email: s.owner_email, grants: grants[s.name] ?? [], personas: PERSONAS });
  }),
  http.put(`${base}/:site/access`, async ({ params, request }) => {
    const b = (await request.json()) as { email?: string; persona?: string; level: SiteGrant["level"]; environments?: string[] };
    const key = params.site as string;
    const g = grant({ email: b.email ?? "", persona: b.persona ?? "", level: b.level, environments: b.environments ?? [], granted_by: "admin@example.com" });
    grants[key] = [...(grants[key] ?? []).filter((x) => (g.persona ? x.persona !== g.persona : x.email !== g.email)), g];
    return HttpResponse.json(g);
  }),
  http.delete(`${base}/:site/access`, ({ params, request }) => {
    const q = new URL(request.url).searchParams;
    const email = q.get("email");
    const persona = q.get("persona");
    grants[params.site as string] = (grants[params.site as string] ?? []).filter((g) => (persona ? g.persona !== persona : g.email !== email));
    return new HttpResponse(null, { status: 204 });
  }),
  http.put(`${base}/:site/environments/:name`, async ({ params, request }) => {
    const s = find(params.site as string);
    if (!s) return missing();
    const b = (await request.json()) as { requires_approval?: boolean; public?: boolean; internal_view?: boolean; keep_releases?: number | "default" };
    const name = params.name as string;
    let e = s.environments.find((x) => x.name === name);
    if (!e) {
      e = env(name, 0);
      s.environments.push(e);
    }
    if (b.public && name === "dev") return HttpResponse.json({ detail: "dev is the working copy, so it is always private" }, { status: 422 });
    if (b.keep_releases !== undefined && !s.permissions.own) return HttpResponse.json({ detail: "you do not have permission to do this on this site" }, { status: 403 });
    if (b.public && e.requires_approval) {
      return HttpResponse.json({ site: s, status: "awaiting_approval", approval: { id: "apr_public1" } });
    }
    Object.assign(e, {
      ...(b.requires_approval !== undefined ? { requires_approval: b.requires_approval } : {}),
      ...(b.public !== undefined ? { public: b.public } : {}),
      ...(b.internal_view !== undefined ? { internal_view: b.internal_view } : {}),
    });
    if (b.keep_releases !== undefined) {
      const keep = b.keep_releases === "default" ? DEFAULT_KEEP : b.keep_releases;
      Object.assign(e, { keep_releases: keep, keep_releases_default: b.keep_releases === "default", releases_kept: keep === 0 ? e.releases_kept : Math.min(e.releases_kept, keep) });
    }
    Object.assign(s, withAddresses(s));
    return HttpResponse.json({ site: s, status: "done" });
  }),
  http.put(`${base}/:site/main-environment`, async ({ params, request }) => {
    const s = find(params.site as string);
    if (!s) return missing();
    const b = (await request.json()) as { environment: string };
    s.main_environment = b.environment;
    Object.assign(s, withAddresses(s));
    return HttpResponse.json(s);
  }),
  http.get("/api/v1/admin/sites/domains", () =>
    HttpResponse.json({
      addresses: [
        { hostname: "example.com", kind: "domain", site: "marketing", environment: "prod", created_by: "jane.doe@example.com", created_at: at },
        { hostname: "marketing.pages.example.net", kind: "site_address", site: "marketing", environment: "dev", created_by: "jane.doe@example.com", created_at: at },
        { hostname: "shop.example.com", kind: "application", application: "shop", created_by: "sarah.chen@example.com", created_at: at },
      ],
    }),
  ),
  http.post("/api/v1/admin/sites/domains", () => HttpResponse.json({}, { status: 201 })),
  http.delete("/api/v1/admin/sites/domains/:hostname", () => new HttpResponse(null, { status: 204 })),
  http.get("/api/v1/admin/settings/sites", () =>
    HttpResponse.json({ pages_domain: "pages.example.net", ...LIMITS, releases_keep: retention.keep_default, releases_mode: retention.mode, updated_by: "sarah.chen@example.com", updated_at: at }),
  ),
  http.put("/api/v1/admin/settings/sites", async ({ request }) => {
    const b = (await request.json()) as { pages_domain: string; releases_keep?: number; releases_mode?: "delete" | "report" };
    if (b.pages_domain.includes("portal.")) {
      return HttpResponse.json({ detail: "sites.pages_domain holds the portal's host, so a page could be served where the portal is" }, { status: 422 });
    }
    if (b.releases_keep !== undefined && b.releases_keep < 0) {
      return HttpResponse.json({ detail: "sites.releases.keep_default: 0 (keep every release) to 100000, not -1" }, { status: 422 });
    }
    if (b.releases_keep !== undefined) retention.keep_default = b.releases_keep;
    if (b.releases_mode) retention.mode = b.releases_mode;
    return HttpResponse.json({ ...b, updated_by: "admin@example.com", updated_at: at });
  }),
  http.get("/api/v1/admin/sites/retention", () => {
    const sweep = { ...SWEEP, mode: retention.mode };
    return HttpResponse.json({ keep_default: retention.keep_default, mode: retention.mode, orphan_grace: "24h0m0s", every: "24h0m0s", sweeps: page() === "no-cleanup" ? [] : [sweep] });
  }),
  ...siteFileHandlers,
  // A file a site serves from a mounted media folder goes only with force (#2591).
  http.delete("/api/v1/resources/:id", ({ params, request }) => {
    const served = String(params.id).startsWith("res-media-");
    if (served && new URL(request.url).searchParams.get("force") !== "true") {
      return HttpResponse.json(
        { detail: "1 site(s) serve this file from a mounted media folder; delete it with force=true to take it off their pages" },
        { status: 409 },
      );
    }
    return HttpResponse.json({ status: "deleted" });
  }),
];

const retention: { keep_default: number; mode: "delete" | "report" } = { keep_default: DEFAULT_KEEP, mode: "delete" };
const SWEEP = {
  mode: "delete", sites_swept: 23, releases: 1284, rewritten: 4, files: 312, bytes: 3_435_973_837, orphans: 6, orphan_bytes: 1_200_000,
  skipped: 0, refused: 0, failed: 0, started_at: "2026-11-11T03:00:00Z", finished_at: "2026-11-11T03:00:41Z",
};

import { http, HttpResponse } from "msw";
import type { Site, SiteGrant, SiteJob, SiteRelease } from "@/api/portal/hooks/sites";

// Sites (#2559): the routes the portal's Sites section reads and writes, over
// module state so a write in one mock session is what the page reads back.
// `?state=` on the document's URL selects a state of the list for a screenshot.

const at = "2026-11-08T13:52:00Z";
const env = (name: string, release: number, requires_approval = false) => ({
  name, release, requires_approval, updated_by: "jane.doe@example.com", updated_at: at,
});
const domain = (hostname: string, site: string, environment: string) => ({
  hostname, site, environment, created_by: "jane.doe@example.com", created_at: at,
});

function makeSites(): Site[] {
  const site = (name: string, description: string, owner: string, environments: Site["environments"], domains: Site["domains"], permissions: Site["permissions"]): Site => ({
    id: `site_${name}`, name, description, owner_email: owner, internal_view: false, environments, domains, permissions,
    created_by: owner, created_at: "2026-09-14T18:20:00Z", updated_at: at, address: `https://${name}.pages.example.net`,
  });
  const admin = { manage: true, own: true, delete: true };
  return [
    site("annual-report", "2026 annual report", "priya.nair@example.com", [env("default", 2)], [], admin),
    site("docs", "Product documentation", "sam.lee@example.com", [env("default", 31), env("prod", 30, true)], [domain("docs.example.com", "docs", "prod")], { manage: false, own: false, delete: false }),
    site(
      "marketing", "Company website", "jane.doe@example.com",
      [env("default", 12), env("dev", 12), env("stage", 11), env("prod", 9, true)],
      [domain("dev.example.com", "marketing", "dev"), domain("go.example.com", "marketing", "stage"), domain("example.com", "marketing", "prod"), domain("www.example.com", "marketing", "prod")],
      admin,
    ),
  ];
}

let sites = makeSites();
const grants: Record<string, SiteGrant[]> = {
  marketing: [
    { email: "david.park@example.com", level: "manage", granted_by: "jane.doe@example.com", created_at: at },
    { email: "pat.rivera@client.example", level: "view", granted_by: "david.park@example.com", created_at: at },
  ],
};
const jobs = new Map<string, SiteJob>();

const release = (number: number, note: string, served_in: string[]): SiteRelease => ({
  id: `srel_${number}`, number, source: `site-${number}.zip`, note, file_count: 418, total_bytes: 61_200_000,
  redirects: [{ from: "/old", to: "/new", status: 301 }], created_by: "jane.doe@example.com", created_at: at, served_in,
});
const releases = [release(12, "New pricing page", ["default", "dev"]), release(11, "Footer links, new logo", ["stage"]), release(9, "Fall homepage", ["prod"])];

const page = () => new URLSearchParams(window.location.search).get("state");
const find = (name: string) => sites.find((s) => s.name === name);
const missing = () => HttpResponse.json({ detail: "no such site" }, { status: 404 });
const LIMITS = { max_release_files: 20000, max_file_bytes: 67108864, max_release_bytes: 2147483648, crawl_max_pages: 2000, crawl_max_bytes: 536870912 };

export function resetSitesMock() {
  sites = makeSites();
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
    const list = state === "empty" ? [] : sites;
    const off = state === "previews-off";
    return HttpResponse.json({
      sites: off ? list.map((s) => ({ ...s, address: "" })) : list, total: list.length, pages_domain: off ? "" : "pages.example.net", limits: LIMITS, can_create: true, admin: true,
    });
  }),
  http.post(base, async ({ request }) => {
    const b = (await request.json()) as { name: string; description: string };
    const s: Site = {
      id: `site_${b.name}`, name: b.name, description: b.description, owner_email: "admin@example.com", internal_view: false, environments: [env("default", 0)], domains: [],
      permissions: { manage: true, own: true, delete: true }, created_by: "admin@example.com", created_at: at, updated_at: at, address: `https://${b.name}.pages.example.net`,
    };
    sites = [...sites, s];
    return HttpResponse.json(s, { status: 201 });
  }),
  http.get(`${base}/:site`, ({ params }) => {
    const s = find(params.site as string);
    return s ? HttpResponse.json(s) : missing();
  }),
  http.patch(`${base}/:site`, async ({ params, request }) => {
    const s = find(params.site as string);
    if (!s) return missing();
    Object.assign(s, await request.json());
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
    return HttpResponse.json({
      ...r,
      files: [
        { path: "index.html", content_type: "text/html", size: 38000, gzip_size: 9000, brotli_size: 7000 },
        { path: "404.html", content_type: "text/html", size: 6000 },
      ],
    });
  }),
  http.get(`${base}/:site/compare`, () =>
    HttpResponse.json({ from: 9, to: 11, changed: ["index.html", "css/site.css"], added: ["images/logo-2026.svg"], removed: ["images/logo.svg"], redirects_added: [{ from: "/a", to: "/b", status: 301 }], redirects_removed: [] }),
  ),
  http.get(`${base}/:site/deployments`, () =>
    HttpResponse.json({
      deployments: [
        { environment: "default", action: "publish", from: 11, to: 12, actor: "jane.doe@example.com", created_at: at },
        { environment: "prod", action: "promote", from: 8, to: 9, actor: "sarah.chen@example.com", created_at: at },
        { environment: "prod", action: "rollback", from: 9, to: 8, actor: "sarah.chen@example.com", created_at: at },
      ],
    }),
  ),
  http.post(`${base}/:site/publish`, async ({ request }) => {
    const b = (await request.json()) as { environment: string };
    return HttpResponse.json(job("publish", b.environment), { status: 202 });
  }),
  http.post(`${base}/:site/copy`, async ({ request }) => {
    const b = (await request.json()) as { environment: string };
    return HttpResponse.json(job("crawl", b.environment), { status: 202 });
  }),
  http.get(`${base}/:site/forms`, () => HttpResponse.json({ forms: ["contact-us", "newsletter"] })),
  http.get(`${base}/:site/jobs`, () => HttpResponse.json({ jobs: [] })),
  http.get(`${base}/:site/jobs/:id`, ({ params }) => HttpResponse.json(jobs.get(params.id as string))),
  http.post(`${base}/:site/jobs/:id/stop`, () => new HttpResponse(null, { status: 204 })),
  http.post(`${base}/:site/promote`, async ({ request }) => {
    const b = (await request.json()) as { to: string };
    return HttpResponse.json({ environment: b.to, release: 11, status: "promoted" });
  }),
  http.post(`${base}/:site/rollback`, async ({ request }) => {
    const b = (await request.json()) as { environment: string; release: number };
    return HttpResponse.json({ environment: b.environment, release: b.release, status: "rollback" });
  }),
  http.post(`${base}/:site/domains`, async ({ params, request }) => {
    const b = (await request.json()) as { hostname: string; environment: string };
    const s = find(params.site as string);
    if (!s) return missing();
    if (s.domains.some((d) => d.hostname === b.hostname)) return HttpResponse.json({ detail: "that domain is already in use" }, { status: 409 });
    if (!s.environments.some((e) => e.name === b.environment)) s.environments.push(env(b.environment, 0));
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
    return HttpResponse.json({ owner_email: s.owner_email, internal_view: s.internal_view, grants: grants[s.name] ?? [] });
  }),
  http.put(`${base}/:site/access`, async ({ params, request }) => {
    const b = (await request.json()) as { email: string; level: SiteGrant["level"] };
    const list = (grants[params.site as string] ??= []);
    const g: SiteGrant = { email: b.email, level: b.level, granted_by: "admin@example.com", created_at: at };
    list.push(g);
    return HttpResponse.json(g);
  }),
  http.delete(`${base}/:site/access`, ({ params, request }) => {
    const email = new URL(request.url).searchParams.get("email");
    grants[params.site as string] = (grants[params.site as string] ?? []).filter((g) => g.email !== email);
    return new HttpResponse(null, { status: 204 });
  }),
  http.get("/api/v1/admin/sites/domains", () =>
    HttpResponse.json({
      addresses: [
        { hostname: "example.com", kind: "domain", site: "marketing", environment: "prod", created_by: "jane.doe@example.com", created_at: at },
        { hostname: "marketing.pages.example.net", kind: "site_address", site: "marketing", environment: "default", created_by: "jane.doe@example.com", created_at: at },
        { hostname: "shop.example.com", kind: "application", application: "shop", created_by: "sarah.chen@example.com", created_at: at },
      ],
    }),
  ),
  http.post("/api/v1/admin/sites/domains", () => HttpResponse.json({}, { status: 201 })),
  http.delete("/api/v1/admin/sites/domains/:hostname", () => new HttpResponse(null, { status: 204 })),
  http.get("/api/v1/admin/settings/sites", () => HttpResponse.json({ pages_domain: "pages.example.net", ...LIMITS, updated_by: "sarah.chen@example.com", updated_at: at })),
  http.put("/api/v1/admin/settings/sites", async ({ request }) => {
    const b = (await request.json()) as { pages_domain: string };
    if (b.pages_domain.includes("portal.")) {
      return HttpResponse.json({ detail: "sites.pages_domain holds the portal's host, so a page could be served where the portal is" }, { status: 422 });
    }
    return HttpResponse.json({ ...b, updated_by: "admin@example.com", updated_at: at });
  }),
];

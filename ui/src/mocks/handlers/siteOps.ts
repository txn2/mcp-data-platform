import { http, HttpResponse } from "msw";
import type { Site } from "@/api/portal/hooks/sites";

// The mocks of the newer Sites routes (#2636, #2647, #2629, #2630): the name
// check, a site's traffic and each replica's, a site file's bytes, and
// removing an environment. They read the sites the Sites mock holds.

const base = "/api/v1/portal/sites";

/** What a typed name becomes, as the server makes it (#2636). */
export function nameFrom(input: string): string {
  return input
    .toLowerCase()
    .replace(/[\s_./:+\-–—]+/g, "-")
    .replace(/[^a-z0-9-]/g, "")
    .replace(/-+/g, "-")
    .replace(/^-|-$/g, "");
}

function refusalOf(name: string, taken: (n: string) => boolean): string {
  if (!name) return "Type a name with at least one letter or digit.";
  if (!/^[a-z]/.test(name)) return `Site name "${name}": start with a letter, then use letters, digits and single hyphens.`;
  if (name.length > 57) return `Site name "${name}" is ${name.length} characters: every address of a site adds "--" and an environment name to it, and one part of an address can be at most 63 characters, so a site name can be at most 57.`;
  if (name.length === 2) return `Site name "${name}": address ${name}--dev has "--" in its 3rd and 4th characters, the form DNS reserves for an encoded international domain name (RFC 5891), so certificate authorities refuse it: a site name cannot be two characters.`;
  if (taken(name)) return `A site named "${name}" exists. Choose another name.`;
  return "";
}

function totals(requests: number, p95: number) {
  return {
    requests,
    classes: { "2xx": Math.round(requests * 0.92), "3xx": Math.round(requests * 0.03), "4xx": Math.round(requests * 0.04), "5xx": requests - Math.round(requests * 0.92) - Math.round(requests * 0.03) - Math.round(requests * 0.04), other: 0 },
    p50_ms: Math.round(p95 / 3),
    p95_ms: p95,
    p99_ms: Math.round(p95 * 2.2),
  };
}

const PNG = Uint8Array.from(atob("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="), (c) => c.charCodeAt(0));

export function siteOpsHandlers(find: (name: string) => Site | undefined, names: () => string[]) {
  return [
    http.post(`${base}/check-name`, async ({ request }) => {
      const b = (await request.json()) as { name: string };
      const name = nameFrom(b.name);
      const refusal = refusalOf(name, (n) => names().includes(n));
      return HttpResponse.json({
        input: b.name,
        name,
        addresses: name ? { dev: `https://${name}--dev.pages.example.net`, prod: `https://${name}.pages.example.net` } : {},
        environment_name_room: Math.max(0, Math.min(32, 61 - name.length)),
        ...(refusal ? { refusal } : {}),
      });
    }),
    http.get(`${base}/:site/traffic`, ({ params, request }) => {
      const s = find(params.site as string);
      if (!s) return HttpResponse.json({ detail: "no such site" }, { status: 404 });
      const range = new URL(request.url).searchParams.get("range") ?? "24h";
      const steps = range === "1h" ? 60 : range === "24h" ? 96 : range === "7d" ? 168 : 120;
      const now = Date.now();
      const step = range === "1h" ? 60_000 : range === "24h" ? 900_000 : range === "7d" ? 3_600_000 : 21_600_000;
      const series = Array.from({ length: steps }, (_, i) => ({ at: new Date(now - (steps - i) * step).toISOString(), ...totals(40 + ((i * 37) % 60), 30 + ((i * 13) % 40)) }));
      const environments = Object.fromEntries(s.environments.filter((e) => e.name !== "dev").map((e, i) => [e.name, totals(1200 / (i + 1), 48 + i * 10)]));
      return HttpResponse.json({ range, step_seconds: step / 1000, recording: true, total: totals(2400, 52), series, environments });
    }),
    http.get("/api/v1/admin/sites/traffic/replicas", ({ request }) =>
      HttpResponse.json({
        range: new URL(request.url).searchParams.get("range") ?? "24h",
        recording: true,
        replicas: [
          { instance: "platform-7d9f-a:8080", ...totals(52000, 44) },
          { instance: "platform-7d9f-b:8080", ...totals(51000, 47) },
        ],
      }),
    ),
    http.get(`${base}/:site/file/content`, ({ request }) => {
      const path = new URL(request.url).searchParams.get("path") ?? "";
      if (/\.(png|webp)$/.test(path)) return new HttpResponse(PNG, { headers: { "Content-Type": "image/png" } });
      return new HttpResponse(`# ${path}\n`, { headers: { "Content-Type": "text/plain; charset=utf-8" } });
    }),
    http.delete(`${base}/:site/environments/:name`, ({ params }) => {
      const s = find(params.site as string);
      if (!s) return HttpResponse.json({ detail: "no such site" }, { status: 404 });
      const name = params.name as string;
      if (name === "dev" || name === "prod") return HttpResponse.json({ detail: `every site has dev and prod, so ${name} cannot be removed: only the environments between them can be` }, { status: 422 });
      s.environments = s.environments.filter((e) => e.name !== name);
      return HttpResponse.json(s);
    }),
  ];
}

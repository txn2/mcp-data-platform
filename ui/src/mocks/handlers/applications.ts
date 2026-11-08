import { http, HttpResponse } from "msw";
import type {
  Application,
  ApplicationDetail,
  ApplicationPartKind,
  ApplicationRelease,
} from "@/api/portal/hooks/applications";
import { shopDiagram } from "../data/applicationDiagram";
import {
  candidateOf,
  mockApplicationBundle,
  mockApplicationDetails,
  mockPartCatalog,
  partView,
  schemaViewOf,
} from "../data/applications";

// Applications (#2411): the portal routes the Applications pages read and
// write. Module state, so a write within one mock session is what the page
// reads back.
//   GET    /portal/applications, POST /portal/applications
//   GET    /portal/applications/:app, PATCH, DELETE
//   POST   /portal/applications/:app/parts, DELETE .../parts/:kind/:id
//   GET    /portal/applications/:app/candidates
//   POST   /portal/applications/:app/release, POST .../rollback
//   GET    /portal/applications/:app/export
//   GET    /portal/applications/:app/diagram
//   POST   /portal/applications/import
//   GET    /portal/application-databases

const BASE = "/api/v1/portal/applications";

const details: Record<string, ApplicationDetail> = JSON.parse(
  JSON.stringify(mockApplicationDetails),
) as Record<string, ApplicationDetail>;

function find(ref: string): ApplicationDetail | undefined {
  return details[ref] ?? Object.values(details).find((d) => d.application.name === ref);
}

function problem(status: number, detail: string) {
  return HttpResponse.json({ status, detail }, { status });
}

function notFound(ref: string) {
  return problem(404, `application "${ref}" not found`);
}

function now(): string {
  return new Date().toISOString();
}

function touch(d: ApplicationDetail): Application {
  d.application.updated_at = now();
  d.parts = d.application.parts.map(partView);
  return d.application;
}

function owningApp(kind: string, id: string): ApplicationDetail | undefined {
  return Object.values(details).find((d) =>
    d.application.parts.some((p) => p.kind === kind && p.id === id),
  );
}

// The application database connections the mock caller reaches.
const APPDB_CONNECTIONS = ["acme-shop", "inventory"];

function holderOf(connection: string, schema: string): ApplicationDetail | undefined {
  return Object.values(details).find(
    (d) => d.application.connection === connection && d.application.schema === schema,
  );
}

// holdSchema makes an application hold a schema (#2539): the schema part, the
// application's connection and schema, and the schema view the page reads.
function holdSchema(d: ApplicationDetail, connection: string, schema: string) {
  d.application.connection = connection;
  d.application.schema = schema;
  d.application.schema_adopted_version = 0;
  d.application.parts.push({ kind: "schema", id: `${connection}/${schema}`, added_by: "sarah.chen@example.com", added_at: now() });
  d.schema = schemaViewOf(connection, schema);
  d.migrations = { applied: 0, pending: [] };
}

// addSchema refuses what the server refuses, in its words, and otherwise
// makes the application hold the schema.
function addSchema(d: ApplicationDetail, id: string) {
  const at = id.indexOf("/");
  const named = at >= 0 ? id.slice(0, at) : "";
  const schema = at >= 0 ? id.slice(at + 1) : id;
  if (schema === "") return problem(422, 'part_id names the schema: "<connection>/<schema>", or the schema alone');
  const a = d.application;
  if (a.schema !== "") {
    return problem(
      409,
      `application "${a.name}" holds schema ${a.connection}/${a.schema} already, and an application holds one schema; remove it first with remove_part`,
    );
  }
  if (named === "") {
    return problem(
      422,
      `you reach several application database connections; name one as part_id "<connection>/${schema}": ${APPDB_CONNECTIONS.join(", ")}`,
    );
  }
  if (!APPDB_CONNECTIONS.includes(named)) {
    return problem(
      422,
      `connection "${named}" is not an application database connection (kind appdb) you reach; name one of: ${APPDB_CONNECTIONS.join(", ")}, as part_id "<connection>/${schema}"`,
    );
  }
  const other = holderOf(named, schema);
  if (other) return problem(409, `schema ${named}/${schema} belongs to application "${other.application.name}"`);
  holdSchema(d, named, schema);
  return HttpResponse.json(touch(d));
}

function releaseSchema(d: ApplicationDetail) {
  d.application.parts = d.application.parts.filter((p) => p.kind !== "schema");
  d.application.connection = "";
  d.application.schema = "";
  d.application.schema_adopted_version = 0;
  delete d.schema;
  delete d.migrations;
}

// blockingProblems is what refuses a release: every problem but the message
// of a finding that does not block (#2540), which is advice.
function blockingProblems(d: ApplicationDetail): string[] {
  const advice = new Set((d.findings ?? []).filter((f) => !f.blocks).map((f) => f.message));
  return d.problems.filter((p) => !advice.has(p));
}

// diagramPins reads a version or compare parameter as the server does: the
// pins it names, or null for a value that is not current, latest or a
// release's number.
function diagramPins(d: ApplicationDetail, version: string): Map<string, number> | null {
  const pins = (r: ApplicationRelease | null | undefined) =>
    new Map((r?.pins ?? []).map((p) => [`${p.kind}/${p.part_id}`, p.version]));
  if (version === "latest") {
    return new Map(d.parts.filter((p) => p.latest_version !== null).map((p) => [`${p.kind}/${p.id}`, p.latest_version!]));
  }
  if (version === "current") return d.current_release ? pins(d.current_release) : diagramPins(d, "latest");
  if (!/^[1-9]\d*$/.test(version)) return null;
  return pins(d.releases.find((r) => r.number === Number(version)));
}

function recordRelease(d: ApplicationDetail, rel: ApplicationRelease): ApplicationRelease {
  d.releases.unshift(rel);
  d.current_release = rel;
  d.application.current_release = rel.number;
  d.pending = [];
  touch(d);
  return rel;
}

export const applicationHandlers = [
  http.get("/api/v1/portal/application-databases", () => {
    const held = (connection: string, schema: string) => holderOf(connection, schema)?.application.name;
    const schemas = (connection: string, names: string[]) =>
      names.map((n) => ({ name: n, ...(held(connection, n) ? { held_by: held(connection, n) } : {}) }));
    return HttpResponse.json({
      connections: [
        { connection: "acme-shop", description: "The storefront database", schemas: schemas("acme-shop", ["public", "shop"]) },
        { connection: "inventory", schemas: schemas("inventory", ["public"]) },
      ],
    });
  }),

  http.get(BASE, () => {
    const applications = Object.values(details).map((d) => d.application);
    return HttpResponse.json({ applications, total: applications.length });
  }),

  http.post(BASE, async ({ request }) => {
    const body = (await request.json()) as {
      name?: string;
      description?: string;
      connection?: string;
      schema?: string;
    };
    const name = (body.name ?? "").trim();
    if (name === "") return problem(422, "an application needs a name");
    if (find(name)) return problem(409, `an application named "${name}" already exists`);
    const id = `app-${name}`;
    const application: Application = {
      id,
      name,
      description: body.description ?? "",
      owner_email: "sarah.chen@example.com",
      connection: "",
      schema: "",
      schema_adopted_version: 0,
      current_release: 0,
      created_by: "sarah.chen@example.com",
      created_at: now(),
      updated_at: now(),
      parts: [],
    };
    const d: ApplicationDetail = {
      application,
      parts: [],
      current_release: null,
      pending: [],
      problems: [],
      findings: [],
      releases: [],
    };
    if (body.connection && body.schema) holdSchema(d, body.connection, body.schema);
    details[id] = d;
    touch(d);
    return HttpResponse.json(application, { status: 201 });
  }),

  http.post(`${BASE}/import`, async ({ request }) => {
    const body = (await request.json()) as {
      bundle?: typeof mockApplicationBundle;
      map?: Record<string, string>;
    };
    const name = body.bundle?.application?.name ?? "";
    if (name === "") return problem(422, "the bundle names no application");
    const d = find(name);
    const created = !d;
    const target: ApplicationDetail = d ?? {
      application: {
        id: `app-${name}`,
        name,
        description: body.bundle?.application?.description ?? "",
        owner_email: "admin@example.com",
        connection: body.map?.[body.bundle?.names?.connections?.[0] ?? ""] ?? "",
        schema: "",
        schema_adopted_version: 0,
        current_release: 0,
        created_by: "admin@example.com",
        created_at: now(),
        updated_at: now(),
        parts: [],
      },
      parts: [],
      current_release: null,
      pending: [],
      problems: [],
      findings: [],
      releases: [],
    };
    if (created) details[target.application.id] = target;
    const rel = recordRelease(target, {
      number: (target.releases[0]?.number ?? 0) + 1,
      action: "import",
      migration_version: 0,
      changes: { parts: [], applied: [], reverted: [] },
      created_by: "admin@example.com",
      created_at: now(),
      pins: target.current_release?.pins ?? [],
    });
    return HttpResponse.json({
      application: name,
      created,
      release: rel,
      scripts: {},
      assets: {},
    });
  }),

  http.get(`${BASE}/:app`, ({ params }) => {
    const d = find(String(params.app));
    return d ? HttpResponse.json(d) : notFound(String(params.app));
  }),

  http.patch(`${BASE}/:app`, async ({ params, request }) => {
    const d = find(String(params.app));
    if (!d) return notFound(String(params.app));
    const body = (await request.json()) as { description?: string; owner_email?: string };
    d.application.description = body.description ?? d.application.description;
    if (body.owner_email) d.application.owner_email = body.owner_email;
    return HttpResponse.json(touch(d));
  }),

  http.delete(`${BASE}/:app`, ({ params }) => {
    const d = find(String(params.app));
    if (!d) return notFound(String(params.app));
    delete details[d.application.id];
    return HttpResponse.json({ deleted: String(params.app) });
  }),

  http.get(`${BASE}/:app/candidates`, ({ params, request }) => {
    const d = find(String(params.app));
    if (!d) return notFound(String(params.app));
    const q = (new URL(request.url).searchParams.get("q") ?? "").toLowerCase();
    const candidates = Object.values(mockPartCatalog)
      .filter((v) => (v.name ?? v.id).toLowerCase().includes(q))
      .filter((v) => !d.application.parts.some((p) => p.kind === v.kind && p.id === v.id))
      .map((v) => {
        const owner = owningApp(v.kind, v.id);
        const c = candidateOf(v);
        return owner ? { ...c, application: { id: owner.application.id, name: owner.application.name } } : c;
      });
    const counts: Partial<Record<ApplicationPartKind, number>> = {};
    for (const c of candidates) counts[c.kind] = (counts[c.kind] ?? 0) + 1;
    return HttpResponse.json({ candidates, counts });
  }),

  http.post(`${BASE}/:app/parts`, async ({ params, request }) => {
    const d = find(String(params.app));
    if (!d) return notFound(String(params.app));
    const body = (await request.json()) as { kind?: ApplicationPartKind; id?: string };
    const kind = body.kind;
    const id = (body.id ?? "").trim();
    if (!kind || id === "") return problem(422, "a part needs a kind and an id");
    if (kind === "schema") return addSchema(d, id);
    const owner = owningApp(kind, id);
    if (owner) return problem(409, `${kind} ${id} already belongs to application "${owner.application.name}"`);
    d.application.parts.push({ kind, id, added_by: "sarah.chen@example.com", added_at: now() });
    if (kind === "script" || kind === "asset") d.pending.push({ kind, part_id: id, from: 0, to: 1 });
    return HttpResponse.json(touch(d));
  }),

  http.delete(`${BASE}/:app/parts/:kind/:id`, ({ params }) => {
    const d = find(String(params.app));
    if (!d) return notFound(String(params.app));
    const kind = String(params.kind);
    const id = decodeURIComponent(String(params.id));
    if (kind === "schema") {
      const held = `${d.application.connection}/${d.application.schema}`;
      if (d.application.schema === "" || (id !== held && id !== d.application.schema)) {
        return problem(404, `application "${d.application.name}" holds no schema ${id}`);
      }
      releaseSchema(d);
      return HttpResponse.json(touch(d));
    }
    d.application.parts = d.application.parts.filter((p) => !(p.kind === kind && p.id === id));
    d.pending = d.pending.filter((p) => !(p.kind === kind && p.part_id === id));
    return HttpResponse.json(touch(d));
  }),

  http.post(`${BASE}/:app/release`, async ({ params, request }) => {
    const d = find(String(params.app));
    if (!d) return notFound(String(params.app));
    const blocking = blockingProblems(d);
    if (blocking.length > 0) return problem(422, blocking.join("; "));
    const body = (await request.json().catch(() => ({}))) as { note?: string };
    const pins = new Map((d.current_release?.pins ?? []).map((p) => [`${p.kind}/${p.part_id}`, p]));
    for (const c of d.pending) pins.set(`${c.kind}/${c.part_id}`, { kind: c.kind, part_id: c.part_id, version: c.to });
    const applied = d.migrations?.pending ?? [];
    const rel = recordRelease(d, {
      number: (d.releases[0]?.number ?? 0) + 1,
      action: "release",
      migration_version: applied[applied.length - 1] ?? d.migrations?.applied ?? 0,
      changes: { parts: d.pending, applied, reverted: [] },
      note: body.note || undefined,
      created_by: "admin@example.com",
      created_at: now(),
      pins: [...pins.values()],
    });
    if (d.migrations) d.migrations = { applied: rel.migration_version, pending: [] };
    if (d.schema) {
      d.schema = { ...d.schema, migration_version: rel.migration_version, released_version: rel.migration_version, pending: [] };
    }
    return HttpResponse.json(rel);
  }),

  http.post(`${BASE}/:app/rollback`, async ({ params, request }) => {
    const d = find(String(params.app));
    if (!d) return notFound(String(params.app));
    const body = (await request.json().catch(() => ({}))) as { to_release?: number };
    const current = d.current_release;
    if (!current) return problem(409, "the application has no release to roll back");
    const to =
      body.to_release ?? d.releases.find((r) => r.number < current.number)?.number ?? 0;
    const target = d.releases.find((r) => r.number === to);
    if (!target) return problem(404, `release ${to} not found`);
    const before = new Map(current.pins.map((p) => [`${p.kind}/${p.part_id}`, p.version]));
    const parts = target.pins
      .filter((p) => before.get(`${p.kind}/${p.part_id}`) !== p.version)
      .map((p) => ({ kind: p.kind, part_id: p.part_id, from: before.get(`${p.kind}/${p.part_id}`) ?? 0, to: p.version }));
    const rel = recordRelease(d, {
      number: (d.releases[0]?.number ?? 0) + 1,
      action: "rollback",
      migration_version: current.migration_version,
      restored: to,
      changes: { parts, applied: [], reverted: [], migrations_kept: "Migrations stay applied: a rollback does not revert the schema." },
      created_by: "admin@example.com",
      created_at: now(),
      pins: target.pins,
    });
    return HttpResponse.json(rel);
  }),

  http.get(`${BASE}/:app/diagram`, ({ params, request }) => {
    const d = find(String(params.app));
    if (!d) return notFound(String(params.app));
    const q = new URL(request.url).searchParams;
    const version = q.get("version") || "current";
    const compare = q.get("compare") || "";
    for (const v of compare ? [version, compare] : [version]) {
      if (diagramPins(d, v) === null) return problem(422, `version "${v}": current, latest or a release's number`);
      if (/^\d+$/.test(v) && !d.releases.some((r) => r.number === Number(v))) return problem(404, `release ${v} not found`);
    }
    return HttpResponse.json(shopDiagram(d, version, diagramPins(d, version)!, compare, diagramPins(d, compare)));
  }),

  http.get(`${BASE}/:app/export`, ({ params, request }) => {
    const d = find(String(params.app));
    if (!d) return notFound(String(params.app));
    const release = Number(new URL(request.url).searchParams.get("release")) || d.application.current_release;
    if (!release) return problem(409, "the application has no release to export");
    const bundle = {
      ...mockApplicationBundle,
      application: { name: d.application.name, description: d.application.description },
      release,
    };
    return new HttpResponse(JSON.stringify(bundle, null, 2), {
      headers: {
        "Content-Type": "application/json",
        "Content-Disposition": `attachment; filename="${d.application.name}-release-${release}.json"`,
      },
    });
  }),
];

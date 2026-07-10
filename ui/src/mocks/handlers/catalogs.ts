import type {
  APICatalogSummary,
  APICatalogSpec,
} from "@/api/admin/hooks/catalogs";
import { http, HttpResponse } from "msw";
import {
  mockCatalogs,
  mockCatalogSpecs,
  mockCatalogEmbeddingHealth,
  mockCatalogEmbeddingStatuses,
  mockEmbeddingProviderStatus,
} from "../data/catalogs";

const ADMIN_BASE = "/api/v1/admin";

// Mutable copies so create/update/delete/clone reflect in subsequent reads
// within a single mock-server session.
const catalogs: APICatalogSummary[] = JSON.parse(JSON.stringify(mockCatalogs));
const specs: Record<string, Record<string, APICatalogSpec>> = JSON.parse(
  JSON.stringify(mockCatalogSpecs),
);

function findCatalog(id: string): APICatalogSummary | undefined {
  return catalogs.find((c) => c.id === id);
}

// ---------------------------------------------------------------------------
// Catalog handlers — mirror the admin API-catalog REST surface consumed by
// src/api/admin/hooks/catalogs.ts.
// ---------------------------------------------------------------------------

export const catalogHandlers = [
  // Platform-wide embedding provider status. Registered before the parameterized
  // /:id route so the literal path is not swallowed by it.
  http.get(`${ADMIN_BASE}/embedding/status`, () =>
    HttpResponse.json(mockEmbeddingProviderStatus),
  ),

  // List all catalogs.
  http.get(`${ADMIN_BASE}/api-catalogs`, () => HttpResponse.json(catalogs)),

  // Catalog-level embedding health roll-up.
  http.get(`${ADMIN_BASE}/api-catalogs/:id/embedding-health`, ({ params }) => {
    const id = String(params.id);
    const health = mockCatalogEmbeddingHealth[id];
    return health
      ? HttpResponse.json(health)
      : HttpResponse.json(
          {
            catalog_id: id,
            specs_total: 0,
            specs_indexed: 0,
            specs_pending: 0,
            specs_running: 0,
            specs_failed: 0,
          },
          { status: 200 },
        );
  }),

  // Per-spec embedding status rows.
  http.get(`${ADMIN_BASE}/api-catalogs/:id/embedding-status`, ({ params }) => {
    const id = String(params.id);
    return HttpResponse.json({ specs: mockCatalogEmbeddingStatuses[id] ?? [] });
  }),

  // Spec list for a catalog (SpecsManager). Returns { specs: [...] }.
  http.get(`${ADMIN_BASE}/api-catalogs/:id/specs`, ({ params }) => {
    const id = String(params.id);
    return HttpResponse.json({ specs: Object.values(specs[id] ?? {}) });
  }),

  // Single spec content.
  http.get(
    `${ADMIN_BASE}/api-catalogs/:id/specs/:specName`,
    ({ params }) => {
      const spec = specs[String(params.id)]?.[String(params.specName)];
      return spec
        ? HttpResponse.json(spec)
        : new HttpResponse(null, { status: 404 });
    },
  ),

  // Single catalog summary.
  http.get(`${ADMIN_BASE}/api-catalogs/:id`, ({ params }) => {
    const catalog = findCatalog(String(params.id));
    return catalog
      ? HttpResponse.json(catalog)
      : new HttpResponse(null, { status: 404 });
  }),

  // Create a catalog.
  http.post(`${ADMIN_BASE}/api-catalogs`, async ({ request }) => {
    const body = (await request.json()) as Partial<APICatalogSummary>;
    const now = new Date().toISOString();
    const created: APICatalogSummary = {
      id: String(body.id ?? `catalog-${catalogs.length + 1}`),
      name: String(body.name ?? body.id ?? "new-catalog"),
      version: body.version,
      display_name: String(body.display_name ?? body.name ?? "New Catalog"),
      description: body.description,
      created_by: "data-platform@acme.example.com",
      created_at: now,
      updated_at: now,
      spec_count: 0,
      ref_count: 0,
    };
    catalogs.push(created);
    specs[created.id] = {};
    return HttpResponse.json(created, { status: 201 });
  }),

  // Update a catalog.
  http.put(`${ADMIN_BASE}/api-catalogs/:id`, async ({ params, request }) => {
    const catalog = findCatalog(String(params.id));
    if (!catalog) return new HttpResponse(null, { status: 404 });
    const body = (await request.json()) as Partial<APICatalogSummary>;
    if (body.name !== undefined) catalog.name = body.name;
    if (body.version !== undefined) catalog.version = body.version;
    if (body.display_name !== undefined) catalog.display_name = body.display_name;
    if (body.description !== undefined) catalog.description = body.description;
    catalog.updated_at = new Date().toISOString();
    return HttpResponse.json(catalog);
  }),

  // Delete a catalog.
  http.delete(`${ADMIN_BASE}/api-catalogs/:id`, ({ params }) => {
    const idx = catalogs.findIndex((c) => c.id === String(params.id));
    if (idx === -1) return new HttpResponse(null, { status: 404 });
    catalogs.splice(idx, 1);
    delete specs[String(params.id)];
    return new HttpResponse(null, { status: 204 });
  }),

  // Clone a catalog.
  http.post(
    `${ADMIN_BASE}/api-catalogs/:id/clone`,
    async ({ params, request }) => {
      const source = findCatalog(String(params.id));
      if (!source) return new HttpResponse(null, { status: 404 });
      const body = (await request.json()) as Partial<APICatalogSummary>;
      const now = new Date().toISOString();
      const cloned: APICatalogSummary = {
        ...source,
        id: String(body.id ?? `${source.id}-copy`),
        name: String(body.name ?? `${source.name}-copy`),
        version: body.version ?? source.version,
        display_name: String(body.display_name ?? `${source.display_name} (copy)`),
        created_at: now,
        updated_at: now,
        ref_count: 0,
      };
      catalogs.push(cloned);
      specs[cloned.id] = JSON.parse(JSON.stringify(specs[source.id] ?? {}));
      return HttpResponse.json(cloned, { status: 201 });
    },
  ),

  // Upsert a spec (inline or url source).
  http.put(
    `${ADMIN_BASE}/api-catalogs/:id/specs/:name`,
    async ({ params, request }) => {
      const id = String(params.id);
      const name = String(params.name);
      const catalog = findCatalog(id);
      if (!catalog) return new HttpResponse(null, { status: 404 });
      const body = (await request.json()) as Partial<APICatalogSpec>;
      const now = new Date().toISOString();
      const existing = specs[id]?.[name];
      const spec: APICatalogSpec = {
        spec_name: name,
        source_kind: body.source_kind ?? "inline",
        content: body.content ?? existing?.content,
        source_url: body.source_url ?? existing?.source_url,
        base_path: body.base_path ?? existing?.base_path,
        title: body.title ?? existing?.title,
        description: body.description ?? existing?.description,
        created_at: existing?.created_at ?? now,
        updated_at: now,
        last_fetched_at: now,
        operation_count: existing?.operation_count ?? 0,
        embedding_count: existing?.embedding_count ?? 0,
        embedding_status: existing?.embedding_status ?? "pending",
        embedding_attempts: existing?.embedding_attempts ?? 0,
      };
      specs[id] ??= {};
      if (!existing) catalog.spec_count += 1;
      specs[id][name] = spec;
      catalog.updated_at = now;
      return HttpResponse.json(spec);
    },
  ),

  // Upload a spec file (multipart). Query string carries optional overrides.
  http.put(
    `${ADMIN_BASE}/api-catalogs/:id/specs/:name/upload`,
    ({ params, request }) => {
      const id = String(params.id);
      const name = String(params.name);
      const catalog = findCatalog(id);
      if (!catalog) return new HttpResponse(null, { status: 404 });
      const url = new URL(request.url);
      const now = new Date().toISOString();
      const existing = specs[id]?.[name];
      const spec: APICatalogSpec = {
        spec_name: name,
        source_kind: "upload",
        content: existing?.content,
        base_path: url.searchParams.get("base_path") ?? existing?.base_path,
        title: url.searchParams.get("title") ?? existing?.title,
        description: url.searchParams.get("description") ?? existing?.description,
        created_at: existing?.created_at ?? now,
        updated_at: now,
        last_fetched_at: now,
        operation_count: existing?.operation_count ?? 0,
        embedding_count: existing?.embedding_count ?? 0,
        embedding_status: "pending",
        embedding_attempts: 0,
      };
      specs[id] ??= {};
      if (!existing) catalog.spec_count += 1;
      specs[id][name] = spec;
      catalog.updated_at = now;
      return HttpResponse.json(spec);
    },
  ),

  // Refresh a url-backed spec.
  http.post(
    `${ADMIN_BASE}/api-catalogs/:id/specs/:name/refresh`,
    ({ params }) => {
      const spec = specs[String(params.id)]?.[String(params.name)];
      if (!spec) return new HttpResponse(null, { status: 404 });
      spec.last_fetched_at = new Date().toISOString();
      spec.updated_at = spec.last_fetched_at;
      return HttpResponse.json(spec);
    },
  ),

  // Manual re-embed (escape hatch). Returns 202 with an enqueue result.
  http.post(
    `${ADMIN_BASE}/api-catalogs/:id/specs/:name/reembed`,
    ({ params }) => {
      const spec = specs[String(params.id)]?.[String(params.name)];
      if (!spec) return new HttpResponse(null, { status: 404 });
      return HttpResponse.json(
        { status: "queued", created: true },
        { status: 202 },
      );
    },
  ),

  // Delete a spec.
  http.delete(
    `${ADMIN_BASE}/api-catalogs/:id/specs/:name`,
    ({ params }) => {
      const id = String(params.id);
      const name = String(params.name);
      const catalog = findCatalog(id);
      if (!catalog || !specs[id]?.[name]) {
        return new HttpResponse(null, { status: 404 });
      }
      delete specs[id][name];
      catalog.spec_count = Math.max(0, catalog.spec_count - 1);
      catalog.updated_at = new Date().toISOString();
      return new HttpResponse(null, { status: 204 });
    },
  ),
];

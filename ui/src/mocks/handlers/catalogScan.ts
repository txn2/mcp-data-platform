import { http, HttpResponse } from "msw";
import type { CatalogImport, CatalogSettings } from "@/api/admin/types";
import { mockCatalogImports, mockCatalogScans, mockCatalogSettings } from "../data/catalogScan";

// The catalog scan admin routes (#1918):
//   GET  /catalog/settings
//   PUT  /catalog/settings
//   GET  /catalog/scans
//   POST /catalog/scans
// and the DataHub import (#1923):
//   GET  /catalog/imports
//   POST /catalog/imports

const ADMIN_BASE = "/api/v1/admin";
let settings: CatalogSettings = { ...mockCatalogSettings };
let imports: CatalogImport[] = [...mockCatalogImports];

export const catalogScanHandlers = [
  http.get(`${ADMIN_BASE}/catalog/settings`, () => HttpResponse.json(settings)),
  http.put(`${ADMIN_BASE}/catalog/settings`, async ({ request }) => {
    const body = (await request.json()) as CatalogSettings;
    settings = { ...body, updated_by: "admin@example.com", updated_at: new Date().toISOString() };
    return HttpResponse.json(settings);
  }),
  http.get(`${ADMIN_BASE}/catalog/scans`, () => HttpResponse.json({ scans: mockCatalogScans })),
  http.post(`${ADMIN_BASE}/catalog/scans`, () => HttpResponse.json({ status: "requested" }, { status: 202 })),
  http.get(`${ADMIN_BASE}/catalog/imports`, () => HttpResponse.json({ imports })),
  http.post(`${ADMIN_BASE}/catalog/imports`, async ({ request }) => {
    const body = (await request.json()) as { url: string };
    const run: CatalogImport = {
      id: crypto.randomUUID(),
      source_url: body.url,
      started_by: "admin@example.com",
      started_at: new Date().toISOString(),
      running: true,
    };
    imports = [run, ...imports];
    return HttpResponse.json(run, { status: 202 });
  }),
];

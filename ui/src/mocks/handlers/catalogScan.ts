import { http, HttpResponse } from "msw";
import type { CatalogSettings } from "@/api/admin/types";
import { mockCatalogScans, mockCatalogSettings } from "../data/catalogScan";

// The catalog scan admin routes (#1918):
//   GET  /catalog/settings
//   PUT  /catalog/settings
//   GET  /catalog/scans
//   POST /catalog/scans

const ADMIN_BASE = "/api/v1/admin";
let settings: CatalogSettings = { ...mockCatalogSettings };

export const catalogScanHandlers = [
  http.get(`${ADMIN_BASE}/catalog/settings`, () => HttpResponse.json(settings)),
  http.put(`${ADMIN_BASE}/catalog/settings`, async ({ request }) => {
    const body = (await request.json()) as CatalogSettings;
    settings = { ...body, updated_by: "admin@example.com", updated_at: new Date().toISOString() };
    return HttpResponse.json(settings);
  }),
  http.get(`${ADMIN_BASE}/catalog/scans`, () => HttpResponse.json({ scans: mockCatalogScans })),
  http.post(`${ADMIN_BASE}/catalog/scans`, () => HttpResponse.json({ status: "requested" }, { status: 202 })),
];

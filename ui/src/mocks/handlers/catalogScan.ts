import { http, HttpResponse } from "msw";
import type { CatalogSettings } from "@/api/admin/types";
import { mockCatalogDocuments, mockCatalogScans, mockCatalogSettings } from "../data/catalogScan";

// The catalog scan admin routes (#1918):
//   GET  /catalog/settings
//   PUT  /catalog/settings
//   GET  /catalog/scans
//   POST /catalog/scans
//   GET  /catalog/documents
//   GET  /catalog/documents/{kind}/{connection}/{filename}

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
  http.get(`${ADMIN_BASE}/catalog/documents`, () => HttpResponse.json({ documents: mockCatalogDocuments })),
  http.get(`${ADMIN_BASE}/catalog/documents/:kind/:connection/:filename`, ({ params }) => {
    const doc = mockCatalogDocuments.find(
      (d) => d.connection_kind === params.kind && d.connection === params.connection && d.filename === params.filename,
    );
    if (!doc) return new HttpResponse(null, { status: 404 });
    return new HttpResponse(doc.mime_type === "application/json" ? '{"openapi":"3.0.3"}' : "type Query { ping: String }", {
      headers: { "Content-Type": doc.mime_type, "Content-Disposition": `attachment; filename="${doc.filename}"` },
    });
  }),
];

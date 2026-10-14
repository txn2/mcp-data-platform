import type { CatalogDocument, CatalogScan, CatalogSettings } from "@/api/admin/types";

// The platform catalog's scan (#1918): three connections' last scans, one of
// which failed, and settings that leave one schema out.

export const mockCatalogScans: CatalogScan[] = [
  {
    connection_kind: "trino",
    connection: "acme",
    started_at: "2026-09-28T18:02:11Z",
    finished_at: "2026-09-28T18:04:37Z",
    datasets: 1212,
    removed: 3,
    unread: [
      {
        path: "legacy",
        error: "reading columns: Error listing tables for catalog legacy: Connection refused",
      },
    ],
  },
  {
    connection_kind: "api",
    connection: "api-test-fixture",
    started_at: "2026-09-28T18:04:37Z",
    finished_at: "2026-09-28T18:04:38Z",
    datasets: 148,
    removed: 0,
    unread: [],
  },
  {
    connection_kind: "graphql",
    connection: "github",
    started_at: "2026-09-28T18:04:38Z",
    finished_at: "2026-09-28T18:04:38Z",
    datasets: 0,
    removed: 0,
    error: "listing operations: no graphql connection \"github\", or its schema has not been read",
    unread: [],
  },
];

export const mockCatalogSettings: CatalogSettings = {
  scan_interval: "6h",
  exclusions: [{ connection_kind: "trino", connection: "acme", entries: ["hive.tmp"] }],
  updated_by: "admin@example.com",
  updated_at: "2026-09-27T09:15:00Z",
};

// The schema documents the scan keeps (#1918): an API connection's spec,
// revised twice, and a GraphQL connection's SDL.
export const mockCatalogDocuments: CatalogDocument[] = [
  {
    id: "4d1e2f3a-6b7c-4d8e-9f0a-1b2c3d4e5f60",
    reference: 'mcp:schema_document:(api,billing,"openapi.json")',
    connection_kind: "api",
    connection: "billing",
    filename: "openapi.json",
    mime_type: "application/json",
    size_bytes: 184_322,
    version: 3,
    updated_at: "2026-10-13T12:00:04Z",
  },
  {
    id: "5e2f3a4b-7c8d-4e9f-0a1b-2c3d4e5f6a71",
    reference: 'mcp:schema_document:(graphql,crm,"schema.graphql")',
    connection_kind: "graphql",
    connection: "crm",
    filename: "schema.graphql",
    mime_type: "application/graphql",
    size_bytes: 41_907,
    version: 1,
    updated_at: "2026-10-13T12:00:09Z",
  },
];

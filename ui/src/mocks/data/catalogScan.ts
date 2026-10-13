import type { CatalogScan, CatalogSettings } from "@/api/admin/types";

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

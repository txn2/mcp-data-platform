import type { CatalogDocument, CatalogImport, CatalogScan, CatalogSettings } from "@/api/admin/types";

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

// The DataHub import (#1923): one finished run with a report, and an earlier
// one that failed to reach DataHub.
export const mockCatalogImports: CatalogImport[] = [
  {
    id: "7b1c2f4e-5d6a-4b8c-9e0f-1a2b3c4d5e6f",
    source_url: "https://datahub.example.com:8080",
    started_by: "admin@example.com",
    started_at: "2026-10-13T16:02:11Z",
    finished_at: "2026-10-13T16:05:48Z",
    running: false,
    report: {
      definitions: { glossary_node: 4, glossary_term: 31, tag: 18, domain: 3, data_product: 2 },
      datasets: { read: 1240, imported: 1118, from_removed: 47, skipped: 122 },
      aspects_written: 3904,
      aspects_unchanged: 0,
      aspects_kept: 12,
      lineage_edges: 86,
      skipped: [
        {
          urn: "urn:li:dataset:(urn:li:dataPlatform:tableau,sales_workbook.revenue,PROD)",
          reason: "Tableau entities are not imported",
        },
        {
          urn: "urn:li:dataset:(urn:li:dataPlatform:trino,legacy.finance.ledger,PROD)",
          reason:
            "no connection's catalog holds a dataset with this path; scan the connection that sees it, then import again",
        },
        {
          urn: "urn:li:dataset:(urn:li:dataPlatform:trino,warehouse.sales.orders,PROD)",
          reason: "written in the platform catalog since, so the catalog's value is kept",
          where: "mcp:dataset:(trino,acme,warehouse.sales.orders) description",
        },
        {
          urn: "urn:li:corpuser:jdoe",
          reason: "the import has no catalog entity for this URN",
          where: "memory_records.content 3f2a9c",
        },
      ],
      skipped_by_reason: {
        "Tableau entities are not imported": 94,
        "no connection's catalog holds a dataset with this path; scan the connection that sees it, then import again": 28,
        "written in the platform catalog since, so the catalog's value is kept": 12,
        "the import has no catalog entity for this URN": 5,
      },
      rewrite: {
        rewritten: {
          "memory_records.entity_urns": 64,
          "memory_records.content": 3,
          "knowledge_changesets.target_urn": 41,
          "knowledge_page_entity_refs.entity_urn": 17,
          "call_records.targets": 220,
        },
        unmapped: 5,
        index_rows_dropped: 1186,
      },
    },
  },
  {
    id: "2e9d8c7b-6a5f-4e3d-2c1b-0a9f8e7d6c5b",
    source_url: "https://datahub.example.com:9002",
    started_by: "admin@example.com",
    started_at: "2026-10-13T15:51:02Z",
    finished_at: "2026-10-13T15:51:03Z",
    running: false,
    error: "listing DATASET: request failed: 404 Not Found",
  },
];

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

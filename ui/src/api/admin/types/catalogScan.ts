// The platform catalog's scan of every connection (#1918).

/** One connection's catalogs, schemas or operations kept out of the catalog,
 * as dotted prefixes. */
export interface CatalogExclusion {
  connection_kind: string;
  connection: string;
  entries: string[];
}

/** The catalog's settings: how often every connection is scanned, and what
 * each keeps out. */
export interface CatalogSettings {
  scan_interval: string;
  exclusions: CatalogExclusion[];
  updated_by?: string;
  updated_at?: string;
}

/** A part of a connection a scan could not read, and why. */
export interface CatalogScanUnread {
  path: string;
  error: string;
}

/**
 * One connection's last scan. A failed scan marks nothing removed; a scan that
 * could not read some parts catalogs the rest and marks nothing under them.
 */
export interface CatalogScan {
  connection_kind: string;
  connection: string;
  started_at: string;
  finished_at: string;
  datasets: number;
  removed: number;
  error?: string;
  unread: CatalogScanUnread[];
}

// The one-time import of a deployment's DataHub into the catalog (#1923).

/** Something an import did not carry over, and why; where names the platform
 * table and column, or the catalog entity, it concerns. */
export interface CatalogImportSkip {
  urn: string;
  reason: string;
  where?: string;
}

/** What an import's rewrite of stored DataHub references did. */
export interface CatalogImportRewrite {
  rewritten: Record<string, number>;
  unmapped: number;
  index_rows_dropped: number;
}

/** What one import did. */
export interface CatalogImportReport {
  definitions: Record<string, number>;
  datasets: { read: number; imported: number; from_removed: number; skipped: number };
  aspects_written: number;
  aspects_unchanged: number;
  aspects_kept: number;
  lineage_edges: number;
  skipped: CatalogImportSkip[];
  skipped_by_reason: Record<string, number>;
  rewrite?: CatalogImportRewrite;
}

/** One run of the import. */
export interface CatalogImport {
  id: string;
  source_url: string;
  started_by: string;
  started_at: string;
  finished_at?: string;
  running: boolean;
  report?: CatalogImportReport;
  error?: string;
}

/** CatalogDocument is a connection's schema document the scan keeps (#1918):
 * an API connection's OpenAPI spec, a GraphQL connection's SDL. */
export interface CatalogDocument {
  id: string;
  reference: string;
  connection_kind: string;
  connection: string;
  filename: string;
  mime_type: string;
  size_bytes: number;
  version: number;
  updated_at: string;
}

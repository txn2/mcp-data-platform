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

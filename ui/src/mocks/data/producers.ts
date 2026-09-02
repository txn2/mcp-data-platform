// What produced each file, and what each script has produced (#1569).
//
// The fixture is deliberately a handful of files rather than every one: most
// assets in a real library were written once by the session that saved them,
// and a library where every file listed three producers would show the panel in
// a state a deployment does not have.
//
// ast-001, the Q4 dashboard the screenshot manifest opens, is the interesting
// case the feature exists for: an hourly script refreshes it and a person edits
// it, so both are listed and only one created it. res-029 carries the other
// half -- a script that replaced the content of a file it did not upload --
// and ast-004 carries a producer whose script has since been deleted.

const now = new Date("2026-08-20T09:00:00Z");
const hoursAgo = (n: number) => new Date(now.getTime() - n * 3_600_000).toISOString();
const daysAgo = (n: number) => new Date(now.getTime() - n * 86_400_000).toISOString();

export interface MockProducer {
  kind: "script" | "session" | "person";
  id: string;
  label?: string;
  exists: boolean;
  created: boolean;
  first_write_at: string;
  last_write_at: string;
  write_count: number;
  last_version: number;
}

/** producersByTarget is keyed "<kind>:<id>", the way the relation is. */
export const producersByTarget: Record<string, MockProducer[]> = {
  "asset:ast-001": [
    {
      kind: "person",
      id: "user-alice",
      label: "alice@example.com",
      exists: true,
      created: false,
      first_write_at: daysAgo(6),
      last_write_at: hoursAgo(5),
      write_count: 3,
      last_version: 9,
    },
    {
      kind: "script",
      id: "script-001",
      label: "daily-sales-report",
      exists: true,
      created: true,
      first_write_at: daysAgo(41),
      last_write_at: hoursAgo(19),
      write_count: 41,
      last_version: 8,
    },
  ],
  "asset:ast-004": [
    {
      kind: "script",
      id: "script-retired",
      label: "quarterly-rollup",
      exists: false,
      created: true,
      first_write_at: daysAgo(120),
      last_write_at: daysAgo(63),
      write_count: 7,
      last_version: 7,
    },
  ],
  "asset:ast-008": [
    {
      kind: "session",
      id: "dps_5f2c9a41b7e34d08",
      exists: true,
      created: true,
      first_write_at: daysAgo(2),
      last_write_at: daysAgo(2),
      write_count: 1,
      last_version: 1,
    },
  ],
  "resource:res-029": [
    {
      kind: "script",
      id: "script-003",
      label: "warehouse-freshness",
      exists: true,
      created: false,
      first_write_at: daysAgo(12),
      last_write_at: daysAgo(1),
      write_count: 4,
      last_version: 5,
    },
    {
      kind: "person",
      id: "user-marcus",
      label: "marcus.webb@example.com",
      exists: true,
      created: true,
      first_write_at: daysAgo(96),
      last_write_at: daysAgo(96),
      write_count: 1,
      last_version: 1,
    },
  ],
};

export interface MockProducedItem {
  target_kind: "asset" | "resource" | "collection";
  target_id: string;
  name?: string;
  /** The address the file's row records, for an asset or a collection. */
  owner_email?: string;
  created: boolean;
  first_write_at: string;
  last_write_at: string;
  write_count: number;
  last_version: number;
  deleted?: boolean;
}

/**
 * producedByScript is the same relation read from the other end. It is stated
 * rather than derived from producersByTarget so a fixture can carry what only
 * this end shows: a file the script wrote that has since been deleted, which no
 * target-keyed entry can hold because the target is gone.
 */
export const producedByScript: Record<string, MockProducedItem[]> = {
  "script-001": [
    {
      target_kind: "asset",
      target_id: "ast-001",
      name: "Q4 Revenue Dashboard",
      owner_email: "sarah.chen@example.com",
      created: true,
      first_write_at: daysAgo(41),
      last_write_at: hoursAgo(19),
      write_count: 41,
      last_version: 8,
    },
    {
      // The collection the script files its outputs under, created by its
      // first run (#1579). It carries an owner address the way an asset does,
      // and a transfer that keeps the outputs leaves it behind the same way.
      target_kind: "collection",
      target_id: "col-001",
      name: "Q4 Performance Review",
      owner_email: "sarah.chen@example.com",
      created: true,
      first_write_at: daysAgo(41),
      last_write_at: daysAgo(41),
      write_count: 1,
      last_version: 0,
    },
    {
      target_kind: "resource",
      target_id: "res-014",
      name: "Regional Sales Extract",
      created: true,
      first_write_at: daysAgo(41),
      last_write_at: hoursAgo(19),
      write_count: 41,
      last_version: 41,
    },
    {
      target_kind: "asset",
      target_id: "ast-removed",
      created: false,
      first_write_at: daysAgo(88),
      last_write_at: daysAgo(52),
      write_count: 6,
      last_version: 6,
      deleted: true,
    },
  ],
  "script-003": [
    {
      target_kind: "resource",
      target_id: "res-029",
      name: "Warehouse Floor Plan",
      created: false,
      first_write_at: daysAgo(12),
      last_write_at: daysAgo(1),
      write_count: 4,
      last_version: 5,
    },
  ],
};

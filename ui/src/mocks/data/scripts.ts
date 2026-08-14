import type {
  PendingReview,
  Script,
  ScriptVersion,
  VersionReview,
} from "@/api/admin/types";

// Managed-script review fixtures (#1287). The set is chosen to show the three
// decisions the surface exists for: a first approval, a change to a script that
// is already running unattended, and a script whose queue is clear.

const now = new Date("2026-08-14T09:00:00Z");
const daysAgo = (n: number) => new Date(now.getTime() - n * 86_400_000).toISOString();

const salesSourceV2 = `# Yesterday's sales by region, refreshed every weekday morning.
rows = platform.query(
    connection="acme-warehouse",
    sql="""
        SELECT region, SUM(amount) AS revenue, COUNT(*) AS orders
          FROM sales.orders
         WHERE order_date = :report_date
         GROUP BY region
         ORDER BY revenue DESC
    """,
    params={"report_date": run["params"]["report_date"]},
)

platform.export(name="daily-sales", format="csv", rows=rows["rows"])
`;

const salesSourceV3 = `# Yesterday's sales by region, refreshed every weekday morning.
rows = platform.query(
    connection="acme-warehouse",
    sql="""
        SELECT region, SUM(amount) AS revenue, COUNT(*) AS orders
          FROM sales.orders
         WHERE order_date = :report_date
         GROUP BY region
         ORDER BY revenue DESC
    """,
    params={"report_date": run["params"]["report_date"]},
)

margins = platform.query(
    connection="acme-finance",
    sql="SELECT region, margin_pct FROM finance.margins WHERE as_of = :report_date",
    params={"report_date": run["params"]["report_date"]},
)

platform.export(name="daily-sales", format="csv", rows=rows["rows"])
platform.export(name="daily-margins", format="csv", rows=margins["rows"])
`;

const churnSource = `# Accounts with no orders in 90 days, for the retention review.
rows = platform.query(
    connection="acme-warehouse",
    sql="SELECT account_id, last_order_at FROM sales.accounts WHERE last_order_at < :cutoff",
    params={"cutoff": run["params"]["cutoff"]},
)

print("dormant accounts: %d" % len(rows["rows"]))
platform.export(name="dormant-accounts", format="csv", rows=rows["rows"])
platform.export(
    name="dormant-accounts",
    format="csv",
    rows=rows["rows"],
    destination="acme-crm-drop",
    key="retention/dormant.csv",
)
`;

export const mockScripts: Script[] = [
  {
    id: "script-001",
    name: "daily-sales-report",
    display_name: "Daily Sales Report",
    description: "Yesterday's sales by region, exported for the morning review.",
    scope: "global",
    owner_email: "sarah.chen@example.com",
    status: "active",
    enabled: true,
    version: 2,
    approved_version_id: "sver-001-v2",
    tags: ["sales", "reporting"],
    updated_at: daysAgo(4),
  },
  {
    id: "script-002",
    name: "dormant-accounts",
    display_name: "Dormant Accounts",
    description: "Accounts with no orders in 90 days, for the retention review.",
    scope: "persona",
    owner_email: "marcus.webb@example.com",
    status: "draft",
    enabled: true,
    version: 1,
    tags: ["retention"],
    updated_at: daysAgo(9),
  },
  {
    id: "script-003",
    name: "warehouse-freshness",
    display_name: "Warehouse Freshness Check",
    description: "Row counts and max load timestamps per warehouse table.",
    scope: "global",
    owner_email: "sarah.chen@example.com",
    status: "active",
    enabled: true,
    version: 5,
    approved_version_id: "sver-003-v5",
    tags: ["operations"],
    updated_at: daysAgo(21),
  },
];

// Two rows: a change to a script that is running today, and a script nobody has
// ever approved. The third script has nothing waiting.
export const mockScriptReviews: PendingReview[] = [
  {
    script_id: "script-002",
    script_name: "dormant-accounts",
    display_name: "Dormant Accounts",
    description: "Accounts with no orders in 90 days, for the retention review.",
    owner_email: "marcus.webb@example.com",
    scope: "persona",
    version: 1,
    version_id: "sver-002-v1",
    version_status: "applied",
    author: "marcus.webb@example.com",
    author_roles: ["analyst"],
    first_approval: true,
    created_at: daysAgo(9),
  },
  {
    script_id: "script-001",
    script_name: "daily-sales-report",
    display_name: "Daily Sales Report",
    description: "Yesterday's sales by region, exported for the morning review.",
    owner_email: "sarah.chen@example.com",
    scope: "global",
    version: 3,
    version_id: "sver-001-v3",
    version_status: "draft",
    author: "sarah.chen@example.com",
    author_roles: ["analyst", "data_engineer"],
    first_approval: false,
    created_at: daysAgo(3),
  },
];

export const mockScriptVersions: Record<string, ScriptVersion[]> = {
  "script-001": [
    {
      id: "sver-001-v3",
      script_id: "script-001",
      version: 3,
      display_name: "Daily Sales Report",
      description: "Adds a margins export alongside the sales export.",
      source: salesSourceV3,
      author: "sarah.chen@example.com",
      author_roles: ["analyst", "data_engineer"],
      status: "draft",
      grants: { roles: [], connections: [], capabilities: [], destinations: [] },
      created_at: daysAgo(3),
    },
    {
      id: "sver-001-v2",
      script_id: "script-001",
      version: 2,
      display_name: "Daily Sales Report",
      description: "Yesterday's sales by region.",
      source: salesSourceV2,
      author: "sarah.chen@example.com",
      author_roles: ["analyst"],
      status: "applied",
      approved_by: "admin@acme.example.com",
      approved_at: daysAgo(30),
      grants: {
        roles: ["analyst"],
        connections: ["acme-warehouse"],
        capabilities: ["platform.query", "platform.export"],
        destinations: [{ name: "portal", kind: "portal" }],
      },
      created_at: daysAgo(31),
    },
    {
      id: "sver-001-v1",
      script_id: "script-001",
      version: 1,
      display_name: "Daily Sales Report",
      description: "First draft.",
      source: salesSourceV2.replace("ORDER BY revenue DESC", "ORDER BY region"),
      author: "sarah.chen@example.com",
      author_roles: ["analyst"],
      status: "applied",
      approved_by: "admin@acme.example.com",
      approved_at: daysAgo(60),
      grants: {
        roles: ["analyst"],
        connections: ["acme-warehouse"],
        capabilities: ["platform.query", "platform.export"],
        destinations: [{ name: "portal", kind: "portal" }],
      },
      created_at: daysAgo(61),
    },
  ],
  "script-002": [
    {
      id: "sver-002-v1",
      script_id: "script-002",
      version: 1,
      display_name: "Dormant Accounts",
      description: "Accounts with no orders in 90 days.",
      source: churnSource,
      author: "marcus.webb@example.com",
      author_roles: ["analyst"],
      status: "applied",
      grants: { roles: [], connections: [], capabilities: [], destinations: [] },
      created_at: daysAgo(9),
    },
  ],
  "script-003": [
    {
      id: "sver-003-v5",
      script_id: "script-003",
      version: 5,
      display_name: "Warehouse Freshness Check",
      description: "Row counts and max load timestamps.",
      source: `rows = platform.query(connection="acme-warehouse", sql="SELECT table_name, loaded_at FROM ops.loads")\nplatform.export(name="freshness", format="csv", rows=rows["rows"])\n`,
      author: "sarah.chen@example.com",
      author_roles: ["data_engineer"],
      status: "applied",
      approved_by: "admin@acme.example.com",
      approved_at: daysAgo(21),
      grants: {
        roles: ["data_engineer"],
        connections: ["acme-warehouse"],
        capabilities: ["platform.query", "platform.export"],
        destinations: [{ name: "portal", kind: "portal" }],
      },
      created_at: daysAgo(22),
    },
  ],
};

// The diff the server computes for v3 against the approved v2: a second
// connection and a second export, which is exactly the widening a reviewer is
// there to notice.
const salesDiff = `--- v2 (approved)
+++ v3 (under review)
@@ -10,4 +10,12 @@
     params={"report_date": run["params"]["report_date"]},
 )

+margins = platform.query(
+    connection="acme-finance",
+    sql="SELECT region, margin_pct FROM finance.margins WHERE as_of = :report_date",
+    params={"report_date": run["params"]["report_date"]},
+)
+
 platform.export(name="daily-sales", format="csv", rows=rows["rows"])
+platform.export(name="daily-margins", format="csv", rows=margins["rows"])
`;

// One review payload per version the queue points at, keyed "<scriptID>/<n>".
export const mockScriptReviewPayloads: Record<string, VersionReview> = {
  "script-001/3": {
    version: mockScriptVersions["script-001"]![0]!,
    referenced: {
      capabilities: ["platform.query", "platform.export"],
      connections: ["acme-finance", "acme-warehouse"],
      destinations: ["portal"],
      dynamic_connections: false,
      dynamic_destinations: false,
    },
    missing_capabilities: ["platform.query", "platform.export"],
    missing_connections: ["acme-finance", "acme-warehouse"],
    missing_destinations: ["portal"],
    approved: {
      version: 2,
      version_id: "sver-001-v2",
      grants: {
        roles: ["analyst"],
        connections: ["acme-warehouse"],
        capabilities: ["platform.query", "platform.export"],
        destinations: [{ name: "portal", kind: "portal" }],
      },
      approved_by: "admin@acme.example.com",
      approved_at: daysAgo(30),
      source_diff: salesDiff,
    },
  },
  "script-001/2": {
    version: mockScriptVersions["script-001"]![1]!,
    referenced: {
      capabilities: ["platform.query", "platform.export"],
      connections: ["acme-warehouse"],
      destinations: ["portal"],
      dynamic_connections: false,
      dynamic_destinations: false,
    },
  },
  "script-002/1": {
    version: mockScriptVersions["script-002"]![0]!,
    referenced: {
      capabilities: ["platform.query", "platform.export"],
      connections: ["acme-warehouse"],
      destinations: ["acme-crm-drop", "portal"],
      dynamic_connections: false,
      dynamic_destinations: false,
    },
    missing_capabilities: ["platform.query", "platform.export"],
    missing_connections: ["acme-warehouse"],
    missing_destinations: ["acme-crm-drop", "portal"],
    findings: [
      {
        severity: "warning",
        line: 8,
        message: "print() output is captured on the run record and truncated at 64KB.",
        hint: "Anything larger than a log belongs in an export.",
      },
    ],
  },
  "script-003/5": {
    version: mockScriptVersions["script-003"]![0]!,
    referenced: {
      capabilities: ["platform.query", "platform.export"],
      connections: ["acme-warehouse"],
      destinations: ["portal"],
      dynamic_connections: false,
      dynamic_destinations: false,
    },
  },
};

// The script review-queue alert settings section (#1287), separate from the
// knowledge queue's own section.
export const mockScriptReviewAlert = {
  enabled: true,
  pending_threshold: 5,
  oldest_pending_days: 7,
  cooldown_hours: 24,
  recipients: ["sarah.chen@example.com"],
  updated_by: "sarah.chen@example.com",
  updated_at: daysAgo(11),
  warnings: [] as string[],
};

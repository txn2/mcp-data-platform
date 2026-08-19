import type {
  PendingReview,
  Script,
  ScriptVersion,
  VersionReview,
} from "@/api/admin/types";
import type {
  ScriptConnectionChoice,
  ScriptContract,
  ScriptRun,
  ScriptRunDetail,
  ScriptSchedule,
} from "@/api/portal/hooks/scripts";

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

// A real script description is a document rather than a caption (#1369): what
// it produces, what its parameters mean, and what it assumes about the data. It
// is markdown, and the script page renders it as markdown.
const salesDescription = [
  "Yesterday's sales by region, exported for the morning review.",
  "",
  "## What it produces",
  "",
  "One CSV asset, `daily-sales`, with a row per region: revenue and order count",
  "for the requested day.",
  "",
  "## Parameters",
  "",
  "- `report_date` — the day to report on. The schedule binds `${fire_date}`, so a",
  "  scheduled fire reports the day it runs.",
  "",
  "## What it assumes",
  "",
  "`sales.orders` is loaded for the requested day by 06:00 in the warehouse's",
  "timezone. A run before the load completes reports a partial day rather than",
  "failing, so check the load before reading a surprising number.",
].join("\n");

export const mockScripts: Script[] = [
  {
    id: "script-001",
    name: "daily-sales-report",
    display_name: "Daily Sales Report",
    description: salesDescription,
    scope: "global",
    owner_email: "sarah.chen@example.com",
    status: "active",
    enabled: true,
    version: 2,
    approved_version_id: "sver-001-v2",
    category: "reporting",
    tags: ["sales", "weekly"],
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
    category: "reporting",
    tags: ["retention"],
    updated_at: daysAgo(9),
  },
  {
    id: "script-004",
    name: "my-margin-check",
    display_name: "My Margin Check",
    description: "Margin by product line, for my own morning read.",
    scope: "personal",
    owner_email: "sarah.chen@example.com",
    status: "active",
    enabled: true,
    version: 1,
    approved_version_id: "sver-004-v1",
    category: "finance",
    tags: ["margins"],
    updated_at: daysAgo(1),
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
    category: "operations",
    tags: ["freshness"],
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
  // A personal script its own owner wrote, approved on save with no reviewer
  // (#1367): the grant was minted from what the source reaches, and the record
  // says nobody looked at it.
  "script-004": [
    {
      id: "sver-004-v1",
      script_id: "script-004",
      version: 1,
      display_name: "My Margin Check",
      description: "Margin by product line.",
      source: `rows = platform.query(connection="acme-finance", sql="SELECT line, margin_pct FROM finance.margins")\nplatform.export(name="my-margins", format="csv", rows=rows["rows"])\n`,
      author: "sarah.chen@example.com",
      author_roles: ["analyst"],
      status: "applied",
      approved_by: "sarah.chen@example.com",
      approved_at: daysAgo(1),
      auto_approved: true,
      grants: {
        roles: ["analyst"],
        connections: ["acme-finance"],
        capabilities: ["platform.query", "platform.export"],
        destinations: [{ name: "portal", kind: "portal" }],
      },
      created_at: daysAgo(1),
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
    // The author ran this exact source before sending it (#1364), which is what
    // a reviewer most wants to know before agreeing that it should run
    // unattended. The other payloads deliberately carry no account, because a
    // version nobody has run is the state the drawer has to state plainly.
    dry_run: {
      id: "dpx_draft_001",
      script_id: "script-001",
      requested_by: "sarah.chen@example.com",
      status: "succeeded",
      log: "reading yesterday's rows\n1,284 rows for 2026-08-17",
      metrics: { steps: 1042, duration_ms: 1830, queries: 1, exports: 1 },
      outputs: [
        {
          name: "daily_sales",
          destination: "portal",
          format: "csv",
          row_count: 1_284,
          bytes: 48_213,
        },
      ],
      created_at: daysAgo(3),
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

// mockReachableConnections is the deployment's connection inventory as the
// enumerator reports it (#1361). A run form narrows it to what the approved
// version was granted; a dry-run form shows it whole, because a draft executes
// as its caller.
export const mockReachableConnections: ScriptConnectionChoice[] = [
  { name: "acme-warehouse", kind: "trino", description: "Production Trino warehouse" },
  { name: "acme-reporting", kind: "trino", description: "Reporting replica" },
  { name: "acme-lake", kind: "s3", description: "Raw object store" },
];

// ---------------------------------------------------------------------------
// Portal script pages (#1290): the owner's view of the same scripts — their
// cadence, their runs, and what those runs produced.
// ---------------------------------------------------------------------------

const hoursAgo = (n: number) => new Date(now.getTime() - n * 3_600_000).toISOString();
const hoursAhead = (n: number) => new Date(now.getTime() + n * 3_600_000).toISOString();

export const mockScriptSchedules: Record<string, ScriptSchedule> = {
  "script-001": {
    id: "sched-001",
    script_id: "script-001",
    cron_spec: "0 7 * * 1-5",
    timezone: "America/Los_Angeles",
    // The binding a fire passes, with the token as written: it expands to the
    // day of the fire, which is what makes a scheduled run reproducible.
    params: { report_date: "${fire_date}" },
    enabled: true,
    next_run_at: hoursAhead(22),
    last_fire_at: hoursAgo(2),
    missed_fires: 0,
  },
  "script-003": {
    id: "sched-003",
    script_id: "script-003",
    cron_spec: "*/30 * * * *",
    timezone: "UTC",
    enabled: false,
    last_fire_at: daysAgo(6),
    missed_fires: 2,
  },
};

// The run history of the daily report: a success this morning, the failure that
// woke somebody yesterday, a fire skipped because the previous run was still
// going, and an older success. Between them they are every terminal state a run
// history has to be able to show.
export const mockScriptRuns: Record<string, ScriptRun[]> = {
  "script-001": [
    {
      id: "run-001",
      status: "succeeded",
      trigger: "schedule",
      version: 2,
      fire_time: hoursAgo(2),
      started_at: hoursAgo(2),
      finished_at: hoursAgo(2),
      duration_ms: 8_420,
      output_count: 1,
    },
    {
      id: "run-002",
      status: "failed",
      trigger: "schedule",
      version: 2,
      fire_time: hoursAgo(26),
      started_at: hoursAgo(26),
      finished_at: hoursAgo(26),
      duration_ms: 3_110,
      error: "platform.query: connection \"acme-warehouse\" refused the query: relation \"sales.orders\" does not exist",
      output_count: 0,
    },
    {
      id: "run-003",
      status: "skipped_overlap",
      trigger: "schedule",
      version: 2,
      fire_time: hoursAgo(50),
      duration_ms: 0,
      output_count: 0,
    },
    {
      id: "run-004",
      status: "succeeded",
      trigger: "tool",
      version: 2,
      fire_time: hoursAgo(74),
      started_at: hoursAgo(74),
      finished_at: hoursAgo(74),
      duration_ms: 9_050,
      output_count: 1,
      requested_by: "sarah.chen@example.com",
    },
  ],
  "script-002": [],
  "script-003": [
    {
      id: "run-101",
      status: "succeeded",
      trigger: "schedule",
      version: 5,
      fire_time: daysAgo(6),
      started_at: daysAgo(6),
      finished_at: daysAgo(6),
      duration_ms: 2_400,
      output_count: 1,
    },
  ],
};

const salesRunLog = `binding report_date=2026-08-13
querying acme-warehouse
1,420 rows in 6.8s
exporting daily-sales as csv
wrote asset version 42
`;

export const mockScriptRunDetails: Record<string, ScriptRunDetail> = {
  "run-001": {
    id: "run-001",
    script_id: "script-001",
    version: 2,
    status: "succeeded",
    trigger: "schedule",
    duration_ms: 8_420,
    output_count: 1,
    fire_time: hoursAgo(2),
    scheduled_for: hoursAgo(2),
    started_at: hoursAgo(2),
    finished_at: hoursAgo(2),
    params: { report_date: "2026-08-13" },
    log: salesRunLog,
    metrics: { steps: 1_284, duration_ms: 8_420, queries: 1, exports: 1 },
    outputs: [
      {
        name: "daily-sales",
        destination: "portal",
        asset_id: "asset-1",
        asset_version: 42,
        format: "csv",
        row_count: 1_420,
        bytes: 98_304,
      },
    ],
    attempt: 1,
    created_at: hoursAgo(2),
  },
  "run-002": {
    id: "run-002",
    script_id: "script-001",
    version: 2,
    status: "failed",
    trigger: "schedule",
    duration_ms: 3_110,
    output_count: 0,
    fire_time: hoursAgo(26),
    scheduled_for: hoursAgo(26),
    started_at: hoursAgo(26),
    finished_at: hoursAgo(26),
    params: { report_date: "2026-08-12" },
    error:
      'platform.query: connection "acme-warehouse" refused the query: relation "sales.orders" does not exist\n  at daily-sales-report.star:3:9 in <toplevel>',
    log: "binding report_date=2026-08-12\nquerying acme-warehouse\n",
    metrics: { steps: 61, duration_ms: 3_110, queries: 1, exports: 0 },
    outputs: [],
    attempt: 1,
    created_at: hoursAgo(26),
  },
  "run-003": {
    id: "run-003",
    script_id: "script-001",
    version: 2,
    status: "skipped_overlap",
    trigger: "schedule",
    duration_ms: 0,
    output_count: 0,
    fire_time: hoursAgo(50),
    scheduled_for: hoursAgo(50),
    metrics: { steps: 0, duration_ms: 0, queries: 0, exports: 0 },
    outputs: [],
    attempt: 0,
    created_at: hoursAgo(50),
  },
  "run-004": {
    id: "run-004",
    script_id: "script-001",
    version: 2,
    status: "succeeded",
    trigger: "tool",
    duration_ms: 9_050,
    output_count: 2,
    fire_time: hoursAgo(74),
    scheduled_for: hoursAgo(74),
    started_at: hoursAgo(74),
    finished_at: hoursAgo(74),
    requested_by: "sarah.chen@example.com",
    params: { report_date: "2026-08-11" },
    log: salesRunLog.replace("2026-08-13", "2026-08-11").replace("version 42", "version 41"),
    log_truncated: true,
    metrics: { steps: 1_301, duration_ms: 9_050, queries: 1, exports: 1 },
    outputs: [
      {
        name: "daily-sales",
        destination: "portal",
        asset_id: "asset-1",
        asset_version: 41,
        format: "csv",
        row_count: 1_402,
        bytes: 97_112,
      },
      {
        name: "daily-sales",
        destination: "acme-crm-drop",
        bucket: "acme-exports",
        key: "sales/2026/08/11/daily-sales.csv",
        format: "csv",
        row_count: 1_402,
        bytes: 97_112,
      },
    ],
    attempt: 1,
    created_at: hoursAgo(74),
  },
  "run-101": {
    id: "run-101",
    script_id: "script-003",
    version: 5,
    status: "succeeded",
    trigger: "schedule",
    duration_ms: 2_400,
    output_count: 1,
    fire_time: daysAgo(6),
    scheduled_for: daysAgo(6),
    started_at: daysAgo(6),
    finished_at: daysAgo(6),
    log: "checked 38 tables\n",
    metrics: { steps: 402, duration_ms: 2_400, queries: 1, exports: 1 },
    outputs: [
      {
        name: "freshness",
        destination: "portal",
        asset_id: "asset-2",
        asset_version: 12,
        format: "csv",
        row_count: 38,
        bytes: 4_096,
      },
    ],
    attempt: 1,
    created_at: daysAgo(6),
  },
};

// The contract each script resolves to. It is the same document an agent's
// fetch of an mcp:script reference returns, which is why the page reads it
// rather than assembling its own answer.
export const mockScriptContracts: Record<string, ScriptContract> = {
  "script-001": {
    id: "script-001",
    name: "daily-sales-report",
    display_name: "Daily Sales Report",
    description: salesDescription,
    owner_email: "sarah.chen@example.com",
    scope: "global",
    category: "reporting",
    tags: ["sales", "weekly"],
    status: "active",
    enabled: true,
    params: [
      {
        name: "report_date",
        type: "date",
        description: "The business date to report on; the schedule pins it to the fire time.",
        required: true,
      },
      {
        name: "source",
        type: "connection",
        description: "Which warehouse the day's rows are read from.",
        required: true,
      },
    ],
    approval: {
      approved: true,
      version: 2,
      approved_by: "admin@acme.example.com",
      approved_at: daysAgo(30),
    },
    schedule: {
      cron_spec: "0 7 * * 1-5",
      timezone: "America/Los_Angeles",
      enabled: true,
      next_run_at: hoursAhead(22),
    },
    last_successful_run: {
      run_id: "run-001",
      version: 2,
      finished_at: hoursAgo(2),
      outputs: [
        {
          name: "daily-sales",
          kind: "portal_asset",
          destination: "portal",
          format: "csv",
          row_count: 1_420,
          bytes: 98_304,
          asset_id: "asset-1",
          asset_version: 42,
        },
      ],
    },
  },
  "script-002": {
    id: "script-002",
    name: "dormant-accounts",
    display_name: "Dormant Accounts",
    description: "Accounts with no orders in 90 days, for the retention review.",
    owner_email: "marcus.webb@example.com",
    scope: "persona",
    personas: ["analyst"],
    category: "reporting",
    tags: ["retention"],
    status: "draft",
    enabled: true,
    params: [
      { name: "cutoff", type: "date", description: "Accounts idle since this date.", required: true },
    ],
    approval: {
      approved: false,
      refusal: "the script has no approved version, so nothing may execute it",
    },
  },
  "script-004": {
    id: "script-004",
    name: "my-margin-check",
    display_name: "My Margin Check",
    description: "Margin by product line, for my own morning read.",
    owner_email: "sarah.chen@example.com",
    scope: "personal",
    category: "finance",
    tags: ["margins"],
    status: "active",
    enabled: true,
    params: [],
    approval: {
      approved: true,
      version: 1,
      approved_by: "sarah.chen@example.com",
      approved_at: daysAgo(1),
      automatic: true,
    },
  },
  "script-003": {
    id: "script-003",
    name: "warehouse-freshness",
    display_name: "Warehouse Freshness Check",
    description: "Row counts and max load timestamps per warehouse table.",
    owner_email: "sarah.chen@example.com",
    scope: "global",
    category: "operations",
    tags: ["freshness"],
    status: "active",
    enabled: true,
    params: [],
    approval: {
      approved: true,
      version: 5,
      approved_by: "admin@acme.example.com",
      approved_at: daysAgo(21),
    },
    schedule: { cron_spec: "*/30 * * * *", timezone: "UTC", enabled: false },
    last_successful_run: {
      run_id: "run-101",
      version: 5,
      finished_at: daysAgo(6),
      outputs: [
        {
          name: "freshness",
          kind: "portal_asset",
          destination: "portal",
          format: "csv",
          row_count: 38,
          bytes: 4_096,
          asset_id: "asset-2",
          asset_version: 12,
        },
      ],
    },
  },
};

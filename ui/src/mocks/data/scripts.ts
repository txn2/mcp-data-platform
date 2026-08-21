import type { Script, ScriptVersion, VersionDetail } from "@/api/admin/types";
import type {
  ScriptConnectionChoice,
  ScriptContract,
  ScriptRun,
  ScriptRunDetail,
  ScriptSchedule,
} from "@/api/portal/hooks/scripts";

// Managed-script fixtures. The set is chosen to show the states the surfaces
// exist for: a scheduled report with a mixed run history, a persona-scoped
// script that has never run, a personal script, and a paused freshness check.

const now = new Date("2026-08-14T09:00:00Z");
const daysAgo = (n: number) => new Date(now.getTime() - n * 86_400_000).toISOString();

const salesSource = `# Yesterday's sales by region, refreshed every weekday morning.
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
    status: "active",
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
    category: "operations",
    tags: ["freshness"],
    updated_at: daysAgo(21),
  },
];

export const mockScriptVersions: Record<string, ScriptVersion[]> = {
  "script-001": [
    {
      id: "sver-001-v2",
      script_id: "script-001",
      version: 2,
      display_name: "Daily Sales Report",
      description: "Yesterday's sales by region.",
      source: salesSource,
      author: "sarah.chen@example.com",
      author_roles: ["analyst"],
      status: "applied",
      created_at: daysAgo(31),
    },
    {
      id: "sver-001-v1",
      script_id: "script-001",
      version: 1,
      display_name: "Daily Sales Report",
      description: "First draft.",
      source: salesSource.replace("ORDER BY revenue DESC", "ORDER BY region"),
      author: "sarah.chen@example.com",
      author_roles: ["analyst"],
      status: "applied",
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
      created_at: daysAgo(9),
    },
  ],
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
      created_at: daysAgo(22),
    },
  ],
};

// One detail payload per version the history points at, keyed "<scriptID>/<n>":
// the snapshot, what a static read of its source found, and — where somebody
// has executed this exact source — the account of that run (#1364).
export const mockScriptVersionDetails: Record<string, VersionDetail> = {
  "script-001/2": {
    version: mockScriptVersions["script-001"]![0]!,
    referenced: {
      capabilities: ["platform.query", "platform.export"],
      connections: ["acme-warehouse"],
      destinations: ["portal"],
      dynamic_connections: false,
      dynamic_destinations: false,
    },
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
  "script-001/1": {
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

// mockReachableConnections is the deployment's connection inventory as the
// enumerator reports it (#1361).
//
// It carries one name under two kinds, which is legitimate and is what made the
// route answer differently on each call (#1384). The server serves only the
// kind a connection parameter can name, so "acme-lake" appears here and never
// in a response.
export const mockReachableConnections: ScriptConnectionChoice[] = [
  { name: "acme-warehouse", kind: "trino", description: "Production Trino warehouse" },
  { name: "acme-reporting", kind: "trino", description: "Reporting replica" },
  { name: "acme-lake", kind: "s3", description: "Raw object store" },
  { name: "acme-warehouse", kind: "s3", description: "Warehouse exports bucket" },
];

// mockBindableConnections is what the connections route actually serves: the
// inventory narrowed to the kind a connection parameter binds to.
export const mockBindableConnections: ScriptConnectionChoice[] = mockReachableConnections.filter(
  (c) => c.kind === "trino",
);

// mockConnectionNames is the distinct names the inventory holds. The static
// validator reports the connection names a script's SOURCE references, with no
// kind narrowing — that narrowing belongs to the picker — so the names are what
// it works from, deduplicated because one name may be carried by two kinds.
export const mockConnectionNames: string[] = [
  ...new Set(mockReachableConnections.map((c) => c.name)),
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
// rather than assembling its own answer. The version is the latest saved one —
// the version a run executes — and the refusal is empty because every fixture
// script is in service.
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
    version: 2,
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
    status: "active",
    enabled: true,
    params: [
      { name: "cutoff", type: "date", description: "Accounts idle since this date.", required: true },
    ],
    version: 1,
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
    version: 1,
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
    version: 5,
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

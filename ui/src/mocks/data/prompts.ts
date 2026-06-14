import type { Prompt } from "@/api/admin/types";

const now = new Date();
function daysAgo(n: number): string {
  const d = new Date(now);
  d.setDate(d.getDate() - n);
  return d.toISOString();
}

// ---------------------------------------------------------------------------
// System prompts
// ---------------------------------------------------------------------------

const discoverDataDomains: Prompt = {
  id: "prompt-001",
  name: "discover-data-domains",
  display_name: "Discover Data Domains",
  description: "Discover available data domains and their schemas",
  content:
    "List all available data domains in the platform, including their schemas, table counts, and last-updated timestamps. Highlight any domains added in the last 30 days.",
  arguments: [],
  category: "platform",
  scope: "system",
  personas: [],
  tags: [],
  status: "approved",
  owner_email: "platform@example.com",
  source: "built-in",
  enabled: true,
  created_at: daysAgo(90),
  updated_at: daysAgo(15),
};

const queryBestPractices: Prompt = {
  id: "prompt-002",
  name: "query-best-practices",
  display_name: "Query Best Practices",
  description: "Guidelines for optimizing queries on this platform",
  content:
    "Provide a concise summary of query optimization best practices for this platform, covering partitioning strategies, predicate pushdown, and join ordering for large retail datasets.",
  arguments: [],
  category: "platform",
  scope: "system",
  personas: [],
  tags: [],
  status: "approved",
  owner_email: "platform@example.com",
  source: "built-in",
  enabled: true,
  created_at: daysAgo(90),
  updated_at: daysAgo(60),
};

// ---------------------------------------------------------------------------
// Global prompts
// ---------------------------------------------------------------------------

const dailySalesReport: Prompt = {
  id: "prompt-003",
  name: "daily-sales-report",
  display_name: "Daily Sales Report",
  description: "Generate daily sales report by region",
  content:
    "Generate a daily sales summary for {{date}} broken down by region. Include total revenue, transaction count, and average order value. Flag any region with revenue below {{threshold}}.",
  arguments: [
    { name: "date", description: "Report date in YYYY-MM-DD format", required: true },
    { name: "threshold", description: "Minimum expected revenue in dollars", required: false },
  ],
  category: "reporting",
  scope: "global",
  personas: [],
  tags: [],
  status: "approved",
  owner_email: "analytics-team@example.com",
  source: "shared",
  enabled: true,
  created_at: daysAgo(45),
  updated_at: daysAgo(5),
};

const inventoryHealthCheck: Prompt = {
  id: "prompt-004",
  name: "inventory-health-check",
  display_name: "Inventory Health Check",
  description: "Check inventory levels and identify reorder needs",
  content:
    "Scan current inventory levels across all warehouses and identify SKUs below their reorder point. Group results by {{category}} and sort by days until stockout.",
  arguments: [
    { name: "category", description: "Product category to focus on (or 'all')", required: true },
  ],
  category: "operations",
  scope: "global",
  personas: [],
  tags: [],
  status: "approved",
  owner_email: "ops-team@example.com",
  source: "shared",
  enabled: true,
  created_at: daysAgo(30),
  updated_at: daysAgo(3),
};

const dataQualityScan: Prompt = {
  id: "prompt-005",
  name: "data-quality-scan",
  display_name: "Data Quality Scan",
  description: "Scan key tables for data quality issues",
  content:
    "Run data quality checks on the {{schema}} schema. Report null rates, duplicate keys, and values outside expected ranges. Summarize findings with severity levels.",
  arguments: [
    { name: "schema", description: "Schema name to scan (e.g. sales, inventory)", required: true },
  ],
  category: "data-ops",
  scope: "global",
  personas: [],
  tags: [],
  status: "approved",
  owner_email: "data-quality@example.com",
  source: "shared",
  enabled: true,
  created_at: daysAgo(60),
  updated_at: daysAgo(10),
};

// ---------------------------------------------------------------------------
// Persona-scoped prompts
// ---------------------------------------------------------------------------

const executiveKpiDashboard: Prompt = {
  id: "prompt-006",
  name: "executive-kpi-dashboard",
  display_name: "Executive KPI Dashboard",
  description: "Generate KPI scorecard for executives",
  content:
    "Build an executive KPI scorecard for {{time_period}} covering revenue, gross margin, customer acquisition cost, and same-store sales growth. Compare against targets and prior period.",
  arguments: [
    { name: "time_period", description: "Time period for the scorecard (e.g. Q4-2025, March 2026)", required: true },
  ],
  category: "executive",
  scope: "persona",
  personas: ["regional-director"],
  tags: [],
  status: "approved",
  owner_email: "exec-analytics@example.com",
  source: "shared",
  enabled: true,
  created_at: daysAgo(40),
  updated_at: daysAgo(7),
};

const etlPipelineStatus: Prompt = {
  id: "prompt-007",
  name: "etl-pipeline-status",
  display_name: "ETL Pipeline Status",
  description: "Check ETL pipeline health and recent failures",
  content:
    "Report the current status of all ETL pipelines that ran in the last {{hours}} hours. Include success rate, average duration, and details for any failed or delayed jobs.",
  arguments: [
    { name: "hours", description: "Lookback window in hours", required: true },
  ],
  category: "data-ops",
  scope: "persona",
  personas: ["data-engineer"],
  tags: [],
  status: "approved",
  owner_email: "data-eng@example.com",
  source: "shared",
  enabled: true,
  created_at: daysAgo(35),
  updated_at: daysAgo(2),
};

const stockLevelAlert: Prompt = {
  id: "prompt-008",
  name: "stock-level-alert",
  display_name: "Stock Level Alert",
  description: "Generate low stock alerts by warehouse",
  content:
    "Identify all SKUs in warehouse {{warehouse_id}} with fewer than {{min_units}} units remaining. Include current stock, daily sell-through rate, and estimated days until stockout.",
  arguments: [
    { name: "warehouse_id", description: "Warehouse identifier (e.g. WH-001)", required: true },
    { name: "min_units", description: "Minimum unit threshold for alerts", required: true },
  ],
  category: "operations",
  scope: "persona",
  personas: ["inventory-analyst"],
  tags: [],
  status: "approved",
  owner_email: "inventory-team@example.com",
  source: "shared",
  enabled: true,
  created_at: daysAgo(25),
  updated_at: daysAgo(4),
};

const revenueForecast: Prompt = {
  id: "prompt-009",
  name: "revenue-forecast",
  display_name: "Revenue Forecast",
  description: "Forecast next quarter revenue by region",
  content:
    "Forecast revenue for {{quarter}} by region using trailing twelve-month trends. Include confidence intervals and highlight regions with projected growth above {{growth_target}}%.",
  arguments: [
    { name: "quarter", description: "Target quarter (e.g. Q3-2026)", required: true },
    { name: "growth_target", description: "Growth percentage threshold to highlight", required: false },
  ],
  category: "finance",
  scope: "persona",
  personas: ["finance-executive"],
  tags: [],
  status: "approved",
  owner_email: "finance-analytics@example.com",
  source: "shared",
  enabled: true,
  created_at: daysAgo(50),
  updated_at: daysAgo(8),
};

// ---------------------------------------------------------------------------
// Personal prompts
// ---------------------------------------------------------------------------

const myWeeklySummary: Prompt = {
  id: "prompt-010",
  name: "my-weekly-summary",
  display_name: "My Weekly Summary",
  description: "Personal weekly data activity summary",
  content:
    "Summarize my data platform activity for the past week, including queries run, artifacts created, and top tables accessed. Highlight anything unusual.",
  arguments: [],
  category: "productivity",
  scope: "personal",
  personas: [],
  tags: [],
  status: "approved",
  owner_email: "j.martinez@example.com",
  source: "user",
  enabled: true,
  created_at: daysAgo(14),
  updated_at: daysAgo(1),
};

const storeComparison: Prompt = {
  id: "prompt-011",
  name: "store-comparison",
  display_name: "Store Comparison",
  description: "Compare metrics between two specific stores",
  content:
    "Compare key performance metrics between store {{store_a}} and store {{store_b}} for the last 30 days. Include revenue, foot traffic, conversion rate, and average basket size.",
  arguments: [
    { name: "store_a", description: "First store ID to compare", required: true },
    { name: "store_b", description: "Second store ID to compare", required: true },
  ],
  category: "analysis",
  scope: "personal",
  personas: [],
  tags: [],
  status: "approved",
  owner_email: "j.martinez@example.com",
  source: "user",
  enabled: true,
  created_at: daysAgo(10),
  updated_at: daysAgo(2),
};

const customSqlTemplate: Prompt = {
  id: "prompt-012",
  name: "custom-sql-template",
  display_name: "Custom SQL Template",
  description: "Template for running common ad-hoc queries",
  content:
    "Run the following ad-hoc query against the {{catalog}} catalog: {{sql_query}}. Format the results as a table and include row count and execution time.",
  arguments: [
    { name: "catalog", description: "Target catalog (e.g. iceberg, hive)", required: true },
    { name: "sql_query", description: "SQL query to execute", required: true },
  ],
  category: "productivity",
  scope: "personal",
  personas: [],
  tags: [],
  status: "approved",
  owner_email: "j.martinez@example.com",
  source: "user",
  enabled: true,
  created_at: daysAgo(20),
  updated_at: daysAgo(6),
};

const incidentRetro: Prompt = {
  id: "prompt-013",
  name: "incident-retro",
  display_name: "Incident Retrospective",
  description:
    "Long-form retrospective template for production incidents. Pulls audit, query, and pipeline activity for the impact window and produces a blameless writeup with timeline, contributing factors, and follow-up actions.",
  content: `# Incident Retrospective — {{incident_id}}

You are writing a **blameless retrospective** for incident **{{incident_id}}**.
Impact window: **{{start_time}} → {{end_time}}** ({{timezone}}).

## 1. Summary

Write 3–5 sentences a non-technical stakeholder would understand. Cover:

- What broke, from the customer's perspective.
- When it started, when it was detected, when it was resolved.
- Who was paged and who actually drove the resolution.
- Severity ({{severity}}) and customer-visible impact.

## 2. Timeline

Build a strict-chronological timeline of events between **{{start_time}}** and **{{end_time}}**. For each entry, include:

| Time ({{timezone}}) | Event | Source | Operator |
| --- | --- | --- | --- |

Sources to pull from:

1. **Audit log** — every tool call by {{primary_operator}} and any teammates joining the incident channel.
2. **Pipeline runs** — \`etl_pipelines.runs\` rows whose \`updated_at\` falls in the window, with their status transitions.
3. **Trino query log** — top 20 long-running queries by user during the window.
4. **DataHub deprecation events** — anything marked deprecated/restored in the window.

> Cite each entry with the underlying record (audit_event_id, query_id, run_id). No paraphrasing without a citation.

## 3. Contributing factors

Identify **at most 5** contributing factors. For each:

- **What** — one-sentence description.
- **Evidence** — direct links to log lines / query IDs / dashboards.
- **Category** — one of: configuration, capacity, deploy, data, dependency, human-process.
- **Counterfactual** — "if X had been different, this incident would have been …".

Reject any factor you cannot evidence. Better to list 2 well-evidenced factors than 5 speculative ones.

## 4. What went well

Explicit, named callouts. Examples:

- The on-call rotation paged the right person within {{page_sla}} minutes.
- The runbook for \`{{affected_pipeline}}\` matched reality and was followed.
- Customer comms went out before any external escalation.

## 5. Follow-up actions

Produce a table with one row per action. Every row must have an owner and a due date — no "TBD".

| # | Action | Owner | Due | Category |
| --- | --- | --- | --- | --- |

Categories: **prevent** (stop recurrence), **detect** (faster signal next time), **respond** (better playbook),
**recover** (faster restore), **communicate** (clearer customer messaging).

## 6. Open questions

Anything you couldn't determine from the available evidence — frame as a question, not an accusation.
These should be resolved during the retro meeting, not left in the document.

---

**Constraints**

- Blameless: describe systems and decisions, never individuals' competence.
- Every factual claim must cite a record from one of the sources above.
- If a source has no relevant records, say so explicitly — do not infer.
- Output the entire writeup as Markdown so it can be pasted into the incidents wiki unedited.`,
  arguments: [
    { name: "incident_id", description: "Internal incident identifier (e.g. INC-2026-0418-01)", required: true },
    { name: "start_time", description: "When impact began, ISO-8601 with offset", required: true },
    { name: "end_time", description: "When impact ended, ISO-8601 with offset", required: true },
    { name: "timezone", description: "Display timezone for the timeline (e.g. America/Los_Angeles)", required: true },
    { name: "severity", description: "Severity tier (SEV1, SEV2, SEV3)", required: true },
    { name: "primary_operator", description: "Email of the person who drove the response", required: true },
    { name: "affected_pipeline", description: "Name of the pipeline most directly impacted", required: false },
    { name: "page_sla", description: "Expected paging SLA in minutes", required: false },
  ],
  category: "incident-response",
  scope: "personal",
  personas: [],
  tags: [],
  status: "approved",
  owner_email: "j.martinez@example.com",
  source: "user",
  enabled: true,
  created_at: daysAgo(8),
  updated_at: daysAgo(1),
};

const weeklyBusinessReview: Prompt = {
  id: "prompt-014",
  name: "weekly-business-review",
  display_name: "Weekly Business Review",
  description:
    "Full week-over-week business review covering revenue, operations, and data quality. Produces a publish-ready Markdown report with KPIs, anomaly callouts, and a one-paragraph executive summary at the top.",
  content: `# Weekly Business Review — Week ending {{week_ending}}

Audience: **{{audience}}** (e.g. exec staff, ops leadership).
Region scope: **{{region}}** (use \`all\` to roll up everything).
Comparison: this week vs. **{{compare_to}}** (\`prior_week\`, \`prior_year\`, or \`plan\`).

> Lead with the answer. The executive summary at the top must stand on its own — assume only ~30% of readers scroll past it.

## Executive summary

One paragraph, **at most 4 sentences**:

1. The single most important number this week and its direction.
2. The biggest positive surprise (with magnitude).
3. The biggest negative surprise (with magnitude and what is being done about it).
4. What you need from the audience this week.

## Revenue scorecard

Pull from \`finance.revenue_daily\` for the {{region}} region.

| Metric | This week | {{compare_to}} | Δ | Δ % | Status |
| --- | --- | --- | --- | --- | --- |
| Net revenue | | | | | |
| Gross margin % | | | | | |
| Average order value | | | | | |
| Customer acquisition cost | | | | | |
| Same-store sales growth | | | | | |

Status is one of: 🟢 on/above plan, 🟡 within {{warn_threshold}}% of plan, 🔴 outside threshold.

If a metric crosses 🔴, attach a 1-sentence root cause hypothesis grounded in the data, plus the audit_event_id
or query_id you used to verify.

## Operations scorecard

Pull from \`ops.warehouse_metrics\` and \`ops.fulfillment_runs\`.

- On-time fulfillment rate (target ≥ {{otf_target}}%).
- Inventory days-on-hand by category (call out anything over {{doh_alert}}).
- Stockouts: SKU count and revenue exposure.
- Top 5 SKUs by lost-sale risk.

For each operations metric outside its target, propose **one** concrete next step,
with the owner and a 7-day-or-less deadline. No "investigate further" placeholders.

## Data platform health

This is the section the audience tends to skip — keep it short and only flag what matters.

- Pipeline success rate this week vs. trailing 8 weeks.
- Any tables flagged \`deprecated\` in DataHub that still received queries.
- Top 3 longest-running recurring queries (candidates for tuning).
- Any audit log gaps (missing days, unusual operator activity).

## Anomalies

For every metric where the week-over-week delta exceeds {{anomaly_sigma}} standard deviations of its
trailing 12-week distribution, produce one block:

\`\`\`
metric:      <name>
this week:   <value>
trailing μ:  <value> (σ = <value>)
delta:       <value> (<σ count>σ)
hypothesis:  <1-sentence explanation, grounded in the data>
evidence:    <query_id or audit_event_id>
action:      <owner — what — by when>
\`\`\`

Reject any block where you cannot fill in **evidence**. A real anomaly with no evidence
is worse than no callout.

## Asks for the audience

A bulleted list. Each item is one decision you need from this audience this week,
phrased as a question with options. If there are none, write "No asks this week."

---

**Style rules**

- Round numbers to the precision a human would speak (\`$4.2M\`, not \`$4,237,891.40\`).
- Percentages to one decimal (\`+12.3%\`, not \`+12.34567%\`).
- All directional comparisons explicit (\`+\`/\`-\` signs, ✅/⚠️ for status).
- No buzzwords (\`leverage\`, \`synergy\`, \`mission-critical\`). State the thing.
- Output the entire report as a single Markdown document, ready to paste.`,
  arguments: [
    { name: "week_ending", description: "Sunday of the report week (YYYY-MM-DD)", required: true },
    { name: "region", description: "Region code, or 'all' for global rollup", required: true },
    { name: "audience", description: "Audience descriptor (e.g. 'exec staff', 'ops leadership')", required: true },
    { name: "compare_to", description: "Comparison baseline: prior_week, prior_year, or plan", required: true },
    { name: "warn_threshold", description: "Yellow-zone band as a percent (default 5)", required: false },
    { name: "otf_target", description: "On-time fulfillment target as a percent", required: false },
    { name: "doh_alert", description: "Days-on-hand threshold that triggers a callout", required: false },
    { name: "anomaly_sigma", description: "Sigma threshold for the anomalies section (default 2)", required: false },
  ],
  category: "reporting",
  scope: "personal",
  personas: [],
  tags: [],
  status: "approved",
  owner_email: "j.martinez@example.com",
  source: "user",
  enabled: true,
  created_at: daysAgo(12),
  updated_at: daysAgo(2),
};

// ---------------------------------------------------------------------------
// Exports
// ---------------------------------------------------------------------------

export const mockAdminPrompts: Prompt[] = [
  discoverDataDomains,
  queryBestPractices,
  dailySalesReport,
  inventoryHealthCheck,
  dataQualityScan,
  executiveKpiDashboard,
  etlPipelineStatus,
  stockLevelAlert,
  revenueForecast,
  myWeeklySummary,
  storeComparison,
  customSqlTemplate,
  incidentRetro,
  weeklyBusinessReview,
];

const personalPrompts: Prompt[] = [
  myWeeklySummary,
  storeComparison,
  customSqlTemplate,
  incidentRetro,
  weeklyBusinessReview,
];

const availablePrompts: Prompt[] = [
  discoverDataDomains,
  queryBestPractices,
  dailySalesReport,
  inventoryHealthCheck,
  dataQualityScan,
  executiveKpiDashboard,
  etlPipelineStatus,
  stockLevelAlert,
  revenueForecast,
];

export const mockPortalPrompts: { personal: Prompt[]; available: Prompt[] } = {
  personal: personalPrompts,
  available: availablePrompts,
};

// ---------------------------------------------------------------------------
// Prompts shared directly with the current user (surfaced on the Prompts page
// "Shared" tab). These are runnable as `shared-<name>`.
// ---------------------------------------------------------------------------

export interface MockSharedPrompt {
  prompt: Prompt;
  share_id: string;
  shared_by: string;
  shared_at: string;
  permission: "viewer" | "editor";
}

export const mockSharedPrompts: MockSharedPrompt[] = [
  {
    prompt: {
      id: "prompt-shared-001",
      name: "regional-deep-dive",
      display_name: "Regional Deep Dive",
      description: "Carol's template for a single-region performance breakdown.",
      content:
        "Produce a deep-dive performance report for region {{region}} over the last quarter, covering revenue, top stores, and notable anomalies.",
      arguments: [{ name: "region", description: "Region to analyze", required: true }],
      category: "analysis",
      scope: "personal",
      personas: [],
      tags: ["regional", "analysis"],
      status: "approved",
      owner_email: "carol@example.com",
      source: "user",
      enabled: true,
      created_at: daysAgo(20),
      updated_at: daysAgo(5),
    },
    share_id: "psh-001",
    shared_by: "carol@example.com",
    shared_at: daysAgo(4),
    permission: "viewer",
  },
  {
    prompt: {
      id: "prompt-shared-002",
      name: "pipeline-health-brief",
      display_name: "Pipeline Health Brief",
      description: "Dave's daily data-pipeline health summary template.",
      content:
        "Summarize the health of all data pipelines for the last 24 hours: failures, latency outliers, and freshness gaps. Flag anything needing attention.",
      arguments: [],
      category: "operations",
      scope: "personal",
      personas: [],
      tags: ["pipeline", "monitoring"],
      status: "approved",
      owner_email: "dave@example.com",
      source: "user",
      enabled: true,
      created_at: daysAgo(30),
      updated_at: daysAgo(2),
    },
    share_id: "psh-002",
    shared_by: "dave@example.com",
    shared_at: daysAgo(1),
    permission: "editor",
  },
];

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
  owner_email: "j.martinez@example.com",
  source: "user",
  enabled: true,
  created_at: daysAgo(20),
  updated_at: daysAgo(6),
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
];

const personalPrompts: Prompt[] = [myWeeklySummary, storeComparison, customSqlTemplate];

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

interface Resource {
  id: string;
  scope: "global" | "persona" | "user";
  scope_id: string;
  category: string;
  filename: string;
  display_name: string;
  description: string;
  mime_type: string;
  size_bytes: number;
  s3_key: string;
  uri: string;
  tags: string[];
  uploader_sub: string;
  uploader_email: string;
  created_at: string;
  updated_at: string;
}

const now = new Date();
function daysAgo(n: number): string {
  const d = new Date(now);
  d.setDate(d.getDate() - n);
  return d.toISOString();
}
function hoursAgo(n: number): string {
  const d = new Date(now);
  d.setHours(d.getHours() - n);
  return d.toISOString();
}

const resources: Resource[] = [
  {
    id: "res-001",
    scope: "global",
    scope_id: "",
    category: "documentation",
    filename: "sql-style-guide.pdf",
    display_name: "SQL Style Guide",
    description:
      "SQL formatting and naming conventions for all data teams. Covers CTEs, join ordering, and alias rules.",
    mime_type: "application/pdf",
    size_bytes: 245_760,
    s3_key: "resources/global/documentation/sql-style-guide.pdf",
    uri: "s3://acme-platform/resources/global/documentation/sql-style-guide.pdf",
    tags: ["sql", "standards"],
    uploader_sub: "sarah-admin",
    uploader_email: "sarah.chen@example.com",
    created_at: daysAgo(45),
    updated_at: daysAgo(12),
  },
  {
    id: "res-002",
    scope: "global",
    scope_id: "",
    category: "templates",
    filename: "data-dictionary.md",
    display_name: "Data Dictionary",
    description:
      "Template for documenting new tables and columns. Includes business context, lineage, and quality rules.",
    mime_type: "text/markdown",
    size_bytes: 18_432,
    s3_key: "resources/global/templates/data-dictionary.md",
    uri: "s3://acme-platform/resources/global/templates/data-dictionary.md",
    tags: ["templates", "docs"],
    uploader_sub: "sarah-admin",
    uploader_email: "sarah.chen@example.com",
    created_at: daysAgo(60),
    updated_at: daysAgo(30),
  },
  {
    id: "res-003",
    scope: "global",
    scope_id: "",
    category: "documentation",
    filename: "query-playbook.pdf",
    display_name: "Query Playbook",
    description:
      "Best practices for writing performant Trino queries. Covers partitioning strategies and predicate pushdown.",
    mime_type: "application/pdf",
    size_bytes: 512_000,
    s3_key: "resources/global/documentation/query-playbook.pdf",
    uri: "s3://acme-platform/resources/global/documentation/query-playbook.pdf",
    tags: ["sql", "performance"],
    uploader_sub: "sarah-admin",
    uploader_email: "sarah.chen@example.com",
    created_at: daysAgo(30),
    updated_at: daysAgo(8),
  },
  {
    id: "res-004",
    scope: "global",
    scope_id: "",
    category: "onboarding",
    filename: "onboarding-guide.html",
    display_name: "Onboarding Guide",
    description:
      "Interactive guide for new platform users. Covers MCP clients, available tools, and data access policies.",
    mime_type: "text/html",
    size_bytes: 98_304,
    s3_key: "resources/global/onboarding/onboarding-guide.html",
    uri: "s3://acme-platform/resources/global/onboarding/onboarding-guide.html",
    tags: ["onboarding"],
    uploader_sub: "sarah-admin",
    uploader_email: "sarah.chen@example.com",
    created_at: daysAgo(90),
    updated_at: daysAgo(5),
  },
  {
    id: "res-005",
    scope: "persona",
    scope_id: "data-engineer",
    category: "runbooks",
    filename: "etl-runbook.md",
    display_name: "ETL Runbook",
    description:
      "Step-by-step procedures for diagnosing and resolving common ETL pipeline failures.",
    mime_type: "text/markdown",
    size_bytes: 34_816,
    s3_key: "resources/persona/data-engineer/runbooks/etl-runbook.md",
    uri: "s3://acme-platform/resources/persona/data-engineer/runbooks/etl-runbook.md",
    tags: ["etl", "runbook"],
    uploader_sub: "marcus-engineer",
    uploader_email: "marcus.johnson@example.com",
    created_at: daysAgo(25),
    updated_at: daysAgo(3),
  },
  {
    id: "res-006",
    scope: "persona",
    scope_id: "data-engineer",
    category: "checklists",
    filename: "migration-checklist.pdf",
    display_name: "Migration Checklist",
    description:
      "Pre-flight checklist for schema migrations. Covers impact assessment, rollback planning, and notifications.",
    mime_type: "application/pdf",
    size_bytes: 156_672,
    s3_key: "resources/persona/data-engineer/checklists/migration-checklist.pdf",
    uri: "s3://acme-platform/resources/persona/data-engineer/checklists/migration-checklist.pdf",
    tags: ["schema", "checklist"],
    uploader_sub: "amanda-engineer",
    uploader_email: "amanda.lee@example.com",
    created_at: daysAgo(40),
    updated_at: daysAgo(15),
  },
  {
    id: "res-007",
    scope: "persona",
    scope_id: "inventory-analyst",
    category: "reference",
    filename: "reorder-points.xlsx",
    display_name: "Reorder Points",
    description:
      "Excel workbook with formulas for calculating reorder points using lead time demand and safety stock.",
    mime_type: "application/xlsx",
    size_bytes: 287_744,
    s3_key: "resources/persona/inventory-analyst/reference/reorder-points.xlsx",
    uri: "s3://acme-platform/resources/persona/inventory-analyst/reference/reorder-points.xlsx",
    tags: ["inventory", "reference"],
    uploader_sub: "rachel-analyst",
    uploader_email: "rachel.thompson@example.com",
    created_at: daysAgo(35),
    updated_at: daysAgo(10),
  },
  {
    id: "res-008",
    scope: "persona",
    scope_id: "inventory-analyst",
    category: "reference",
    filename: "seasonal-factors.csv",
    display_name: "Seasonal Factors",
    description:
      "Monthly seasonal adjustment multipliers by product category. Used for demand forecasting normalization.",
    mime_type: "text/csv",
    size_bytes: 12_288,
    s3_key: "resources/persona/inventory-analyst/reference/seasonal-factors.csv",
    uri: "s3://acme-platform/resources/persona/inventory-analyst/reference/seasonal-factors.csv",
    tags: ["inventory", "forecasting"],
    uploader_sub: "rachel-analyst",
    uploader_email: "rachel.thompson@example.com",
    created_at: daysAgo(20),
    updated_at: daysAgo(20),
  },
  {
    id: "res-009",
    scope: "user",
    scope_id: "marcus-engineer",
    category: "queries",
    filename: "query-templates.sql",
    display_name: "Query Templates",
    description:
      "Frequently used SQL query templates for daily sales aggregation and inventory reconciliation.",
    mime_type: "application/sql",
    size_bytes: 8_192,
    s3_key: "resources/user/marcus-engineer/queries/query-templates.sql",
    uri: "s3://acme-platform/resources/user/marcus-engineer/queries/query-templates.sql",
    tags: ["sql", "templates"],
    uploader_sub: "marcus-engineer",
    uploader_email: "marcus.johnson@example.com",
    created_at: daysAgo(14),
    updated_at: hoursAgo(6),
  },
  {
    id: "res-010",
    scope: "user",
    scope_id: "rachel-analyst",
    category: "notes",
    filename: "dashboard-notes.md",
    display_name: "Dashboard Notes",
    description:
      "Working notes on KPI definitions and data source mappings for the regional inventory review.",
    mime_type: "text/markdown",
    size_bytes: 6_144,
    s3_key: "resources/user/rachel-analyst/notes/dashboard-notes.md",
    uri: "s3://acme-platform/resources/user/rachel-analyst/notes/dashboard-notes.md",
    tags: ["notes"],
    uploader_sub: "rachel-analyst",
    uploader_email: "rachel.thompson@example.com",
    created_at: daysAgo(7),
    updated_at: hoursAgo(18),
  },
  {
    id: "res-011",
    scope: "user",
    scope_id: "david-director",
    category: "reference",
    filename: "store-list.csv",
    display_name: "Store List",
    description:
      "Western region stores with location codes, square footage, and district manager assignments.",
    mime_type: "text/csv",
    size_bytes: 15_360,
    s3_key: "resources/user/david-director/reference/store-list.csv",
    uri: "s3://acme-platform/resources/user/david-director/reference/store-list.csv",
    tags: ["stores", "reference"],
    uploader_sub: "david-director",
    uploader_email: "david.park@example.com",
    created_at: daysAgo(10),
    updated_at: daysAgo(2),
  },
  {
    id: "res-012",
    scope: "user",
    scope_id: "emily-analyst",
    category: "reports",
    filename: "weekly-report.html",
    display_name: "Weekly Report",
    description:
      "HTML template for weekly inventory status reports with chart placeholders and summary tables.",
    mime_type: "text/html",
    size_bytes: 22_528,
    s3_key: "resources/user/emily-analyst/reports/weekly-report.html",
    uri: "s3://acme-platform/resources/user/emily-analyst/reports/weekly-report.html",
    tags: ["reports", "templates"],
    uploader_sub: "emily-analyst",
    uploader_email: "emily.watson@example.com",
    created_at: daysAgo(18),
    updated_at: daysAgo(4),
  },
];

export const mockResources = {
  resources,
  total: resources.length,
};

// A relative path rather than the "@/" alias: vite.config.ts imports this
// fixture module (through assetRefs) in a plain Node context where the alias is
// not configured, so a "@/" specifier there is resolved as a bare package name
// and the dev server fails to start.
import { isThemeable, isThumbnailSupported } from "../../lib/thumbnailSupport";

interface Resource {
  id: string;
  scope: "global" | "persona" | "user";
  scope_id: string;
  /** The folder path this resource is filed under inside its library. */
  path: string;
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
  last_read_at?: string;
  usage?: ResourceUsage;
  // The captured PNGs stored beside the resource's own object (#1554). A
  // themeable family carries both; one that brings its own colours carries only
  // the light key and serves it in both modes.
  thumbnail_s3_key?: string;
  thumbnail_dark_s3_key?: string;
  thumbnail_captured_at?: string;
  thumbnail_dark_captured_at?: string;
}

interface ResourceUsage {
  reads_30d: number;
  reads_90d: number;
  by_surface_30d?: Record<string, number>;
  last_read_at?: string;
}

interface ResourceVersion {
  resource_id: string;
  version: number;
  mime_type: string;
  size_bytes: number;
  s3_key: string;
  uploader_sub: string;
  uploader_email: string;
  restored_from?: number;
  // change_summary says why the content changed, for a revision the platform
  // wrote on the uploader's behalf. Absent for a revision somebody uploaded.
  change_summary?: string;
  created_at: string;
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
    path: "documentation",
    filename: "sql-style-guide.md",
    display_name: "SQL Style Guide",
    description:
      "SQL formatting and naming conventions for all data teams. Covers CTEs, join ordering, and alias rules.",
    mime_type: "text/markdown",
    size_bytes: 24_576,
    s3_key: "resources/global/documentation/sql-style-guide.md",
    uri: "s3://acme-platform/resources/global/documentation/sql-style-guide.md",
    tags: ["sql", "standards"],
    uploader_sub: "sarah-admin",
    uploader_email: "sarah.chen@example.com",
    created_at: daysAgo(45),
    updated_at: daysAgo(12),
    // Both captures, dated to the file's own last write, which is what makes
    // this resource NOT pending: it is the one that documents a stored tile and
    // the Recapture control beside it (#1568).
    thumbnail_s3_key: "resources/global/documentation/.thumbnail.png",
    thumbnail_dark_s3_key: "resources/global/documentation/.thumbnail_dark.png",
    thumbnail_captured_at: daysAgo(12),
    thumbnail_dark_captured_at: daysAgo(12),
  },
  {
    id: "res-002",
    scope: "global",
    scope_id: "",
    path: "templates/reporting",
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
    path: "documentation/architecture",
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
    path: "onboarding",
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
    path: "runbooks",
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
    path: "checklists",
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
    path: "reference/dictionaries",
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
    path: "reference/dictionaries",
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
    path: "queries",
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
    path: "notes",
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
    path: "reference/glossary",
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
    path: "reports",
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
  {
    id: "res-013",
    scope: "global",
    scope_id: "",
    path: "documentation/architecture",
    filename: "data-governance-policy.pdf",
    display_name: "Data Governance Policy",
    description:
      "Company-wide policy on data classification, retention, PII handling, and access review cadence.",
    mime_type: "application/pdf",
    size_bytes: 689_152,
    s3_key: "resources/global/documentation/data-governance-policy.pdf",
    uri: "s3://acme-platform/resources/global/documentation/data-governance-policy.pdf",
    tags: ["governance", "compliance", "pii"],
    uploader_sub: "sarah-admin",
    uploader_email: "sarah.chen@example.com",
    created_at: daysAgo(120),
    updated_at: daysAgo(6),
  },
  {
    id: "res-014",
    scope: "global",
    scope_id: "",
    path: "templates/reporting",
    filename: "incident-postmortem.md",
    display_name: "Incident Postmortem Template",
    description:
      "Blameless postmortem template with timeline, impact, root cause, and follow-up action sections.",
    mime_type: "text/markdown",
    size_bytes: 11_264,
    s3_key: "resources/global/templates/incident-postmortem.md",
    uri: "s3://acme-platform/resources/global/templates/incident-postmortem.md",
    tags: ["templates", "incidents"],
    uploader_sub: "sarah-admin",
    uploader_email: "sarah.chen@example.com",
    created_at: daysAgo(75),
    updated_at: daysAgo(22),
  },
  {
    id: "res-015",
    scope: "global",
    scope_id: "",
    path: "reference/glossary",
    filename: "glossary.csv",
    display_name: "Business Glossary Export",
    description:
      "Flat export of the DataHub business glossary: term, definition, owner, and related datasets.",
    mime_type: "text/csv",
    size_bytes: 142_336,
    s3_key: "resources/global/reference/glossary.csv",
    uri: "s3://acme-platform/resources/global/reference/glossary.csv",
    tags: ["glossary", "reference"],
    uploader_sub: "marcus-engineer",
    uploader_email: "marcus.johnson@example.com",
    created_at: daysAgo(50),
    updated_at: daysAgo(1),
  },
  {
    id: "res-016",
    scope: "global",
    scope_id: "",
    path: "onboarding",
    filename: "platform-architecture.svg",
    display_name: "Platform Architecture Diagram",
    description:
      "Annotated SVG of the platform: gateway, middleware chain, toolkits, and semantic layer.",
    mime_type: "image/svg+xml",
    size_bytes: 64_512,
    s3_key: "resources/global/onboarding/platform-architecture.svg",
    uri: "s3://acme-platform/resources/global/onboarding/platform-architecture.svg",
    tags: ["onboarding", "architecture"],
    uploader_sub: "sarah-admin",
    uploader_email: "sarah.chen@example.com",
    created_at: daysAgo(88),
    updated_at: daysAgo(9),
  },
  {
    id: "res-017",
    scope: "persona",
    scope_id: "data-engineer",
    path: "runbooks",
    filename: "trino-tuning.md",
    display_name: "Trino Performance Tuning",
    description:
      "Runbook for diagnosing slow Trino queries: spilled joins, skewed partitions, and worker scaling.",
    mime_type: "text/markdown",
    size_bytes: 41_984,
    s3_key: "resources/persona/data-engineer/runbooks/trino-tuning.md",
    uri: "s3://acme-platform/resources/persona/data-engineer/runbooks/trino-tuning.md",
    tags: ["trino", "performance", "runbook"],
    uploader_sub: "amanda-engineer",
    uploader_email: "amanda.lee@example.com",
    created_at: daysAgo(28),
    updated_at: hoursAgo(30),
  },
  {
    id: "res-018",
    scope: "persona",
    scope_id: "data-engineer",
    path: "queries",
    filename: "data-quality-checks.sql",
    display_name: "Data Quality Checks",
    description:
      "Library of reusable SQL assertions: null rates, referential integrity, and freshness windows.",
    mime_type: "application/sql",
    size_bytes: 26_624,
    s3_key: "resources/persona/data-engineer/queries/data-quality-checks.sql",
    uri: "s3://acme-platform/resources/persona/data-engineer/queries/data-quality-checks.sql",
    tags: ["sql", "data-quality"],
    uploader_sub: "marcus-engineer",
    uploader_email: "marcus.johnson@example.com",
    created_at: daysAgo(33),
    updated_at: daysAgo(5),
  },
  {
    id: "res-019",
    scope: "persona",
    scope_id: "inventory-analyst",
    path: "runbooks",
    filename: "stockout-investigation.md",
    display_name: "Stockout Investigation",
    description:
      "How to trace a stockout from POS data back through replenishment, lead time, and supplier delays.",
    mime_type: "text/markdown",
    size_bytes: 19_456,
    s3_key: "resources/persona/inventory-analyst/runbooks/stockout-investigation.md",
    uri: "s3://acme-platform/resources/persona/inventory-analyst/runbooks/stockout-investigation.md",
    tags: ["inventory", "runbook"],
    uploader_sub: "rachel-analyst",
    uploader_email: "rachel.thompson@example.com",
    created_at: daysAgo(16),
    updated_at: daysAgo(2),
  },
  {
    id: "res-020",
    scope: "persona",
    scope_id: "regional-director",
    path: "reference",
    filename: "district-targets.xlsx",
    display_name: "District Targets",
    description:
      "Quarterly revenue and margin targets by district, with prior-year actuals for comparison.",
    mime_type: "application/xlsx",
    size_bytes: 318_464,
    s3_key: "resources/persona/regional-director/reference/district-targets.xlsx",
    uri: "s3://acme-platform/resources/persona/regional-director/reference/district-targets.xlsx",
    tags: ["targets", "reference"],
    uploader_sub: "david-director",
    uploader_email: "david.park@example.com",
    created_at: daysAgo(22),
    updated_at: daysAgo(7),
  },
  {
    id: "res-021",
    scope: "persona",
    scope_id: "finance-executive",
    path: "documentation",
    filename: "revenue-recognition.pdf",
    display_name: "Revenue Recognition Rules",
    description:
      "How revenue is recognized across channels: returns, store credits, gift cards, and deferred revenue.",
    mime_type: "application/pdf",
    size_bytes: 421_888,
    s3_key: "resources/persona/finance-executive/documentation/revenue-recognition.pdf",
    uri: "s3://acme-platform/resources/persona/finance-executive/documentation/revenue-recognition.pdf",
    tags: ["finance", "revenue"],
    uploader_sub: "sarah-admin",
    uploader_email: "sarah.chen@example.com",
    created_at: daysAgo(64),
    updated_at: daysAgo(19),
  },
  {
    id: "res-022",
    scope: "persona",
    scope_id: "store-manager",
    path: "checklists",
    filename: "daily-open-close.md",
    display_name: "Daily Open/Close Checklist",
    description:
      "Store opening and closing procedures, including the end-of-day reconciliation data submission.",
    mime_type: "text/markdown",
    size_bytes: 9_216,
    s3_key: "resources/persona/store-manager/checklists/daily-open-close.md",
    uri: "s3://acme-platform/resources/persona/store-manager/checklists/daily-open-close.md",
    tags: ["stores", "checklist"],
    uploader_sub: "david-director",
    uploader_email: "david.park@example.com",
    created_at: daysAgo(44),
    updated_at: daysAgo(11),
  },
  {
    id: "res-023",
    scope: "user",
    scope_id: "marcus-engineer",
    path: "notes",
    filename: "lineage-debug.md",
    display_name: "Lineage Debug Notes",
    description:
      "Working notes tracing a broken lineage edge between the orders and fulfillment datasets.",
    mime_type: "text/markdown",
    size_bytes: 7_680,
    s3_key: "resources/user/marcus-engineer/notes/lineage-debug.md",
    uri: "s3://acme-platform/resources/user/marcus-engineer/notes/lineage-debug.md",
    tags: ["lineage", "notes"],
    uploader_sub: "marcus-engineer",
    uploader_email: "marcus.johnson@example.com",
    created_at: daysAgo(5),
    updated_at: hoursAgo(3),
  },
  {
    id: "res-024",
    scope: "user",
    scope_id: "amanda-engineer",
    path: "queries",
    filename: "backfill-helpers.sql",
    display_name: "Backfill Helpers",
    description:
      "Parameterized backfill queries for re-running daily aggregates over a historical date range.",
    mime_type: "application/sql",
    size_bytes: 13_312,
    s3_key: "resources/user/amanda-engineer/queries/backfill-helpers.sql",
    uri: "s3://acme-platform/resources/user/amanda-engineer/queries/backfill-helpers.sql",
    tags: ["sql", "backfill"],
    uploader_sub: "amanda-engineer",
    uploader_email: "amanda.lee@example.com",
    created_at: daysAgo(9),
    updated_at: daysAgo(1),
  },
  {
    id: "res-025",
    scope: "user",
    scope_id: "david-director",
    path: "reports",
    filename: "qbr-deck.pdf",
    display_name: "QBR Deck",
    description:
      "Quarterly business review deck for the western region with store performance highlights.",
    mime_type: "application/pdf",
    size_bytes: 2_457_600,
    s3_key: "resources/user/david-director/reports/qbr-deck.pdf",
    uri: "s3://acme-platform/resources/user/david-director/reports/qbr-deck.pdf",
    tags: ["reports", "qbr"],
    uploader_sub: "david-director",
    uploader_email: "david.park@example.com",
    created_at: daysAgo(13),
    updated_at: daysAgo(3),
  },
  {
    id: "res-026",
    scope: "user",
    scope_id: "emily-analyst",
    path: "notes",
    filename: "forecast-assumptions.md",
    display_name: "Forecast Assumptions",
    description:
      "Assumptions behind the holiday demand forecast: promo calendar, weather, and prior-year lift.",
    mime_type: "text/markdown",
    size_bytes: 10_240,
    s3_key: "resources/user/emily-analyst/notes/forecast-assumptions.md",
    uri: "s3://acme-platform/resources/user/emily-analyst/notes/forecast-assumptions.md",
    tags: ["forecasting", "notes"],
    uploader_sub: "emily-analyst",
    uploader_email: "emily.watson@example.com",
    created_at: daysAgo(6),
    updated_at: hoursAgo(20),
  },
  {
    id: "res-027",
    scope: "global",
    scope_id: "",
    path: "runbooks",
    filename: "oauth-troubleshooting.md",
    display_name: "OAuth Troubleshooting",
    description:
      "Diagnosing inbound OAuth failures for MCP clients: token refresh, audience mismatch, and clock skew.",
    mime_type: "text/markdown",
    size_bytes: 28_672,
    s3_key: "resources/global/runbooks/oauth-troubleshooting.md",
    uri: "s3://acme-platform/resources/global/runbooks/oauth-troubleshooting.md",
    tags: ["oauth", "runbook", "auth"],
    uploader_sub: "sarah-admin",
    uploader_email: "sarah.chen@example.com",
    created_at: daysAgo(38),
    updated_at: daysAgo(4),
  },
  {
    id: "res-028",
    scope: "global",
    scope_id: "",
    path: "templates",
    filename: "dashboard-starter.html",
    display_name: "Dashboard Starter",
    description:
      "Starter HTML asset wired to the platform's chart components for building shareable dashboards.",
    mime_type: "text/html",
    size_bytes: 134_144,
    s3_key: "resources/global/templates/dashboard-starter.html",
    uri: "s3://acme-platform/resources/global/templates/dashboard-starter.html",
    tags: ["templates", "dashboards"],
    uploader_sub: "marcus-engineer",
    uploader_email: "marcus.johnson@example.com",
    created_at: daysAgo(52),
    updated_at: daysAgo(14),
  },
  // A `visual` section for the data-engineer persona. A library fills up with
  // more than prose, and the page shows a section holding nothing but images as
  // a grid of tiles rather than as rows -- which a fixture library of documents
  // alone never reaches. The last one is past the tile size cutoff, so its tile
  // stands in for the image rather than pulling the whole object (#1471).
  {
    id: "res-029",
    scope: "persona",
    scope_id: "data-engineer",
    path: "visual",
    filename: "warehouse-floor.png",
    display_name: "Warehouse Floor Plan",
    description: "Annotated floor plan of the Portland distribution centre, revised after the mezzanine build.",
    mime_type: "image/png",
    size_bytes: 412_672,
    s3_key: "resources/persona/data-engineer/visual/warehouse-floor.png",
    uri: "s3://acme-platform/resources/persona/data-engineer/visual/warehouse-floor.png",
    tags: ["facilities", "portland"],
    uploader_sub: "marcus-engineer",
    uploader_email: "marcus.johnson@example.com",
    created_at: daysAgo(18),
    updated_at: hoursAgo(3),
  },
  {
    id: "res-030",
    scope: "persona",
    scope_id: "data-engineer",
    path: "visual",
    filename: "rack-elevation.png",
    display_name: "Rack Elevation",
    description: "Elevation drawing of the aisle-4 racking, used to label the bin identifiers in the inventory extract.",
    mime_type: "image/png",
    size_bytes: 268_288,
    s3_key: "resources/persona/data-engineer/visual/rack-elevation.png",
    uri: "s3://acme-platform/resources/persona/data-engineer/visual/rack-elevation.png",
    tags: ["facilities", "inventory"],
    uploader_sub: "amanda-engineer",
    uploader_email: "amanda.lee@example.com",
    created_at: daysAgo(16),
    updated_at: hoursAgo(6),
  },
  {
    id: "res-031",
    scope: "persona",
    scope_id: "data-engineer",
    path: "visual",
    filename: "loading-dock.png",
    display_name: "Loading Dock Layout",
    description: "Dock door numbering and staging lanes, the reference for the dock_id column in the shipments table.",
    mime_type: "image/png",
    size_bytes: 331_776,
    s3_key: "resources/persona/data-engineer/visual/loading-dock.png",
    uri: "s3://acme-platform/resources/persona/data-engineer/visual/loading-dock.png",
    tags: ["facilities", "logistics"],
    uploader_sub: "marcus-engineer",
    uploader_email: "marcus.johnson@example.com",
    created_at: daysAgo(11),
    updated_at: hoursAgo(9),
  },
  {
    id: "res-032",
    scope: "persona",
    scope_id: "data-engineer",
    path: "visual",
    filename: "pipeline-topology.png",
    display_name: "Pipeline Topology",
    description: "Full-resolution export of the ingestion topology poster, past the size a tile will load inline.",
    mime_type: "image/png",
    size_bytes: 6_291_456,
    s3_key: "resources/persona/data-engineer/visual/pipeline-topology.png",
    uri: "s3://acme-platform/resources/persona/data-engineer/visual/pipeline-topology.png",
    tags: ["etl", "architecture"],
    uploader_sub: "amanda-engineer",
    uploader_email: "amanda.lee@example.com",
    created_at: daysAgo(30),
    updated_at: hoursAgo(12),
  },
  {
    id: "res-033",
    scope: "user",
    scope_id: "rachel-analyst",
    path: "visual",
    filename: "store-frontage.png",
    display_name: "Store Frontage",
    description: "Frontage photograph of the Bellevue store, taken for the regional performance write-up.",
    mime_type: "image/png",
    size_bytes: 224_256,
    s3_key: "resources/user/rachel-analyst/visual/store-frontage.png",
    uri: "s3://acme-platform/resources/user/rachel-analyst/visual/store-frontage.png",
    tags: ["stores", "bellevue"],
    uploader_sub: "rachel-analyst",
    uploader_email: "rachel.thompson@example.com",
    created_at: daysAgo(8),
    updated_at: hoursAgo(2),
  },
  {
    id: "res-034",
    scope: "user",
    scope_id: "rachel-analyst",
    path: "visual",
    filename: "aisle-endcap.png",
    display_name: "Aisle Endcap",
    description: "Endcap display photographed during the promo audit, referenced by the promo compliance notes.",
    mime_type: "image/png",
    size_bytes: 189_440,
    s3_key: "resources/user/rachel-analyst/visual/aisle-endcap.png",
    uri: "s3://acme-platform/resources/user/rachel-analyst/visual/aisle-endcap.png",
    tags: ["stores", "promo"],
    uploader_sub: "rachel-analyst",
    uploader_email: "rachel.thompson@example.com",
    created_at: daysAgo(8),
    updated_at: hoursAgo(5),
  },
];


// The bytes each captured resource serves. The content endpoint falls back to
// a type-appropriate stub for everything else, but a fixture a screenshot
// opens has to carry content that reads as the file it claims to be: a CSV
// viewer renders whatever it is given as a table, so a placeholder string
// becomes a one-column table in the docs.
export const mockResourceContent: Record<string, string> = {
  "res-001": [
    "# SQL Style Guide",
    "",
    "Formatting and naming conventions for every team writing SQL against the",
    "warehouse. Reviewed quarterly by the data platform group.",
    "",
    "## Naming",
    "",
    "- Tables are plural and snake_case: `store_transactions`, not `StoreTxn`.",
    "- A date column ends in `_date`; a timestamp column ends in `_at`.",
    "- Aliases are meaningful, never single letters: `orders o` is banned,",
    "  `orders AS ord` is fine.",
    "",
    "## Common table expressions",
    "",
    "Prefer a CTE over a nested subquery once nesting reaches two levels. Name",
    "each CTE for the thing it produces, not for its position:",
    "",
    "```sql",
    "WITH regional_totals AS (",
    "    SELECT region_id, SUM(revenue) AS revenue",
    "    FROM warehouse.public.transactions",
    "    GROUP BY region_id",
    ")",
    "SELECT r.name, t.revenue",
    "FROM regional_totals AS t",
    "JOIN warehouse.public.regions AS r ON r.id = t.region_id",
    "ORDER BY t.revenue DESC",
    "```",
    "",
    "## Joins",
    "",
    "State the join type. `JOIN` alone is an inner join, but write `INNER JOIN`",
    "so the reader does not have to know that. Put the join key on its own line",
    "when the condition has more than one term.",
    "",
    "## Filters",
    "",
    "Always bound a query on a partitioned table by its partition column, even",
    "when the range is wide. An unbounded scan of `transactions` reads two years",
    "of data to answer a question about one week.",
    "",
  ].join("\n"),
  "res-009": [
    "-- Query templates for daily sales aggregation and inventory reconciliation.",
    "-- Bind :start_date and :end_date; both are inclusive.",
    "",
    "-- Daily revenue by region",
    "SELECT r.name AS region,",
    "       t.transaction_date,",
    "       SUM(ti.line_total) AS revenue,",
    "       COUNT(DISTINCT t.transaction_id) AS baskets",
    "FROM warehouse.public.transactions AS t",
    "INNER JOIN warehouse.public.transaction_items AS ti",
    "        ON ti.transaction_id = t.transaction_id",
    "INNER JOIN warehouse.public.stores AS s ON s.store_id = t.store_id",
    "INNER JOIN warehouse.public.regions AS r ON r.region_id = s.region_id",
    "WHERE t.transaction_date BETWEEN :start_date AND :end_date",
    "GROUP BY r.name, t.transaction_date",
    "ORDER BY t.transaction_date, revenue DESC;",
    "",
    "-- Inventory reconciliation: on-hand against reorder point",
    "SELECT s.store_code,",
    "       p.sku,",
    "       i.quantity_on_hand,",
    "       i.reorder_point,",
    "       i.quantity_on_hand - i.reorder_point AS cover",
    "FROM warehouse.public.inventory AS i",
    "INNER JOIN warehouse.public.products AS p ON p.product_id = i.product_id",
    "INNER JOIN warehouse.public.stores AS s ON s.store_id = i.store_id",
    "WHERE i.quantity_on_hand < i.reorder_point",
    "ORDER BY cover ASC;",
    "",
  ].join("\n"),
  "res-011": [
    "store_code,region,city,state,opened_on,square_feet",
    "STR-0142,West,Portland,OR,2019-03-04,24500",
    "STR-0148,West,Eugene,OR,2020-08-17,18200",
    "STR-0203,West,Sacramento,CA,2017-11-02,31000",
    "STR-0211,West,Fresno,CA,2021-01-25,22750",
    "STR-0219,West,Bakersfield,CA,2018-06-11,20100",
    "STR-0305,West,Reno,NV,2022-04-30,19850",
    "STR-0312,West,Boise,ID,2016-09-19,26400",
    "STR-0341,West,Spokane,WA,2020-02-14,21300",
    "STR-0350,West,Tacoma,WA,2015-05-08,28900",
    "STR-0377,West,Bellingham,WA,2023-07-21,17600",
    "",
  ].join("\n"),
  "res-015": [
    "term,definition,owner,related_dataset",
    "Net Revenue,Gross revenue less returns and discounts,finance@example.com,warehouse.public.transactions",
    "Basket,One completed transaction regardless of line count,analytics@example.com,warehouse.public.transactions",
    "Cover,Days of inventory remaining at current sell-through,ops@example.com,warehouse.public.inventory",
    "Comp Store,A store open for the full prior-year period,finance@example.com,warehouse.public.stores",
    "Reorder Point,Stock level at which replenishment is triggered,ops@example.com,warehouse.public.inventory",
    "Shrink,Inventory loss not explained by recorded sales,ops@example.com,warehouse.public.inventory",
    "Attach Rate,Share of baskets containing a given category,analytics@example.com,warehouse.public.transaction_items",
    "Sell-Through,Units sold as a share of units received,analytics@example.com,warehouse.public.inventory",
    "",
  ].join("\n"),
};

// Read activity for the resources the detail view is opened on. Only a few
// carry usage: a library where every file is heavily read has nothing to teach
// a curator, and res-002 is deliberately left with no reads at all so the
// never-read flag has something to render.
export const mockResourceUsage: Record<string, ResourceUsage> = {
  "res-001": {
    reads_30d: 46,
    reads_90d: 118,
    by_surface_30d: { mcp_read: 31, fetch: 11, rest_download: 4 },
    last_read_at: hoursAgo(5),
  },
  "res-027": {
    reads_30d: 3,
    reads_90d: 12,
    by_surface_30d: { mcp_read: 3 },
    last_read_at: daysAgo(9),
  },
};

// Version trails for the resources whose detail view exercises the panel:
// res-001 has been revised twice (the second a restore of v1), res-011 carries
// only what its owner uploaded until a registration corrects it (see
// recordCorrection below), and res-027 has been revised once.
export const mockResourceVersions: Record<string, ResourceVersion[]> = {
  "res-001": [
    {
      resource_id: "res-001",
      version: 3,
      mime_type: "text/markdown",
      size_bytes: 24_576,
      s3_key: "resources/global/global/res-001/v/rev3/sql-style-guide.md",
      uploader_sub: "sarah-admin",
      uploader_email: "sarah.chen@example.com",
      restored_from: 1,
      created_at: daysAgo(2),
    },
    {
      resource_id: "res-001",
      version: 2,
      mime_type: "text/markdown",
      size_bytes: 25_190,
      s3_key: "resources/global/global/res-001/v/rev2/sql-style-guide.md",
      uploader_sub: "marcus-engineer",
      uploader_email: "marcus.johnson@example.com",
      created_at: daysAgo(11),
    },
    {
      resource_id: "res-001",
      version: 1,
      mime_type: "text/markdown",
      size_bytes: 24_576,
      s3_key: "resources/global/sql-style-guide.md",
      uploader_sub: "sarah-admin",
      uploader_email: "sarah.chen@example.com",
      created_at: daysAgo(45),
    },
  ],
  // The CSV a registration has to correct before it can read it (see
  // tornCSVSourceID in ./tables). Until the correction is taken the trail is
  // one uploaded version with nothing to say about why it exists.
  "res-011": [
    {
      resource_id: "res-011",
      version: 1,
      mime_type: "text/csv",
      size_bytes: 15_360,
      s3_key: "resources/user/david-director/reference/store-list.csv",
      uploader_sub: "david-director",
      uploader_email: "david.park@example.com",
      created_at: daysAgo(10),
    },
  ],
  "res-027": [
    {
      resource_id: "res-027",
      version: 1,
      mime_type: "text/markdown",
      size_bytes: 28_672,
      s3_key: "resources/global/runbooks/oauth-troubleshooting.md",
      uploader_sub: "sarah-admin",
      uploader_email: "sarah.chen@example.com",
      created_at: daysAgo(38),
    },
  ],
};

// isCorrected reports whether a file's head version is one the platform wrote,
// which is what makes it readable as a table. The refusal is keyed off it as
// well as the correction, so registering the same file twice in one session
// meets the second registration with what a second registration really meets:
// a file that is already correct.
export function isCorrected(resourceID: string): boolean {
  return !!mockResourceVersions[resourceID]?.[0]?.change_summary;
}

// recordCorrection is what a registration that corrected a file does to the
// file: a new version under a per-revision directory, carrying the sentence
// saying what changed, and the resource's head moved onto it. The two move
// together because that is the contract of the real store -- a head pointing at
// bytes no version row records is a broken state -- and because the panel is
// only worth capturing if taking the offer is what makes it change (#1450).
//
// It is idempotent: registering the same file twice in one session corrects it
// once, since the second registration reads a file that is already correct.
export function recordCorrection(resourceID: string, summary: string, by: string): void {
  const trail = mockResourceVersions[resourceID];
  const head = trail?.[0];
  const resource = resources.find((r) => r.id === resourceID);
  if (!trail || !head || !resource || isCorrected(resourceID)) {
    return;
  }
  const version = head.version + 1;
  const s3Key = `resources/${resource.scope}/${resource.scope_id || "global"}/${resourceID}/v/rev${version}/${resource.filename}`;
  // The revision is recorded against whoever asked for the correction, not the
  // person who uploaded the version before it: the real path builds its claims
  // from the caller. Its size is the corrected bytes, which are smaller than
  // the torn ones by the line breaks the correction took out.
  const corrected: ResourceVersion = {
    resource_id: resourceID,
    version,
    mime_type: head.mime_type,
    size_bytes: head.size_bytes - 94,
    s3_key: s3Key,
    uploader_sub: by.split("@")[0] ?? by,
    uploader_email: by,
    change_summary: summary,
    created_at: new Date().toISOString(),
  };
  trail.unshift(corrected);
  resource.s3_key = s3Key;
  resource.size_bytes = corrected.size_bytes;
  resource.updated_at = corrected.created_at;
}

// The list carries last_read_at (what the admin table sorts and flags on); the
// detail read is where the usage rollup is attached.
for (const r of resources) {
  const usage = mockResourceUsage[r.id];
  if (usage?.last_read_at) {
    r.last_read_at = usage.last_read_at;
  }
}

// Every fixture a browser could capture carries a settled capture, dated to the
// file's own last write, which is what the server compares against.
//
// The library has to be settled for the same reason the asset fixtures are: the
// capture queue is mounted in the portal shell, so an unsettled library is work
// on the main thread of EVERY page under test -- a content fetch and an
// html2canvas rasterization per file, on every navigation -- and the tests
// waiting on that thread time out. The one spec that wants a capture to happen
// marks its own subject stale (`__STALE_THUMBNAILS__`).
//
// res-001 is left as it was declared above: it is the fixture with a drawn tile
// behind it, which is what the library and the thumbnail-panel captures show.
for (const r of resources) {
  if (!isThumbnailSupported(r.mime_type)) continue;
  r.thumbnail_s3_key ??= `thumbnails/${r.id}.png`;
  r.thumbnail_captured_at ??= r.updated_at;
  if (isThemeable(r.mime_type)) {
    r.thumbnail_dark_s3_key ??= `thumbnails/${r.id}_dark.png`;
    r.thumbnail_dark_captured_at ??= r.updated_at;
  }
}

export const mockResources = {
  resources,
  total: resources.length,
  content: mockResourceContent,
};

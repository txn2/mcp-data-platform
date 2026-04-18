import type {
  Collection,
  SharedCollection,
} from "@/api/portal/types";

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

export const mockCollections: Collection[] = [
  {
    id: "col-001",
    owner_id: "user-alice",
    owner_email: "alice@example.com",
    name: "Q4 Performance Review",
    description:
      "Executive collection with revenue dashboards and KPI reports for the Q4 2025 board review.",
    config: { thumbnail_size: "large" },
    asset_tags: ["dashboard", "revenue", "q4-2025", "kpi"],
    sections: [
      {
        id: "sec-001-a",
        collection_id: "col-001",
        title: "Overview",
        description:
          "High-level revenue and KPI snapshots for executive consumption.",
        position: 0,
        items: [
          {
            id: "itm-001-a1",
            section_id: "sec-001-a",
            asset_id: "ast-001",
            position: 0,
            asset_name: "Q4 Revenue Dashboard",
            asset_content_type: "text/html",
            asset_description:
              "Interactive revenue breakdown by region and product line for Q4 2025.",
            created_at: daysAgo(3),
          },
          {
            id: "itm-001-a2",
            section_id: "sec-001-a",
            asset_id: "ast-004",
            position: 1,
            asset_name: "KPI Scorecard Component",
            asset_content_type: "text/jsx",
            asset_description:
              "React component showing key performance indicators with trend arrows.",
            created_at: daysAgo(3),
          },
          {
            id: "itm-001-a3",
            section_id: "sec-001-a",
            asset_id: "ast-008",
            position: 2,
            asset_name: "Regional Sales Summary",
            asset_content_type: "text/csv",
            asset_description:
              "CSV export of quarterly sales by region with revenue and unit counts.",
            created_at: daysAgo(3),
          },
        ],
        created_at: daysAgo(3),
      },
      {
        id: "sec-001-b",
        collection_id: "col-001",
        title: "Regional Analysis",
        description:
          "Store-by-store and regional performance drilldowns.",
        position: 1,
        items: [
          {
            id: "itm-001-b1",
            section_id: "sec-001-b",
            asset_id: "ast-007",
            position: 0,
            asset_name: "ACME Corp Sales Dashboard",
            asset_content_type: "text/jsx",
            asset_description:
              "Full interactive sales dashboard with recharts, tabs, KPI cards, and regional breakdowns.",
            created_at: daysAgo(2),
          },
          {
            id: "itm-001-b2",
            section_id: "sec-001-b",
            asset_id: "ast-002",
            position: 1,
            asset_name: "Sales Pipeline Chart",
            asset_content_type: "image/svg+xml",
            asset_description:
              "SVG visualization of the current sales pipeline stages.",
            created_at: daysAgo(2),
          },
        ],
        created_at: daysAgo(3),
      },
      {
        id: "sec-001-c",
        collection_id: "col-001",
        title: "Action Items",
        description:
          "Follow-up tasks and strategic recommendations from Q4 data.",
        position: 2,
        items: [
          {
            id: "itm-001-c1",
            section_id: "sec-001-c",
            asset_id: "ast-005",
            position: 0,
            asset_name: "Customer Segmentation Analysis",
            asset_content_type: "text/html",
            asset_description:
              "HTML report showing customer segments with purchasing behavior patterns.",
            created_at: daysAgo(1),
          },
          {
            id: "itm-001-c2",
            section_id: "sec-001-c",
            asset_id: "ast-006",
            position: 1,
            asset_name: "Data Quality Summary",
            asset_content_type: "text/markdown",
            asset_description:
              "Overview of data quality metrics across key tables.",
            created_at: daysAgo(1),
          },
        ],
        created_at: daysAgo(3),
      },
    ],
    created_at: daysAgo(3),
    updated_at: hoursAgo(4),
  },
  {
    id: "col-002",
    owner_id: "user-alice",
    owner_email: "alice@example.com",
    name: "Inventory Health Monitor",
    description:
      "Analyst collection tracking warehouse inventory levels and reorder thresholds across all ACME Corp facilities.",
    config: { thumbnail_size: "medium" },
    asset_tags: ["inventory", "warehouse", "monitoring"],
    sections: [
      {
        id: "sec-002-a",
        collection_id: "col-002",
        title: "Warehouse Status",
        description:
          "Current stock levels and capacity utilization by warehouse.",
        position: 0,
        items: [
          {
            id: "itm-002-a1",
            section_id: "sec-002-a",
            asset_id: "ast-003",
            position: 0,
            asset_name: "Weekly Inventory Report",
            asset_content_type: "text/markdown",
            asset_description:
              "Markdown summary of inventory levels across all warehouses.",
            created_at: daysAgo(5),
          },
          {
            id: "itm-002-a2",
            section_id: "sec-002-a",
            asset_id: "ast-008",
            position: 1,
            asset_name: "Regional Sales Summary",
            asset_content_type: "text/csv",
            asset_description:
              "CSV export of quarterly sales by region with revenue and unit counts.",
            created_at: daysAgo(5),
          },
          {
            id: "itm-002-a3",
            section_id: "sec-002-a",
            asset_id: "ast-004",
            position: 2,
            asset_name: "KPI Scorecard Component",
            asset_content_type: "text/jsx",
            asset_description:
              "React component showing key performance indicators with trend arrows.",
            created_at: daysAgo(4),
          },
        ],
        created_at: daysAgo(5),
      },
      {
        id: "sec-002-b",
        collection_id: "col-002",
        title: "Reorder Alerts",
        description:
          "Items approaching reorder thresholds and suggested purchase orders.",
        position: 1,
        items: [
          {
            id: "itm-002-b1",
            section_id: "sec-002-b",
            asset_id: "ast-006",
            position: 0,
            asset_name: "Data Quality Summary",
            asset_content_type: "text/markdown",
            asset_description:
              "Overview of data quality metrics across key tables.",
            created_at: daysAgo(4),
          },
          {
            id: "itm-002-b2",
            section_id: "sec-002-b",
            asset_id: "ast-001",
            position: 1,
            asset_name: "Q4 Revenue Dashboard",
            asset_content_type: "text/html",
            asset_description:
              "Interactive revenue breakdown by region and product line for Q4 2025.",
            created_at: daysAgo(4),
          },
        ],
        created_at: daysAgo(5),
      },
    ],
    created_at: daysAgo(5),
    updated_at: daysAgo(2),
  },
  {
    id: "col-003",
    owner_id: "user-alice",
    owner_email: "alice@example.com",
    name: "Data Quality Playbook",
    description:
      "Engineering collection documenting data quality issues discovered during Q4 audits and their remediation steps.",
    config: { thumbnail_size: "small" },
    asset_tags: ["data-quality", "engineering", "remediation"],
    sections: [
      {
        id: "sec-003-a",
        collection_id: "col-003",
        title: "Issues Found",
        description:
          "Catalog of data quality issues identified across ACME Corp data assets.",
        position: 0,
        items: [
          {
            id: "itm-003-a1",
            section_id: "sec-003-a",
            asset_id: "ast-006",
            position: 0,
            asset_name: "Data Quality Summary",
            asset_content_type: "text/markdown",
            asset_description:
              "Overview of data quality metrics across key tables.",
            created_at: daysAgo(8),
          },
          {
            id: "itm-003-a2",
            section_id: "sec-003-a",
            asset_id: "ast-ext-002",
            position: 1,
            asset_name: "API Latency Report",
            asset_content_type: "text/html",
            asset_description:
              "Performance analysis of API response times by endpoint.",
            created_at: daysAgo(8),
          },
          {
            id: "itm-003-a3",
            section_id: "sec-003-a",
            asset_id: "ast-003",
            position: 2,
            asset_name: "Weekly Inventory Report",
            asset_content_type: "text/markdown",
            asset_description:
              "Markdown summary of inventory levels across all warehouses.",
            created_at: daysAgo(7),
          },
        ],
        created_at: daysAgo(8),
      },
      {
        id: "sec-003-b",
        collection_id: "col-003",
        title: "Remediation Steps",
        description:
          "Corrective actions and validation queries for each identified issue.",
        position: 1,
        items: [
          {
            id: "itm-003-b1",
            section_id: "sec-003-b",
            asset_id: "ast-005",
            position: 0,
            asset_name: "Customer Segmentation Analysis",
            asset_content_type: "text/html",
            asset_description:
              "HTML report showing customer segments with purchasing behavior patterns.",
            created_at: daysAgo(7),
          },
          {
            id: "itm-003-b2",
            section_id: "sec-003-b",
            asset_id: "ast-004",
            position: 1,
            asset_name: "KPI Scorecard Component",
            asset_content_type: "text/jsx",
            asset_description:
              "React component showing key performance indicators with trend arrows.",
            created_at: daysAgo(6),
          },
        ],
        created_at: daysAgo(8),
      },
    ],
    created_at: daysAgo(8),
    updated_at: daysAgo(3),
  },
  {
    id: "col-004",
    owner_id: "user-alice",
    owner_email: "alice@example.com",
    name: "Sales Training Resources",
    description:
      "Shared training materials for new and experienced sales team members at ACME Corp retail locations.",
    config: { thumbnail_size: "medium" },
    asset_tags: ["training", "sales", "onboarding"],
    sections: [
      {
        id: "sec-004-a",
        collection_id: "col-004",
        title: "Onboarding",
        description:
          "Essential dashboards and reports for new sales associates to review during their first week.",
        position: 0,
        items: [
          {
            id: "itm-004-a1",
            section_id: "sec-004-a",
            asset_id: "ast-002",
            position: 0,
            asset_name: "Sales Pipeline Chart",
            asset_content_type: "image/svg+xml",
            asset_description:
              "SVG visualization of the current sales pipeline stages.",
            created_at: daysAgo(12),
          },
          {
            id: "itm-004-a2",
            section_id: "sec-004-a",
            asset_id: "ast-ext-001",
            position: 1,
            asset_name: "Monthly Sales Trends",
            asset_content_type: "image/svg+xml",
            asset_description:
              "Line chart showing month-over-month sales growth.",
            created_at: daysAgo(12),
          },
          {
            id: "itm-004-a3",
            section_id: "sec-004-a",
            asset_id: "ast-001",
            position: 2,
            asset_name: "Q4 Revenue Dashboard",
            asset_content_type: "text/html",
            asset_description:
              "Interactive revenue breakdown by region and product line for Q4 2025.",
            created_at: daysAgo(11),
          },
        ],
        created_at: daysAgo(12),
      },
      {
        id: "sec-004-b",
        collection_id: "col-004",
        title: "Advanced Analytics",
        description:
          "Deep-dive analytics tools for experienced sales staff targeting regional performance improvements.",
        position: 1,
        items: [
          {
            id: "itm-004-b1",
            section_id: "sec-004-b",
            asset_id: "ast-007",
            position: 0,
            asset_name: "ACME Corp Sales Dashboard",
            asset_content_type: "text/jsx",
            asset_description:
              "Full interactive sales dashboard with recharts, tabs, KPI cards, and regional breakdowns.",
            created_at: daysAgo(10),
          },
          {
            id: "itm-004-b2",
            section_id: "sec-004-b",
            asset_id: "ast-005",
            position: 1,
            asset_name: "Customer Segmentation Analysis",
            asset_content_type: "text/html",
            asset_description:
              "HTML report showing customer segments with purchasing behavior patterns.",
            created_at: daysAgo(10),
          },
          {
            id: "itm-004-b3",
            section_id: "sec-004-b",
            asset_id: "ast-008",
            position: 2,
            asset_name: "Regional Sales Summary",
            asset_content_type: "text/csv",
            asset_description:
              "CSV export of quarterly sales by region with revenue and unit counts.",
            created_at: daysAgo(9),
          },
        ],
        created_at: daysAgo(12),
      },
    ],
    created_at: daysAgo(12),
    updated_at: daysAgo(1),
  },
  {
    id: "col-005",
    owner_id: "user-alice",
    owner_email: "alice@example.com",
    name: "Regional Director Briefing Pack",
    description:
      "Monthly briefing materials prepared for ACME Corp regional directors covering store performance, market trends, and executive priorities.",
    config: { thumbnail_size: "large" },
    asset_tags: ["briefing", "executive", "regional", "monthly"],
    sections: [
      {
        id: "sec-005-a",
        collection_id: "col-005",
        title: "Executive Summary",
        description:
          "Top-line metrics and highlights for the monthly director meeting.",
        position: 0,
        items: [
          {
            id: "itm-005-a1",
            section_id: "sec-005-a",
            asset_id: "ast-004",
            position: 0,
            asset_name: "KPI Scorecard Component",
            asset_content_type: "text/jsx",
            asset_description:
              "React component showing key performance indicators with trend arrows.",
            created_at: daysAgo(6),
          },
          {
            id: "itm-005-a2",
            section_id: "sec-005-a",
            asset_id: "ast-001",
            position: 1,
            asset_name: "Q4 Revenue Dashboard",
            asset_content_type: "text/html",
            asset_description:
              "Interactive revenue breakdown by region and product line for Q4 2025.",
            created_at: daysAgo(6),
          },
        ],
        created_at: daysAgo(6),
      },
      {
        id: "sec-005-b",
        collection_id: "col-005",
        title: "Store Performance",
        description:
          "Individual store scorecards and comparative rankings across the region.",
        position: 1,
        items: [
          {
            id: "itm-005-b1",
            section_id: "sec-005-b",
            asset_id: "ast-007",
            position: 0,
            asset_name: "ACME Corp Sales Dashboard",
            asset_content_type: "text/jsx",
            asset_description:
              "Full interactive sales dashboard with recharts, tabs, KPI cards, and regional breakdowns.",
            created_at: daysAgo(5),
          },
          {
            id: "itm-005-b2",
            section_id: "sec-005-b",
            asset_id: "ast-003",
            position: 1,
            asset_name: "Weekly Inventory Report",
            asset_content_type: "text/markdown",
            asset_description:
              "Markdown summary of inventory levels across all warehouses.",
            created_at: daysAgo(5),
          },
          {
            id: "itm-005-b3",
            section_id: "sec-005-b",
            asset_id: "ast-008",
            position: 2,
            asset_name: "Regional Sales Summary",
            asset_content_type: "text/csv",
            asset_description:
              "CSV export of quarterly sales by region with revenue and unit counts.",
            created_at: daysAgo(5),
          },
        ],
        created_at: daysAgo(6),
      },
      {
        id: "sec-005-c",
        collection_id: "col-005",
        title: "Market Trends",
        description:
          "External market data and competitive positioning analysis for the retail sector.",
        position: 2,
        items: [
          {
            id: "itm-005-c1",
            section_id: "sec-005-c",
            asset_id: "ast-ext-001",
            position: 0,
            asset_name: "Monthly Sales Trends",
            asset_content_type: "image/svg+xml",
            asset_description:
              "Line chart showing month-over-month sales growth.",
            created_at: daysAgo(4),
          },
          {
            id: "itm-005-c2",
            section_id: "sec-005-c",
            asset_id: "ast-005",
            position: 1,
            asset_name: "Customer Segmentation Analysis",
            asset_content_type: "text/html",
            asset_description:
              "HTML report showing customer segments with purchasing behavior patterns.",
            created_at: daysAgo(4),
          },
        ],
        created_at: daysAgo(6),
      },
    ],
    created_at: daysAgo(6),
    updated_at: hoursAgo(12),
  },
  {
    id: "col-006",
    owner_id: "user-alice",
    owner_email: "alice@example.com",
    name: "New Product Launch Analysis",
    description:
      "Product team collection tracking the Sparkler Supreme fireworks launch with performance metrics and competitive intelligence.",
    config: { thumbnail_size: "medium" },
    asset_tags: ["product-launch", "fireworks", "competitive-analysis"],
    sections: [
      {
        id: "sec-006-a",
        collection_id: "col-006",
        title: "Launch Metrics",
        description:
          "Sales velocity, regional adoption, and customer reception data from the first 30 days post-launch.",
        position: 0,
        items: [
          {
            id: "itm-006-a1",
            section_id: "sec-006-a",
            asset_id: "ast-007",
            position: 0,
            asset_name: "ACME Corp Sales Dashboard",
            asset_content_type: "text/jsx",
            asset_description:
              "Full interactive sales dashboard with recharts, tabs, KPI cards, and regional breakdowns.",
            created_at: daysAgo(2),
          },
          {
            id: "itm-006-a2",
            section_id: "sec-006-a",
            asset_id: "ast-002",
            position: 1,
            asset_name: "Sales Pipeline Chart",
            asset_content_type: "image/svg+xml",
            asset_description:
              "SVG visualization of the current sales pipeline stages.",
            created_at: daysAgo(2),
          },
          {
            id: "itm-006-a3",
            section_id: "sec-006-a",
            asset_id: "ast-008",
            position: 2,
            asset_name: "Regional Sales Summary",
            asset_content_type: "text/csv",
            asset_description:
              "CSV export of quarterly sales by region with revenue and unit counts.",
            created_at: daysAgo(1),
          },
        ],
        created_at: daysAgo(2),
      },
      {
        id: "sec-006-b",
        collection_id: "col-006",
        title: "Competitive Landscape",
        description:
          "Market positioning and competitor product comparisons for the fireworks retail category.",
        position: 1,
        items: [
          {
            id: "itm-006-b1",
            section_id: "sec-006-b",
            asset_id: "ast-005",
            position: 0,
            asset_name: "Customer Segmentation Analysis",
            asset_content_type: "text/html",
            asset_description:
              "HTML report showing customer segments with purchasing behavior patterns.",
            created_at: daysAgo(1),
          },
          {
            id: "itm-006-b2",
            section_id: "sec-006-b",
            asset_id: "ast-ext-001",
            position: 1,
            asset_name: "Monthly Sales Trends",
            asset_content_type: "image/svg+xml",
            asset_description:
              "Line chart showing month-over-month sales growth.",
            created_at: hoursAgo(18),
          },
        ],
        created_at: daysAgo(2),
      },
    ],
    created_at: daysAgo(2),
    updated_at: hoursAgo(6),
  },
];

export const mockSharedCollections: SharedCollection[] = [
  {
    collection: {
      id: "col-ext-001",
      owner_id: "user-carol",
      owner_email: "carol@example.com",
      name: "West Region Store Analytics",
      description:
        "Carol's curated collection of store performance dashboards for the western region including foot traffic and conversion analysis.",
      config: { thumbnail_size: "large" },
      asset_tags: ["stores", "west-region", "analytics"],
      sections: [
        {
          id: "sec-ext-001-a",
          collection_id: "col-ext-001",
          title: "Performance Overview",
          description:
            "Regional KPIs and store rankings for the western territory.",
          position: 0,
          items: [
            {
              id: "itm-ext-001-a1",
              section_id: "sec-ext-001-a",
              asset_id: "ast-ext-001",
              position: 0,
              asset_name: "Monthly Sales Trends",
              asset_content_type: "image/svg+xml",
              asset_description:
                "Line chart showing month-over-month sales growth.",
              created_at: daysAgo(9),
            },
            {
              id: "itm-ext-001-a2",
              section_id: "sec-ext-001-a",
              asset_id: "ast-001",
              position: 1,
              asset_name: "Q4 Revenue Dashboard",
              asset_content_type: "text/html",
              asset_description:
                "Interactive revenue breakdown by region and product line for Q4 2025.",
              created_at: daysAgo(9),
            },
          ],
          created_at: daysAgo(9),
        },
        {
          id: "sec-ext-001-b",
          collection_id: "col-ext-001",
          title: "Store Deep Dives",
          description:
            "Individual store analysis with inventory and sales correlation data.",
          position: 1,
          items: [
            {
              id: "itm-ext-001-b1",
              section_id: "sec-ext-001-b",
              asset_id: "ast-003",
              position: 0,
              asset_name: "Weekly Inventory Report",
              asset_content_type: "text/markdown",
              asset_description:
                "Markdown summary of inventory levels across all warehouses.",
              created_at: daysAgo(8),
            },
            {
              id: "itm-ext-001-b2",
              section_id: "sec-ext-001-b",
              asset_id: "ast-007",
              position: 1,
              asset_name: "ACME Corp Sales Dashboard",
              asset_content_type: "text/jsx",
              asset_description:
                "Full interactive sales dashboard with recharts, tabs, KPI cards, and regional breakdowns.",
              created_at: daysAgo(8),
            },
          ],
          created_at: daysAgo(9),
        },
      ],
      created_at: daysAgo(9),
      updated_at: daysAgo(2),
    },
    share_id: "shr-col-ext-001",
    shared_by: "carol@example.com",
    shared_at: daysAgo(7),
    permission: "viewer",
  },
  {
    collection: {
      id: "col-ext-002",
      owner_id: "user-dave",
      owner_email: "dave@example.com",
      name: "Platform Health Dashboard Pack",
      description:
        "Dave's engineering collection monitoring API performance, data pipeline health, and system reliability metrics.",
      config: { thumbnail_size: "small" },
      asset_tags: ["engineering", "platform", "monitoring", "api"],
      sections: [
        {
          id: "sec-ext-002-a",
          collection_id: "col-ext-002",
          title: "System Metrics",
          description:
            "API latency, throughput, and error rate tracking across all platform services.",
          position: 0,
          items: [
            {
              id: "itm-ext-002-a1",
              section_id: "sec-ext-002-a",
              asset_id: "ast-ext-002",
              position: 0,
              asset_name: "API Latency Report",
              asset_content_type: "text/html",
              asset_description:
                "Performance analysis of API response times by endpoint.",
              created_at: daysAgo(14),
            },
            {
              id: "itm-ext-002-a2",
              section_id: "sec-ext-002-a",
              asset_id: "ast-006",
              position: 1,
              asset_name: "Data Quality Summary",
              asset_content_type: "text/markdown",
              asset_description:
                "Overview of data quality metrics across key tables.",
              created_at: daysAgo(14),
            },
            {
              id: "itm-ext-002-a3",
              section_id: "sec-ext-002-a",
              asset_id: "ast-004",
              position: 2,
              asset_name: "KPI Scorecard Component",
              asset_content_type: "text/jsx",
              asset_description:
                "React component showing key performance indicators with trend arrows.",
              created_at: daysAgo(13),
            },
          ],
          created_at: daysAgo(14),
        },
        {
          id: "sec-ext-002-b",
          collection_id: "col-ext-002",
          title: "Pipeline Reliability",
          description:
            "Data pipeline success rates and processing time analysis for daily batch jobs.",
          position: 1,
          items: [
            {
              id: "itm-ext-002-b1",
              section_id: "sec-ext-002-b",
              asset_id: "ast-003",
              position: 0,
              asset_name: "Weekly Inventory Report",
              asset_content_type: "text/markdown",
              asset_description:
                "Markdown summary of inventory levels across all warehouses.",
              created_at: daysAgo(12),
            },
            {
              id: "itm-ext-002-b2",
              section_id: "sec-ext-002-b",
              asset_id: "ast-008",
              position: 1,
              asset_name: "Regional Sales Summary",
              asset_content_type: "text/csv",
              asset_description:
                "CSV export of quarterly sales by region with revenue and unit counts.",
              created_at: daysAgo(12),
            },
          ],
          created_at: daysAgo(14),
        },
      ],
      created_at: daysAgo(14),
      updated_at: daysAgo(5),
    },
    share_id: "shr-col-ext-002",
    shared_by: "dave@example.com",
    shared_at: daysAgo(10),
    permission: "editor",
  },
];

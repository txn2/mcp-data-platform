import type { Asset, Share, SharedAsset } from "@/api/portal/types";
import { agentSessions } from "./audit";
import { citationsFor } from "./calls";

// The first assets are attributed to sessions the audit mock actually
// recorded, so a session opened in the admin UI shows the asset it saved
// instead of an empty output section.

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

export const mockAssets: Asset[] = [
  {
    id: "ast-001",
    owner_id: "user-alice",
    owner_email: "alice@example.com",
    name: "Q4 Revenue Dashboard",
    description: "Interactive revenue breakdown by region and product line for Q4 2025.",
    content_type: "text/html",
    s3_bucket: "portal-assets",
    s3_key: "assets/ast-001.html",
    thumbnail_s3_key: "thumbnails/ast-001.png",
    size_bytes: 4_250,
    tags: ["dashboard", "revenue", "q4-2025"],
    provenance: {
      session_id: agentSessions[0]!,
      user_id: "user-alice",
      captures: [
        {
          tool: "save_asset",
          captured_at: daysAgo(3),
          version: 1,
          session_id: agentSessions[0]!,
          event_ids: ["evt-q4-1", "evt-q4-2"],
          calls: [
            {
              event_id: "evt-q4-1",
              kind: "tool",
              tool: "datahub_get_entity",
              connection: "acme-catalog",
              summary: "urn:li:dataset:(urn:li:dataPlatform:trino,sales.quarterly,PROD)",
              purpose: "Checking the ownership and freshness of the revenue table before charting it.",
              outcome: "success",
              duration_ms: 212,
              timestamp: daysAgo(3),
            },
            {
              event_id: "evt-q4-2",
              kind: "sql",
              tool: "trino_query",
              connection: "warehouse",
              statement: "SELECT region, SUM(revenue) FROM sales.quarterly GROUP BY region",
              purpose: "Totalling Q4 revenue by region for the board dashboard.",
              outcome: "success",
              duration_ms: 1_840,
              timestamp: daysAgo(3),
            },
          ],
        },
        {
          tool: "manage_asset",
          captured_at: daysAgo(1),
          version: 5,
          session_id: agentSessions[0]!,
          explicit: true,
          event_ids: ["evt-q4-3"],
          calls: [
            {
              event_id: "evt-q4-3",
              kind: "api",
              tool: "api_invoke_endpoint",
              connection: "crm",
              method: "GET",
              path: "/v1/accounts?segment=enterprise",
              operation_id: "listAccounts",
              purpose: "Adding the enterprise account names the revenue split is broken out by.",
              outcome: "success",
              duration_ms: 640,
              timestamp: daysAgo(1),
            },
          ],
        },
      ],
    },
    session_id: agentSessions[0]!,
    current_version: 5,
    created_at: daysAgo(3),
    updated_at: daysAgo(1),
  },
  {
    id: "ast-002",
    owner_id: "user-alice",
    owner_email: "alice@example.com",
    name: "Sales Pipeline Chart",
    description: "SVG visualization of the current sales pipeline stages.",
    content_type: "image/svg+xml",
    s3_bucket: "portal-assets",
    s3_key: "assets/ast-002.svg",
    thumbnail_s3_key: "thumbnails/ast-002.png",
    size_bytes: 6_800,
    tags: ["chart", "sales", "pipeline"],
    // Kept in the pre-#1320 shape: the panel renders both, and an asset saved
    // before the platform recorded sources by reference still reads.
    provenance: {
      session_id: agentSessions[1]!,
      user_id: "user-alice",
      tool_calls: [
        { tool_name: "trino_query", timestamp: daysAgo(5), parameters: { sql: "SELECT stage, COUNT(*) FROM sales.pipeline GROUP BY stage" } },
        { tool_name: "save_asset", timestamp: daysAgo(5), parameters: { name: "Sales Pipeline Chart" } },
      ],
    },
    session_id: agentSessions[1]!,
    current_version: 3,
    created_at: daysAgo(5),
    updated_at: daysAgo(5),
  },
  {
    id: "ast-003",
    owner_id: "user-alice",
    owner_email: "alice@example.com",
    name: "Weekly Inventory Report",
    description: "Markdown summary of inventory levels across all warehouses.",
    content_type: "text/markdown",
    thumbnail_s3_key: "thumbnails/ast-003.png",
    s3_bucket: "portal-assets",
    s3_key: "assets/ast-003.md",
    size_bytes: 2_100,
    tags: ["report", "inventory", "weekly"],
    provenance: {
      session_id: agentSessions[2]!,
      user_id: "user-alice",
      captures: [
        {
          tool: "save_asset",
          captured_at: daysAgo(2),
          version: 1,
          session_id: agentSessions[2]!,
          event_ids: ["evt-inv-1", "evt-inv-2"],
          calls: [
            {
              event_id: "evt-inv-1",
              kind: "sql",
              tool: "trino_query",
              connection: "warehouse",
              statement: "SELECT warehouse, SUM(quantity) FROM inventory.level GROUP BY warehouse",
              purpose: "Summing stock per warehouse for the weekly report.",
              outcome: "error",
              error: "TABLE_NOT_FOUND: inventory.level",
              duration_ms: 95,
              timestamp: daysAgo(2),
            },
            {
              event_id: "evt-inv-2",
              kind: "sql",
              tool: "trino_query",
              connection: "warehouse",
              statement: "SELECT warehouse, SUM(qty) FROM inventory.levels GROUP BY warehouse",
              purpose: "Summing stock per warehouse for the weekly report.",
              outcome: "success",
              duration_ms: 1_120,
              timestamp: daysAgo(2),
            },
          ],
        },
      ],
    },
    session_id: agentSessions[2]!,
    current_version: 1,
    created_at: daysAgo(2),
    updated_at: daysAgo(2),
  },
  {
    id: "ast-004",
    owner_id: "user-alice",
    owner_email: "alice@example.com",
    name: "KPI Scorecard Component",
    description: "React component showing key performance indicators with trend arrows.",
    content_type: "text/jsx",
    s3_bucket: "portal-assets",
    s3_key: "assets/ast-004.jsx",
    thumbnail_s3_key: "thumbnails/ast-004.png",
    size_bytes: 3_400,
    tags: ["component", "kpi", "react"],
    provenance: {
      session_id: "sess-ddd",
      user_id: "user-alice",
      tool_calls: [
        { tool_name: "trino_query", timestamp: daysAgo(7), parameters: { sql: "SELECT metric, value, trend FROM analytics.kpi_scores" } },
        { tool_name: "datahub_search", timestamp: daysAgo(7), parameters: { query: "KPI definitions" } },
        { tool_name: "save_asset", timestamp: daysAgo(7), parameters: { name: "KPI Scorecard Component" } },
      ],
    },
    session_id: "sess-ddd",
    current_version: 1,
    created_at: daysAgo(7),
    updated_at: daysAgo(4),
  },
  {
    id: "ast-005",
    owner_id: "user-alice",
    owner_email: "alice@example.com",
    name: "Customer Segmentation Analysis",
    description: "HTML report showing customer segments with purchasing behavior patterns.",
    content_type: "text/html",
    thumbnail_s3_key: "thumbnails/ast-005.png",
    s3_bucket: "portal-assets",
    s3_key: "assets/ast-005.html",
    size_bytes: 5_900,
    tags: ["analysis", "customers", "segmentation"],
    provenance: {
      session_id: "sess-eee",
      user_id: "user-alice",
      tool_calls: [
        { tool_name: "trino_query", timestamp: daysAgo(10), parameters: { sql: "SELECT customer_id, purchase_date, amount FROM sales.transactions" } },
        { tool_name: "trino_query", timestamp: daysAgo(10), parameters: { sql: "SELECT customer_id, recency, frequency, monetary FROM analytics.rfm_scores" } },
        { tool_name: "save_asset", timestamp: daysAgo(10), parameters: { name: "Customer Segmentation Analysis" } },
      ],
    },
    session_id: "sess-eee",
    current_version: 1,
    created_at: daysAgo(10),
    updated_at: daysAgo(10),
  },
  {
    id: "ast-006",
    owner_id: "user-alice",
    owner_email: "alice@example.com",
    name: "Data Quality Summary",
    description: "Overview of data quality metrics across key tables.",
    content_type: "text/markdown",
    thumbnail_s3_key: "thumbnails/ast-006.png",
    s3_bucket: "portal-assets",
    s3_key: "assets/ast-006.md",
    size_bytes: 1_800,
    tags: ["data-quality", "monitoring"],
    provenance: {
      session_id: "sess-fff",
      user_id: "user-alice",
      tool_calls: [
        { tool_name: "datahub_search", timestamp: daysAgo(1), parameters: { query: "tables with quality issues" } },
        { tool_name: "save_asset", timestamp: daysAgo(1), parameters: { name: "Data Quality Summary" } },
      ],
    },
    session_id: "sess-fff",
    current_version: 1,
    created_at: daysAgo(1),
    updated_at: daysAgo(1),
  },
  {
    id: "ast-007",
    owner_id: "user-alice",
    owner_email: "alice@example.com",
    name: "ACME Corp Sales Dashboard",
    description: "Full interactive sales dashboard with recharts, tabs, KPI cards, and regional breakdowns. Tests complex JSX rendering.",
    content_type: "text/jsx",
    thumbnail_s3_key: "thumbnails/ast-007.png",
    s3_bucket: "portal-assets",
    s3_key: "assets/ast-007.jsx",
    size_bytes: 25_400,
    tags: ["dashboard", "sales", "jsx", "recharts"],
    provenance: {
      session_id: "sess-ggg",
      user_id: "user-alice",
      tool_calls: [
        { tool_name: "trino_query", timestamp: daysAgo(1), parameters: { sql: "SELECT store, year, SUM(revenue) FROM sales.annual GROUP BY store, year" } },
        { tool_name: "datahub_search", timestamp: daysAgo(1), parameters: { query: "store metadata" } },
        { tool_name: "save_asset", timestamp: daysAgo(1), parameters: { name: "ACME Corp Sales Dashboard" } },
      ],
    },
    session_id: "sess-ggg",
    current_version: 2,
    created_at: daysAgo(1),
    updated_at: daysAgo(1),
  },
  {
    id: "ast-008",
    owner_id: "user-alice",
    owner_email: "alice@example.com",
    name: "Regional Sales Summary",
    description: "CSV export of quarterly sales by region with revenue and unit counts.",
    content_type: "text/csv",
    thumbnail_s3_key: "thumbnails/ast-008.png",
    s3_bucket: "portal-assets",
    s3_key: "assets/ast-008.csv",
    size_bytes: 1_240,
    tags: ["sales", "csv", "quarterly"],
    provenance: {
      session_id: "sess-hhh",
      user_id: "user-alice",
      tool_calls: [
        { tool_name: "trino_query", timestamp: daysAgo(2), parameters: { sql: "SELECT region, quarter, revenue, units FROM sales.regional" } },
        { tool_name: "save_asset", timestamp: daysAgo(2), parameters: { name: "Regional Sales Summary" } },
      ],
    },
    session_id: "sess-hhh",
    current_version: 1,
    created_at: daysAgo(2),
    updated_at: daysAgo(2),
  },
];

export const mockShares: Record<string, Share[]> = {
  "ast-001": [
    {
      id: "shr-001",
      asset_id: "ast-001",
      token: "tok_abc123def456ghi789jkl012mno345pq",
      created_by: "user-alice",
      permission: "viewer",
      access_mode: "public",
      expires_at: new Date(now.getTime() + 24 * 60 * 60 * 1000).toISOString(),
      revoked: false,
      access_count: 12,
      last_accessed_at: hoursAgo(2),
      created_at: daysAgo(2),
    },
    {
      id: "shr-002",
      asset_id: "ast-001",
      token: "tok_zzz999yyy888xxx777www666vvv555uu",
      created_by: "user-alice",
      shared_with_user_id: "user-bob",
      permission: "editor",
      access_mode: "restricted",
      revoked: false,
      access_count: 3,
      last_accessed_at: daysAgo(1),
      created_at: daysAgo(2),
    },
  ],
  "ast-003": [
    {
      id: "shr-003",
      asset_id: "ast-003",
      token: "tok_rep111aaa222bbb333ccc444ddd555ee",
      created_by: "user-alice",
      permission: "viewer",
      access_mode: "public",
      expires_at: new Date(now.getTime() + 7 * 24 * 60 * 60 * 1000).toISOString(),
      revoked: false,
      access_count: 5,
      last_accessed_at: hoursAgo(6),
      created_at: daysAgo(1),
    },
  ],
};

export const mockSharedWithMe: SharedAsset[] = [
  {
    asset: {
      id: "ast-ext-001",
      owner_id: "user-carol",
      owner_email: "carol@example.com",
      name: "Monthly Sales Trends",
      description: "Line chart showing month-over-month sales growth.",
      content_type: "image/svg+xml",
      s3_bucket: "portal-assets",
      s3_key: "assets/ast-ext-001.svg",
      thumbnail_s3_key: "thumbnails/ast-ext-001.png",
      size_bytes: 4_200,
      tags: ["chart", "sales", "trends"],
      provenance: { session_id: "sess-ext1", user_id: "user-carol" },
      session_id: "sess-ext1",
      current_version: 1,
      created_at: daysAgo(4),
      updated_at: daysAgo(4),
    },
    share_id: "shr-ext-001",
    shared_by: "carol@example.com",
    shared_at: daysAgo(3),
    permission: "viewer",
  },
  {
    asset: {
      id: "ast-ext-002",
      owner_id: "user-dave",
      owner_email: "dave@example.com",
      name: "API Latency Report",
      description: "Performance analysis of API response times by endpoint.",
      content_type: "text/html",
      s3_bucket: "portal-assets",
      s3_key: "assets/ast-ext-002.html",
      thumbnail_s3_key: "thumbnails/ast-ext-002.png",
      size_bytes: 3_600,
      tags: ["performance", "api", "latency"],
      provenance: { session_id: "sess-ext2", user_id: "user-dave" },
      session_id: "sess-ext2",
      current_version: 1,
      created_at: daysAgo(6),
      updated_at: daysAgo(6),
    },
    share_id: "shr-ext-002",
    shared_by: "dave@example.com",
    shared_at: daysAgo(5),
    permission: "editor",
  },
];

// An asset that a recorded call produced NAMES that call in its own provenance,
// which is where the call catalog reads satisfaction from. Naming is the whole
// distinction: a call the default window swept up is recorded on the asset and
// is not evidence that it answered anything (#1353). The citations are applied
// here rather than written into the literals above because the ids belong to
// generated audit events: writing them by hand would be writing ids that do not
// exist.
for (const asset of mockAssets) {
  const citation = citationsFor(asset.id);
  if (!citation) continue;
  const { eventIDs, kind } = citation;
  asset.provenance = {
    ...(asset.provenance ?? {}),
    captures: [
      ...(asset.provenance?.captures ?? []),
      kind === "export"
        ? {
            // An export names one call without being asked to: the statement it
            // streamed into the file. It is cited per call, inside a capture the
            // caller did not name wholesale.
            tool: "trino_export",
            captured_at: asset.updated_at,
            version: asset.current_version,
            session_id: asset.session_id ?? "",
            event_ids: eventIDs,
            calls: eventIDs.map((id) => ({
              event_id: id,
              kind: "sql" as const,
              tool: "trino_export",
              connection: "warehouse",
              outcome: "success" as const,
              cited: true,
              timestamp: asset.updated_at,
            })),
          }
        : {
            // A save that named its sources: the whole capture is cited, which
            // is what `explicit` records.
            tool: "save_asset",
            captured_at: asset.updated_at,
            version: asset.current_version,
            session_id: asset.session_id ?? "",
            explicit: true,
            event_ids: eventIDs,
            calls: [],
          },
    ],
  };
}

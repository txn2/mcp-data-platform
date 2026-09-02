/**
 * The MCP Apps shipped with the platform, as screenshot subjects.
 *
 * An MCP App is a self-contained HTML document the platform serves as a UI
 * resource; an MCP client renders it in an iframe and talks to it over
 * postMessage. That is the whole contract, so capturing one needs no running
 * platform and no MCP client -- only the document, the `app-config` the server
 * injects into it, and a host that answers the same three messages a client
 * answers. `mcpapp.spec.ts` is that host.
 *
 * The fixture data below is the ACME Corp deployment the rest of the
 * screenshot corpus shows, so the apps read as the same deployment the portal
 * screenshots come from. It is example data: no real company, address, or
 * credential appears in it.
 */

export interface McpAppRoute {
  /** Capture slug; the file lands as `app-<slug>-<theme>.webp`. */
  slug: string;
  /** Directory under `apps/` holding the app's index.html. */
  dir: string;
  /** The tool whose result this app renders. */
  toolName: string;
  /** What the server injects into `<script id="app-config">` at serve time. */
  config: Record<string, unknown>;
  /** The tool result the host pushes, as the app would receive it. */
  data: unknown;
  /**
   * The panel the host offers. Width is host-controlled (these apps lay out
   * at the conversation width); height is a starting value only -- the app
   * reports its own through `ui/notifications/size-changed` and the capture
   * resizes to it, exactly as a client does.
   */
  size: { width: number; height: number };
  /**
   * A selector that exists only once the app has rendered its tool result.
   * It must be content-bearing: `body` matches an app that received nothing,
   * which would be captured as a blank panel by a passing test.
   */
  waitFor: string;
  /**
   * Extra views of the same app, each a click inside the iframe. An app with
   * tabs is not documented by its first tab alone, and its tabs are only
   * populated once the tool result arrives, so they are captured here rather
   * than being separate fixtures. The base view is always captured too.
   */
  views?: { suffix: string; click: string }[];
}

const brand = {
  brand_name: "ACME Corp",
  brand_url: "https://acme.example.com",
  logo_svg: "",
  logo_url: "",
};

export const mcpApps: McpAppRoute[] = [
  {
    slug: "platform-info",
    dir: "platform-info",
    toolName: "platform_info",
    config: brand,
    size: { width: 900, height: 500 },
    waitFor: "#content",
    views: [
      {
        suffix: "agent-instructions",
        click: '.tab-btn[data-tab="agent-instructions"]',
      },
    ],
    data: {
      name: "ACME Corp Data Platform",
      version: "1.126.7",
      description:
        "Use this MCP server for all questions about ACME Corp, a national retail company. Covers store locations, product catalog, sales transactions, customer data, and inventory management.",
      agent_instructions:
        "Discover before you act: call `search` first, then query. Persist every report with `save_asset` so it lands in the portal with a shareable link.",
      tags: [
        "ACME Corp",
        "retail",
        "sales",
        "transactions",
        "inventory",
        "stores",
        "products",
        "customers",
      ],
      portal_url: "https://portal.acme.example.com",
      toolkits: ["platform", "trino", "datahub", "s3"],
      toolkit_descriptions: {
        platform:
          "Core platform tools: deployment info, connection listing, and resource access.",
        trino: "Federated SQL across the warehouse and the object store.",
        datahub: "The metadata catalog: owners, tags, glossary terms, lineage.",
        s3: "Object storage for exports and uploaded reference material.",
      },
      features: {
        semantic_enrichment: true,
        query_enrichment: true,
        storage_enrichment: true,
        audit_logging: true,
        knowledge_capture: true,
      },
      // The one persona the caller's roles mapped to, which is what the
      // response carries; there is no roster of every persona in it.
      persona: {
        name: "analyst",
        display_name: "Data Analyst",
        description: "Reads the warehouse and publishes findings to the portal.",
      },
    },
  },
  {
    slug: "prompt-browser",
    dir: "prompt-browser",
    toolName: "manage_prompt",
    config: brand,
    size: { width: 900, height: 500 },
    // Text the app can only show once the prompt list arrived.
    waitFor: "text=Daily Sales Report",
    data: {
      prompts: [
        {
          id: "prompt_a1",
          name: "daily-sales-report",
          display_name: "Daily Sales Report",
          description:
            "Generate a daily sales summary by region with day-over-day deltas.",
          content:
            "Analyze sales data for {date} grouped by region. Compare against the prior day and call out any region moving more than 10%.",
          arguments: [
            {
              name: "date",
              description: "The date to analyze (YYYY-MM-DD)",
              required: true,
            },
          ],
          scope: "global",
          status: "approved",
          version: 4,
          approved_by: "sarah.chen@example.com",
          approved_at: "2026-07-01T14:30:00Z",
          tags: ["sales", "reporting"],
          collection_id: "col_1",
          run_count: 37,
          last_run_at: "2026-07-20T09:00:00Z",
          updated_at: "2026-07-01T14:30:00Z",
          enabled: true,
        },
        {
          id: "prompt_b2",
          name: "churn-investigation",
          display_name: "Churn Investigation",
          description:
            "Walk through the churn cohort tables and produce a suspect list.",
          content:
            "Investigate churn for the {segment} segment over the last {window} days. List the top accounts at risk with the signals behind each.",
          arguments: [
            {
              name: "segment",
              description: "Customer segment name",
              required: true,
            },
            {
              name: "window",
              description: "Lookback window in days",
              required: false,
            },
          ],
          scope: "persona",
          personas: ["analyst"],
          status: "approved",
          version: 2,
          approved_by: "sarah.chen@example.com",
          approved_at: "2026-06-15T10:00:00Z",
          tags: ["retention"],
          run_count: 12,
          updated_at: "2026-06-15T10:00:00Z",
          enabled: true,
        },
        {
          id: "prompt_c3",
          name: "inventory-reorder-check",
          display_name: "Inventory Reorder Check",
          description: "Personal working prompt, not yet promoted.",
          content:
            "List every SKU below its reorder point, with the store and the days of cover remaining.",
          arguments: [],
          scope: "personal",
          owner_email: "lisa.chang@example.com",
          status: "draft",
          version: 1,
          tags: [],
          run_count: 0,
          updated_at: "2026-07-18T08:00:00Z",
          enabled: true,
        },
      ],
      count: 3,
      collections: [{ id: "col_1", name: "Sales Reporting", prompt_count: 1 }],
    },
  },
  {
    slug: "query-results",
    dir: "query-results",
    toolName: "trino_query",
    config: {},
    size: { width: 900, height: 500 },
    // A cell from the result, so the wait proves the table rendered rows.
    waitFor: "text=Southeast",
    data: {
      columns: [
        { name: "region", type: "varchar" },
        { name: "stores", type: "integer" },
        { name: "revenue", type: "double" },
        { name: "units", type: "integer" },
      ],
      rows: [
        { region: "Northeast", stores: 71, revenue: 4820150.5, units: 118420 },
        { region: "Southeast", stores: 94, revenue: 6310475.75, units: 152880 },
        { region: "Midwest", stores: 66, revenue: 3985200.0, units: 101340 },
        { region: "Southwest", stores: 58, revenue: 3420980.25, units: 88710 },
        { region: "West", stores: 103, revenue: 7104330.0, units: 169255 },
      ],
      stats: {
        row_count: 5,
        duration_ms: 412,
        query_id: "20260829_101204_00042_ac9xq",
      },
    },
  },
];

import type { PersonaSummary, PersonaDetail } from "@/api/admin/types";
import { mockTools } from "./system";

// ---------------------------------------------------------------------------
// Pattern matching helper — resolves tool patterns against mockTools
// ---------------------------------------------------------------------------

function matchesPattern(toolName: string, pattern: string): boolean {
  if (pattern === "*") return true;
  if (pattern.endsWith("*")) {
    return toolName.startsWith(pattern.slice(0, -1));
  }
  return toolName === pattern;
}

function resolveTools(
  allow: string[],
  deny: string[],
): string[] {
  return mockTools
    .map((t) => `${t.name}:${t.connection}`)
    .filter((key) => {
      const name = key.split(":")[0]!;
      const allowed = allow.some((p) => matchesPattern(name, p));
      const denied = deny.some((p) => matchesPattern(name, p));
      return allowed && !denied;
    });
}

// ---------------------------------------------------------------------------
// Detail records — one per ACME persona
// ---------------------------------------------------------------------------

export const mockPersonaDetails: Record<string, PersonaDetail> = {
  admin: {
    name: "admin",
    display_name: "Administrator",
    description: "Full platform access for system administrators and DevOps engineers.",
    roles: ["admin"],
    priority: 0,
    allow_tools: ["*"],
    deny_tools: [],
    tools: resolveTools(["*"], []),
    context: {
      description_prefix:
        "You are speaking with a platform administrator who has full access to all data and tools.",
      agent_instructions_suffix:
        "Always provide detailed technical context. The admin may need raw URNs, query plans, and system-level details.",
    },
  },
  "data-engineer": {
    name: "data-engineer",
    display_name: "Data Engineer",
    description: "ETL pipeline development, schema management, and data quality monitoring.",
    roles: ["data_engineer"],
    priority: 10,
    allow_tools: ["trino_*", "datahub_*", "s3_*", "save_artifact"],
    deny_tools: ["capture_insight"],
    tools: resolveTools(
      ["trino_*", "datahub_*", "s3_*", "save_artifact"],
      ["capture_insight"],
    ),
    context: {
      description_prefix:
        "This user is a data engineer responsible for ETL pipelines and schema management.",
      agent_instructions_suffix:
        "Focus on schema details, data lineage, and query optimization. Include SQL examples when relevant.",
    },
  },
  "inventory-analyst": {
    name: "inventory-analyst",
    display_name: "Inventory Analyst",
    description: "Stock level monitoring, reorder analysis, and warehouse operations reporting.",
    roles: ["inventory_analyst"],
    priority: 20,
    allow_tools: [
      "trino_query",
      "trino_describe_table",
      "datahub_search",
      "datahub_get_entity",
      "s3_list_objects",
      "save_artifact",
      "capture_insight",
    ],
    deny_tools: ["trino_explain", "datahub_get_lineage", "s3_get_object"],
    tools: resolveTools(
      [
        "trino_query",
        "trino_describe_table",
        "datahub_search",
        "datahub_get_entity",
        "s3_list_objects",
        "save_artifact",
        "capture_insight",
      ],
      ["trino_explain", "datahub_get_lineage", "s3_get_object"],
    ),
    context: {
      description_prefix:
        "This user is an inventory analyst focused on stock levels, reorder points, and warehouse operations.",
      agent_instructions_suffix:
        "Always filter queries to inventory-related schemas. Present data in terms of stock levels, days-of-supply, and reorder status.",
    },
  },
  "regional-director": {
    name: "regional-director",
    display_name: "Regional Director",
    description: "Executive-level regional performance dashboards and KPI summaries.",
    roles: ["regional_director"],
    priority: 30,
    allow_tools: ["trino_query", "datahub_search", "save_artifact"],
    deny_tools: [
      "trino_explain",
      "trino_describe_table",
      "datahub_get_lineage",
      "datahub_get_schema",
      "s3_list_objects",
      "s3_get_object",
    ],
    tools: resolveTools(
      ["trino_query", "datahub_search", "save_artifact"],
      [
        "trino_explain",
        "trino_describe_table",
        "datahub_get_lineage",
        "datahub_get_schema",
        "s3_list_objects",
        "s3_get_object",
      ],
    ),
    context: {
      description_prefix:
        "This user is a Regional Director who needs high-level performance summaries, not technical details.",
      agent_instructions_suffix:
        "Present data as executive summaries with KPIs and trends. Avoid technical jargon, SQL, and raw table details. Use charts and visualizations when possible.",
    },
  },
  "finance-executive": {
    name: "finance-executive",
    display_name: "Finance Executive",
    description: "Financial reporting, revenue analysis, and budget variance tracking.",
    roles: ["finance_executive"],
    priority: 30,
    allow_tools: ["trino_query", "save_artifact"],
    deny_tools: [
      "trino_explain",
      "trino_describe_table",
      "trino_browse",
      "datahub_search",
      "datahub_get_entity",
      "datahub_get_schema",
      "datahub_get_lineage",
      "datahub_browse",
      "s3_list_objects",
      "s3_get_object",
      "s3_list_buckets",
      "capture_insight",
    ],
    tools: resolveTools(
      ["trino_query", "save_artifact"],
      [
        "trino_explain",
        "trino_describe_table",
        "trino_browse",
        "datahub_search",
        "datahub_get_entity",
        "datahub_get_schema",
        "datahub_get_lineage",
        "datahub_browse",
        "s3_list_objects",
        "s3_get_object",
        "s3_list_buckets",
        "capture_insight",
      ],
    ),
    context: {
      description_prefix:
        "This user is a Finance Executive who needs financial reports and revenue analysis.",
      agent_instructions_suffix:
        "Focus exclusively on financial metrics: revenue, margins, costs, and budget variance. All numbers should be in dollar amounts with appropriate formatting.",
    },
  },
  "store-manager": {
    name: "store-manager",
    display_name: "Store Manager",
    description: "Store-level operations: daily sales, inventory counts, staffing, and customer traffic.",
    roles: ["store_manager"],
    priority: 20,
    allow_tools: [
      "trino_query",
      "datahub_search",
      "save_artifact",
      "capture_insight",
    ],
    deny_tools: [
      "trino_explain",
      "datahub_get_lineage",
      "s3_list_objects",
      "s3_get_object",
    ],
    tools: resolveTools(
      [
        "trino_query",
        "datahub_search",
        "save_artifact",
        "capture_insight",
      ],
      [
        "trino_explain",
        "datahub_get_lineage",
        "s3_list_objects",
        "s3_get_object",
      ],
    ),
    context: {
      description_prefix:
        "This user is a Store Manager who needs store-level operational data.",
      agent_instructions_suffix:
        "Always filter data to the user's assigned store. Present information relevant to daily store operations: sales, inventory, staffing, and customer traffic.",
    },
  },
};

// ---------------------------------------------------------------------------
// Summary list (derived from details)
// ---------------------------------------------------------------------------

export const mockPersonas: PersonaSummary[] = Object.values(mockPersonaDetails)
  .map((d) => ({
    name: d.name,
    display_name: d.display_name,
    roles: d.roles,
    tool_count: d.tools.length,
  }))
  .sort((a, b) => a.name.localeCompare(b.name));

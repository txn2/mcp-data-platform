import type { PersonaSummary, PersonaDetail } from "@/api/types";
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
    roles: ["admin"],
    priority: 0,
    allow_tools: ["*"],
    deny_tools: [],
    tools: resolveTools(["*"], []),
    prompts: {
      system_prefix:
        "You are the ACME data platform administrator with full access.",
    },
  },
  "data-engineer": {
    name: "data-engineer",
    display_name: "Data Engineer",
    roles: ["data_engineer"],
    priority: 0,
    allow_tools: ["trino_*", "datahub_*", "s3_*"],
    deny_tools: ["s3_delete_*"],
    tools: resolveTools(["trino_*", "datahub_*", "s3_*"], ["s3_delete_*"]),
    prompts: {
      system_prefix:
        "You are helping an ACME data engineer build and maintain data pipelines.",
    },
    hints: {
      trino_query: "Use iceberg catalog for production tables",
      datahub_search: "Search the catalog before writing new queries",
    },
  },
  "inventory-analyst": {
    name: "inventory-analyst",
    display_name: "Inventory Analyst",
    roles: ["inventory_analyst"],
    priority: 0,
    allow_tools: [
      "trino_query",
      "trino_describe_table",
      "trino_list_*",
      "datahub_search",
      "datahub_get_entity",
      "datahub_get_schema",
      "s3_list_*",
    ],
    deny_tools: [],
    tools: resolveTools(
      [
        "trino_query",
        "trino_describe_table",
        "trino_list_*",
        "datahub_search",
        "datahub_get_entity",
        "datahub_get_schema",
        "s3_list_*",
      ],
      [],
    ),
    prompts: {
      system_prefix:
        "You are helping an ACME inventory analyst track stock levels across 1,000 stores. Focus on the inventory and retail schemas.",
    },
    hints: {
      trino_query: "Start with inventory.inventory_levels for current stock",
      datahub_search: "Check metadata before joining tables",
    },
  },
  "regional-director": {
    name: "regional-director",
    display_name: "Regional Director",
    roles: ["regional_director"],
    priority: 0,
    allow_tools: [
      "trino_query",
      "trino_describe_table",
      "datahub_search",
      "datahub_get_entity",
      "s3_list_objects",
      "s3_get_object",
    ],
    deny_tools: [],
    tools: resolveTools(
      [
        "trino_query",
        "trino_describe_table",
        "datahub_search",
        "datahub_get_entity",
        "s3_list_objects",
        "s3_get_object",
      ],
      [],
    ),
    prompts: {
      system_prefix:
        "You are providing regional performance data to an ACME regional director. Focus on the analytics schema and regional reports.",
    },
  },
  "finance-executive": {
    name: "finance-executive",
    display_name: "Finance Executive",
    roles: ["finance_executive"],
    priority: 0,
    allow_tools: [
      "datahub_search",
      "datahub_get_entity",
      "datahub_get_schema",
      "s3_list_objects",
      "s3_get_object",
    ],
    deny_tools: [],
    tools: resolveTools(
      [
        "datahub_search",
        "datahub_get_entity",
        "datahub_get_schema",
        "s3_list_objects",
        "s3_get_object",
      ],
      [],
    ),
    prompts: {
      system_prefix:
        "You are providing financial insights to an ACME executive. Focus on revenue, margins, and high-level KPIs. Avoid technical details.",
    },
  },
  "store-manager": {
    name: "store-manager",
    display_name: "Store Manager",
    roles: ["store_manager"],
    priority: 0,
    allow_tools: [
      "trino_query",
      "trino_describe_table",
      "datahub_search",
      "s3_get_object",
    ],
    deny_tools: [],
    tools: resolveTools(
      [
        "trino_query",
        "trino_describe_table",
        "datahub_search",
        "s3_get_object",
      ],
      [],
    ),
    prompts: {
      system_prefix:
        "You are helping an ACME store manager access store-level data. Focus on daily sales, inventory, and employee scheduling.",
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

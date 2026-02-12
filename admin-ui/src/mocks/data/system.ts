import type {
  SystemInfo,
  ToolInfo,
  ConnectionInfo,
} from "@/api/types";

export const mockSystemInfo: SystemInfo = {
  name: "mcp-data-platform",
  version: "0.18.0-dev",
  description: "Semantic data platform MCP server",
  transport: "http",
  config_mode: "database",
  features: {
    audit: true,
    oauth: false,
    knowledge: true,
    admin: true,
    database: true,
  },
  toolkit_count: 3,
  persona_count: 2,
};

export const mockTools: ToolInfo[] = [
  { name: "trino_query", toolkit: "production", kind: "trino", connection: "prod-trino" },
  { name: "trino_describe_table", toolkit: "production", kind: "trino", connection: "prod-trino" },
  { name: "trino_list_catalogs", toolkit: "production", kind: "trino", connection: "prod-trino" },
  { name: "trino_list_schemas", toolkit: "production", kind: "trino", connection: "prod-trino" },
  { name: "trino_list_tables", toolkit: "production", kind: "trino", connection: "prod-trino" },
  { name: "trino_explain", toolkit: "production", kind: "trino", connection: "prod-trino" },
  { name: "datahub_search", toolkit: "primary", kind: "datahub", connection: "prod-datahub" },
  { name: "datahub_get_entity", toolkit: "primary", kind: "datahub", connection: "prod-datahub" },
  { name: "datahub_get_schema", toolkit: "primary", kind: "datahub", connection: "prod-datahub" },
  { name: "datahub_get_lineage", toolkit: "primary", kind: "datahub", connection: "prod-datahub" },
  { name: "datahub_get_column_lineage", toolkit: "primary", kind: "datahub", connection: "prod-datahub" },
  { name: "s3_list_objects", toolkit: "data-lake", kind: "s3", connection: "prod-s3" },
  { name: "s3_get_object", toolkit: "data-lake", kind: "s3", connection: "prod-s3" },
  { name: "s3_list_buckets", toolkit: "data-lake", kind: "s3", connection: "prod-s3" },
];

export const mockConnections: ConnectionInfo[] = [
  {
    kind: "trino",
    name: "production",
    connection: "prod-trino",
    tools: ["trino_query", "trino_describe_table", "trino_list_catalogs", "trino_list_schemas", "trino_list_tables", "trino_explain"],
  },
  {
    kind: "datahub",
    name: "primary",
    connection: "prod-datahub",
    tools: ["datahub_search", "datahub_get_entity", "datahub_get_schema", "datahub_get_lineage", "datahub_get_column_lineage"],
  },
  {
    kind: "s3",
    name: "data-lake",
    connection: "prod-s3",
    tools: ["s3_list_objects", "s3_get_object", "s3_list_buckets"],
  },
];

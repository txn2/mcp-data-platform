import type {
  SystemInfo,
  ToolInfo,
  ConnectionInfo,
} from "@/api/types";

export const mockSystemInfo: SystemInfo = {
  name: "acme-data-platform",
  version: "1.4.2",
  commit: "a1b2c3d",
  build_date: "2025-01-15T10:30:00Z",
  description: "ACME Corporation Retail Data Platform",
  transport: "http",
  config_mode: "database",
  portal_title: "Admin Portal",
  portal_logo: "",
  portal_logo_light: "",
  portal_logo_dark: "",
  features: {
    audit: true,
    oauth: true,
    knowledge: true,
    admin: true,
    database: true,
  },
  toolkit_count: 6,
  persona_count: 6,
};

export const mockTools: ToolInfo[] = [
  // acme-warehouse (trino) — production data warehouse
  { name: "trino_query", toolkit: "acme-warehouse", kind: "trino", connection: "acme-warehouse" },
  { name: "trino_describe_table", toolkit: "acme-warehouse", kind: "trino", connection: "acme-warehouse" },
  { name: "trino_list_catalogs", toolkit: "acme-warehouse", kind: "trino", connection: "acme-warehouse" },
  { name: "trino_list_schemas", toolkit: "acme-warehouse", kind: "trino", connection: "acme-warehouse" },
  { name: "trino_list_tables", toolkit: "acme-warehouse", kind: "trino", connection: "acme-warehouse" },
  { name: "trino_explain", toolkit: "acme-warehouse", kind: "trino", connection: "acme-warehouse" },
  // acme-staging (trino) — staging environment
  { name: "trino_query", toolkit: "acme-staging", kind: "trino", connection: "acme-staging" },
  { name: "trino_describe_table", toolkit: "acme-staging", kind: "trino", connection: "acme-staging" },
  // acme-catalog (datahub) — production metadata catalog
  { name: "datahub_search", toolkit: "acme-catalog", kind: "datahub", connection: "acme-catalog" },
  { name: "datahub_get_entity", toolkit: "acme-catalog", kind: "datahub", connection: "acme-catalog" },
  { name: "datahub_get_schema", toolkit: "acme-catalog", kind: "datahub", connection: "acme-catalog" },
  { name: "datahub_get_lineage", toolkit: "acme-catalog", kind: "datahub", connection: "acme-catalog" },
  { name: "datahub_get_column_lineage", toolkit: "acme-catalog", kind: "datahub", connection: "acme-catalog" },
  // acme-catalog-staging (datahub) — staging catalog
  { name: "datahub_search", toolkit: "acme-catalog-staging", kind: "datahub", connection: "acme-catalog-staging" },
  { name: "datahub_get_entity", toolkit: "acme-catalog-staging", kind: "datahub", connection: "acme-catalog-staging" },
  // acme-data-lake (s3) — raw data lake
  { name: "s3_list_objects", toolkit: "acme-data-lake", kind: "s3", connection: "acme-data-lake" },
  { name: "s3_get_object", toolkit: "acme-data-lake", kind: "s3", connection: "acme-data-lake" },
  { name: "s3_list_buckets", toolkit: "acme-data-lake", kind: "s3", connection: "acme-data-lake" },
  // acme-reports (s3) — generated reports
  { name: "s3_list_objects", toolkit: "acme-reports", kind: "s3", connection: "acme-reports" },
  { name: "s3_get_object", toolkit: "acme-reports", kind: "s3", connection: "acme-reports" },
];

export const mockConnections: ConnectionInfo[] = [
  {
    kind: "trino",
    name: "acme-warehouse",
    connection: "acme-warehouse",
    tools: ["trino_query", "trino_describe_table", "trino_list_catalogs", "trino_list_schemas", "trino_list_tables", "trino_explain"],
    hidden_tools: ["trino_explain"],
  },
  {
    kind: "trino",
    name: "acme-staging",
    connection: "acme-staging",
    tools: ["trino_query", "trino_describe_table"],
    hidden_tools: [],
  },
  {
    kind: "datahub",
    name: "acme-catalog",
    connection: "acme-catalog",
    tools: ["datahub_search", "datahub_get_entity", "datahub_get_schema", "datahub_get_lineage", "datahub_get_column_lineage"],
    hidden_tools: [],
  },
  {
    kind: "datahub",
    name: "acme-catalog-staging",
    connection: "acme-catalog-staging",
    tools: ["datahub_search", "datahub_get_entity"],
    hidden_tools: ["datahub_search", "datahub_get_entity"],
  },
  {
    kind: "s3",
    name: "acme-data-lake",
    connection: "acme-data-lake",
    tools: ["s3_list_objects", "s3_get_object", "s3_list_buckets"],
    hidden_tools: [],
  },
  {
    kind: "s3",
    name: "acme-reports",
    connection: "acme-reports",
    tools: ["s3_list_objects", "s3_get_object"],
    hidden_tools: [],
  },
];

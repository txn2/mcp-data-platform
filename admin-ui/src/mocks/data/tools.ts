import type { ToolSchema, ToolCallResponse } from "@/api/types";

// ---------------------------------------------------------------------------
// Seeded PRNG (mulberry32) — deterministic mock results across page loads
// ---------------------------------------------------------------------------
function mulberry32(seed: number): () => number {
  let s = seed | 0;
  return () => {
    s = (s + 0x6d2b79f5) | 0;
    let t = Math.imul(s ^ (s >>> 15), 1 | s);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const rand = mulberry32(20260301);

function seededInt(min: number, max: number): number {
  return Math.floor(rand() * (max - min + 1)) + min;
}

// ---------------------------------------------------------------------------
// Tool Schemas — 14 unique tools
// ---------------------------------------------------------------------------

export const mockToolSchemas: Record<string, ToolSchema> = {
  trino_query: {
    name: "trino_query",
    kind: "trino",
    description:
      "Execute a SQL query against Trino and return results. Supports SELECT queries only. Results are limited to prevent excessive data transfer.",
    parameters: {
      type: "object",
      required: ["sql"],
      properties: {
        sql: {
          type: "string",
          description: "The SQL query to execute",
          format: "sql",
        },
        limit: {
          type: "integer",
          description: "Maximum number of rows to return",
          default: 100,
        },
        timeout_seconds: {
          type: "integer",
          description: "Query timeout in seconds",
          default: 30,
        },
        format: {
          type: "string",
          description: "Output format for results",
          enum: ["json", "csv", "markdown"],
          default: "markdown",
        },
      },
    },
  },
  trino_explain: {
    name: "trino_explain",
    kind: "trino",
    description:
      "Get the execution plan for a SQL query. Use this to understand how Trino will execute a query and identify potential performance issues.",
    parameters: {
      type: "object",
      required: ["sql"],
      properties: {
        sql: {
          type: "string",
          description: "The SQL query to explain",
          format: "sql",
        },
        type: {
          type: "string",
          description: "Type of explain output",
          enum: ["logical", "distributed", "io", "validate"],
          default: "logical",
        },
      },
    },
  },
  trino_list_catalogs: {
    name: "trino_list_catalogs",
    kind: "trino",
    description:
      "List all available catalogs in the Trino cluster. Catalogs are the top-level containers for schemas and tables.",
    parameters: {
      type: "object",
      required: [],
      properties: {},
    },
  },
  trino_list_schemas: {
    name: "trino_list_schemas",
    kind: "trino",
    description:
      "List all schemas in a catalog. Schemas are containers for tables within a catalog.",
    parameters: {
      type: "object",
      required: ["catalog"],
      properties: {
        catalog: {
          type: "string",
          description: "The catalog to list schemas from",
        },
      },
    },
  },
  trino_list_tables: {
    name: "trino_list_tables",
    kind: "trino",
    description:
      "List all tables in a schema. Optionally filter by a LIKE pattern.",
    parameters: {
      type: "object",
      required: ["catalog", "schema"],
      properties: {
        catalog: {
          type: "string",
          description: "The catalog containing the schema",
        },
        schema: {
          type: "string",
          description: "The schema to list tables from",
        },
        pattern: {
          type: "string",
          description: "Optional LIKE pattern to filter table names",
        },
      },
    },
  },
  trino_describe_table: {
    name: "trino_describe_table",
    kind: "trino",
    description:
      "Get detailed information about a table including column names, types, and optionally a sample of data.",
    parameters: {
      type: "object",
      required: ["catalog", "schema", "table"],
      properties: {
        catalog: {
          type: "string",
          description: "The catalog containing the table",
        },
        schema: {
          type: "string",
          description: "The schema containing the table",
        },
        table: {
          type: "string",
          description: "The table name to describe",
        },
        include_sample: {
          type: "boolean",
          description: "Include sample rows in the output",
          default: false,
        },
      },
    },
  },
  datahub_search: {
    name: "datahub_search",
    kind: "datahub",
    description:
      "Search for datasets, dashboards, pipelines, and other assets in the DataHub catalog. When a QueryProvider is configured, results include query_context showing which entities are queryable.",
    parameters: {
      type: "object",
      required: ["query"],
      properties: {
        query: {
          type: "string",
          description: "Search query string",
        },
        entity_type: {
          type: "string",
          description: "Filter by entity type",
          enum: ["DATASET", "DASHBOARD", "DATA_FLOW", "DATA_JOB"],
        },
        limit: {
          type: "integer",
          description: "Maximum number of results",
          default: 10,
        },
        offset: {
          type: "integer",
          description: "Offset for pagination",
          default: 0,
        },
      },
    },
  },
  datahub_get_entity: {
    name: "datahub_get_entity",
    kind: "datahub",
    description:
      "Get detailed metadata for a DataHub entity by its URN. When a QueryProvider (e.g., Trino) is configured, also returns: query_table (resolved table path), query_examples (generated sample SQL), query_availability (row count, availability status).",
    parameters: {
      type: "object",
      required: ["urn"],
      properties: {
        urn: {
          type: "string",
          description: "The DataHub URN of the entity",
          format: "urn",
        },
      },
    },
  },
  datahub_get_schema: {
    name: "datahub_get_schema",
    kind: "datahub",
    description:
      "Get the schema (fields, types, descriptions) for a dataset. Returns query_table (resolved table path) when QueryProvider is configured. For row counts and query examples, use datahub_get_entity instead.",
    parameters: {
      type: "object",
      required: ["urn"],
      properties: {
        urn: {
          type: "string",
          description: "The DataHub URN of the dataset",
          format: "urn",
        },
      },
    },
  },
  datahub_get_lineage: {
    name: "datahub_get_lineage",
    kind: "datahub",
    description:
      "Get upstream or downstream lineage for a DataHub entity. When a QueryProvider is configured, includes execution_context mapping URNs to query engine tables.",
    parameters: {
      type: "object",
      required: ["urn"],
      properties: {
        urn: {
          type: "string",
          description: "The DataHub URN of the entity",
          format: "urn",
        },
        direction: {
          type: "string",
          description: "Lineage direction",
          enum: ["UPSTREAM", "DOWNSTREAM"],
          default: "DOWNSTREAM",
        },
        depth: {
          type: "integer",
          description: "Maximum depth of lineage hops",
          default: 1,
        },
      },
    },
  },
  datahub_get_column_lineage: {
    name: "datahub_get_column_lineage",
    kind: "datahub",
    description:
      "Get fine-grained column-level lineage for a dataset. Returns mappings showing how downstream columns are derived from upstream columns. Useful for understanding data transformations at the field level.",
    parameters: {
      type: "object",
      required: ["urn"],
      properties: {
        urn: {
          type: "string",
          description: "The DataHub URN of the dataset",
          format: "urn",
        },
      },
    },
  },
  s3_list_buckets: {
    name: "s3_list_buckets",
    kind: "s3",
    description:
      "List all accessible S3 buckets. Returns bucket names and creation dates.",
    parameters: {
      type: "object",
      required: [],
      properties: {},
    },
  },
  s3_list_objects: {
    name: "s3_list_objects",
    kind: "s3",
    description:
      "List objects in an S3 bucket. Supports prefix filtering, delimiter for folder simulation, and pagination.",
    parameters: {
      type: "object",
      required: ["bucket"],
      properties: {
        bucket: {
          type: "string",
          description: "The S3 bucket name",
        },
        prefix: {
          type: "string",
          description: "Prefix to filter objects",
        },
        delimiter: {
          type: "string",
          description: "Delimiter for folder simulation (usually /)",
        },
        max_keys: {
          type: "integer",
          description: "Maximum number of objects to return",
          default: 1000,
        },
      },
    },
  },
  s3_get_object: {
    name: "s3_get_object",
    kind: "s3",
    description:
      "Retrieve the content of an S3 object. For text content, returns the content directly. For binary content, returns base64-encoded data. Large objects may be truncated based on size limits.",
    parameters: {
      type: "object",
      required: ["bucket", "key"],
      properties: {
        bucket: {
          type: "string",
          description: "The S3 bucket name",
        },
        key: {
          type: "string",
          description: "The object key (path)",
        },
      },
    },
  },
};

// ---------------------------------------------------------------------------
// Mock Result Generators — ACME-themed responses per tool
// ---------------------------------------------------------------------------

export function generateMockResult(
  toolName: string,
  params: Record<string, unknown>,
): ToolCallResponse {
  const duration = seededInt(50, 800);

  switch (toolName) {
    case "trino_query":
      return trinoQueryResult(params, duration);
    case "trino_explain":
      return trinoExplainResult(params, duration);
    case "trino_list_catalogs":
      return textResult(
        JSON.stringify(
          ["acme_warehouse", "iceberg", "system", "hive", "memory"],
          null,
          2,
        ),
        duration,
      );
    case "trino_list_schemas":
      return textResult(
        JSON.stringify(
          [
            "sales",
            "inventory",
            "finance",
            "analytics",
            "staging",
            "information_schema",
          ],
          null,
          2,
        ),
        duration,
      );
    case "trino_list_tables":
      return trinoListTablesResult(duration);
    case "trino_describe_table":
      return trinoDescribeResult(params, duration);
    case "datahub_search":
      return datahubSearchResult(params, duration);
    case "datahub_get_entity":
      return datahubEntityResult(params, duration);
    case "datahub_get_schema":
      return datahubSchemaResult(duration);
    case "datahub_get_lineage":
      return datahubLineageResult(params, duration);
    case "datahub_get_column_lineage":
      return datahubColumnLineageResult(duration);
    case "s3_list_buckets":
      return s3ListBucketsResult(duration);
    case "s3_list_objects":
      return s3ListObjectsResult(params, duration);
    case "s3_get_object":
      return s3GetObjectResult(params, duration);
    default:
      return textResult(`Unknown tool: ${toolName}`, duration);
  }
}

function textResult(text: string, duration: number, enrichmentBlocks?: string[]): ToolCallResponse {
  const content = [{ type: "text" as const, text }];
  if (enrichmentBlocks) {
    for (const block of enrichmentBlocks) {
      content.push({ type: "text" as const, text: block });
    }
  }
  return { content, is_error: false, duration_ms: duration };
}

// ---------------------------------------------------------------------------
// Enrichment blocks — cross-service context injected by middleware
// ---------------------------------------------------------------------------

/** Trino → DataHub: semantic context appended to Trino query/describe results */
function trinoSemanticEnrichment(table: string): string {
  return JSON.stringify({
    semantic_context: {
      description: `Daily aggregated sales figures for ACME Corporation — ${table}`,
      owners: [
        { owner: "marcus.johnson@acme-corp.com", type: "DATAOWNER" },
        { owner: "data-engineering", type: "DATAOWNER", group: true },
      ],
      tags: ["certified", "pii-free", "tier-1"],
      domain: { name: "Retail Analytics", urn: "urn:li:domain:retail-analytics" },
      quality_score: 0.94,
      deprecation: null,
      urn: `urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.sales.${table},PROD)`,
      glossary_terms: [
        { urn: "urn:li:glossaryTerm:Revenue", name: "Revenue" },
        { urn: "urn:li:glossaryTerm:RetailMetrics", name: "Retail Metrics" },
      ],
      custom_properties: {
        refresh_schedule: "daily @ 02:00 UTC",
        sla: "99.5% availability",
        retention_days: "730",
      },
      last_modified: { time: "2026-02-10T14:23:00Z", actor: "urn:li:corpuser:marcus.johnson" },
    },
    column_context: {
      revenue: {
        description: "Total revenue in USD for the period",
        glossary_terms: [{ urn: "urn:li:glossaryTerm:Revenue", name: "Revenue" }],
        tags: ["metric"],
        is_pii: false,
        is_sensitive: false,
      },
      store_id: {
        description: "Unique identifier for the ACME retail store",
        glossary_terms: [],
        tags: ["primary-key"],
        is_pii: false,
        is_sensitive: false,
      },
      customer_count: {
        description: "Distinct customer count for the period",
        glossary_terms: [],
        tags: ["metric"],
        is_pii: false,
        is_sensitive: false,
      },
    },
    inheritance_sources: [
      "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.staging.raw_pos_events,PROD)",
    ],
  }, null, 2);
}

/** DataHub → Trino: query availability appended to DataHub search/entity results */
function datahubQueryEnrichment(urns: string[]): string {
  const context: Record<string, unknown> = {};
  for (const urn of urns) {
    const table = urn.match(/trino,([^,]+),/)?.[1] ?? "iceberg.sales.daily_sales";
    context[urn] = {
      available: true,
      row_count: 145_230,
      query_table: table,
      last_queried: "2026-02-11T16:42:00Z",
      sample_queries: [
        {
          query: `SELECT * FROM ${table} LIMIT 10`,
          description: "Preview first 10 rows",
        },
        {
          query: `SELECT region, SUM(revenue) as total_revenue FROM ${table} GROUP BY region ORDER BY total_revenue DESC`,
          description: "Revenue by region",
        },
      ],
    };
  }
  return JSON.stringify({ query_context: context }, null, 2);
}

/** S3 → DataHub: semantic metadata for S3 locations */
function s3SemanticEnrichment(bucket: string, prefix: string): string {
  return JSON.stringify({
    semantic_context: {
      matching_datasets: [
        {
          urn: `urn:li:dataset:(urn:li:dataPlatform:s3,${bucket}/${prefix}daily_sales,PROD)`,
          name: "daily_sales",
          description: "Daily aggregated sales by store and region — raw Parquet files",
          owners: ["marcus.johnson@acme-corp.com"],
          tags: ["certified", "raw-data"],
          domain: "Retail Analytics",
          quality_score: 0.91,
        },
        {
          urn: `urn:li:dataset:(urn:li:dataPlatform:s3,${bucket}/${prefix}store_transactions,PROD)`,
          name: "store_transactions",
          description: "Raw POS transaction records from all ACME stores",
          owners: ["amanda.lee@acme-corp.com"],
          tags: ["pii", "raw-data"],
          domain: "Retail Analytics",
          quality_score: 0.87,
        },
      ],
      note: "Semantic metadata from DataHub for S3 location",
    },
  }, null, 2);
}

function trinoQueryResult(
  params: Record<string, unknown>,
  duration: number,
): ToolCallResponse {
  const sql = String(params.sql ?? "").toLowerCase();
  let text: string;

  if (sql.includes("inventory") || sql.includes("stock")) {
    text = [
      "| sku | product_name | warehouse | quantity | reorder_point | last_counted |",
      "|-----|-------------|-----------|----------|---------------|--------------|",
      "| FW-1001 | Phantom 500g Finale | Dallas-TX | 2,340 | 500 | 2026-02-10 |",
      "| FW-1002 | Thunder King 12pk | Dallas-TX | 1,856 | 400 | 2026-02-10 |",
      "| FW-1003 | Sparkle Fountain 6pk | Memphis-TN | 4,210 | 1,000 | 2026-02-09 |",
      "| FW-1004 | Roman Candle Assort | Memphis-TN | 987 | 300 | 2026-02-09 |",
      "| FW-1005 | Grand Patriot Box | Kansas City-MO | 3,145 | 800 | 2026-02-11 |",
      "",
      `5 rows returned (${duration}ms)`,
    ].join("\n");
  } else {
    text = [
      "| transaction_id | store_id | sale_date | total_amount | items | payment_method |",
      "|---------------|----------|-----------|-------------|-------|----------------|",
      "| TXN-90281 | ACME-042 | 2026-02-11 | $247.50 | 6 | credit_card |",
      "| TXN-90282 | ACME-042 | 2026-02-11 | $89.99 | 2 | cash |",
      "| TXN-90283 | ACME-015 | 2026-02-11 | $1,240.00 | 15 | credit_card |",
      "| TXN-90284 | ACME-015 | 2026-02-11 | $45.00 | 1 | debit_card |",
      "| TXN-90285 | ACME-108 | 2026-02-11 | $562.75 | 8 | credit_card |",
      "| TXN-90286 | ACME-108 | 2026-02-10 | $178.25 | 4 | cash |",
      "| TXN-90287 | ACME-023 | 2026-02-10 | $3,450.00 | 22 | credit_card |",
      "| TXN-90288 | ACME-023 | 2026-02-10 | $99.99 | 3 | debit_card |",
      "| TXN-90289 | ACME-067 | 2026-02-10 | $725.50 | 10 | credit_card |",
      "| TXN-90290 | ACME-067 | 2026-02-10 | $312.00 | 5 | cash |",
      "",
      `10 rows returned (${duration}ms)`,
    ].join("\n");
  }

  const table = sql.includes("inventory") ? "inventory_levels" : "store_transactions";
  return textResult(text, duration, [trinoSemanticEnrichment(table)]);
}

function trinoExplainResult(
  params: Record<string, unknown>,
  duration: number,
): ToolCallResponse {
  const sql = String(params.sql ?? "SELECT 1");
  const text = [
    `Query Plan for: ${sql}`,
    "",
    "Fragment 0 [SINGLE]",
    "    Output layout: [region, _col1]",
    "    Output partitioning: SINGLE []",
    "    - Output[columnNames = [region, total_revenue]] => [region:varchar, _col1:decimal(38,2)]",
    "        - Aggregate(FINAL)[region] => [region:varchar, _col1:decimal(38,2)]",
    "            _col1 := sum(_col1_0)",
    "        - LocalExchange[HASH][$hashvalue] (region) => [region:varchar, _col1_0:decimal(38,2), $hashvalue:bigint]",
    "            - RemoteSource[1] => [region:varchar, _col1_0:decimal(38,2), $hashvalue_1:bigint]",
    "",
    "Fragment 1 [SOURCE]",
    "    Output layout: [region, _col1_0, $hashvalue_2]",
    "    Output partitioning: HASH [region][$hashvalue_2]",
    "    - Aggregate(PARTIAL)[region] => [region:varchar, _col1_0:decimal(38,2), $hashvalue_2:bigint]",
    "            _col1_0 := sum(revenue)",
    "        - ScanFilterProject[table = iceberg:sales.daily_sales] => [region:varchar, revenue:decimal(12,2), $hashvalue_2:bigint]",
    "                Estimates: {rows: 145000, cpu: ?, memory: 0B, network: 0B}",
    "                revenue := 4:revenue:decimal(12,2)",
    "                region := 1:region:varchar",
    "",
    `Estimated cost: CPU 2.34s, Memory 128MB, Network 4.2MB`,
  ].join("\n");

  return textResult(text, duration, [trinoSemanticEnrichment("daily_sales")]);
}

function trinoListTablesResult(duration: number): ToolCallResponse {
  const tables = [
    { name: "daily_sales", type: "TABLE" },
    { name: "store_transactions", type: "TABLE" },
    { name: "inventory_levels", type: "TABLE" },
    { name: "product_catalog", type: "TABLE" },
    { name: "customer_segments", type: "TABLE" },
    { name: "regional_performance", type: "VIEW" },
    { name: "supply_chain_orders", type: "TABLE" },
    { name: "price_adjustments", type: "TABLE" },
    { name: "return_rates", type: "VIEW" },
    { name: "employee_schedules", type: "TABLE" },
  ];
  return textResult(JSON.stringify(tables, null, 2), duration);
}

function trinoDescribeResult(
  params: Record<string, unknown>,
  duration: number,
): ToolCallResponse {
  const tableName = String(params.table ?? "daily_sales");
  const includeSample = params.include_sample === true;

  const columns = [
    { name: "sale_date", type: "DATE", nullable: false, description: "Date of the sale" },
    { name: "store_id", type: "VARCHAR", nullable: false, description: "Unique store identifier" },
    { name: "region", type: "VARCHAR", nullable: false, description: "Geographic region" },
    { name: "revenue", type: "DECIMAL(12,2)", nullable: false, description: "Total revenue" },
    { name: "units_sold", type: "INTEGER", nullable: false, description: "Number of units sold" },
    { name: "avg_ticket", type: "DECIMAL(8,2)", nullable: true, description: "Average transaction amount" },
    { name: "customer_count", type: "INTEGER", nullable: true, description: "Unique customer count" },
  ];

  const result: Record<string, unknown> = {
    table: `${params.catalog ?? "iceberg"}.${params.schema ?? "sales"}.${tableName}`,
    columns,
    row_count: 145_230,
  };

  if (includeSample) {
    result.sample = [
      { sale_date: "2026-02-11", store_id: "ACME-042", region: "South", revenue: 12450.00, units_sold: 89, avg_ticket: 139.89, customer_count: 67 },
      { sale_date: "2026-02-11", store_id: "ACME-015", region: "Northeast", revenue: 18920.50, units_sold: 134, avg_ticket: 141.20, customer_count: 102 },
      { sale_date: "2026-02-11", store_id: "ACME-108", region: "Midwest", revenue: 9875.25, units_sold: 72, avg_ticket: 137.15, customer_count: 55 },
    ];
  }

  return textResult(JSON.stringify(result, null, 2), duration, [trinoSemanticEnrichment(tableName)]);
}

function datahubSearchResult(
  params: Record<string, unknown>,
  duration: number,
): ToolCallResponse {
  const query = String(params.query ?? "").toLowerCase();
  const results = [
    {
      urn: "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.sales.daily_sales,PROD)",
      name: "daily_sales",
      platform: "trino",
      type: "DATASET",
      description: "Daily aggregated sales by store and region",
      owners: ["marcus.johnson@acme-corp.com"],
      tags: ["certified", "pii-free"],
    },
    {
      urn: "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.sales.store_transactions,PROD)",
      name: "store_transactions",
      platform: "trino",
      type: "DATASET",
      description: "Raw POS transaction records from all ACME stores",
      owners: ["amanda.lee@acme-corp.com"],
      tags: ["pii", "raw"],
    },
    {
      urn: "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.inventory.inventory_levels,PROD)",
      name: "inventory_levels",
      platform: "trino",
      type: "DATASET",
      description: "Current inventory levels by warehouse and SKU",
      owners: ["rachel.thompson@acme-corp.com"],
      tags: ["certified", "real-time"],
    },
    {
      urn: "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.finance.regional_performance,PROD)",
      name: "regional_performance",
      platform: "trino",
      type: "DATASET",
      description: "Revenue and margin analysis by region and quarter",
      owners: ["jennifer.martinez@acme-corp.com"],
      tags: ["certified", "executive"],
    },
  ];

  // Filter loosely by query
  const filtered = query
    ? results.filter(
        (r) =>
          r.name.includes(query) ||
          r.description.toLowerCase().includes(query) ||
          r.tags.some((t) => t.includes(query)),
      )
    : results;

  const resultSet = filtered.length > 0 ? filtered : results;
  const urns = resultSet.map((r) => r.urn);
  return textResult(JSON.stringify(resultSet, null, 2), duration, [datahubQueryEnrichment(urns)]);
}

function datahubEntityResult(
  params: Record<string, unknown>,
  duration: number,
): ToolCallResponse {
  const urn = String(params.urn ?? "");
  const entity = {
    urn,
    name: urn.match(/\.([^,]+),/)?.[1] ?? "daily_sales",
    platform: "trino",
    description: "Daily aggregated sales figures by store, region, and product category for ACME Corporation retail locations",
    owners: [
      { owner: "marcus.johnson@acme-corp.com", type: "DATAOWNER" },
      { owner: "data-engineering", type: "DATAOWNER", group: true },
    ],
    tags: ["certified", "pii-free", "tier-1"],
    glossary_terms: [
      { urn: "urn:li:glossaryTerm:Revenue", name: "Revenue" },
      { urn: "urn:li:glossaryTerm:RetailMetrics", name: "Retail Metrics" },
    ],
    deprecation: null,
    quality_score: 0.94,
    last_modified: "2026-02-10T14:23:00Z",
    custom_properties: {
      refresh_schedule: "daily @ 02:00 UTC",
      sla: "99.5% availability",
      retention_days: "730",
    },
  };
  return textResult(JSON.stringify(entity, null, 2), duration, [datahubQueryEnrichment([urn || "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.sales.daily_sales,PROD)"])]);
}

function datahubSchemaResult(duration: number): ToolCallResponse {
  const fields = [
    { name: "sale_date", type: "DATE", description: "Date of the sale transaction", nullable: false, tags: [] },
    { name: "store_id", type: "STRING", description: "Unique identifier for the ACME retail store", nullable: false, tags: ["primary-key"] },
    { name: "region", type: "STRING", description: "Geographic sales region (South, Northeast, Midwest, West)", nullable: false, tags: [] },
    { name: "revenue", type: "DECIMAL", description: "Total revenue in USD", nullable: false, tags: ["metric", "Revenue"] },
    { name: "units_sold", type: "INT", description: "Total number of units sold", nullable: false, tags: ["metric"] },
    { name: "avg_ticket", type: "DECIMAL", description: "Average transaction amount", nullable: true, tags: ["metric"] },
    { name: "customer_count", type: "INT", description: "Distinct customer count", nullable: true, tags: ["metric"] },
    { name: "product_category", type: "STRING", description: "Top-level product category", nullable: true, tags: [] },
  ];
  return textResult(JSON.stringify(fields, null, 2), duration, [
    datahubQueryEnrichment(["urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.sales.daily_sales,PROD)"]),
  ]);
}

function datahubLineageResult(
  params: Record<string, unknown>,
  duration: number,
): ToolCallResponse {
  const direction = String(params.direction ?? "DOWNSTREAM");
  const lineage = {
    entity: String(params.urn ?? ""),
    direction,
    relationships:
      direction === "UPSTREAM"
        ? [
            {
              urn: "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.staging.raw_pos_events,PROD)",
              name: "raw_pos_events",
              type: "DATASET",
              degree: 1,
            },
            {
              urn: "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.staging.store_master,PROD)",
              name: "store_master",
              type: "DATASET",
              degree: 1,
            },
            {
              urn: "urn:li:dataJob:(urn:li:dataFlow:(airflow,acme_etl,PROD),daily_sales_agg)",
              name: "daily_sales_agg",
              type: "DATA_JOB",
              degree: 1,
            },
          ]
        : [
            {
              urn: "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.analytics.regional_performance,PROD)",
              name: "regional_performance",
              type: "DATASET",
              degree: 1,
            },
            {
              urn: "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.analytics.executive_summary,PROD)",
              name: "executive_summary",
              type: "DATASET",
              degree: 2,
            },
            {
              urn: "urn:li:dashboard:(looker,acme_sales_dashboard)",
              name: "ACME Sales Dashboard",
              type: "DASHBOARD",
              degree: 2,
            },
          ],
  };
  return textResult(JSON.stringify(lineage, null, 2), duration);
}

function datahubColumnLineageResult(duration: number): ToolCallResponse {
  const lineage = {
    mappings: [
      {
        downstream_column: "revenue",
        upstream_columns: [
          { dataset: "staging.raw_pos_events", column: "item_total", transform: "SUM(item_total)" },
        ],
      },
      {
        downstream_column: "units_sold",
        upstream_columns: [
          { dataset: "staging.raw_pos_events", column: "quantity", transform: "SUM(quantity)" },
        ],
      },
      {
        downstream_column: "store_id",
        upstream_columns: [
          { dataset: "staging.raw_pos_events", column: "store_id", transform: "PASSTHROUGH" },
        ],
      },
      {
        downstream_column: "region",
        upstream_columns: [
          { dataset: "staging.store_master", column: "region_name", transform: "LOOKUP(store_id)" },
        ],
      },
      {
        downstream_column: "avg_ticket",
        upstream_columns: [
          { dataset: "staging.raw_pos_events", column: "item_total", transform: "AVG(item_total) GROUP BY txn_id" },
        ],
      },
    ],
  };
  return textResult(JSON.stringify(lineage, null, 2), duration);
}

function s3ListBucketsResult(duration: number): ToolCallResponse {
  const buckets = [
    { name: "acme-raw-transactions", creation_date: "2024-03-15T00:00:00Z" },
    { name: "acme-analytics-output", creation_date: "2024-03-15T00:00:00Z" },
    { name: "acme-ml-features", creation_date: "2024-06-01T00:00:00Z" },
    { name: "acme-report-archive", creation_date: "2024-01-10T00:00:00Z" },
    { name: "acme-data-exports", creation_date: "2025-01-20T00:00:00Z" },
  ];
  return textResult(JSON.stringify(buckets, null, 2), duration);
}

function s3ListObjectsResult(
  params: Record<string, unknown>,
  duration: number,
): ToolCallResponse {
  const bucket = String(params.bucket ?? "acme-raw-transactions");
  const prefix = String(params.prefix ?? "");
  const objects = [
    { key: `${prefix}daily_sales/2026-02-11/part-00000.parquet`, size: 2_458_624, last_modified: "2026-02-11T03:15:00Z" },
    { key: `${prefix}daily_sales/2026-02-11/part-00001.parquet`, size: 2_312_448, last_modified: "2026-02-11T03:15:01Z" },
    { key: `${prefix}daily_sales/2026-02-10/part-00000.parquet`, size: 2_567_890, last_modified: "2026-02-10T03:14:58Z" },
    { key: `${prefix}store_transactions/2026-02-11/part-00000.parquet`, size: 15_234_048, last_modified: "2026-02-11T04:00:00Z" },
    { key: `${prefix}store_transactions/2026-02-11/part-00001.parquet`, size: 14_987_264, last_modified: "2026-02-11T04:00:02Z" },
    { key: `${prefix}inventory_snapshot/2026-02-11.csv`, size: 856_320, last_modified: "2026-02-11T06:00:00Z" },
    { key: `${prefix}_metadata/manifest.json`, size: 4_096, last_modified: "2026-02-11T03:20:00Z" },
  ];
  return textResult(JSON.stringify(objects, null, 2), duration, [s3SemanticEnrichment(bucket, prefix)]);
}

function s3GetObjectResult(
  params: Record<string, unknown>,
  duration: number,
): ToolCallResponse {
  const key = String(params.key ?? "");

  const bucket = String(params.bucket ?? "acme-raw-transactions");
  const enrichment = [s3SemanticEnrichment(bucket, "")];

  if (key.endsWith(".json")) {
    const text = JSON.stringify(
      {
        version: "1.0",
        generated_at: "2026-02-11T03:20:00Z",
        tables: ["daily_sales", "store_transactions", "inventory_levels"],
        row_counts: { daily_sales: 145230, store_transactions: 2_340_567, inventory_levels: 28_450 },
        status: "complete",
      },
      null,
      2,
    );
    return textResult(text, duration, enrichment);
  }

  const text = [
    "sale_date,store_id,region,revenue,units_sold",
    "2026-02-11,ACME-042,South,12450.00,89",
    "2026-02-11,ACME-015,Northeast,18920.50,134",
    "2026-02-11,ACME-108,Midwest,9875.25,72",
    "2026-02-11,ACME-023,West,22340.00,156",
    "2026-02-11,ACME-067,South,15680.75,112",
    "2026-02-10,ACME-042,South,11890.00,82",
    "2026-02-10,ACME-015,Northeast,17450.25,128",
    "2026-02-10,ACME-108,Midwest,10230.50,76",
    "...(truncated, 145230 total rows)",
  ].join("\n");

  return textResult(text, duration, enrichment);
}

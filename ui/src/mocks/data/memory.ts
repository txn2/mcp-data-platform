import type { MemoryRecord, MemoryStats } from "@/api/admin/types";

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

export const mockMemoryRecords: MemoryRecord[] = [
  {
    id: "mem-001",
    created_at: daysAgo(28),
    updated_at: daysAgo(2),
    created_by: "sarah.chen@example.com",
    persona: "admin",
    dimension: "data_knowledge",
    content:
      "The daily_sales table in the retail schema is partitioned by sale_date and should always be queried with a date range filter to prevent full table scans that degrade cluster performance.",
    category: "usage_guidance",
    confidence: "high",
    source: "admin_manual",
    entity_urns: [
      "urn:li:dataset:(urn:li:dataPlatform:trino,retail.daily_sales,PROD)",
    ],
    related_columns: [
      {
        urn: "urn:li:dataset:(urn:li:dataPlatform:trino,retail.daily_sales,PROD)",
        column: "sale_date",
        relevance: "partition_key",
      },
    ],
    metadata: { partition_type: "date", retention_days: 730 },
    status: "active",
    last_verified: daysAgo(2),
  },
  {
    id: "mem-002",
    created_at: daysAgo(25),
    updated_at: daysAgo(5),
    created_by: "marcus.johnson@example.com",
    persona: "data-engineer",
    dimension: "table_relationships",
    content:
      "The inventory_levels table joins to product_catalog on product_id and to store_locations on store_id. Always use these join keys — there are no alternate foreign key paths between these tables.",
    category: "relationship",
    confidence: "high",
    source: "insight_applied",
    entity_urns: [
      "urn:li:dataset:(urn:li:dataPlatform:trino,inventory.inventory_levels,PROD)",
      "urn:li:dataset:(urn:li:dataPlatform:trino,retail.product_catalog,PROD)",
    ],
    related_columns: [
      {
        urn: "urn:li:dataset:(urn:li:dataPlatform:trino,inventory.inventory_levels,PROD)",
        column: "product_id",
        relevance: "join_key",
      },
      {
        urn: "urn:li:dataset:(urn:li:dataPlatform:trino,inventory.inventory_levels,PROD)",
        column: "store_id",
        relevance: "join_key",
      },
    ],
    metadata: {},
    status: "active",
    last_verified: daysAgo(5),
  },
  {
    id: "mem-003",
    created_at: daysAgo(22),
    updated_at: daysAgo(1),
    created_by: "rachel.thompson@example.com",
    persona: "inventory-analyst",
    dimension: "business_context",
    content:
      "ACME Corp fiscal year starts February 1st. When users ask about Q1 they mean Feb-Apr, Q2 is May-Jul, Q3 is Aug-Oct, and Q4 is Nov-Jan. The analytics.fiscal_calendar table maps calendar dates to fiscal periods.",
    category: "business_context",
    confidence: "high",
    source: "user_feedback",
    entity_urns: [
      "urn:li:dataset:(urn:li:dataPlatform:trino,analytics.fiscal_calendar,PROD)",
    ],
    related_columns: [
      {
        urn: "urn:li:dataset:(urn:li:dataPlatform:trino,analytics.fiscal_calendar,PROD)",
        column: "fiscal_quarter",
        relevance: "primary",
      },
    ],
    metadata: { fiscal_year_start: "February 1" },
    status: "active",
    last_verified: daysAgo(1),
  },
  {
    id: "mem-004",
    created_at: daysAgo(20),
    updated_at: daysAgo(3),
    created_by: "marcus.johnson@example.com",
    persona: "data-engineer",
    dimension: "data_knowledge",
    content:
      "Revenue amounts in store_transactions are stored in cents as integers. Divide by 100 to get dollar values. The total_amount column includes tax; use subtotal_amount for pre-tax figures.",
    category: "correction",
    confidence: "high",
    source: "insight_applied",
    entity_urns: [
      "urn:li:dataset:(urn:li:dataPlatform:trino,retail.store_transactions,PROD)",
    ],
    related_columns: [
      {
        urn: "urn:li:dataset:(urn:li:dataPlatform:trino,retail.store_transactions,PROD)",
        column: "total_amount",
        relevance: "primary",
      },
      {
        urn: "urn:li:dataset:(urn:li:dataPlatform:trino,retail.store_transactions,PROD)",
        column: "subtotal_amount",
        relevance: "primary",
      },
    ],
    metadata: { unit: "cents", currency: "USD" },
    status: "active",
    last_verified: daysAgo(3),
  },
  {
    id: "mem-005",
    created_at: daysAgo(18),
    updated_at: daysAgo(18),
    created_by: "rachel.thompson@example.com",
    persona: "inventory-analyst",
    dimension: "query_patterns",
    content:
      "For reorder point analysis, use: SELECT product_id, store_id, avg_daily_demand * lead_time_days + safety_stock AS reorder_point FROM inventory.reorder_parameters WHERE active = true.",
    category: "enhancement",
    confidence: "medium",
    source: "automatic_capture",
    entity_urns: [
      "urn:li:dataset:(urn:li:dataPlatform:trino,inventory.reorder_parameters,PROD)",
    ],
    related_columns: [
      {
        urn: "urn:li:dataset:(urn:li:dataPlatform:trino,inventory.reorder_parameters,PROD)",
        column: "avg_daily_demand",
        relevance: "calculation_input",
      },
    ],
    metadata: {},
    status: "active",
  },
  {
    id: "mem-006",
    created_at: daysAgo(16),
    updated_at: daysAgo(4),
    created_by: "amanda.lee@example.com",
    persona: "data-engineer",
    dimension: "data_knowledge",
    content:
      "The supply_chain_orders table has a known data quality issue: orders from the Southeast region prior to March 2024 have NULL values in the estimated_delivery_date column due to a migration bug. Use COALESCE with order_date + 7 as a fallback.",
    category: "correction",
    confidence: "high",
    source: "user_feedback",
    entity_urns: [
      "urn:li:dataset:(urn:li:dataPlatform:trino,retail.supply_chain_orders,PROD)",
    ],
    related_columns: [
      {
        urn: "urn:li:dataset:(urn:li:dataPlatform:trino,retail.supply_chain_orders,PROD)",
        column: "estimated_delivery_date",
        relevance: "affected_column",
      },
    ],
    metadata: { affected_region: "Southeast", cutoff_date: "2024-03-01" },
    status: "active",
    last_verified: daysAgo(4),
  },
  {
    id: "mem-007",
    created_at: daysAgo(15),
    updated_at: daysAgo(15),
    created_by: "david.park@example.com",
    persona: "regional-director",
    dimension: "business_context",
    content:
      "Store IDs follow the format STR-XXXX where the first two digits of XXXX represent the region code: 10-19 = Northeast, 20-29 = Southeast, 30-39 = Midwest, 40-49 = Southwest, 50-59 = West Coast.",
    category: "business_context",
    confidence: "high",
    source: "admin_manual",
    entity_urns: [],
    related_columns: [],
    metadata: {
      region_codes: {
        Northeast: "10-19",
        Southeast: "20-29",
        Midwest: "30-39",
        Southwest: "40-49",
        "West Coast": "50-59",
      },
    },
    status: "active",
  },
  {
    id: "mem-008",
    created_at: daysAgo(14),
    updated_at: daysAgo(6),
    created_by: "marcus.johnson@example.com",
    persona: "data-engineer",
    dimension: "query_patterns",
    content:
      "When aggregating daily_sales by week, use date_trunc('week', sale_date) and be aware that Trino weeks start on Monday. For ACME's Sunday-start business week, use date_trunc('week', sale_date + INTERVAL '1' DAY) - INTERVAL '1' DAY.",
    category: "usage_guidance",
    confidence: "high",
    source: "insight_applied",
    entity_urns: [
      "urn:li:dataset:(urn:li:dataPlatform:trino,retail.daily_sales,PROD)",
    ],
    related_columns: [
      {
        urn: "urn:li:dataset:(urn:li:dataPlatform:trino,retail.daily_sales,PROD)",
        column: "sale_date",
        relevance: "primary",
      },
    ],
    metadata: { business_week_start: "Sunday" },
    status: "active",
    last_verified: daysAgo(6),
  },
  {
    id: "mem-009",
    created_at: daysAgo(12),
    updated_at: daysAgo(12),
    created_by: "emily.watson@example.com",
    persona: "inventory-analyst",
    dimension: "business_context",
    content:
      "Seasonal adjustment factors for holiday categories (fireworks, sparklers, party supplies) peak in Q4 fiscal (Nov-Jan calendar). The seasonal_factors table in the analytics schema holds monthly multipliers by product_category.",
    category: "business_context",
    confidence: "medium",
    source: "automatic_capture",
    entity_urns: [
      "urn:li:dataset:(urn:li:dataPlatform:trino,analytics.seasonal_factors,PROD)",
    ],
    related_columns: [
      {
        urn: "urn:li:dataset:(urn:li:dataPlatform:trino,analytics.seasonal_factors,PROD)",
        column: "product_category",
        relevance: "primary",
      },
      {
        urn: "urn:li:dataset:(urn:li:dataPlatform:trino,analytics.seasonal_factors,PROD)",
        column: "month_multiplier",
        relevance: "primary",
      },
    ],
    metadata: {},
    status: "active",
  },
  {
    id: "mem-010",
    created_at: daysAgo(11),
    updated_at: daysAgo(11),
    created_by: "lisa.chang@example.com",
    persona: "data-engineer",
    dimension: "table_relationships",
    content:
      "The regional_performance view joins daily_sales, store_locations, and customer_segments. It is refreshed nightly at 02:00 UTC and should not be queried during the 02:00-02:30 refresh window.",
    category: "usage_guidance",
    confidence: "high",
    source: "admin_manual",
    entity_urns: [
      "urn:li:dataset:(urn:li:dataPlatform:trino,analytics.regional_performance,PROD)",
    ],
    related_columns: [],
    metadata: { refresh_schedule: "02:00 UTC daily" },
    status: "active",
  },
  {
    id: "mem-011",
    created_at: daysAgo(10),
    updated_at: hoursAgo(8),
    created_by: "rachel.thompson@example.com",
    persona: "inventory-analyst",
    dimension: "data_knowledge",
    content:
      "The return_rates table calculates returns as a percentage of units sold. A return_rate above 15% triggers an automatic quality review flag in the product_catalog table.",
    category: "enhancement",
    confidence: "medium",
    source: "automatic_capture",
    entity_urns: [
      "urn:li:dataset:(urn:li:dataPlatform:trino,retail.return_rates,PROD)",
      "urn:li:dataset:(urn:li:dataPlatform:trino,retail.product_catalog,PROD)",
    ],
    related_columns: [
      {
        urn: "urn:li:dataset:(urn:li:dataPlatform:trino,retail.return_rates,PROD)",
        column: "return_rate",
        relevance: "primary",
      },
    ],
    metadata: { threshold_pct: 15 },
    status: "active",
    last_verified: hoursAgo(8),
  },
  {
    id: "mem-012",
    created_at: daysAgo(9),
    updated_at: daysAgo(9),
    created_by: "marcus.johnson@example.com",
    persona: "data-engineer",
    dimension: "query_patterns",
    content:
      "For year-over-year comparisons on daily_sales, always join against analytics.fiscal_calendar to align fiscal periods rather than using simple date arithmetic, which breaks across fiscal year boundaries.",
    category: "usage_guidance",
    confidence: "high",
    source: "insight_applied",
    entity_urns: [
      "urn:li:dataset:(urn:li:dataPlatform:trino,retail.daily_sales,PROD)",
      "urn:li:dataset:(urn:li:dataPlatform:trino,analytics.fiscal_calendar,PROD)",
    ],
    related_columns: [],
    metadata: {},
    status: "active",
  },
  {
    id: "mem-013",
    created_at: daysAgo(8),
    updated_at: daysAgo(1),
    created_by: "amanda.lee@example.com",
    persona: "data-engineer",
    dimension: "data_knowledge",
    content:
      "The price_adjustments table tracks promotional pricing. The effective_price column reflects the final shelf price after all discounts. Use original_price for MSRP comparisons.",
    category: "correction",
    confidence: "high",
    source: "user_feedback",
    entity_urns: [
      "urn:li:dataset:(urn:li:dataPlatform:trino,retail.price_adjustments,PROD)",
    ],
    related_columns: [
      {
        urn: "urn:li:dataset:(urn:li:dataPlatform:trino,retail.price_adjustments,PROD)",
        column: "effective_price",
        relevance: "primary",
      },
      {
        urn: "urn:li:dataset:(urn:li:dataPlatform:trino,retail.price_adjustments,PROD)",
        column: "original_price",
        relevance: "primary",
      },
    ],
    metadata: {},
    status: "active",
    last_verified: daysAgo(1),
  },
  {
    id: "mem-014",
    created_at: daysAgo(7),
    updated_at: daysAgo(7),
    created_by: "sarah.chen@example.com",
    persona: "admin",
    dimension: "user_preferences",
    content:
      "Regional directors prefer aggregated summaries by district rather than individual store breakdowns. Default to GROUP BY district_id unless a specific store is mentioned.",
    category: "enhancement",
    confidence: "medium",
    source: "user_feedback",
    entity_urns: [],
    related_columns: [],
    metadata: { applies_to_persona: "regional-director" },
    status: "active",
  },
  {
    id: "mem-015",
    created_at: daysAgo(6),
    updated_at: daysAgo(6),
    created_by: "marcus.johnson@example.com",
    persona: "data-engineer",
    dimension: "table_relationships",
    content:
      "The customer_segments table is a Type 2 slowly changing dimension. Use the is_current = true filter to get the latest segment assignment. Historical segment membership is tracked via valid_from and valid_to columns.",
    category: "relationship",
    confidence: "high",
    source: "insight_applied",
    entity_urns: [
      "urn:li:dataset:(urn:li:dataPlatform:trino,retail.customer_segments,PROD)",
    ],
    related_columns: [
      {
        urn: "urn:li:dataset:(urn:li:dataPlatform:trino,retail.customer_segments,PROD)",
        column: "is_current",
        relevance: "filter_key",
      },
    ],
    metadata: { scd_type: 2 },
    status: "active",
  },
  {
    id: "mem-016",
    created_at: daysAgo(5),
    updated_at: daysAgo(5),
    created_by: "emily.watson@example.com",
    persona: "inventory-analyst",
    dimension: "query_patterns",
    content:
      "To find products below reorder point: SELECT il.product_id, il.current_qty, rp.reorder_point FROM inventory.inventory_levels il JOIN inventory.reorder_parameters rp ON il.product_id = rp.product_id AND il.store_id = rp.store_id WHERE il.current_qty < rp.reorder_point.",
    category: "enhancement",
    confidence: "high",
    source: "automatic_capture",
    entity_urns: [
      "urn:li:dataset:(urn:li:dataPlatform:trino,inventory.inventory_levels,PROD)",
      "urn:li:dataset:(urn:li:dataPlatform:trino,inventory.reorder_parameters,PROD)",
    ],
    related_columns: [
      {
        urn: "urn:li:dataset:(urn:li:dataPlatform:trino,inventory.inventory_levels,PROD)",
        column: "current_qty",
        relevance: "primary",
      },
    ],
    metadata: {},
    status: "active",
  },
  {
    id: "mem-017",
    created_at: daysAgo(4),
    updated_at: daysAgo(4),
    created_by: "lisa.chang@example.com",
    persona: "data-engineer",
    dimension: "data_knowledge",
    content:
      "The warehouse_transfers table was migrated from the legacy system in January 2024. Records before that date use the old warehouse_code format (WH-NNN) while newer records use the standardized facility_id (FAC-NNNNN).",
    category: "correction",
    confidence: "medium",
    source: "user_feedback",
    entity_urns: [
      "urn:li:dataset:(urn:li:dataPlatform:trino,inventory.warehouse_transfers,PROD)",
    ],
    related_columns: [
      {
        urn: "urn:li:dataset:(urn:li:dataPlatform:trino,inventory.warehouse_transfers,PROD)",
        column: "facility_id",
        relevance: "primary",
      },
    ],
    metadata: { migration_date: "2024-01-15" },
    status: "stale",
    stale_reason:
      "Legacy warehouse_code records were backfilled with facility_id mappings in April 2024",
    stale_at: daysAgo(4),
  },
  {
    id: "mem-018",
    created_at: daysAgo(3),
    updated_at: daysAgo(3),
    created_by: "rachel.thompson@example.com",
    persona: "inventory-analyst",
    dimension: "user_preferences",
    content:
      "Inventory analysts prefer results sorted by days_of_supply ascending so critically low stock items appear first in query results.",
    category: "enhancement",
    confidence: "medium",
    source: "user_feedback",
    entity_urns: [],
    related_columns: [],
    metadata: { applies_to_persona: "inventory-analyst" },
    status: "active",
  },
  {
    id: "mem-019",
    created_at: daysAgo(2),
    updated_at: daysAgo(2),
    created_by: "marcus.johnson@example.com",
    persona: "data-engineer",
    dimension: "data_knowledge",
    content:
      "The finance.gl_entries table uses a double-entry bookkeeping model. Every transaction has matching debit and credit rows. Summing amount without filtering by entry_type will always return zero.",
    category: "usage_guidance",
    confidence: "high",
    source: "admin_manual",
    entity_urns: [
      "urn:li:dataset:(urn:li:dataPlatform:trino,finance.gl_entries,PROD)",
    ],
    related_columns: [
      {
        urn: "urn:li:dataset:(urn:li:dataPlatform:trino,finance.gl_entries,PROD)",
        column: "entry_type",
        relevance: "filter_key",
      },
      {
        urn: "urn:li:dataset:(urn:li:dataPlatform:trino,finance.gl_entries,PROD)",
        column: "amount",
        relevance: "primary",
      },
    ],
    metadata: {},
    status: "active",
  },
  {
    id: "mem-020",
    created_at: daysAgo(20),
    updated_at: daysAgo(1),
    created_by: "sarah.chen@example.com",
    persona: "admin",
    dimension: "business_context",
    content:
      "The customer loyalty tiers were restructured in Q3 FY2024. The old Bronze/Silver/Gold tiers were replaced with Explorer/Enthusiast/Champion. Queries referencing the old tier names should use the analytics.loyalty_tier_mapping table for translation.",
    category: "correction",
    confidence: "high",
    source: "insight_applied",
    entity_urns: [
      "urn:li:dataset:(urn:li:dataPlatform:trino,analytics.loyalty_tier_mapping,PROD)",
    ],
    related_columns: [],
    metadata: {
      old_tiers: ["Bronze", "Silver", "Gold"],
      new_tiers: ["Explorer", "Enthusiast", "Champion"],
    },
    status: "superseded",
    stale_reason:
      "Superseded by updated memory that includes the new Trailblazer tier added in Q1 FY2025",
    stale_at: daysAgo(1),
  },
];

export const mockMemoryStats: MemoryStats = {
  total: 20,
  by_dimension: {
    data_knowledge: 6,
    query_patterns: 4,
    business_context: 4,
    user_preferences: 2,
    table_relationships: 4,
  },
  by_category: {
    correction: 5,
    enhancement: 4,
    usage_guidance: 5,
    relationship: 2,
    business_context: 4,
  },
  by_status: {
    active: 17,
    stale: 1,
    superseded: 2,
  },
};

export const mockPortalMemoryRecords: MemoryRecord[] = [
  mockMemoryRecords[2]!,
  mockMemoryRecords[4]!,
  mockMemoryRecords[8]!,
  mockMemoryRecords[10]!,
  mockMemoryRecords[15]!,
  mockMemoryRecords[17]!,
  mockMemoryRecords[3]!,
  mockMemoryRecords[7]!,
];

export const mockPortalMemoryStats: MemoryStats = {
  total: 8,
  by_dimension: {
    data_knowledge: 2,
    query_patterns: 2,
    business_context: 2,
    user_preferences: 1,
    table_relationships: 1,
  },
  by_category: {
    correction: 1,
    enhancement: 3,
    usage_guidance: 1,
    business_context: 2,
    relationship: 1,
  },
  by_status: {
    active: 8,
  },
};

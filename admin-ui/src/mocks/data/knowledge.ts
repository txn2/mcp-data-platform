import type { Insight, Changeset } from "@/api/types";

// ---------------------------------------------------------------------------
// Seeded PRNG (mulberry32) — deterministic mock data across page loads
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

const rand = mulberry32(20260101); // distinct seed from audit (20240215)

function seededItem<T>(arr: T[]): T {
  return arr[Math.floor(rand() * arr.length)]!;
}

function seededInt(min: number, max: number): number {
  return Math.floor(rand() * (max - min + 1)) + min;
}

// ---------------------------------------------------------------------------
// Domain data — ACME retail / fireworks
// ---------------------------------------------------------------------------

const acmeUsers = [
  { email: "sarah.chen@acme-corp.com", persona: "admin" },
  { email: "marcus.johnson@acme-corp.com", persona: "data-engineer" },
  { email: "rachel.thompson@acme-corp.com", persona: "inventory-analyst" },
  { email: "david.park@acme-corp.com", persona: "regional-director" },
  { email: "amanda.lee@acme-corp.com", persona: "data-engineer" },
  { email: "emily.watson@acme-corp.com", persona: "inventory-analyst" },
  { email: "lisa.chang@acme-corp.com", persona: "data-engineer" },
];

const trinoSchemas = ["retail", "inventory", "finance", "analytics"];
const trinoTables = [
  "daily_sales",
  "store_transactions",
  "inventory_levels",
  "product_catalog",
  "customer_segments",
  "regional_performance",
  "supply_chain_orders",
  "price_adjustments",
  "return_rates",
];

function randomUrn(): string {
  const schema = seededItem(trinoSchemas);
  const table = seededItem(trinoTables);
  return `urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.${schema}.${table},PROD)`;
}

const categories = [
  "correction",
  "business_context",
  "data_quality",
  "usage_guidance",
  "relationship",
  "enhancement",
] as const;

const confidences = ["high", "medium", "low"] as const;

// Insight text pool per category — realistic ACME retail domain
const insightTexts: Record<string, string[]> = {
  correction: [
    "The 'revenue' column in daily_sales actually represents gross revenue before discounts, not net revenue as documented.",
    "Column 'qty_on_hand' in inventory_levels includes items in transit, contradicting the column description.",
    "The 'region_code' field uses legacy FIPS codes, not the ISO 3166-2 codes stated in metadata.",
    "product_catalog.category_id references a deprecated category taxonomy; the active one is in category_v2_id.",
    "store_transactions.discount_pct is stored as a decimal (0.15) not a percentage (15) despite the column name.",
    "The 'status' column in supply_chain_orders uses integer codes (1-5), not the string enum shown in DataHub.",
    "return_rates.return_reason includes 'damaged_in_transit' which is mapped to the wrong GL code downstream.",
    "price_adjustments.effective_date uses store-local timezone, not UTC as documented.",
  ],
  business_context: [
    "Daily sales spike 300-400% during the July 4th week — this is the fireworks peak season for ACME.",
    "Regional performance metrics exclude franchise locations; only corporate stores are included.",
    "The 'customer_segments' table is refreshed weekly on Mondays; mid-week queries show stale data.",
    "Inventory levels below 50 units trigger automatic reorder for Class C fireworks items.",
    "Q4 sales data includes Black Friday / New Year's Eve promotions which skew avg transaction value.",
    "The analytics.regional_performance view excludes Hawaii and Alaska due to shipping restrictions.",
    "Return rates above 8% trigger compliance review for product safety under CPSC regulations.",
    "Store transactions after 10pm are batch-loaded next morning; real-time queries miss late sales.",
  ],
  data_quality: [
    "Approximately 3% of store_transactions have NULL customer_id due to cash register fallback mode.",
    "inventory_levels has duplicate rows for warehouse WH-07 from a migration error in Jan 2026.",
    "price_adjustments contains 12 rows with negative discount amounts from a bulk import bug.",
    "daily_sales is missing data for stores 401-405 on 2026-01-15 due to POS system outage.",
    "The product_catalog has 47 items with category_id=0 (uncategorized) that need manual classification.",
    "customer_segments.lifetime_value has outliers above $50k that are likely B2B accounts miscategorized.",
    "supply_chain_orders has timestamp gaps between 2am-4am UTC due to ETL maintenance windows.",
    "regional_performance.yoy_growth is NULL for new stores opened in the last 12 months.",
  ],
  usage_guidance: [
    "Always filter daily_sales by is_voided=false to exclude cancelled transactions from totals.",
    "Join inventory_levels with product_catalog on sku_id (not product_id) for accurate stock counts.",
    "Use the analytics.daily_sales_enriched view instead of raw retail.daily_sales for pre-joined dimensions.",
    "When querying supply_chain_orders, add WHERE status != 5 to exclude cancelled orders.",
    "For accurate margin calculations, use product_catalog.cost_basis, not the deprecated unit_cost field.",
    "The regional_performance table should be queried with GROUP BY fiscal_quarter, not calendar_quarter.",
    "Always use COALESCE(discount_pct, 0) on store_transactions as NULLs mean no discount, not unknown.",
  ],
  relationship: [
    "daily_sales feeds into the analytics.regional_performance materialized view via a nightly Spark job.",
    "inventory_levels is the source of truth for the supply_chain_orders reorder trigger pipeline.",
    "customer_segments is derived from store_transactions using the ML segmentation pipeline (runs weekly).",
    "product_catalog is mastered in the ERP system and synced to the data warehouse via Debezium CDC.",
    "return_rates joins with store_transactions on (store_id, transaction_date) for return attribution.",
    "The finance.revenue_reconciliation view combines daily_sales + return_rates + price_adjustments.",
  ],
  enhancement: [
    "Adding a 'channel' tag (online/in-store/wholesale) to store_transactions would improve segmentation.",
    "The product_catalog should include a 'hazmat_class' column for fireworks compliance reporting.",
    "Consider adding a glossary term for 'net_revenue' to clarify the discount treatment across datasets.",
    "inventory_levels would benefit from a 'last_counted_at' column for cycle count tracking.",
    "Adding owner tags for the finance team on all finance.* tables would improve discoverability.",
  ],
};

const actionTypes = [
  "update_description",
  "add_tag",
  "add_glossary_term",
  "update_column_description",
  "add_owner",
  "deprecate_field",
];

const columnNames = [
  "revenue",
  "qty_on_hand",
  "region_code",
  "category_id",
  "discount_pct",
  "status",
  "customer_id",
  "sku_id",
  "store_id",
  "transaction_date",
  "unit_cost",
  "cost_basis",
];

const relevances = ["primary", "secondary", "contextual"];

// ---------------------------------------------------------------------------
// Status distribution: 15 pending, 8 approved, 8 rejected, 12 applied, 4 superseded, 3 rolled_back
// ---------------------------------------------------------------------------
const statusPool: string[] = [
  ...Array(15).fill("pending"),
  ...Array(8).fill("approved"),
  ...Array(8).fill("rejected"),
  ...Array(12).fill("applied"),
  ...Array(4).fill("superseded"),
  ...Array(3).fill("rolled_back"),
];

// Shuffle the status pool deterministically
for (let i = statusPool.length - 1; i > 0; i--) {
  const j = Math.floor(rand() * (i + 1));
  [statusPool[i], statusPool[j]] = [statusPool[j]!, statusPool[i]!];
}

const reviewers = [
  "sarah.chen@acme-corp.com",
  "marcus.johnson@acme-corp.com",
  "amanda.lee@acme-corp.com",
];

const reviewNotesPool = [
  "Verified against source system documentation.",
  "Confirmed with domain expert.",
  "Cross-checked with ETL pipeline logs.",
  "Matches findings from last data quality audit.",
  "Approved — high-priority correction.",
  "Rejected — this behavior is by design per the data contract.",
  "Rejected — needs more evidence before applying.",
  "Superseded by more recent insight.",
];

// ---------------------------------------------------------------------------
// Generate insights
// ---------------------------------------------------------------------------

function generateInsights(count: number): Insight[] {
  const insights: Insight[] = [];
  const now = new Date();

  for (let i = 0; i < count; i++) {
    const user = seededItem(acmeUsers);
    const category = seededItem([...categories]);
    const status = statusPool[i % statusPool.length]!;
    const confidence = seededItem([...confidences]);

    // Spread across 14 days, weighted recent
    const daysAgo = Math.floor(rand() * rand() * 14);
    const ts = new Date(now);
    ts.setDate(ts.getDate() - daysAgo);
    ts.setHours(seededInt(8, 18), seededInt(0, 59), seededInt(0, 59));

    const entityUrns = [randomUrn()];
    if (rand() > 0.6) entityUrns.push(randomUrn());

    const texts = insightTexts[category]!;
    const insightText = texts[i % texts.length]!;

    // Suggested actions (1-2 per insight)
    const suggestedActions = [
      {
        action_type: seededItem(actionTypes),
        target: entityUrns[0]!,
        detail: `Update metadata for ${category} finding`,
      },
    ];
    if (rand() > 0.5) {
      suggestedActions.push({
        action_type: seededItem(actionTypes),
        target: entityUrns[0]!,
        detail: `Add documentation note for downstream consumers`,
      });
    }

    // Related columns (0-3 per insight)
    const relatedColumns = [];
    const numCols = seededInt(0, 3);
    for (let c = 0; c < numCols; c++) {
      relatedColumns.push({
        urn: entityUrns[0]!,
        column: seededItem(columnNames),
        relevance: seededItem(relevances),
      });
    }

    // Lifecycle fields based on status
    let reviewed_by: string | undefined;
    let reviewed_at: string | undefined;
    let review_notes: string | undefined;
    let applied_by: string | undefined;
    let applied_at: string | undefined;
    let changeset_ref: string | undefined;

    if (status !== "pending") {
      reviewed_by = seededItem(reviewers);
      const reviewDate = new Date(ts);
      reviewDate.setHours(reviewDate.getHours() + seededInt(1, 48));
      reviewed_at = reviewDate.toISOString();
      review_notes = seededItem(reviewNotesPool);
    }

    if (status === "applied" || status === "rolled_back") {
      applied_by = seededItem(reviewers);
      const applyDate = new Date(reviewed_at!);
      applyDate.setHours(applyDate.getHours() + seededInt(1, 24));
      applied_at = applyDate.toISOString();
      changeset_ref = `cs-${String(seededInt(1, 10)).padStart(3, "0")}`;
    }

    insights.push({
      id: `ins-${String(i + 1).padStart(3, "0")}`,
      created_at: ts.toISOString(),
      session_id: `sess-${String(seededInt(100, 999))}`,
      captured_by: user.email,
      persona: user.persona,
      category,
      insight_text: insightText,
      confidence,
      entity_urns: entityUrns,
      related_columns: relatedColumns,
      suggested_actions: suggestedActions,
      status,
      reviewed_by,
      reviewed_at,
      review_notes,
      applied_by,
      applied_at,
      changeset_ref,
    });
  }

  return insights.sort(
    (a, b) =>
      new Date(b.created_at).getTime() - new Date(a.created_at).getTime(),
  );
}

// ---------------------------------------------------------------------------
// Generate changesets linked to applied/rolled_back insights
// ---------------------------------------------------------------------------

function generateChangesets(insights: Insight[]): Changeset[] {
  const appliedInsights = insights.filter(
    (i) => i.status === "applied" || i.status === "rolled_back",
  );
  const changesets: Changeset[] = [];

  // Group by changeset_ref
  const byRef = new Map<string, Insight[]>();
  for (const ins of appliedInsights) {
    if (!ins.changeset_ref) continue;
    if (!byRef.has(ins.changeset_ref)) byRef.set(ins.changeset_ref, []);
    byRef.get(ins.changeset_ref)!.push(ins);
  }

  const changeTypes = [
    "update_description",
    "add_tag",
    "update_column_description",
    "add_glossary_term",
    "add_owner",
  ];

  let idx = 0;
  for (const [ref, insightsForRef] of byRef) {
    const firstInsight = insightsForRef[0]!;
    const isRolledBack = insightsForRef.some(
      (i) => i.status === "rolled_back",
    );
    const changeType = seededItem(changeTypes);

    const cs: Changeset = {
      id: ref,
      created_at: firstInsight.applied_at ?? firstInsight.created_at,
      target_urn: firstInsight.entity_urns[0]!,
      change_type: changeType,
      previous_value: generatePreviousValue(changeType),
      new_value: generateNewValue(changeType, firstInsight),
      source_insight_ids: insightsForRef.map((i) => i.id),
      approved_by: firstInsight.reviewed_by ?? "system",
      applied_by: firstInsight.applied_by ?? "system",
      rolled_back: isRolledBack,
    };

    if (isRolledBack) {
      const rbDate = new Date(cs.created_at);
      rbDate.setHours(rbDate.getHours() + seededInt(2, 72));
      cs.rolled_back_by = seededItem(reviewers);
      cs.rolled_back_at = rbDate.toISOString();
    }

    changesets.push(cs);
    idx++;
  }

  // Add a few standalone changesets to reach ~10
  while (changesets.length < 10) {
    const changeType = seededItem(changeTypes);
    const ts = new Date();
    ts.setDate(ts.getDate() - seededInt(1, 10));
    ts.setHours(seededInt(9, 17), seededInt(0, 59));

    const isRolledBack = idx >= 8; // last 2 are rolled back
    const cs: Changeset = {
      id: `cs-${String(idx + 1).padStart(3, "0")}`,
      created_at: ts.toISOString(),
      target_urn: randomUrn(),
      change_type: changeType,
      previous_value: generatePreviousValue(changeType),
      new_value: generateNewValue(changeType),
      source_insight_ids: [
        `ins-${String(seededInt(1, 50)).padStart(3, "0")}`,
      ],
      approved_by: seededItem(reviewers),
      applied_by: seededItem(reviewers),
      rolled_back: isRolledBack,
    };

    if (isRolledBack) {
      const rbDate = new Date(ts);
      rbDate.setHours(rbDate.getHours() + seededInt(4, 48));
      cs.rolled_back_by = seededItem(reviewers);
      cs.rolled_back_at = rbDate.toISOString();
    }

    changesets.push(cs);
    idx++;
  }

  return changesets.sort(
    (a, b) =>
      new Date(b.created_at).getTime() - new Date(a.created_at).getTime(),
  );
}

function generatePreviousValue(
  changeType: string,
): Record<string, unknown> {
  switch (changeType) {
    case "update_description":
      return { description: "Original table description from initial import." };
    case "add_tag":
      return { tags: ["pii"] };
    case "update_column_description":
      return {
        column: seededItem(columnNames),
        description: "Auto-generated column description.",
      };
    case "add_glossary_term":
      return { glossary_terms: [] };
    case "add_owner":
      return { owners: ["data-platform-team"] };
    default:
      return {};
  }
}

function generateNewValue(
  changeType: string,
  insight?: Insight,
): Record<string, unknown> {
  switch (changeType) {
    case "update_description":
      return {
        description:
          insight?.insight_text ??
          "Updated description based on domain knowledge.",
      };
    case "add_tag":
      return { tags: ["pii", "fireworks-compliance"] };
    case "update_column_description":
      return {
        column: seededItem(columnNames),
        description:
          "Corrected description based on verified business logic.",
      };
    case "add_glossary_term":
      return { glossary_terms: ["net_revenue"] };
    case "add_owner":
      return { owners: ["data-platform-team", "finance-team"] };
    default:
      return {};
  }
}

// ---------------------------------------------------------------------------
// Exports
// ---------------------------------------------------------------------------

export const mockInsights: Insight[] = generateInsights(50);
export const mockChangesets: Changeset[] = generateChangesets(mockInsights);

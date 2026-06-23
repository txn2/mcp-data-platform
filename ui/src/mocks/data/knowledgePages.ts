import type { KnowledgePage } from "@/api/portal/types";

// mockKnowledgePages is a mutable in-memory store for the knowledge-pages MSW
// handlers (create/update/delete mutate it). Seeded with a spread of canonical
// pages across several tags so the browse view (count, tag facet, recency list)
// and content search have realistic material.
export const mockKnowledgePages: KnowledgePage[] = [
  {
    id: "kp-seed-1",
    slug: "fiscal-calendar",
    title: "Fiscal Calendar",
    summary: "How the company defines fiscal quarters.",
    body:
      "# Fiscal Calendar\n\nOur fiscal year starts in **February**.\n\n- Q1: February - April\n- Q2: May - July\n- Q3: August - October\n- Q4: November - January\n",
    tags: ["finance", "calendar"],
    created_by: "sarah.chen@example.com",
    updated_by: "sarah.chen@example.com",
    current_version: 2,
    created_at: "2026-06-01T10:00:00Z",
    updated_at: "2026-06-10T12:00:00Z",
  },
  {
    id: "kp-seed-2",
    slug: "revenue-definition",
    title: "Revenue Definition",
    summary: "What the amount column means.",
    body:
      "# Revenue Definition\n\nThe `amount` column is **gross margin before returns**, not gross revenue. Use `net_revenue` for top-line reporting.\n",
    tags: ["finance", "metrics"],
    created_by: "sarah.chen@example.com",
    updated_by: "sarah.chen@example.com",
    current_version: 1,
    created_at: "2026-06-05T09:00:00Z",
    updated_at: "2026-06-18T14:30:00Z",
  },
  {
    id: "kp-seed-3",
    slug: "customer-pii-handling",
    title: "Customer PII Handling",
    summary: "Which columns are personal data and how to treat them.",
    body:
      "# Customer PII Handling\n\n`email`, `phone`, and `address` are **PII**. Never join them into shared marts without masking. See the governance policy before exporting.\n",
    tags: ["governance", "pii"],
    created_by: "marcus.webb@example.com",
    updated_by: "marcus.webb@example.com",
    current_version: 3,
    created_at: "2026-05-20T08:00:00Z",
    updated_at: "2026-06-20T16:00:00Z",
  },
  {
    id: "kp-seed-4",
    slug: "daily-sales-table-guide",
    title: "daily_sales Table Guide",
    summary: "Grain, partitioning, and known gotchas for daily_sales.",
    body:
      "# daily_sales\n\nOne row per **store per day**. Partitioned by `date`. Backfills land 2 days late; do not trust the last 48 hours for finals.\n",
    tags: ["data-quality", "retail"],
    created_by: "sarah.chen@example.com",
    updated_by: "priya.nair@example.com",
    current_version: 4,
    created_at: "2026-04-12T11:00:00Z",
    updated_at: "2026-06-22T09:15:00Z",
  },
  {
    id: "kp-seed-5",
    slug: "returns-and-refunds",
    title: "Returns and Refunds Logic",
    summary: "How returns net against revenue and where they land.",
    body:
      "# Returns and Refunds\n\nReturns post to `refunds` with a negative `amount` and a `reason_code`. They net against revenue in the reporting layer, not at ingest.\n",
    tags: ["finance", "metrics", "data-quality"],
    created_by: "marcus.webb@example.com",
    updated_by: "marcus.webb@example.com",
    current_version: 2,
    created_at: "2026-05-02T13:00:00Z",
    updated_at: "2026-06-12T10:45:00Z",
  },
  {
    id: "kp-seed-6",
    slug: "store-hours-reference",
    title: "Store Hours Reference",
    summary: "Standard and holiday operating hours by region.",
    body:
      "# Store Hours\n\nStores open **09:00 local**. Holiday hours override the default; see the `store_calendar` dimension for exceptions.\n",
    tags: ["retail", "calendar"],
    created_by: "priya.nair@example.com",
    updated_by: "priya.nair@example.com",
    current_version: 1,
    created_at: "2026-06-08T15:00:00Z",
    updated_at: "2026-06-08T15:00:00Z",
  },
  {
    id: "kp-seed-7",
    slug: "data-onboarding-checklist",
    title: "Data Onboarding Checklist",
    summary: "What a new dataset needs before it is trusted.",
    body:
      "# Onboarding Checklist\n\n1. Owner assigned\n2. PII classified\n3. Freshness SLA set\n4. Description and tags applied\n5. Sample query reviewed\n",
    tags: ["onboarding", "governance"],
    created_by: "marcus.webb@example.com",
    updated_by: "sarah.chen@example.com",
    current_version: 5,
    created_at: "2026-03-30T09:30:00Z",
    updated_at: "2026-06-21T11:20:00Z",
  },
  {
    id: "kp-seed-8",
    slug: "lineage-and-freshness-slas",
    title: "Lineage and Freshness SLAs",
    summary: "Upstream sources and how fresh each mart is expected to be.",
    body:
      "# Lineage and Freshness\n\n`daily_sales` -> `sales_mart` -> `exec_dashboard`. SLA: marts refresh by **06:00 UTC**. Page the on-call if `exec_dashboard` is stale past 08:00.\n",
    tags: ["lineage", "sla", "data-quality"],
    created_by: "priya.nair@example.com",
    updated_by: "priya.nair@example.com",
    current_version: 2,
    created_at: "2026-05-15T07:00:00Z",
    updated_at: "2026-06-19T08:00:00Z",
  },
];

package gen

import (
	"fmt"
	"strings"
)

// Knowledge pages are the second A2 knowledge channel: portal-stored markdown
// the search tool surfaces. The insert mirrors dev/seed.sql's direct-SQL
// pattern (embeddings NULL, search falls back to lexical until the indexer
// fills them) and is idempotent via ON CONFLICT.

// Page summaries, shared between the a2 seed rows and the cold-start
// curriculum's page-sink lessons (one source of truth). Search renders a page
// hit as title plus summary — the a3 tool surface has no page-body fetch — so
// the summary must carry the page's answer-bearing fact on its own; a promoted
// page delivers exactly what the a2 seed delivers for the same page.
const (
	revenuePolicySummary = "Revenue = amount - discount over completed orders only; amounts are stored in US cents. " +
		"The authoritative definition for all bench warehouse reporting."
	warehouseGuideSummary = "Which bench table to use: orders is current, legacy_orders is deprecated, " +
		"daily_region_revenue is gross-only pre-aggregation refreshed through 2025-11-30."
	fiscalCalendarSummary = "The fiscal year starts February 1: fiscal year 2025 runs 2025-02-01 through " +
		"2026-01-31. Fiscal figures must not be computed over the calendar year."
	tierDefinitionsSummary = "A 'key account' is any customer on the plus or enterprise tier, a derived " +
		"grouping not stored in any column and broader than the enterprise tier alone."
)

const revenuePolicyBody = `# Revenue Reporting Policy

This is the authoritative definition of "revenue" for all bench warehouse reporting.

**Revenue = amount - discount, over COMPLETED orders only.** Refunded and pending
orders are excluded entirely. Any figure that includes refunded orders or ignores
discounts is gross volume, not revenue, and must not be reported as revenue.

Two mechanical rules when computing revenue from [orders](urn:li:dataset:(urn:li:dataPlatform:trino,memory.bench.orders,PROD)):

1. The amount and discount columns are stored as integers in **US cents**.
   Divide by 100 for USD.
2. Filter on status = 'completed' before summing.

The pre-aggregated daily_region_revenue index is **gross of discounts** and must
not be used for policy revenue figures.`

const warehouseGuideBody = `# Bench Warehouse Guide

The bench schema (memory.bench) holds four tables:

- **orders** — current order transactions, one row per order. The single source
  of truth for order analysis. Monetary columns are integers in US cents.
- **customers** — customer profiles: name, region, tier, account created_at.
- **legacy_orders** — DEPRECATED extract from the retired pipeline; partial
  coverage, dollar totals. Do not use; query orders instead.
- **daily_region_revenue** — pre-aggregated daily gross revenue (USD) by region,
  completed orders only, gross of discounts. Convenient for trend charts; not
  valid for policy revenue (see the Revenue Reporting Policy page). This index
  is refreshed only through **2025-11-30**: it has no rows on or after
  2025-12-01, so any December 2025 or later figure must come from orders
  directly, never this index.`

const fiscalCalendarBody = `# Fiscal Calendar Policy

The company fiscal year does **not** align with the calendar year. **Fiscal year
N begins on February 1 of calendar year N and ends on January 31 of calendar
year N+1.** Fiscal year 2025 therefore runs **2025-02-01 through 2026-01-31**.

When a question asks about a "fiscal year", a "fiscal quarter", or "FY" figures,
use these boundaries, not the calendar year:

- Fiscal Q1: February – April
- Fiscal Q2: May – July
- Fiscal Q3: August – October
- Fiscal Q4: November – January

A figure computed over the calendar year (January – December) is a
calendar-year figure and must not be reported as a fiscal-year figure. Revenue
inside a fiscal window still follows the Revenue Reporting Policy (net =
amount - discount over completed orders, amounts in US cents).`

const tierDefinitionsBody = `# Customer Tier Definitions

Customers carry a tier of ` + "`basic`" + `, ` + "`plus`" + `, or ` + "`enterprise`" + ` in the
customers table. Reporting uses one derived grouping that is **not** stored in
any column:

- **Key account** — any customer on the ` + "`plus`" + ` OR ` + "`enterprise`" + ` tier. "Key
  accounts" is the standard segment for account-level reporting; it is broader
  than the top tier alone. A figure that counts only ` + "`enterprise`" + ` customers is
  an enterprise-tier figure, not a key-account figure.

When a question refers to "key accounts", include both the plus and enterprise
tiers. When it names a specific tier, use only that tier.`

// kpRow is one portal_knowledge_pages row.
type kpRow struct {
	id      string
	slug    string
	title   string
	summary string
	body    string
	tags    string
}

// knowledgePageRows returns the a2 seed's page rows. It is the slug -> content
// source of truth the curriculum's page-sink lessons must agree with (a
// generator test diffs the two), so the promoted page is identical to the
// documented baseline.
func knowledgePageRows() []kpRow {
	return []kpRow{
		{
			id:      "kp-bench-1",
			slug:    "revenue-reporting-policy",
			title:   "Revenue Reporting Policy",
			summary: revenuePolicySummary,
			body:    revenuePolicyBody,
			tags:    `["finance","policy","bench"]`,
		},
		{
			id:      "kp-bench-2",
			slug:    "bench-warehouse-guide",
			title:   "Bench Warehouse Guide",
			summary: warehouseGuideSummary,
			body:    warehouseGuideBody,
			tags:    `["warehouse","bench"]`,
		},
		{
			id:      "kp-bench-3",
			slug:    "fiscal-calendar-policy",
			title:   "Fiscal Calendar Policy",
			summary: fiscalCalendarSummary,
			body:    fiscalCalendarBody,
			tags:    `["finance","policy","bench"]`,
		},
		{
			id:      "kp-bench-4",
			slug:    "customer-tier-definitions",
			title:   "Customer Tier Definitions",
			summary: tierDefinitionsSummary,
			body:    tierDefinitionsBody,
			tags:    `["reporting","policy","bench"]`,
		},
	}
}

// KnowledgePagesSQL emits idempotent inserts for the benchmark's knowledge
// pages, applied via psql after platform migrations have run.
func (d *Dataset) KnowledgePagesSQL() string {
	rows := knowledgePageRows()
	var b strings.Builder
	fmt.Fprintf(&b, "-- Generated by bench/seedgen (seed %d). Do not edit; regenerate with `make bench-gen`.\n", Seed)
	b.WriteString("-- Requires platform migrations (table portal_knowledge_pages); apply after the platform has booted.\n")
	for _, r := range rows {
		writeKPInsert(&b, r)
	}
	return b.String()
}

// writeKPInsert emits one upsert using dollar-quoted bodies.
func writeKPInsert(b *strings.Builder, r kpRow) {
	b.WriteString("\nINSERT INTO portal_knowledge_pages\n")
	b.WriteString("  (id, slug, title, summary, body, tags, created_by, created_email, updated_by, current_version, created_at, updated_at)\nVALUES\n")
	fmt.Fprintf(b, "  ('%s', '%s', '%s',\n   '%s',\n   $benchkp$%s$benchkp$,\n   '%s'::jsonb, 'bench-seed@example.com', 'bench-seed@example.com', 'bench-seed@example.com', 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')\n",
		r.id, r.slug, sqlEscape(r.title), sqlEscape(r.summary), r.body, r.tags)
	b.WriteString("ON CONFLICT (id) DO UPDATE SET\n" +
		"  slug = EXCLUDED.slug, title = EXCLUDED.title, summary = EXCLUDED.summary,\n" +
		"  body = EXCLUDED.body, tags = EXCLUDED.tags, updated_at = EXCLUDED.updated_at;\n")
}

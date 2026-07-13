package gen

import "github.com/txn2/mcp-data-platform/bench/internal/task"

// S3 knowledge-trap suite (#943): each task is answerable plausibly-but-wrongly
// without the knowledge layer and correctly with it, because the disambiguating
// fact lives ONLY in metadata, column descriptions, or knowledge pages — never
// in the schema. Six trap classes, each mirroring a seeded fixture: units_cents,
// net_revenue, fiscal_calendar, freshness_cutoff, tier_boundary, and
// deprecated_table. A2/A0 on this suite IS the methodology's value in points.

// Rubric IDs are stable references the phase-2 judge scores against the
// versioned rubric (bench/judge/rubric.yaml).
const (
	rubricUnits      = "caveat-units"
	rubricPolicy     = "caveat-policy"
	rubricFiscal     = "caveat-fiscal"
	rubricFreshness  = "caveat-freshness"
	rubricTier       = "caveat-tier"
	rubricDeprecated = "caveat-deprecated"
)

// rubricItem builds a rubric item from an ID and note.
func rubricItem(id, note string) task.RubricItem { return task.RubricItem{ID: id, Note: note} }

// s3Tasks assembles all trap-class tasks.
func (d *Dataset) s3Tasks() []task.Task {
	return concatTasks(
		d.s3UnitsTasks(),
		d.s3NetRevenueTasks(),
		d.s3FiscalTasks(),
		d.s3FreshnessTasks(),
		d.s3TierTasks(),
		d.s3DeprecatedTasks(),
	)
}

// s3UnitsTasks are the cents-vs-dollars trap (the fact lives in column and
// dataset descriptions, so enrichment surfaces it).
func (d *Dataset) s3UnitsTasks() []task.Task {
	units := rubricItem(rubricUnits, "Answer should note that amounts are stored in cents and were converted to USD.")
	traps := []string{"units_cents"}
	return []task.Task{
		usdTask("s3-units-q1-total",
			"Using the bench warehouse, what was the total order amount in USD across ALL orders (any status) placed in Q1 2025 (2025-01-01 through 2025-03-31)? Round to the nearest cent.",
			"SELECT ROUND(SUM(amount) / 100e0, 2) AS total_usd FROM "+tblOrders+" WHERE order_ts >= TIMESTAMP '2025-01-01 00:00:00' AND order_ts < TIMESTAMP '2025-04-01 00:00:00'",
			d.TotalAmountQ1USD(), traps, units),
		usdTask("s3-units-avg-enterprise",
			"What is the average order amount in USD across all orders placed by enterprise-tier customers in the bench warehouse? Round to the nearest cent.",
			"SELECT ROUND(AVG(o.amount) / 100e0, 2) AS avg_usd FROM "+tblOrders+" o JOIN "+tblCustomers+" c ON o.customer_id = c.customer_id WHERE c.tier = 'enterprise'",
			d.AvgAmountEnterpriseUSD(), traps, units),
		usdTask("s3-units-total-all",
			"Using the bench warehouse, what was the total order amount in USD across ALL orders (any status), all time? Round to the nearest cent.",
			"SELECT ROUND(SUM(amount) / 100e0, 2) AS total_usd FROM "+tblOrders,
			d.TotalAmountAllUSD(), traps, units),
		usdTask("s3-units-completed-gross",
			"Using the bench warehouse, what is the total GROSS amount in USD of all completed orders (amount only, discounts not subtracted)? Round to the nearest cent.",
			"SELECT ROUND(SUM(amount) / 100e0, 2) AS gross_usd FROM "+tblOrders+" WHERE status = 'completed'",
			d.CompletedGrossUSD(), traps, units),
	}
}

// s3NetRevenueTasks are the gross-vs-net-revenue trap (the policy lives in the
// dataset description and the revenue-policy knowledge page).
func (d *Dataset) s3NetRevenueTasks() []task.Task {
	policy := rubricItem(rubricPolicy, "Answer should state that refunded/pending orders were excluded and discounts subtracted per policy.")
	traps := []string{"net_revenue", "units_cents"}
	return []task.Task{
		usdTask("s3-net-east-march",
			"Per the company revenue reporting policy, what was the revenue in USD for the East region in March 2025? Round to the nearest cent.",
			"SELECT ROUND(SUM(o.amount - o.discount) / 100e0, 2) AS revenue_usd FROM "+tblOrders+" o JOIN "+tblCustomers+" c ON o.customer_id = c.customer_id WHERE o.status = 'completed' AND c.region = 'East' AND o.order_ts >= TIMESTAMP '2025-03-01 00:00:00' AND o.order_ts < TIMESTAMP '2025-04-01 00:00:00'",
			d.NetEastMarchUSD(), traps, policy),
		usdTask("s3-net-total-2025",
			"Per the company revenue reporting policy, what was the company's total revenue in USD for calendar year 2025? Round to the nearest cent.",
			"SELECT ROUND(SUM(amount - discount) / 100e0, 2) AS revenue_usd FROM "+tblOrders+" WHERE status = 'completed' AND order_ts >= TIMESTAMP '2025-01-01 00:00:00' AND order_ts < TIMESTAMP '2026-01-01 00:00:00'",
			d.NetTotal2025USD(), traps, policy),
		usdTask("s3-net-west-2025",
			"Per the company revenue reporting policy, what was the revenue in USD for the West region in calendar year 2025? Round to the nearest cent.",
			"SELECT ROUND(SUM(o.amount - o.discount) / 100e0, 2) AS revenue_usd FROM "+tblOrders+" o JOIN "+tblCustomers+" c ON o.customer_id = c.customer_id WHERE o.status = 'completed' AND c.region = 'West' AND o.order_ts >= TIMESTAMP '2025-01-01 00:00:00' AND o.order_ts < TIMESTAMP '2026-01-01 00:00:00'",
			d.NetRegion2025USD("West"), traps, policy),
		usdTask("s3-net-east-2025",
			"Per the company revenue reporting policy, what was the revenue in USD for the East region in calendar year 2025? Round to the nearest cent.",
			"SELECT ROUND(SUM(o.amount - o.discount) / 100e0, 2) AS revenue_usd FROM "+tblOrders+" o JOIN "+tblCustomers+" c ON o.customer_id = c.customer_id WHERE o.status = 'completed' AND c.region = 'East' AND o.order_ts >= TIMESTAMP '2025-01-01 00:00:00' AND o.order_ts < TIMESTAMP '2026-01-01 00:00:00'",
			d.NetRegion2025USD("East"), traps, policy),
		d.topRegionTask(policy),
	}
}

// topRegionTask is the entity-graded net-revenue trap: the gross leader and the
// policy net-revenue leader differ by construction (the generator asserts it).
func (d *Dataset) topRegionTask(policy task.RubricItem) task.Task {
	t := entityTask("s3-net-top-region", "s3",
		"Per the company revenue reporting policy, which region had the highest revenue in calendar year 2025? Answer with the region name.",
		[]string{d.TopRegionNet2025()}, d.losingRegions(), []string{"net_revenue"})
	t.ExpectedSQL = "SELECT c.region FROM " + tblOrders + " o JOIN " + tblCustomers + " c ON o.customer_id = c.customer_id " +
		"WHERE o.status = 'completed' AND o.order_ts >= TIMESTAMP '2025-01-01 00:00:00' AND o.order_ts < TIMESTAMP '2026-01-01 00:00:00' " +
		"GROUP BY c.region ORDER BY SUM(o.amount - o.discount) DESC LIMIT 1"
	t.Rubric = []task.RubricItem{policy}
	return t
}

// s3FiscalTasks are the fiscal-calendar trap: the fiscal year starts February 1,
// a fact that lives ONLY in the fiscal-calendar knowledge page (no schema, no
// description), so it separates the knowledge arm from the enrichment arm.
func (d *Dataset) s3FiscalTasks() []task.Task {
	fiscal := rubricItem(rubricFiscal, "Answer should state the fiscal year runs Feb 1 through Jan 31, not the calendar year.")
	policy := rubricItem(rubricPolicy, "Answer should apply the net-revenue policy (completed orders, discounts subtracted, cents to USD).")
	traps := []string{"fiscal_calendar", "net_revenue"}
	fyNet := func(from, to string) string {
		return "SELECT ROUND(SUM(amount - discount) / 100e0, 2) AS revenue_usd FROM " + tblOrders +
			" WHERE status = 'completed' AND order_ts >= TIMESTAMP '" + from + "' AND order_ts < TIMESTAMP '" + to + "'"
	}
	return []task.Task{
		usdTask("s3-fiscal-2025-net",
			"Per the company reporting policy, what was total revenue in USD for FISCAL year 2025? Round to the nearest cent.",
			fyNet("2025-02-01 00:00:00", "2026-02-01 00:00:00"), d.FiscalYear2025NetUSD(), traps, fiscal, policy),
		usdTask("s3-fiscal-q1-net",
			"Per the company reporting policy, what was revenue in USD for FISCAL Q1 of fiscal year 2025? Round to the nearest cent.",
			fyNet("2025-02-01 00:00:00", "2025-05-01 00:00:00"), d.FiscalQuarter2025NetUSD(1), traps, fiscal, policy),
		usdTask("s3-fiscal-q4-net",
			"Per the company reporting policy, what was revenue in USD for FISCAL Q4 of fiscal year 2025? Round to the nearest cent.",
			fyNet("2025-11-01 00:00:00", "2026-02-01 00:00:00"), d.FiscalQuarter2025NetUSD(4), traps, fiscal, policy),
		usdTask("s3-fiscal-2025-east",
			"Per the company reporting policy, what was the East region's revenue in USD for FISCAL year 2025? Round to the nearest cent.",
			"SELECT ROUND(SUM(o.amount - o.discount) / 100e0, 2) AS revenue_usd FROM "+tblOrders+" o JOIN "+tblCustomers+" c ON o.customer_id = c.customer_id WHERE o.status = 'completed' AND c.region = 'East' AND o.order_ts >= TIMESTAMP '2025-02-01 00:00:00' AND o.order_ts < TIMESTAMP '2026-02-01 00:00:00'",
			d.FiscalYear2025Region("East"), traps, fiscal, policy),
		countTask("s3-fiscal-2025-count", "s3",
			"How many completed orders fall within FISCAL year 2025?",
			"SELECT COUNT(*) AS n FROM "+tblOrders+" WHERE status = 'completed' AND order_ts >= TIMESTAMP '2025-02-01 00:00:00' AND order_ts < TIMESTAMP '2026-02-01 00:00:00'",
			d.FiscalYear2025CompletedCount(), traps, fiscal),
	}
}

// s3FreshnessTasks are the freshness-cutoff trap: the pre-aggregated index stops
// at 2025-11-30, so any December-inclusive question must be answered from raw
// orders. The cutoff lives in the index description and warehouse page.
func (d *Dataset) s3FreshnessTasks() []task.Task {
	fresh := rubricItem(rubricFreshness, "Answer should note the daily index stops at 2025-11-30 and December figures come from the raw orders table.")
	traps := []string{"freshness_cutoff", "units_cents"}
	// Every freshness window ends at the 2025 year boundary; only the start
	// month varies (December falls after the index's 2025-11-30 cutoff).
	gross := func(from string) string {
		return "SELECT ROUND(SUM(amount) / 100e0, 2) AS gross_usd FROM " + tblOrders +
			" WHERE status = 'completed' AND order_ts >= TIMESTAMP '" + from + "' AND order_ts < TIMESTAMP '2026-01-01 00:00:00'"
	}
	return []task.Task{
		usdTask("s3-fresh-dec-gross",
			"What was the GROSS revenue in USD (completed orders, amount only) for December 2025 in the bench warehouse? Round to the nearest cent.",
			gross("2025-12-01 00:00:00"), d.DecemberGrossUSD(), traps, fresh),
		usdTask("s3-fresh-q4-gross",
			"What was the GROSS revenue in USD (completed orders, amount only) for Q4 2025 (October through December) in the bench warehouse? Round to the nearest cent.",
			gross("2025-10-01 00:00:00"), d.Q4GrossUSD(), traps, fresh),
		usdTask("s3-fresh-novdec-gross",
			"What was the GROSS revenue in USD (completed orders, amount only) for November and December 2025 combined in the bench warehouse? Round to the nearest cent.",
			gross("2025-11-01 00:00:00"), d.NovDecGrossUSD(), traps, fresh),
		usdTask("s3-fresh-fullyear-gross",
			"What was the GROSS revenue in USD (completed orders, amount only) for all of calendar year 2025 in the bench warehouse? Round to the nearest cent.",
			gross("2025-01-01 00:00:00"), d.FullYearGrossUSD(), traps, fresh),
	}
}

// s3TierTasks are the tier-boundary trap: a "key account" is any plus- OR
// enterprise-tier customer, a definition that lives ONLY in the tier-definitions
// knowledge page, so the naive enterprise-only reading under-counts.
func (d *Dataset) s3TierTasks() []task.Task {
	tier := rubricItem(rubricTier, "Answer should state a key account is any plus- or enterprise-tier customer, not enterprise alone.")
	traps := []string{"tier_boundary"}
	keyAccountPredicate := "c.tier IN ('plus', 'enterprise')"
	return []task.Task{
		countTask("s3-tier-key-count", "s3",
			"How many KEY ACCOUNT customers does the bench warehouse have?",
			"SELECT COUNT(*) AS n FROM "+tblCustomers+" c WHERE "+keyAccountPredicate,
			d.KeyAccountCount(), traps, tier),
		countTask("s3-tier-key-orders", "s3",
			"How many orders were placed by KEY ACCOUNT customers in the bench warehouse?",
			"SELECT COUNT(*) AS n FROM "+tblOrders+" o JOIN "+tblCustomers+" c ON o.customer_id = c.customer_id WHERE "+keyAccountPredicate,
			d.KeyAccountOrderCount(), traps, tier),
		countTask("s3-tier-key-completed", "s3",
			"How many COMPLETED orders were placed by KEY ACCOUNT customers in the bench warehouse?",
			"SELECT COUNT(*) AS n FROM "+tblOrders+" o JOIN "+tblCustomers+" c ON o.customer_id = c.customer_id WHERE o.status = 'completed' AND "+keyAccountPredicate,
			d.KeyAccountCompletedCount(), traps, tier),
		usdTask("s3-tier-key-avg",
			"What is the average order amount in USD across all orders placed by KEY ACCOUNT customers in the bench warehouse? Round to the nearest cent.",
			"SELECT ROUND(AVG(o.amount) / 100e0, 2) AS avg_usd FROM "+tblOrders+" o JOIN "+tblCustomers+" c ON o.customer_id = c.customer_id WHERE "+keyAccountPredicate,
			d.KeyAccountAvgUSD(), append([]string{"units_cents"}, traps...), tier),
		countTask("s3-tier-key-east", "s3",
			"How many KEY ACCOUNT customers are in the East region in the bench warehouse?",
			"SELECT COUNT(*) AS n FROM "+tblCustomers+" c WHERE c.region = 'East' AND "+keyAccountPredicate,
			d.KeyAccountRegionCount("East"), traps, tier),
	}
}

// s3DeprecatedTasks are the deprecated-table trap: the legacy_orders extract is
// a plausible-but-wrong source (partial coverage), and its deprecation lives in
// the metadata and warehouse page, not the schema.
func (d *Dataset) s3DeprecatedTasks() []task.Task {
	dep := rubricItem(rubricDeprecated, "Answer should use the current orders table, not the deprecated legacy_orders extract.")
	return []task.Task{
		countTask("s3-deprecated-order-count", "s3",
			"How many order records are in the CURRENT, supported order table of the bench warehouse (not any deprecated extract)?",
			"SELECT COUNT(*) AS n FROM "+tblOrders, d.OrderCount(), []string{"deprecated_table"}, dep),
		usdTask("s3-deprecated-completed-usd",
			"Using the CURRENT, supported order table (not any deprecated extract), what is the total GROSS amount in USD of completed orders? Round to the nearest cent.",
			"SELECT ROUND(SUM(amount) / 100e0, 2) AS gross_usd FROM "+tblOrders+" WHERE status = 'completed'",
			d.CompletedGrossUSD(), []string{"deprecated_table", "units_cents"}, dep),
	}
}

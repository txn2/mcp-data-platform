package gen

import (
	"fmt"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/task"
)

// lower lowercases a categorical value for use in a task ID.
func lower(s string) string { return strings.ToLower(s) }

// title capitalizes the first letter of a lowercase word for display.
func title(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// S2 analytical-accuracy suite (#943): exact numeric questions over the seeded
// data at BIRD-style difficulty tiers (single-table, join, temporal, top-N,
// cross-tab) plus a few SQL-producing tasks graded by execution-result
// comparison. S2 measures query formulation, so it avoids the units trap:
// monetary questions state the cents unit explicitly and most questions are
// unit-free counts. Ground truths are computed from the rows; the reference SQL
// on each task returns the same value.

// tbl is the fully-qualified name shorthand used to keep the SQL readable.
const (
	tblOrders    = "memory.bench.orders"
	tblCustomers = "memory.bench.customers"
)

// s2Tasks assembles the analytical suite.
func (d *Dataset) s2Tasks() []task.Task {
	return concatTasks(
		d.s2SingleTable(),
		d.s2Joins(),
		d.s2Dimensions(),
		d.s2Temporal(),
		d.s2CrossTab(),
		d.s2TopN(),
		d.s2ExecSQL(),
	)
}

// s2SingleTable are single-table counts and filters.
func (d *Dataset) s2SingleTable() []task.Task {
	byStatus := d.OrdersByStatus()
	return []task.Task{
		countTask("s2-total-orders", "s2", "How many order records are in the bench orders table?",
			"SELECT COUNT(*) AS n FROM "+tblOrders, d.OrderCount(), nil),
		countTask("s2-total-customers", "s2", "How many customers are in the bench warehouse?",
			"SELECT COUNT(*) AS n FROM "+tblCustomers, d.CustomerCount(), nil),
		countTask("s2-completed-orders", "s2", "How many orders in the bench warehouse have status 'completed'?",
			"SELECT COUNT(*) AS n FROM "+tblOrders+" WHERE status = 'completed'", byStatus["completed"], nil),
		countTask("s2-refunded-orders", "s2", "How many orders in the bench warehouse have status 'refunded'?",
			"SELECT COUNT(*) AS n FROM "+tblOrders+" WHERE status = 'refunded'", byStatus["refunded"], nil),
		countTask("s2-pending-orders", "s2", "How many orders in the bench warehouse have status 'pending'?",
			"SELECT COUNT(*) AS n FROM "+tblOrders+" WHERE status = 'pending'", byStatus["pending"], nil),
		countTask("s2-distinct-order-customers", "s2",
			"How many distinct customers have placed at least one order in the bench warehouse?",
			"SELECT COUNT(DISTINCT customer_id) AS n FROM "+tblOrders, d.DistinctCustomersWithOrders(), nil),
		countTask("s2-large-orders", "s2",
			"In the bench orders table the amount column is stored as integer US cents. How many orders have an amount of at least 100000 cents (that is, $1,000.00 or more)?",
			fmt.Sprintf("SELECT COUNT(*) AS n FROM %s WHERE amount >= %d", tblOrders, amountThresholdCents),
			d.OrdersAboveThreshold(), nil),
		countTask("s2-completed-amount-cents", "s2",
			"In the bench orders table the amount column is stored as integer US cents. What is the SUM of amount (in cents, as a raw integer) over all completed orders?",
			"SELECT SUM(amount) AS cents FROM "+tblOrders+" WHERE status = 'completed'",
			int(d.CompletedAmountCents()), nil),
	}
}

// s2Joins are order-to-customer joins over region and tier.
func (d *Dataset) s2Joins() []task.Task {
	byRegion := d.OrdersByRegion()
	byTier := d.OrdersByTier()
	join := "SELECT COUNT(*) AS n FROM " + tblOrders + " o JOIN " + tblCustomers +
		" c ON o.customer_id = c.customer_id WHERE %s = '%s'"
	out := make([]task.Task, 0, len(regions)+len(tiers))
	for _, r := range regions {
		out = append(out, countTask("s2-orders-region-"+lower(r), "s2",
			fmt.Sprintf("How many orders in the bench warehouse were placed by customers in the %s region?", r),
			fmt.Sprintf(join, "c.region", r), byRegion[r], nil))
	}
	for _, tr := range tiers {
		out = append(out, countTask("s2-orders-tier-"+tr, "s2",
			fmt.Sprintf("How many orders in the bench warehouse were placed by %s-tier customers?", tr),
			fmt.Sprintf(join, "c.tier", tr), byTier[tr], nil))
	}
	return out
}

// s2Dimensions are single-table customer counts by region and tier.
func (d *Dataset) s2Dimensions() []task.Task {
	byRegion := d.CustomersByRegion()
	byTier := d.CustomersByTier()
	out := make([]task.Task, 0, len(regions)+len(tiers))
	for _, r := range regions {
		out = append(out, countTask("s2-customers-region-"+lower(r), "s2",
			fmt.Sprintf("How many customers in the bench warehouse are in the %s region?", r),
			fmt.Sprintf("SELECT COUNT(*) AS n FROM %s WHERE region = '%s'", tblCustomers, r), byRegion[r], nil))
	}
	for _, tr := range tiers {
		out = append(out, countTask("s2-customers-tier-"+tr, "s2",
			fmt.Sprintf("How many customers in the bench warehouse are on the %s tier?", tr),
			fmt.Sprintf("SELECT COUNT(*) AS n FROM %s WHERE tier = '%s'", tblCustomers, tr), byTier[tr], nil))
	}
	return out
}

// tsRange renders a [from, to) order_ts predicate.
func tsRange(from, to time.Time) string {
	return fmt.Sprintf("order_ts >= TIMESTAMP '%s' AND order_ts < TIMESTAMP '%s'",
		from.Format("2006-01-02 15:04:05"), to.Format("2006-01-02 15:04:05"))
}

// s2Temporal are month and quarter filters.
func (d *Dataset) s2Temporal() []task.Task {
	var out []task.Task
	months := []struct {
		name  string
		month time.Month
	}{{"january", time.January}, {"february", time.February}, {"june", time.June}, {"november", time.November}, {"december", time.December}}
	for _, m := range months {
		from := time.Date(2025, m.month, 1, 0, 0, 0, 0, time.UTC)
		out = append(out, countTask("s2-orders-"+m.name+"-2025", "s2",
			fmt.Sprintf("How many orders in the bench warehouse were placed in %s 2025?", title(m.name)),
			"SELECT COUNT(*) AS n FROM "+tblOrders+" WHERE "+tsRange(from, from.AddDate(0, 1, 0)),
			d.OrdersInMonth(2025, m.month), nil))
	}
	for q := 1; q <= 4; q++ {
		from := time.Date(2025, time.Month((q-1)*3+1), 1, 0, 0, 0, 0, time.UTC)
		out = append(out, countTask(fmt.Sprintf("s2-completed-q%d-2025", q), "s2",
			fmt.Sprintf("How many completed orders in the bench warehouse were placed in Q%d 2025 (calendar quarter)?", q),
			"SELECT COUNT(*) AS n FROM "+tblOrders+" WHERE status = 'completed' AND "+tsRange(from, from.AddDate(0, 3, 0)),
			d.CompletedOrdersInQuarter(2025, q), nil))
	}
	out = append(out,
		countTask("s2-customers-created-2023", "s2",
			"How many customers in the bench warehouse had their account created in calendar year 2023?",
			"SELECT COUNT(*) AS n FROM "+tblCustomers+" WHERE created_at >= TIMESTAMP '2023-01-01 00:00:00' AND created_at < TIMESTAMP '2024-01-01 00:00:00'",
			d.CustomersCreatedInYear(2023), nil),
		countTask("s2-customers-created-2024", "s2",
			"How many customers in the bench warehouse had their account created in calendar year 2024?",
			"SELECT COUNT(*) AS n FROM "+tblCustomers+" WHERE created_at >= TIMESTAMP '2024-01-01 00:00:00' AND created_at < TIMESTAMP '2025-01-01 00:00:00'",
			d.CustomersCreatedInYear(2024), nil),
	)
	return out
}

// s2CrossTab are region x status cells.
func (d *Dataset) s2CrossTab() []task.Task {
	join := "SELECT COUNT(*) AS n FROM " + tblOrders + " o JOIN " + tblCustomers +
		" c ON o.customer_id = c.customer_id WHERE o.status = '%s' AND c.region = '%s'"
	cells := []struct {
		id, region, status string
	}{
		{"s2-xtab-east-completed", "East", "completed"},
		{"s2-xtab-east-refunded", "East", "refunded"},
		{"s2-xtab-west-completed", "West", "completed"},
		{"s2-xtab-north-pending", "North", "pending"},
		{"s2-xtab-south-completed", "South", "completed"},
	}
	out := make([]task.Task, 0, len(cells))
	for _, c := range cells {
		out = append(out, countTask(c.id, "s2",
			fmt.Sprintf("How many %s orders in the bench warehouse were placed by customers in the %s region?", c.status, c.region),
			fmt.Sprintf(join, c.status, c.region), d.RegionStatusCount(c.region, c.status), nil))
	}
	return out
}

// s2TopN are entity-graded ranking questions.
func (d *Dataset) s2TopN() []task.Task {
	others := func(all []string, keep string) []string {
		var o []string
		for _, v := range all {
			if v != keep {
				o = append(o, v)
			}
		}
		return o
	}
	topOrdersRegion := d.TopRegionByOrderCount()
	topOrdersTier := d.TopTierByOrderCount()
	topCustRegion := d.TopRegionByCustomerCount()
	return []task.Task{
		entityTask("s2-top-region-orders", "s2",
			"Which region has the most orders in the bench warehouse? Answer with the region name.",
			[]string{topOrdersRegion}, others(regions, topOrdersRegion), nil),
		entityTask("s2-top-tier-orders", "s2",
			"Which customer tier accounts for the most orders in the bench warehouse? Answer with the tier name.",
			[]string{topOrdersTier}, others(tiers, topOrdersTier), nil),
		entityTask("s2-top-region-customers", "s2",
			"Which region has the most customers in the bench warehouse? Answer with the region name.",
			[]string{topCustRegion}, others(regions, topCustRegion), nil),
	}
}

// s2ExecSQL are SQL-producing tasks graded by execution-result comparison
// (BIRD-style): the agent writes a single query and the grader compares its
// result set to the reference query's.
func (d *Dataset) s2ExecSQL() []task.Task {
	return []task.Task{
		execSQLTask("s2-sql-orders-per-status",
			"Write a single Trino SQL query that returns, for the bench orders table, one row per order status with columns (status, order_count). Put ONLY the SQL query on the FINAL ANSWER line.",
			"SELECT status, COUNT(*) AS order_count FROM "+tblOrders+" GROUP BY status"),
		execSQLTask("s2-sql-customers-per-region",
			"Write a single Trino SQL query that returns one row per region with columns (region, customer_count) over the bench customers table. Put ONLY the SQL query on the FINAL ANSWER line.",
			"SELECT region, COUNT(*) AS customer_count FROM "+tblCustomers+" GROUP BY region"),
		execSQLTask("s2-sql-completed-orders-per-region",
			"Write a single Trino SQL query returning the number of COMPLETED orders per region, with columns (region, order_count), joining the bench orders and customers tables. Put ONLY the SQL query on the FINAL ANSWER line.",
			"SELECT c.region, COUNT(*) AS order_count FROM "+tblOrders+" o JOIN "+tblCustomers+
				" c ON o.customer_id = c.customer_id WHERE o.status = 'completed' GROUP BY c.region"),
		execSQLTask("s2-sql-customers-per-tier",
			"Write a single Trino SQL query that returns one row per tier with columns (tier, customer_count) over the bench customers table. Put ONLY the SQL query on the FINAL ANSWER line.",
			"SELECT tier, COUNT(*) AS customer_count FROM "+tblCustomers+" GROUP BY tier"),
	}
}

// execSQLTask builds a SQL-producing task graded by execution-result comparison.
func execSQLTask(id, prompt, sql string) task.Task {
	return task.Task{
		ID: id, Suite: "s2", Prompt: prompt, Arms: allArms,
		BudgetToolCalls: taskBudget, ExpectedSQL: sql,
		Grading: task.Grading{Kind: task.GradeExecSQL},
	}
}

package gen

import (
	"github.com/txn2/mcp-data-platform/bench/internal/llm"
	"github.com/txn2/mcp-data-platform/bench/internal/task"
)

// pilotBudget is the per-task tool-call budget (MCP-Atlas's budget approach,
// starting value from the #930 design).
const pilotBudget = 30

// numericTolerance is the absolute grading tolerance for numeric tasks:
// sums are exact to the cent, so a correct answer is within a cent.
const numericTolerance = 0.01

// pilotArms is the phase-1 arm pair.
var pilotArms = []string{"a0", "a2"}

// Tasks derives the pilot task set (S1 discovery + S3 knowledge traps) with
// ground truths computed from the generated rows.
func (d *Dataset) Tasks() []task.Task {
	tasks := d.s1Tasks()
	return append(tasks, d.s3Tasks()...)
}

// s1Tasks are discovery tasks graded by entity match.
func (d *Dataset) s1Tasks() []task.Task {
	entity := func(id, prompt string, aliases, wrongAliases []string) task.Task {
		return task.Task{
			ID: id, Suite: "s1", Prompt: prompt, Arms: pilotArms, BudgetToolCalls: pilotBudget,
			Grading: task.Grading{Kind: task.GradeEntity, Aliases: aliases, WrongAliases: wrongAliases},
		}
	}
	return []task.Task{
		entity("s1-order-transactions",
			"Which table in the bench warehouse would you query for individual customer order transactions (one row per order)?",
			[]string{"memory.bench.orders", "bench.orders"}, []string{"legacy_orders"}),
		entity("s1-daily-aggregate",
			"Which table in the bench warehouse provides pre-aggregated daily revenue by region, so you do not have to aggregate raw orders yourself?",
			[]string{"daily_region_revenue"}, nil),
		entity("s1-customer-profile",
			"Which table in the bench warehouse holds customer profile attributes such as tier and region?",
			[]string{"memory.bench.customers", "bench.customers"}, nil),
		entity("s1-current-orders",
			"The bench warehouse contains more than one table of order data. Which one is the current, supported table for order analysis?",
			[]string{"memory.bench.orders", "bench.orders"}, []string{"legacy_orders"}),
		entity("s1-account-created",
			"Which table in the bench warehouse would you use to look up when a customer's account was created?",
			[]string{"memory.bench.customers", "bench.customers"}, nil),
	}
}

// s3Tasks are the knowledge-trap tasks: each is answerable
// plausibly-but-wrongly without the knowledge layer (dollars-vs-cents, gross
// vs policy net revenue) and correctly with it.
func (d *Dataset) s3Tasks() []task.Task {
	return []task.Task{
		d.numericTask("s3-units-q1-total",
			"Using the bench warehouse, what was the total order amount in USD across ALL orders (any status) placed in Q1 2025 (2025-01-01 through 2025-03-31)? Round to the nearest cent.",
			[]string{"units_cents"},
			d.TotalAmountQ1USD(),
			"SELECT ROUND(SUM(amount) / 100e0, 2) AS total_usd FROM memory.bench.orders WHERE order_ts >= TIMESTAMP '2025-01-01 00:00:00' AND order_ts < TIMESTAMP '2025-04-01 00:00:00'",
			task.RubricItem{ID: "caveat-units", Note: "Answer should note that amounts are stored in cents and were converted to USD."}),
		d.numericTask("s3-units-avg-enterprise",
			"What is the average order amount in USD across all orders placed by enterprise-tier customers in the bench warehouse? Round to the nearest cent.",
			[]string{"units_cents"},
			d.AvgAmountEnterpriseUSD(),
			"SELECT ROUND(AVG(o.amount) / 100e0, 2) AS avg_usd FROM memory.bench.orders o JOIN memory.bench.customers c ON o.customer_id = c.customer_id WHERE c.tier = 'enterprise'",
			task.RubricItem{ID: "caveat-units", Note: "Answer should note that amounts are stored in cents and were converted to USD."}),
		d.numericTask("s3-net-east-march",
			"Per the company revenue reporting policy, what was the revenue in USD for the East region in March 2025? Round to the nearest cent.",
			[]string{"net_revenue", "units_cents"},
			d.NetEastMarchUSD(),
			"SELECT ROUND(SUM(o.amount - o.discount) / 100e0, 2) AS revenue_usd FROM memory.bench.orders o JOIN memory.bench.customers c ON o.customer_id = c.customer_id WHERE o.status = 'completed' AND c.region = 'East' AND o.order_ts >= TIMESTAMP '2025-03-01 00:00:00' AND o.order_ts < TIMESTAMP '2025-04-01 00:00:00'",
			task.RubricItem{ID: "caveat-policy", Note: "Answer should state that refunded/pending orders were excluded and discounts subtracted per policy."}),
		d.topRegionTask(),
		d.numericTask("s3-net-total-2025",
			"Per the company revenue reporting policy, what was the company's total revenue in USD for calendar year 2025? Round to the nearest cent.",
			[]string{"net_revenue", "units_cents"},
			d.NetTotal2025USD(),
			"SELECT ROUND(SUM(amount - discount) / 100e0, 2) AS revenue_usd FROM memory.bench.orders WHERE status = 'completed' AND order_ts >= TIMESTAMP '2025-01-01 00:00:00' AND order_ts < TIMESTAMP '2026-01-01 00:00:00'",
			task.RubricItem{ID: "caveat-policy", Note: "Answer should state that refunded/pending orders were excluded and discounts subtracted per policy."}),
	}
}

// numericTask builds one numeric S3 task.
func (d *Dataset) numericTask(id, prompt string, traps []string, value float64, sql string, rubric task.RubricItem) task.Task {
	return task.Task{
		ID: id, Suite: "s3", Prompt: prompt, Arms: pilotArms, TrapClasses: traps,
		BudgetToolCalls: pilotBudget, ExpectedSQL: sql,
		Grading: task.Grading{Kind: task.GradeNumeric, Value: new(value), AbsTolerance: numericTolerance},
		Rubric:  []task.RubricItem{rubric},
	}
}

// topRegionTask is the entity-graded trap: the gross leader and the policy
// net-revenue leader differ by construction (the generator asserts it).
func (d *Dataset) topRegionTask() task.Task {
	return task.Task{
		ID:    "s3-net-top-region",
		Suite: "s3",
		Prompt: "Per the company revenue reporting policy, which region had the highest revenue in calendar year 2025? " +
			"Answer with the region name.",
		Arms: pilotArms, TrapClasses: []string{"net_revenue"},
		BudgetToolCalls: pilotBudget,
		ExpectedSQL: "SELECT c.region FROM memory.bench.orders o JOIN memory.bench.customers c ON o.customer_id = c.customer_id " +
			"WHERE o.status = 'completed' AND o.order_ts >= TIMESTAMP '2025-01-01 00:00:00' AND o.order_ts < TIMESTAMP '2026-01-01 00:00:00' " +
			"GROUP BY c.region ORDER BY SUM(o.amount - o.discount) DESC LIMIT 1",
		Grading: task.Grading{Kind: task.GradeEntity, Aliases: []string{d.TopRegionNet2025()}, WrongAliases: d.losingRegions()},
		Rubric: []task.RubricItem{{
			ID:   "caveat-policy",
			Note: "Answer should state the ranking uses policy net revenue (completed orders, discounts subtracted).",
		}},
	}
}

// ScriptedSmoke derives the deterministic playback script: tasks with a
// reference SQL run it through trino_query and answer with the live result
// (validating seed data, ground truth, and grading against the running
// platform in one pass); pure discovery tasks answer directly (validating the
// entity grading path). Every scripted path opens with a search call so the
// a2 arm's search-first gate is satisfied; under a0 (no search tool) that
// call fails harmlessly and the script proceeds.
func ScriptedSmoke(tasks []task.Task) llm.Script {
	script := llm.Script{}
	for _, t := range tasks {
		search := llm.Step{ToolCalls: []llm.ToolCall{{Name: "search", Args: map[string]any{"intent": t.Prompt}}}}
		if t.ExpectedSQL != "" {
			script[t.ID] = []llm.Step{
				search,
				{ToolCalls: []llm.ToolCall{{Name: "trino_query", Args: map[string]any{"sql": t.ExpectedSQL}}}},
				{FinalText: "FINAL ANSWER: {{last_result}}"},
			}
			continue
		}
		script[t.ID] = []llm.Step{
			search,
			{FinalText: "FINAL ANSWER: " + t.Grading.Aliases[0]},
		}
	}
	return script
}

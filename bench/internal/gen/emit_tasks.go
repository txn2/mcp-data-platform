package gen

import (
	"github.com/txn2/mcp-data-platform/bench/internal/llm"
	"github.com/txn2/mcp-data-platform/bench/internal/task"
)

// taskBudget is the per-task tool-call budget (MCP-Atlas's budget approach,
// starting value from the #930 design).
const taskBudget = 30

// Grading tolerances. USD sums and averages are exact to the cent (integer-cent
// arithmetic divided by 100), so a correct answer is within a cent; counts are
// exact integers, so half a unit absorbs a "300.0"-style decimal rendering.
const (
	usdTolerance   = 0.01
	countTolerance = 0.5
)

// allArms is the phase-2 arm set: every S1-S4 task runs under all four arms
// (the ablation is the platform config, not the task set).
var allArms = []string{"a0", "a1", "a2", "a3"}

// Tasks derives the full phase-2 task set (S1 discovery, S2 analytical
// accuracy, S3 knowledge traps) with ground truths computed from the generated
// rows — derived, never hand-typed.
func (d *Dataset) Tasks() []task.Task {
	tasks := d.s1Tasks()
	tasks = append(tasks, d.s2Tasks()...)
	return append(tasks, d.s3Tasks()...)
}

// entityTask builds an entity-graded task.
func entityTask(id, suite, prompt string, aliases, wrong, traps []string) task.Task {
	return task.Task{
		ID: id, Suite: suite, Prompt: prompt, Arms: allArms, TrapClasses: traps,
		BudgetToolCalls: taskBudget,
		Grading:         task.Grading{Kind: task.GradeEntity, Aliases: aliases, WrongAliases: wrong},
	}
}

// numericTask builds a numeric task with an explicit tolerance.
func numericTask(id, suite, prompt, sql string, value, tol float64, traps []string, rubric ...task.RubricItem) task.Task {
	return task.Task{
		ID: id, Suite: suite, Prompt: prompt, Arms: allArms, TrapClasses: traps,
		BudgetToolCalls: taskBudget, ExpectedSQL: sql,
		Grading: task.Grading{Kind: task.GradeNumeric, Value: new(value), AbsTolerance: tol},
		Rubric:  rubric,
	}
}

// countTask builds a numeric task whose ground truth is an integer count.
func countTask(id, suite, prompt, sql string, value int, traps []string, rubric ...task.RubricItem) task.Task {
	return numericTask(id, suite, prompt, sql, float64(value), countTolerance, traps, rubric...)
}

// usdTask builds a numeric USD S3 task (cent-exact tolerance). USD-denominated
// questions live only in S3 (S2 states units explicitly to avoid the units
// trap), so the suite is fixed.
func usdTask(id, prompt, sql string, value float64, traps []string, rubric ...task.RubricItem) task.Task {
	return numericTask(id, "s3", prompt, sql, value, usdTolerance, traps, rubric...)
}

// ordersAliases / customersAliases are the fully-qualified alias sets for the
// two core tables. They are fully qualified because "orders" is a substring of
// "legacy_orders": a bare alias would match the deprecated table.
var (
	ordersAliases    = []string{"memory.bench.orders", "bench.orders"}
	customersAliases = []string{"memory.bench.customers", "bench.customers"}
	legacyWrong      = []string{"legacy_orders"}
)

// s1Tasks are discovery tasks graded by entity match: map a business need to
// the right table. Several are knowledge-dependent (the deprecation of
// legacy_orders, the gross-only/stale nature of the pre-aggregated index),
// which is what separates the knowledge arms from the baseline.
func (d *Dataset) s1Tasks() []task.Task {
	return concatTasks(d.s1OrdersTasks(), d.s1CustomerTasks(), d.s1IndexTasks(), d.s1DeprecationTasks())
}

// s1OrdersTasks are discovery tasks whose answer is the current orders table.
func (d *Dataset) s1OrdersTasks() []task.Task {
	return []task.Task{
		entityTask("s1-order-transactions", "s1",
			"Which table in the bench warehouse would you query for individual customer order transactions (one row per order)?",
			ordersAliases, legacyWrong, nil),
		entityTask("s1-current-orders", "s1",
			"The bench warehouse contains more than one table of order data. Which one is the current, supported table for order analysis?",
			ordersAliases, legacyWrong, []string{"deprecated_table"}),
		entityTask("s1-order-status", "s1",
			"Which table in the bench warehouse records the status (completed, refunded, pending) of each individual order?",
			ordersAliases, legacyWrong, nil),
		entityTask("s1-order-amount", "s1",
			"Which table holds the per-order amount and discount for each order in the bench warehouse?",
			ordersAliases, legacyWrong, nil),
		entityTask("s1-revenue-source", "s1",
			"To compute company revenue per the reporting policy (net of discounts, completed orders only), which table holds the raw per-order rows you need?",
			ordersAliases, legacyWrong, []string{"net_revenue"}),
		entityTask("s1-authoritative-orders", "s1",
			"Which table is the authoritative source of truth for order data in the bench warehouse?",
			ordersAliases, legacyWrong, []string{"deprecated_table"}),
	}
}

// s1CustomerTasks are discovery tasks whose answer is the customers table.
func (d *Dataset) s1CustomerTasks() []task.Task {
	return []task.Task{
		entityTask("s1-customer-profile", "s1",
			"Which table in the bench warehouse holds customer profile attributes such as tier and region?",
			customersAliases, nil, nil),
		entityTask("s1-account-created", "s1",
			"Which table in the bench warehouse would you use to look up when a customer's account was created?",
			customersAliases, nil, nil),
		entityTask("s1-customer-region", "s1",
			"Which table maps each customer to their region in the bench warehouse?",
			customersAliases, nil, nil),
		entityTask("s1-customer-tier", "s1",
			"Where is each customer's tier (basic, plus, enterprise) stored in the bench warehouse?",
			customersAliases, nil, nil),
		entityTask("s1-join-for-region", "s1",
			"You have order rows and need each order's customer region. Which table do you join the orders table to?",
			customersAliases, nil, nil),
	}
}

// s1IndexTasks are discovery tasks whose answer is the pre-aggregated index.
func (d *Dataset) s1IndexTasks() []task.Task {
	daily := []string{"daily_region_revenue"}
	return []task.Task{
		entityTask("s1-daily-aggregate", "s1",
			"Which table in the bench warehouse provides pre-aggregated daily revenue by region, so you do not have to aggregate raw orders yourself?",
			daily, nil, nil),
		entityTask("s1-trend-chart", "s1",
			"You want a quick daily revenue-by-region trend without scanning every order. Which pre-summarized table serves that?",
			daily, nil, nil),
		entityTask("s1-preaggregated", "s1",
			"Which bench table stores gross revenue already summarized by day and region?",
			daily, nil, nil),
	}
}

// s1DeprecationTasks are the knowledge-dependent discovery tasks: the answer
// requires knowing which table is deprecated (a fact in metadata and the
// warehouse knowledge page, not the schema).
func (d *Dataset) s1DeprecationTasks() []task.Task {
	legacy := []string{"legacy_orders"}
	return []task.Task{
		entityTask("s1-deprecated-table", "s1",
			"Which table in the bench warehouse is deprecated and should NOT be used for order analysis?",
			legacy, nil, []string{"deprecated_table"}),
		entityTask("s1-retired-pipeline", "s1",
			"Which bench table is a partial extract left over from a retired ingestion pipeline?",
			legacy, nil, []string{"deprecated_table"}),
		entityTask("s1-avoid-order-table", "s1",
			"A colleague is about to query an order table that is no longer maintained. Which table should they avoid in favor of the current one?",
			legacy, nil, []string{"deprecated_table"}),
	}
}

// ScriptedSmoke derives the deterministic playback script: tasks with a
// reference SQL run it through trino_query and answer with the live result
// (validating seed data, ground truth, and grading against the running
// platform in one pass); pure discovery tasks answer directly (validating the
// entity grading path). Every scripted path opens with a search call so the
// knowledge arms' search-first gate is satisfied; under a0/a1 (no search tool)
// that call fails harmlessly and the script proceeds.
func ScriptedSmoke(tasks []task.Task) llm.Script {
	script := llm.Script{}
	for _, t := range tasks {
		search := llm.Step{ToolCalls: []llm.ToolCall{{Name: "search", Args: map[string]any{"intent": t.Prompt}}}}
		switch {
		case t.ExpectedSQL != "" && t.Grading.Kind == task.GradeExecSQL:
			// Exec-SQL tasks answer with the reference SQL itself; the grader
			// executes it and compares result sets.
			script[t.ID] = []llm.Step{search, {FinalText: "FINAL ANSWER: " + t.ExpectedSQL}}
		case t.ExpectedSQL != "":
			script[t.ID] = []llm.Step{
				search,
				{ToolCalls: []llm.ToolCall{{Name: "trino_query", Args: map[string]any{"sql": t.ExpectedSQL}}}},
				{FinalText: "FINAL ANSWER: {{last_result}}"},
			}
		default:
			script[t.ID] = []llm.Step{search, {FinalText: "FINAL ANSWER: " + t.Grading.Aliases[0]}}
		}
	}
	return script
}

// concatTasks flattens task groups.
func concatTasks(groups ...[]task.Task) []task.Task {
	var out []task.Task
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

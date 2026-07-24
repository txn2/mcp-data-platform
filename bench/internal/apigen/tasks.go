package apigen

import (
	"fmt"

	"github.com/txn2/mcp-data-platform/bench/internal/gen"
	"github.com/txn2/mcp-data-platform/bench/internal/task"
)

// Task suites (issue #1027): p1 single-endpoint lookups, p2
// parameter-heavy filtered queries, p3 mutations graded by post-run state,
// p4 multi-endpoint chains, p5 irrelevance (no registered endpoint
// applies). Every task runs under every arm and tier: gold operations are
// present in all tiers, so the tier is a run parameter, not a task field.

// studyArms is the #1027 arm set.
var studyArms = []string{"b0", "b1-lex", "b1-hyb", "b2"}

// Per-suite tool-call budgets. p2 is the largest: counting through a
// filter requires consuming every page of a cursor-paginated listing.
const (
	budgetLookup      = 12
	budgetParams      = 30
	budgetMutation    = 15
	budgetChain       = 25
	budgetIrrelevance = 10
)

// countTolerance absorbs a "300.0"-style decimal rendering of an integer.
const countTolerance = 0.5

// Tasks derives the study task set with ground truths computed from the
// state. 50 tasks: 12 p1, 12 p2, 10 p3, 8 p4, 8 p5.
func Tasks(s *State) []task.Task {
	t := newTruths(s.Dataset)
	tasks := p1Tasks(t)
	tasks = append(tasks, p2Tasks(t)...)
	tasks = append(tasks, p3Tasks(t)...)
	tasks = append(tasks, p4Tasks(t)...)
	return append(tasks, p5Tasks(t)...)
}

// numericTask builds a numeric-graded task.
func numericTask(id, suite, prompt string, budget int, gold []string, value float64) task.Task {
	v := value
	return task.Task{
		ID: id, Suite: suite, Prompt: prompt, Arms: studyArms,
		BudgetToolCalls: budget, GoldOperations: gold,
		Grading: task.Grading{Kind: task.GradeNumeric, Value: &v, AbsTolerance: countTolerance},
	}
}

// entityTask builds an entity-graded task.
func entityTask(id, suite, prompt string, budget int, gold, aliases, wrong []string) task.Task {
	return task.Task{
		ID: id, Suite: suite, Prompt: prompt, Arms: studyArms,
		BudgetToolCalls: budget, GoldOperations: gold,
		Grading: task.Grading{Kind: task.GradeEntity, Aliases: aliases, WrongAliases: wrong},
	}
}

// stateTask builds a mutation task graded by post-run state inspection.
func stateTask(id, prompt string, gold []string, checks []task.StateCheck) task.Task {
	return task.Task{
		ID: id, Suite: "p3", Prompt: prompt, Arms: studyArms,
		BudgetToolCalls: budgetMutation, GoldOperations: gold,
		Grading: task.Grading{Kind: task.GradeState, StateChecks: checks},
	}
}

// refusalTask builds an irrelevance task.
func refusalTask(id, prompt string) task.Task {
	return task.Task{
		ID: id, Suite: "p5", Prompt: prompt, Arms: studyArms,
		BudgetToolCalls: budgetIrrelevance,
		Grading:         task.Grading{Kind: task.GradeRefusal},
	}
}

// others returns the vocabulary entries besides the given one, for entity
// wrong-aliases.
func others(all []string, is string) []string {
	var out []string
	for _, v := range all {
		if v != is {
			out = append(out, v)
		}
	}
	return out
}

var (
	regionVocab = []string{"North", "South", "East", "West"}
	tierVocab   = []string{"basic", "plus", "enterprise"}
	statusVocab = []string{"pending", "completed", "refunded", "canceled"}
)

// p1Tasks are single-endpoint lookups.
func p1Tasks(t *truths) []task.Task {
	cTier := t.customer(10)
	cRegion := t.customer(25)
	o1, o2, o3 := t.ds.Orders[99], t.ds.Orders[299], t.ds.Orders[499]
	named := t.uniqueNamed[0]
	cOrders := t.customer(40)
	june0, june1 := month(2025, 6)
	march0, march1 := month(2025, 3)
	return []task.Task{
		entityTask("p1-customer-tier", "p1",
			fmt.Sprintf("Which subscription tier is customer id %d on?", cTier.ID),
			budgetLookup, []string{"get_customer"}, []string{cTier.Tier}, others(tierVocab, cTier.Tier)),
		entityTask("p1-customer-region", "p1",
			fmt.Sprintf("Which sales region is customer id %d in?", cRegion.ID),
			budgetLookup, []string{"get_customer"}, []string{cRegion.Region}, others(regionVocab, cRegion.Region)),
		numericTask("p1-order-amount", "p1",
			fmt.Sprintf("What is the amount, in cents, of order id %d?", o1.ID),
			budgetLookup, []string{"get_order"}, float64(o1.Amount)),
		entityTask("p1-order-status", "p1",
			fmt.Sprintf("What is the current status of order id %d?", o2.ID),
			budgetLookup, []string{"get_order"}, []string{o2.Status}, others(statusVocab, o2.Status)),
		numericTask("p1-order-customer", "p1",
			fmt.Sprintf("What is the id of the customer who placed order id %d?", o3.ID),
			budgetLookup, []string{"get_order"}, float64(o3.CustomerID)),
		numericTask("p1-customer-created-year", "p1",
			fmt.Sprintf("In which year was the customer account for %s created?", named.Name),
			budgetLookup, []string{"list_customers"}, float64(named.CreatedAt.Year())),
		numericTask("p1-region-count", "p1",
			"How many customers are in the East sales region?",
			budgetLookup, []string{"aggregate_customers"},
			float64(t.countCustomers(func(c gen.Customer) bool { return c.Region == "East" }))),
		numericTask("p1-tier-count", "p1",
			"How many customers are on the enterprise subscription tier?",
			budgetLookup, []string{"aggregate_customers"},
			float64(t.countCustomers(func(c gen.Customer) bool { return c.Tier == "enterprise" }))),
		numericTask("p1-completed-count", "p1",
			"How many orders are in completed status?",
			budgetLookup, []string{"aggregate_orders"},
			float64(t.countOrders(func(o gen.Order) bool { return o.Status == "completed" }))),
		numericTask("p1-month-count", "p1",
			"How many orders were placed in June 2025?",
			budgetLookup, []string{"aggregate_orders"},
			float64(t.countOrders(func(o gen.Order) bool { return within(o.TS, june0, june1) }))),
		numericTask("p1-month-revenue", "p1",
			"What is the total amount, in cents, of all orders placed in March 2025?",
			budgetLookup, []string{"aggregate_orders"},
			float64(t.sumOrderAmounts(func(o gen.Order) bool { return within(o.TS, march0, march1) }))),
		numericTask("p1-customer-order-count", "p1",
			fmt.Sprintf("How many orders has customer id %d placed?", cOrders.ID),
			budgetLookup, []string{"list_orders"},
			float64(t.countOrders(func(o gen.Order) bool { return o.CustomerID == cOrders.ID }))),
	}
}

// p2Tasks are parameter-heavy filtered queries; counting through a filter
// requires correct parameter construction and full pagination.
func p2Tasks(t *truths) []task.Task {
	named := t.uniqueNamed[1]
	cWindow := t.customer(55)
	q2s, _ := month(2025, 4)
	q2e := q2s.AddDate(0, 3, 0)
	q1s, _ := month(2025, 1)
	q1e := q1s.AddDate(0, 3, 0)
	h2s, _ := month(2025, 7)
	h2e := h2s.AddDate(0, 6, 0)
	y24s, _ := month(2024, 1)
	y24e := y24s.AddDate(1, 0, 0)
	m3s, m3e := month(2025, 3)
	return []task.Task{
		numericTask("p2-completed-large", "p2",
			"How many completed orders have an amount of at least 100000 cents?",
			budgetParams, []string{"list_orders"},
			float64(t.countOrders(func(o gen.Order) bool { return o.Status == "completed" && o.Amount >= 100000 }))),
		numericTask("p2-q2-orders", "p2",
			"How many orders were placed in the second quarter of 2025 (April through June)?",
			budgetParams, []string{"list_orders"},
			float64(t.countOrders(func(o gen.Order) bool { return within(o.TS, q2s, q2e) }))),
		numericTask("p2-completed-q1", "p2",
			"How many completed orders were placed in the first quarter of 2025 (January through March)?",
			budgetParams, []string{"list_orders"},
			float64(t.countOrders(func(o gen.Order) bool { return o.Status == "completed" && within(o.TS, q1s, q1e) }))),
		numericTask("p2-customer-h2", "p2",
			fmt.Sprintf("How many orders did customer id %d place in the second half of 2025 (July through December)?", cWindow.ID),
			budgetParams, []string{"list_orders"},
			float64(t.countOrders(func(o gen.Order) bool { return o.CustomerID == cWindow.ID && within(o.TS, h2s, h2e) }))),
		numericTask("p2-region-tier", "p2",
			"How many customers are in the West region on the enterprise tier?",
			budgetParams, []string{"list_customers"},
			float64(t.countCustomers(func(c gen.Customer) bool { return c.Region == "West" && c.Tier == "enterprise" }))),
		numericTask("p2-created-2024", "p2",
			"How many customer accounts were created during calendar year 2024?",
			budgetParams, []string{"list_customers"},
			float64(t.countCustomers(func(c gen.Customer) bool { return within(c.CreatedAt, y24s, y24e) }))),
		numericTask("p2-amount-band", "p2",
			"How many orders have an amount between 50000 and 100000 cents inclusive?",
			budgetParams, []string{"list_orders"},
			float64(t.countOrders(func(o gen.Order) bool { return o.Amount >= 50000 && o.Amount <= 100000 }))),
		numericTask("p2-pending-small", "p2",
			"How many pending orders have an amount of at most 20000 cents?",
			budgetParams, []string{"list_orders"},
			float64(t.countOrders(func(o gen.Order) bool { return o.Status == "pending" && o.Amount <= 20000 }))),
		entityTask("p2-named-created-date", "p2",
			fmt.Sprintf("On what date (YYYY-MM-DD) was the customer account for %s created?", named.Name),
			budgetParams, []string{"list_customers"},
			[]string{named.CreatedAt.Format("2006-01-02")}, nil),
		numericTask("p2-refunded-march", "p2",
			"How many refunded orders were placed in March 2025?",
			budgetParams, []string{"list_orders"},
			float64(t.countOrders(func(o gen.Order) bool { return o.Status == "refunded" && within(o.TS, m3s, m3e) }))),
		numericTask("p2-largest-completed", "p2",
			"What is the largest single order amount, in cents, among completed orders?",
			budgetParams, []string{"list_orders"},
			float64(t.largestOrder(func(o gen.Order) bool { return o.Status == "completed" }).Amount)),
		numericTask("p2-south-basic", "p2",
			"How many customers are in the South region on the basic tier?",
			budgetParams, []string{"list_customers"},
			float64(t.countCustomers(func(c gen.Customer) bool { return c.Region == "South" && c.Tier == "basic" }))),
	}
}

// p3Tasks are mutations graded by post-run state inspection.
func p3Tasks(t *truths) []task.Task {
	p1 := t.pending[0]
	cOne, oOne := t.customerWithOnePending()
	// Move/upgrade exemplars are picked so each mutation is a real
	// change (the target differs from the current value); attempts are
	// state-isolated by the runner's reset, so exemplar overlap across
	// tasks is harmless.
	cMove := t.customerNotIn("South", "")
	cUp := t.customerNotIn("", "enterprise")
	cDown := t.customerNotIn("", "basic")
	cBoth := t.customerNotIn("North", "plus")
	cCreate1, cCreate2 := t.customer(12), t.customer(33)
	namedCreate := t.uniqueNamed[2]
	pTwoA, pTwoB := t.pending[2], t.pending[3]
	canceled := func(id int) []task.StateCheck {
		return []task.StateCheck{{Resource: "orders", ID: int64(id), Fields: map[string]any{"status": "canceled"}}}
	}
	return []task.Task{
		stateTask("p3-cancel-order",
			fmt.Sprintf("Cancel order id %d.", p1.ID),
			[]string{"cancel_order"}, canceled(p1.ID)),
		stateTask("p3-cancel-only-pending",
			fmt.Sprintf("Customer id %d has one pending order; cancel it.", cOne.ID),
			[]string{"list_orders", "cancel_order"}, canceled(oOne.ID)),
		stateTask("p3-move-region",
			fmt.Sprintf("Move customer id %d to the South sales region.", cMove.ID),
			[]string{"update_customer"},
			[]task.StateCheck{{Resource: "customers", ID: int64(cMove.ID), Fields: map[string]any{"region": "South"}}}),
		stateTask("p3-upgrade-tier",
			fmt.Sprintf("Upgrade customer id %d to the enterprise subscription tier.", cUp.ID),
			[]string{"update_customer"},
			[]task.StateCheck{{Resource: "customers", ID: int64(cUp.ID), Fields: map[string]any{"tier": "enterprise"}}}),
		stateTask("p3-downgrade-tier",
			fmt.Sprintf("Downgrade customer id %d to the basic subscription tier.", cDown.ID),
			[]string{"update_customer"},
			[]task.StateCheck{{Resource: "customers", ID: int64(cDown.ID), Fields: map[string]any{"tier": "basic"}}}),
		stateTask("p3-move-and-upgrade",
			fmt.Sprintf("Move customer id %d to the North region and change their tier to plus.", cBoth.ID),
			[]string{"update_customer"},
			[]task.StateCheck{{Resource: "customers", ID: int64(cBoth.ID), Fields: map[string]any{"region": "North", "tier": "plus"}}}),
		stateTask("p3-create-dollars",
			fmt.Sprintf("Create a new order for customer id %d for $150.00 (15000 cents).", cCreate1.ID),
			[]string{"create_order"},
			[]task.StateCheck{{Resource: "orders", Fields: map[string]any{"customer_id": int64(cCreate1.ID), "amount": int64(15000), "status": "pending"}}}),
		stateTask("p3-create-cents",
			fmt.Sprintf("Create a new order for customer id %d with an amount of 250000 cents.", cCreate2.ID),
			[]string{"create_order"},
			[]task.StateCheck{{Resource: "orders", Fields: map[string]any{"customer_id": int64(cCreate2.ID), "amount": int64(250000), "status": "pending"}}}),
		stateTask("p3-cancel-two",
			fmt.Sprintf("Cancel orders %d and %d.", pTwoA.ID, pTwoB.ID),
			[]string{"cancel_order"},
			append(canceled(pTwoA.ID), canceled(pTwoB.ID)...)),
		stateTask("p3-create-named",
			fmt.Sprintf("Create a new order for the customer named %s with an amount of 9900 cents.", namedCreate.Name),
			[]string{"list_customers", "create_order"},
			[]task.StateCheck{{Resource: "orders", Fields: map[string]any{"customer_id": int64(namedCreate.ID), "amount": int64(9900), "status": "pending"}}}),
	}
}

// p4Tasks are multi-endpoint chains where one response feeds the next
// request.
func p4Tasks(t *truths) []task.Task {
	oA, oB, oC := t.ds.Orders[149], t.ds.Orders[649], t.ds.Orders[949]
	custA := t.customer(oA.CustomerID)
	custB := t.customer(oB.CustomerID)
	namedTotal := t.uniqueNamed[4]
	namedCount := t.uniqueNamed[5]
	namedLargest := t.uniqueNamed[6]
	namedPendingCount := t.uniqueNamed[7]
	latestOfC := t.latestOrder(func(o gen.Order) bool { return o.CustomerID == oC.CustomerID })
	largest := t.largestOrder(func(gen.Order) bool { return true })
	largestCustomer := t.customer(largest.CustomerID)
	return []task.Task{
		entityTask("p4-order-region", "p4",
			fmt.Sprintf("Which sales region is the customer who placed order id %d in?", oA.ID),
			budgetChain, []string{"get_order", "get_customer"},
			[]string{custA.Region}, others(regionVocab, custA.Region)),
		entityTask("p4-order-tier", "p4",
			fmt.Sprintf("Which subscription tier is the customer who placed order id %d on?", oB.ID),
			budgetChain, []string{"get_order", "get_customer"},
			[]string{custB.Tier}, others(tierVocab, custB.Tier)),
		numericTask("p4-named-total", "p4",
			fmt.Sprintf("What is the total amount, in cents, of all completed orders placed by the customer named %s?", namedTotal.Name),
			budgetChain, []string{"list_customers", "list_orders"},
			float64(t.sumOrderAmounts(func(o gen.Order) bool { return o.CustomerID == namedTotal.ID && o.Status == "completed" }))),
		numericTask("p4-named-count", "p4",
			fmt.Sprintf("How many orders has the customer named %s placed?", namedCount.Name),
			budgetChain, []string{"list_customers", "list_orders"},
			float64(t.countOrders(func(o gen.Order) bool { return o.CustomerID == namedCount.ID }))),
		numericTask("p4-named-largest", "p4",
			fmt.Sprintf("What is the largest single order amount, in cents, placed by the customer named %s?", namedLargest.Name),
			budgetChain, []string{"list_customers", "list_orders"},
			float64(t.largestOrder(func(o gen.Order) bool { return o.CustomerID == namedLargest.ID }).Amount)),
		entityTask("p4-order-latest-status", "p4",
			fmt.Sprintf("What is the status of the most recently placed order of the customer who placed order id %d?", oC.ID),
			budgetChain, []string{"get_order", "list_orders"},
			[]string{latestOfC.Status}, others(statusVocab, latestOfC.Status)),
		entityTask("p4-largest-region", "p4",
			"Which sales region is the customer who placed the single largest order (by amount) in?",
			budgetChain, []string{"list_orders", "get_customer"},
			[]string{largestCustomer.Region}, others(regionVocab, largestCustomer.Region)),
		numericTask("p4-named-pending-count", "p4",
			fmt.Sprintf("How many pending orders does the customer named %s have?", namedPendingCount.Name),
			budgetChain, []string{"list_customers", "list_orders"},
			float64(t.countOrders(func(o gen.Order) bool { return o.CustomerID == namedPendingCount.ID && o.Status == "pending" }))),
	}
}

// p5Tasks are irrelevance tasks: no registered endpoint can do what is
// asked, and several are deliberate near-misses of gold capabilities
// (refund vs cancel, delete vs update). Every entity referenced exists
// (real customer and order ids), so the only missing thing is the
// capability itself, never the record.
func p5Tasks(t *truths) []task.Task {
	// A completed order: the most tempting wrong move is cancel_order,
	// which correctly refuses non-pending orders, so an agent that
	// "acts anyway" is detectable both ways.
	var completed gen.Order
	for _, o := range t.ds.Orders {
		if o.Status == "completed" {
			completed = o
			break
		}
	}
	return []task.Task{
		refusalTask("p5-send-email", "Email customer id 12 a copy of their most recent receipt."),
		refusalTask("p5-refund", fmt.Sprintf("Issue a refund for order id %d.", completed.ID)),
		refusalTask("p5-delete-customer", "Delete the customer record with id 30."),
		refusalTask("p5-slack", "Post today's total order count to the #ops channel."),
		refusalTask("p5-export-pdf", "Generate a PDF account statement for customer id 8."),
		refusalTask("p5-merge", "Merge customer records 14 and 15 into a single account."),
		refusalTask("p5-subscribe", "Subscribe customer id 22 to the monthly product newsletter."),
		refusalTask("p5-password-reset", "Reset the password on customer id 5's account."),
	}
}

package gen

import (
	"strconv"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/llm"
	"github.com/txn2/mcp-data-platform/bench/internal/protocol"
	"github.com/txn2/mcp-data-platform/bench/internal/task"
)

// S5 lifecycle protocols (issue #944, grown for statistical rigor in #965). Each
// teaches a NOVEL definition — one that is deliberately NOT in the seeded
// knowledge pages or catalog metadata, so the only way to answer its recall
// question is the taught memory. That keeps the measurement clean: in the
// transfer protocols a learner who never saw the teach can answer only because
// the promote stage pushed the fact into shared knowledge, not because the seed
// already contained it. The set splits into twenty promote+transfer protocols
// and ten supersede protocols (promote and update are mutually exclusive — the
// platform never supersedes an applied insight). Doubling the set (issue #965)
// roughly doubles every metric's denominator — the supersede metrics most of
// all — so the lifecycle rates carry tighter bootstrap confidence intervals.
// Recall/update answers are computed from the dataset (never hand-typed), like
// the S1-S3 truths.

// protocolBudget caps tool calls per lifecycle episode. Successful episodes use
// well under ten calls; the cap exists to stop a thrashing episode (one that
// cannot find the memory and loops on search) from running to a large,
// expensive budget. A tight cap bounds worst-case cost, and hitting it is itself
// a measurable outcome (budget exhaustion in the transcript).
const protocolBudget = 15

// numeric grades an exact-cent USD sum (tolerance one cent).
func numericGrade(v float64) task.Grading {
	return task.Grading{Kind: task.GradeNumeric, Value: &v, AbsTolerance: 0.01}
}

// countGrade grades an integer count (half-unit tolerance absorbs formatting).
func countGrade(n int) task.Grading {
	v := float64(n)
	return task.Grading{Kind: task.GradeNumeric, Value: &v, AbsTolerance: 0.5}
}

// entityGrade grades a single categorical answer, rejecting the known wrong ones.
func entityGrade(alias string, wrong ...string) task.Grading {
	return task.Grading{Kind: task.GradeEntity, Aliases: []string{alias}, WrongAliases: wrong}
}

// teachPrompt frames a fact conversationally and asks the agent to save it.
func teachPrompt(fact string) string {
	return "For the analysis ahead I need you to remember a specific working definition and apply it in later sessions. " +
		fact + " Please save this definition to memory now and link it to the dataset it concerns."
}

// correctionPrompt frames a superseding correction.
func correctionPrompt(fact string) string {
	return "Correction to the definition I gave you earlier. " + fact +
		" Please update your saved knowledge so future answers use this corrected definition."
}

// pagePayload builds a knowledge page for the knowledge_page sink from the
// fact. The slug is unique to the protocol and distinct from the seeded pages
// (so no shared a2 summary applies), and the fact — a self-contained
// one-sentence definition — doubles as the summary: search renders a page hit
// as title plus summary, and the a3 tool surface has no page-body fetch, so
// the summary is the only channel the promoted fact reaches a learner through.
func pagePayload(slug, title, fact string) *protocol.PagePayload {
	return &protocol.PagePayload{Slug: slug, Title: title, Summary: fact, Body: "# " + title + "\n\n" + fact}
}

// otherRegions returns every region except one, for entity wrong-answer sets.
func otherRegions(except string) []string {
	var out []string
	for _, r := range regions {
		if r != except {
			out = append(out, r)
		}
	}
	return out
}

// otherTiers returns every tier except one.
func otherTiers(except string) []string {
	var out []string
	for _, t := range tiers {
		if t != except {
			out = append(out, t)
		}
	}
	return out
}

// Protocols builds the committed S5 protocol set from the dataset.
func (d *Dataset) Protocols() []protocol.Protocol {
	orders := benchURN("orders")
	customers := benchURN("customers")
	grossLeader := d.topRegionGrossAll()
	netLeader := d.TopRegionNet2025()

	ps := make([]protocol.Protocol, 0, 30)
	ps = append(ps, d.updateProtocols(orders, customers, grossLeader, netLeader)...)
	ps = append(ps, d.moreUpdateProtocols(orders, customers)...)
	ps = append(ps, d.singleShotProtocols(orders, customers)...)
	ps = append(ps, d.moreSingleShotProtocols(orders, customers)...)
	return ps
}

// updateProtocols are the five protocols that exercise the supersede stage: each
// teaches a definition, then corrects it to a different computable value. They do
// NOT promote (no transfer stage): the platform never supersedes an already-applied
// insight, so measuring recall-first supersede requires the taught insight to stay
// pending. The transfer mechanic is measured separately by the single-shot set.
func (d *Dataset) updateProtocols(orders, customers, grossLeader, netLeader string) []protocol.Protocol {
	westNet := d.NetRegion2025USD("West")
	eastNet := d.NetRegion2025USD("East")
	q1 := d.CompletedOrdersInQuarter(2025, 1)
	q3 := d.CompletedOrdersInQuarter(2025, 3)
	june := d.OrdersInMonth(2025, time.June)
	feb := d.OrdersInMonth(2025, time.February)
	northOrders := d.OrdersByRegion()["North"]
	eastOrders := d.OrdersByRegion()["East"]

	return []protocol.Protocol{
		{
			ID: "lc-primary-region", Title: "Primary region (gross then net)",
			Fact:      "The 'primary region' is the region with the highest gross revenue (sum of amount over completed orders) in memory.bench.orders.",
			EntityURN: orders, Sink: protocol.SinkDataHub, BudgetToolCalls: protocolBudget,
			Teach:  protocol.TeachStage{Prompt: teachPrompt("The 'primary region' is the region with the highest gross revenue (sum of the amount column over completed orders) in memory.bench.orders.")},
			Recall: protocol.RecallStage{Prompt: "Which region is the primary region? Answer with the region name.", Grading: entityGrade(grossLeader, netLeader)},
			Update: &protocol.UpdateStage{
				Prompt: correctionPrompt("The 'primary region' is actually the region with the highest NET revenue (amount minus discount over completed orders), not gross."),
				Fact:   "The 'primary region' is the region with the highest net revenue (amount minus discount over completed orders).",
				Recall: protocol.RecallStage{Prompt: "Given the corrected definition, which region is the primary region now? Answer with the region name.", Grading: entityGrade(netLeader, grossLeader)},
			},
			Abstain: &protocol.AbstainStage{Prompt: "What is the average customer satisfaction score for the primary region?"},
		},
		{
			ID: "lc-focus-region-net", Title: "Focus region net revenue (West then East)",
			Fact:      "The 'focus region' for this study is West.",
			EntityURN: orders, Sink: protocol.SinkKnowledgePage, BudgetToolCalls: protocolBudget,
			Page:   pagePayload("focus-region-definition", "Focus Region Definition", "The 'focus region' for this study is West."),
			Teach:  protocol.TeachStage{Prompt: teachPrompt("The 'focus region' for this study is West.")},
			Recall: protocol.RecallStage{Prompt: netQuestion("What was the total net revenue for the focus region in calendar year 2025"), Grading: numericGrade(westNet)},
			Update: &protocol.UpdateStage{
				Prompt:          correctionPrompt("The 'focus region' is now East, not West."),
				Fact:            "The 'focus region' for this study is East.",
				Recall:          protocol.RecallStage{Prompt: netQuestion("Given the corrected focus region, what was its total net revenue in calendar year 2025"), Grading: numericGrade(eastNet)},
				SupersededValue: new(westNet),
			},
			Abstain: &protocol.AbstainStage{Prompt: "What was the total shipping cost for the focus region in 2025, in USD?"},
		},
		{
			ID: "lc-reporting-window", Title: "Reporting window completed orders (Q1 then Q3)",
			Fact:      "The 'reporting window' for this study is calendar quarter 1 of 2025 (January through March).",
			EntityURN: orders, Sink: protocol.SinkDataHub, BudgetToolCalls: protocolBudget,
			Teach:  protocol.TeachStage{Prompt: teachPrompt("The 'reporting window' for this study is calendar quarter 1 of 2025 (January through March).")},
			Recall: protocol.RecallStage{Prompt: "How many completed orders fall in the reporting window? Answer with the count.", Grading: countGrade(q1)},
			Update: &protocol.UpdateStage{
				Prompt:          correctionPrompt("The 'reporting window' is now calendar quarter 3 of 2025 (July through September)."),
				Fact:            "The 'reporting window' for this study is calendar quarter 3 of 2025 (July through September).",
				Recall:          protocol.RecallStage{Prompt: "Given the corrected reporting window, how many completed orders fall in it? Answer with the count.", Grading: countGrade(q3)},
				SupersededValue: new(float64(q1)),
			},
			Abstain: &protocol.AbstainStage{Prompt: "How many support tickets were opened during the reporting window?"},
		},
		{
			ID: "lc-focus-month", Title: "Focus month order count (June then February)",
			Fact:      "The 'focus month' for this study is June 2025.",
			EntityURN: orders, Sink: protocol.SinkDataHub, BudgetToolCalls: protocolBudget,
			Teach:  protocol.TeachStage{Prompt: teachPrompt("The 'focus month' for this study is June 2025.")},
			Recall: protocol.RecallStage{Prompt: "How many orders were placed in the focus month? Answer with the count.", Grading: countGrade(june)},
			Update: &protocol.UpdateStage{
				Prompt:          correctionPrompt("The 'focus month' is now February 2025, not June."),
				Fact:            "The 'focus month' for this study is February 2025.",
				Recall:          protocol.RecallStage{Prompt: "Given the corrected focus month, how many orders were placed in it? Answer with the count.", Grading: countGrade(feb)},
				SupersededValue: new(float64(june)),
			},
			Abstain: &protocol.AbstainStage{Prompt: "What was the average delivery time in days for orders in the focus month?"},
		},
		{
			ID: "lc-core-market", Title: "Core market order count (North then East)",
			Fact:      "The 'core market' is the North region.",
			EntityURN: customers, Sink: protocol.SinkKnowledgePage, BudgetToolCalls: protocolBudget,
			Page:   pagePayload("core-market-definition", "Core Market Definition", "The 'core market' is the North region."),
			Teach:  protocol.TeachStage{Prompt: teachPrompt("The 'core market' is the North region.")},
			Recall: protocol.RecallStage{Prompt: "How many orders were placed by customers in the core market? Answer with the count.", Grading: countGrade(northOrders)},
			Update: &protocol.UpdateStage{
				Prompt:          correctionPrompt("The 'core market' is now the East region, not North."),
				Fact:            "The 'core market' is the East region.",
				Recall:          protocol.RecallStage{Prompt: "Given the corrected core market, how many orders were placed by its customers? Answer with the count.", Grading: countGrade(eastOrders)},
				SupersededValue: new(float64(northOrders)),
			},
			Abstain: &protocol.AbstainStage{Prompt: "What is the customer churn rate for the core market?"},
		},
	}
}

// moreUpdateProtocols are five additional supersede protocols (issue #965) that
// double the supersede denominator, the noisiest S5 measurement. Each teaches a
// novel, dataset-computable definition and corrects it to a different computable
// value, exactly like updateProtocols; none promotes.
func (d *Dataset) moreUpdateProtocols(orders, customers string) []protocol.Protocol {
	q2 := d.CompletedOrdersInQuarter(2025, 2)
	q4 := d.CompletedOrdersInQuarter(2025, 4)
	northNet := d.NetRegion2025USD("North")
	southNet := d.NetRegion2025USD("South")
	march := d.OrdersInMonth(2025, time.March)
	sept := d.OrdersInMonth(2025, time.September)
	plusOrders := d.OrdersByTier()["plus"]
	entOrders := d.OrdersByTier()["enterprise"]
	cohort2023 := d.CustomersCreatedInYear(2023)
	cohort2024 := d.CustomersCreatedInYear(2024)

	return []protocol.Protocol{
		{
			ID: "lc-billing-window", Title: "Billing window completed orders (Q2 then Q4)",
			Fact:      "The 'billing window' for this study is calendar quarter 2 of 2025 (April through June).",
			EntityURN: orders, Sink: protocol.SinkDataHub, BudgetToolCalls: protocolBudget,
			Teach:  protocol.TeachStage{Prompt: teachPrompt("The 'billing window' for this study is calendar quarter 2 of 2025 (April through June).")},
			Recall: protocol.RecallStage{Prompt: "How many completed orders fall in the billing window? Answer with the count.", Grading: countGrade(q2)},
			Update: &protocol.UpdateStage{
				Prompt:          correctionPrompt("The 'billing window' is now calendar quarter 4 of 2025 (October through December)."),
				Fact:            "The 'billing window' for this study is calendar quarter 4 of 2025 (October through December).",
				Recall:          protocol.RecallStage{Prompt: "Given the corrected billing window, how many completed orders fall in it? Answer with the count.", Grading: countGrade(q4)},
				SupersededValue: new(float64(q2)),
			},
			Abstain: &protocol.AbstainStage{Prompt: "How many invoices were disputed during the billing window?"},
		},
		{
			ID: "lc-anchor-region", Title: "Anchor region net revenue (North then South)",
			Fact:      "The 'anchor region' for this study is North.",
			EntityURN: orders, Sink: protocol.SinkKnowledgePage, BudgetToolCalls: protocolBudget,
			Page:   pagePayload("anchor-region-definition", "Anchor Region Definition", "The 'anchor region' for this study is North."),
			Teach:  protocol.TeachStage{Prompt: teachPrompt("The 'anchor region' for this study is North.")},
			Recall: protocol.RecallStage{Prompt: netQuestion("What was the total net revenue for the anchor region in calendar year 2025"), Grading: numericGrade(northNet)},
			Update: &protocol.UpdateStage{
				Prompt:          correctionPrompt("The 'anchor region' is now South, not North."),
				Fact:            "The 'anchor region' for this study is South.",
				Recall:          protocol.RecallStage{Prompt: netQuestion("Given the corrected anchor region, what was its total net revenue in calendar year 2025"), Grading: numericGrade(southNet)},
				SupersededValue: new(northNet),
			},
			Abstain: &protocol.AbstainStage{Prompt: "What was the total marketing budget for the anchor region in 2025, in USD?"},
		},
		{
			ID: "lc-benchmark-month", Title: "Benchmark month order count (March then September)",
			Fact:      "The 'benchmark month' for this study is March 2025.",
			EntityURN: orders, Sink: protocol.SinkDataHub, BudgetToolCalls: protocolBudget,
			Teach:  protocol.TeachStage{Prompt: teachPrompt("The 'benchmark month' for this study is March 2025.")},
			Recall: protocol.RecallStage{Prompt: "How many orders were placed in the benchmark month? Answer with the count.", Grading: countGrade(march)},
			Update: &protocol.UpdateStage{
				Prompt:          correctionPrompt("The 'benchmark month' is now September 2025, not March."),
				Fact:            "The 'benchmark month' for this study is September 2025.",
				Recall:          protocol.RecallStage{Prompt: "Given the corrected benchmark month, how many orders were placed in it? Answer with the count.", Grading: countGrade(sept)},
				SupersededValue: new(float64(march)),
			},
			Abstain: &protocol.AbstainStage{Prompt: "What was the average discount rate in the benchmark month?"},
		},
		{
			ID: "lc-headline-tier", Title: "Headline tier order count (plus then enterprise)",
			Fact:      "The 'headline tier' for this study is the plus tier.",
			EntityURN: customers, Sink: protocol.SinkKnowledgePage, BudgetToolCalls: protocolBudget,
			Page:   pagePayload("headline-tier-definition", "Headline Tier Definition", "The 'headline tier' for this study is the plus tier."),
			Teach:  protocol.TeachStage{Prompt: teachPrompt("The 'headline tier' for this study is the plus tier.")},
			Recall: protocol.RecallStage{Prompt: "How many orders were placed by customers on the headline tier? Answer with the count.", Grading: countGrade(plusOrders)},
			Update: &protocol.UpdateStage{
				Prompt:          correctionPrompt("The 'headline tier' is now the enterprise tier, not plus."),
				Fact:            "The 'headline tier' for this study is the enterprise tier.",
				Recall:          protocol.RecallStage{Prompt: "Given the corrected headline tier, how many orders were placed by its customers? Answer with the count.", Grading: countGrade(entOrders)},
				SupersededValue: new(float64(plusOrders)),
			},
			Abstain: &protocol.AbstainStage{Prompt: "What is the average satisfaction score for the headline tier?"},
		},
		{
			ID: "lc-reference-cohort", Title: "Reference cohort customer count (2023 then 2024)",
			Fact:      "The 'reference cohort' is the set of customers whose account was created during calendar year 2023.",
			EntityURN: customers, Sink: protocol.SinkDataHub, BudgetToolCalls: protocolBudget,
			Teach:  protocol.TeachStage{Prompt: teachPrompt("The 'reference cohort' is the set of customers whose account was created during calendar year 2023.")},
			Recall: protocol.RecallStage{Prompt: "How many customers are in the reference cohort? Answer with the count.", Grading: countGrade(cohort2023)},
			Update: &protocol.UpdateStage{
				Prompt:          correctionPrompt("The 'reference cohort' is now the set of customers created during calendar year 2024, not 2023."),
				Fact:            "The 'reference cohort' is the set of customers whose account was created during calendar year 2024.",
				Recall:          protocol.RecallStage{Prompt: "Given the corrected reference cohort, how many customers are in it? Answer with the count.", Grading: countGrade(cohort2024)},
				SupersededValue: new(float64(cohort2023)),
			},
			Abstain: &protocol.AbstainStage{Prompt: "What is the annual retention rate of the reference cohort?"},
		},
	}
}

// singleShotProtocols are the ten teach/recall/promote/transfer/abstain
// protocols (no supersede stage), covering both sinks and both anchor tables.
func (d *Dataset) singleShotProtocols(orders, customers string) []protocol.Protocol {
	priority := d.OrdersAboveThreshold()
	topLine := d.CompletedGrossUSD()
	active := d.DistinctCustomersWithOrders()
	premium := d.OrdersByTier()["enterprise"]
	leadRegion := d.TopRegionByOrderCount()
	houseTier := d.TopTierByOrderCount()
	cohort := d.CustomersCreatedInYear(2024)
	settlement := d.Q4GrossUSD()
	peakRegion := d.TopRegionByCustomerCount()
	december := d.OrdersInMonth(2025, time.December)

	return []protocol.Protocol{
		singleShot("lc-priority-orders", "Priority order count",
			"A 'priority order' is any order whose amount is at least $1,000.00 (100000 cents).",
			orders, protocol.SinkKnowledgePage, pagePayload("priority-order-definition", "Priority Order Definition", "A 'priority order' is any order whose amount is at least $1,000.00 (100000 cents)."),
			"How many priority orders are there? Answer with the count.", countGrade(priority),
			"How many warehouse inventory units back the priority orders?"),
		singleShot("lc-top-line", "Top-line 2025",
			"In our reports 'top-line' means gross revenue: the sum of the amount column over completed orders, with no discount subtracted.",
			orders, protocol.SinkDataHub, nil,
			netlessQuestion("What was the top-line for completed orders in calendar year 2025"), numericGrade(topLine),
			"What was the marketing spend that produced the 2025 top-line?"),
		singleShot("lc-active-customers", "Active customers",
			"An 'active customer' is any customer who has placed at least one order.",
			customers, protocol.SinkKnowledgePage, pagePayload("active-customer-definition", "Active Customer Definition", "An 'active customer' is any customer who has placed at least one order."),
			"How many active customers are there? Answer with the count.", countGrade(active),
			"What is the average lifetime value of an active customer, in USD?"),
		singleShot("lc-premium-orders", "Premium order count",
			"A 'premium order' is any order placed by a customer on the enterprise tier.",
			orders, protocol.SinkKnowledgePage, pagePayload("premium-order-definition", "Premium Order Definition", "A 'premium order' is any order placed by a customer on the enterprise tier."),
			"How many premium orders are there? Answer with the count.", countGrade(premium),
			"How many premium orders were returned because of a product defect?"),
		singleShot("lc-leading-region", "Leading region by volume",
			"The 'leading region' is the region whose customers placed the most orders.",
			orders, protocol.SinkDataHub, nil,
			"Which region is the leading region? Answer with the region name.", entityGrade(leadRegion, otherRegions(leadRegion)...),
			"What is the employee headcount of the leading region?"),
		singleShot("lc-house-tier", "House tier",
			"The 'house tier' is the customer tier that placed the most orders.",
			customers, protocol.SinkKnowledgePage, pagePayload("house-tier-definition", "House Tier Definition", "The 'house tier' is the customer tier that placed the most orders."),
			"Which tier is the house tier? Answer with the tier name.", entityGrade(houseTier, otherTiers(houseTier)...),
			"What is the net promoter score of the house tier?"),
		singleShot("lc-target-cohort", "Target cohort 2024",
			"The 'target cohort' is the set of customers whose account was created during calendar year 2024.",
			customers, protocol.SinkDataHub, nil,
			"How many customers are in the target cohort? Answer with the count.", countGrade(cohort),
			"What is the subscription renewal rate for the target cohort?"),
		singleShot("lc-settlement-total", "Settlement total Q4",
			"The 'settlement total' is the gross revenue (sum of amount over completed orders) for calendar quarter 4 of 2025 (October through December).",
			orders, protocol.SinkKnowledgePage, pagePayload("settlement-total-definition", "Settlement Total Definition", "The 'settlement total' is the gross revenue (sum of amount over completed orders) for calendar quarter 4 of 2025 (October through December)."),
			netlessQuestion("What is the settlement total"), numericGrade(settlement),
			"How much tax was collected within the settlement total?"),
		singleShot("lc-peak-region", "Peak region by customers",
			"The 'peak region' is the region with the most customers.",
			customers, protocol.SinkKnowledgePage, pagePayload("peak-region-definition", "Peak Region Definition", "The 'peak region' is the region with the most customers."),
			"Which region is the peak region? Answer with the region name.", entityGrade(peakRegion, otherRegions(peakRegion)...),
			"What is the office square footage of the peak region?"),
		singleShot("lc-reporting-cohort", "Reporting cohort December",
			"The 'reporting cohort' is the set of orders placed in December 2025.",
			orders, protocol.SinkDataHub, nil,
			"How many orders are in the reporting cohort? Answer with the count.", countGrade(december),
			"How many items on average were in each order of the reporting cohort?"),
	}
}

// moreSingleShotProtocols are ten additional teach/recall/promote/transfer/
// abstain protocols (issue #965) that double the single-shot set, tightening the
// capture, personal-recall, and transfer confidence intervals. Each teaches a
// novel, dataset-computable definition distinct from the seeded knowledge and
// from every other protocol's concept, spanning both sinks and both anchors.
func (d *Dataset) moreSingleShotProtocols(orders, customers string) []protocol.Protocol {
	flagshipOrders := d.OrdersByRegion()["South"]
	standardOrders := d.OrderCount() - d.OrdersAboveThreshold()
	holiday := d.NovDecGrossUSD()
	closeout := d.DecemberGrossUSD()
	charter := d.CustomersByTier()["plus"]
	signature := d.CustomersByTier()["enterprise"]
	clearance := d.RegionStatusCount("West", "completed")
	valueTier := d.BottomTierByOrderCount()
	quietRegion := d.BottomRegionByOrderCount()
	spring := d.OrdersInMonth(2025, time.April)

	return []protocol.Protocol{
		singleShot("lc-flagship-region", "Flagship region order count",
			"The 'flagship region' for this study is the South region.",
			orders, protocol.SinkDataHub, nil,
			"How many orders were placed by customers in the flagship region? Answer with the count.", countGrade(flagshipOrders),
			"What is the retail store footprint of the flagship region?"),
		singleShot("lc-standard-order", "Standard order count",
			"A 'standard order' is any order whose amount is below $1,000.00 (100000 cents).",
			orders, protocol.SinkKnowledgePage, pagePayload("standard-order-definition", "Standard Order Definition", "A 'standard order' is any order whose amount is below $1,000.00 (100000 cents)."),
			"How many standard orders are there? Answer with the count.", countGrade(standardOrders),
			"How many standard orders were returned for a refund?"),
		singleShot("lc-holiday-total", "Holiday total",
			"The 'holiday total' is the gross revenue (sum of amount over completed orders) for November and December 2025.",
			orders, protocol.SinkKnowledgePage, pagePayload("holiday-total-definition", "Holiday Total Definition", "The 'holiday total' is the gross revenue (sum of amount over completed orders) for November and December 2025."),
			netlessQuestion("What is the holiday total"), numericGrade(holiday),
			"How many gift cards were sold within the holiday total?"),
		singleShot("lc-closeout-total", "Closeout total",
			"The 'closeout total' is the gross revenue (sum of amount over completed orders) for December 2025.",
			orders, protocol.SinkDataHub, nil,
			netlessQuestion("What is the closeout total"), numericGrade(closeout),
			"What was the total shipping cost within the closeout total?"),
		singleShot("lc-charter-cohort", "Charter cohort",
			"The 'charter cohort' is the set of customers on the plus tier.",
			customers, protocol.SinkKnowledgePage, pagePayload("charter-cohort-definition", "Charter Cohort Definition", "The 'charter cohort' is the set of customers on the plus tier."),
			"How many customers are in the charter cohort? Answer with the count.", countGrade(charter),
			"What is the annual renewal rate of the charter cohort?"),
		singleShot("lc-signature-segment", "Signature segment",
			"The 'signature segment' is the set of customers on the enterprise tier.",
			customers, protocol.SinkDataHub, nil,
			"How many customers are in the signature segment? Answer with the count.", countGrade(signature),
			"What is the churn rate of the signature segment?"),
		singleShot("lc-clearance-set", "Clearance set",
			"The 'clearance set' is the set of completed orders placed by customers in the West region.",
			orders, protocol.SinkKnowledgePage, pagePayload("clearance-set-definition", "Clearance Set Definition", "The 'clearance set' is the set of completed orders placed by customers in the West region."),
			"How many orders are in the clearance set? Answer with the count.", countGrade(clearance),
			"How many orders in the clearance set were expedited?"),
		singleShot("lc-value-tier", "Value tier",
			"The 'value tier' is the customer tier that placed the fewest orders.",
			customers, protocol.SinkKnowledgePage, pagePayload("value-tier-definition", "Value Tier Definition", "The 'value tier' is the customer tier that placed the fewest orders."),
			"Which tier is the value tier? Answer with the tier name.", entityGrade(valueTier, otherTiers(valueTier)...),
			"What is the annual membership fee for the value tier?"),
		singleShot("lc-quiet-region", "Quiet region by volume",
			"The 'quiet region' is the region whose customers placed the fewest orders.",
			orders, protocol.SinkDataHub, nil,
			"Which region is the quiet region? Answer with the region name.", entityGrade(quietRegion, otherRegions(quietRegion)...),
			"What is the employee headcount of the quiet region?"),
		singleShot("lc-spring-window", "Spring window order count",
			"The 'spring window' for this study is April 2025.",
			orders, protocol.SinkDataHub, nil,
			"How many orders fall in the spring window? Answer with the count.", countGrade(spring),
			"What was the average delivery time in days for orders in the spring window?"),
	}
}

// singleShot builds a teach/recall/promote/transfer/abstain protocol (no update).
func singleShot(id, title, fact, urn, sink string, page *protocol.PagePayload, recallPrompt string, grade task.Grading, abstainPrompt string) protocol.Protocol {
	return protocol.Protocol{
		ID: id, Title: title, Fact: fact, EntityURN: urn, Sink: sink, Page: page,
		BudgetToolCalls: protocolBudget,
		Teach:           protocol.TeachStage{Prompt: teachPrompt(fact)},
		Recall:          protocol.RecallStage{Prompt: recallPrompt, Grading: grade},
		Transfer:        &protocol.RecallStage{Prompt: recallPrompt, Grading: grade},
		Abstain:         &protocol.AbstainStage{Prompt: abstainPrompt},
	}
}

// netQuestion appends the net-revenue computation rule so only the recalled
// definition (which region/window) is the unknown, not the arithmetic.
func netQuestion(stem string) string {
	return stem + "? Compute net revenue as amount minus discount over completed orders only, amounts are integer US cents (divide by 100), in USD rounded to the nearest cent."
}

// netlessQuestion appends the gross computation rule for top-line/settlement.
func netlessQuestion(stem string) string {
	return stem + "? Sum the amount column over completed orders only; amounts are integer US cents (divide by 100), in USD rounded to the nearest cent."
}

// ScriptedLifecycleSmoke builds a deterministic per-episode playback for the
// no-API-key lifecycle smoke: it captures via memory_capture, recalls via search
// and answers with each stage's computed ground truth, promotes through the
// harness, and abstains on the never-taught fact. One smoke run validates handle
// threading, the insight/changeset APIs, supersede, grading, and the metrics
// against the live platform with no model.
func ScriptedLifecycleSmoke(protocols []protocol.Protocol) map[string]map[string][]llm.Step {
	out := make(map[string]map[string][]llm.Step, len(protocols))
	for _, p := range protocols {
		stages := map[string][]llm.Step{
			"teach":   {captureStep(p.Fact, p.EntityURN, "business_context"), {FinalText: "saved the definition"}},
			"recall":  {searchStep(), {FinalText: "FINAL ANSWER: " + answerString(p.Recall.Grading)}},
			"abstain": {{FinalText: "FINAL ANSWER: INSUFFICIENT INFORMATION"}},
		}
		if p.Transfer != nil {
			stages["transfer"] = []llm.Step{searchStep(), {FinalText: "FINAL ANSWER: " + answerString(p.Transfer.Grading)}}
		}
		if p.Update != nil {
			stages["update"] = []llm.Step{captureStep(p.Update.Fact, p.EntityURN, "correction"), {FinalText: "updated the definition"}}
			stages["update_recall"] = []llm.Step{searchStep(), {FinalText: "FINAL ANSWER: " + answerString(p.Update.Recall.Grading)}}
		}
		out[p.ID] = stages
	}
	return out
}

// captureStep records a schema_entity memory linked to the entity, matching the
// real memory_capture tool contract (type + content, entity_urns for linkage).
func captureStep(content, urn, category string) llm.Step {
	return llm.Step{ToolCalls: []llm.ToolCall{{Name: "memory_capture", Args: map[string]any{
		"type": "schema_entity", "content": content, "category": category, "entity_urns": []any{urn},
	}}}}
}

// searchStep issues a discovery call, so the smoke exercises the search path and
// the recall-surfaced signal.
func searchStep() llm.Step {
	return llm.Step{ToolCalls: []llm.ToolCall{{Name: "search", Args: map[string]any{"intent": "recall the saved definition"}}}}
}

// answerString renders a grading's ground truth as the smoke's final answer:
// the first alias for entity grading, the value (two decimals) for numeric.
func answerString(g task.Grading) string {
	if g.Kind == task.GradeEntity {
		if len(g.Aliases) > 0 {
			return g.Aliases[0]
		}
		return ""
	}
	if g.Value != nil {
		return strconv.FormatFloat(*g.Value, 'f', 2, 64)
	}
	return ""
}

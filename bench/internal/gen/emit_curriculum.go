package gen

import (
	"github.com/txn2/mcp-data-platform/bench/internal/curriculum"
	"github.com/txn2/mcp-data-platform/bench/internal/llm"
	"github.com/txn2/mcp-data-platform/bench/internal/protocol"
	"github.com/txn2/mcp-data-platform/bench/internal/task"
)

// The cold-start curriculum (issue #963) teaches, one lesson at a time, the S3
// trap facts the A2 seed pre-loads — but starting from the empty baseline
// (DataHubMCEsEmpty, no knowledge pages). Each lesson promotes its fact to the
// same channel A2 delivers it through: the datahub entity description (units,
// freshness, deprecation) or a portal knowledge page (net-revenue policy, fiscal
// calendar, tier definitions). apply_knowledge restores the description text and
// knowledge pages that carry the answer-bearing facts, so the S3 trap suite (all
// of whose disambiguating facts live in that text) reaches its A2 accuracy
// ceiling once all six lessons are promoted. It does not re-create A2's auxiliary
// aspects — globalTags, the structured deprecation aspect, editableSchema column
// docs, customProperties — so the final enrichment layer is not the whole A2
// catalog, only the fact-bearing channels the trap suite reads.
//
// Lesson order is the curve's x-axis. It runs foundational-first so a multi-fact
// trap flips to correct only once every fact it needs has landed: units before
// net-revenue (net figures are also in cents), then the calendar/freshness/tier/
// deprecation facts each independent trap classes depend on.

// coldStartBudget caps tool calls in a cold-start teach episode, matching the S5
// protocol budget (a capture episode uses well under this; the cap bounds a
// thrashing search that never reaches the capture tool).
const coldStartBudget = protocolBudget

// Datahub-sink lesson facts. Each becomes the promoted entity description, so it
// must carry the knowledge the trap suite needs on its own. The freshness and
// deprecation facts reuse the exact A2 description text (dailyDescription,
// legacyDescription), so the promoted description is byte-identical to A2's for
// those entities (the auxiliary A2 aspects are not re-created; see the package
// comment above).
const (
	unitsCentsFact = "In memory.bench.orders the monetary columns amount and discount are stored as " +
		"INTEGERS IN US CENTS, not dollars; divide by 100 to get USD. Any total computed without dividing " +
		"by 100 is off by a factor of 100."
	netRevenueFact = "Company revenue reporting policy: revenue = amount - discount, over COMPLETED orders " +
		"only (refunded and pending orders excluded). Amounts are in US cents. A gross figure that ignores " +
		"discounts or includes non-completed orders is not policy revenue."
	fiscalCalendarFact = "The company fiscal year runs February 1 through January 31: fiscal year 2025 is " +
		"2025-02-01 through 2026-01-31. Fiscal figures must not be computed over the January-December calendar year."
	tierBoundaryFact = "A 'key account' is any customer on the plus OR enterprise tier — a derived segment " +
		"broader than the enterprise tier alone and not stored in any column. Counting only enterprise customers " +
		"undercounts key accounts."
)

// Curriculum builds the committed cold-start curriculum from the dataset. Its
// eval set is the S3 trap suite (loaded from the tasks directory at run time),
// whose ground truth is generated, so nothing is duplicated here.
func (d *Dataset) Curriculum() curriculum.Curriculum {
	orders := benchURN("orders")
	customers := benchURN("customers")
	daily := benchURN("daily_region_revenue")
	legacy := benchURN("legacy_orders")
	return curriculum.Curriculum{
		ID:        "cs-traps",
		Title:     "Cold-start knowledge growth over the S3 trap suite",
		EvalSuite: "s3",
		Lessons: []curriculum.Lesson{
			datahubLesson("cs-units-cents", "Monetary columns are integer cents", "units_cents", unitsCentsFact, orders),
			pageLesson("cs-net-revenue", "Net-revenue reporting policy", "net_revenue", netRevenueFact, orders,
				"revenue-reporting-policy", "Revenue Reporting Policy", revenuePolicySummary, revenuePolicyBody),
			pageLesson("cs-fiscal-calendar", "Fiscal calendar boundaries", "fiscal_calendar", fiscalCalendarFact, orders,
				"fiscal-calendar-policy", "Fiscal Calendar Policy", fiscalCalendarSummary, fiscalCalendarBody),
			datahubLesson("cs-freshness-cutoff", "Daily index freshness cutoff", "freshness_cutoff", dailyDescription, daily),
			pageLesson("cs-tier-boundary", "Key-account tier definition", "tier_boundary", tierBoundaryFact, customers,
				"customer-tier-definitions", "Customer Tier Definitions", tierDefinitionsSummary, tierDefinitionsBody),
			datahubLesson("cs-deprecated-table", "legacy_orders is deprecated", "deprecated_table", legacyDescription, legacy),
		},
	}
}

// datahubLesson builds a lesson that promotes its fact to the entity's catalog
// description (delivered to any identity via cross-enrichment).
func datahubLesson(id, title, trapClass, fact, urn string) curriculum.Lesson {
	return curriculum.Lesson{
		ID: id, Title: title, TrapClass: trapClass, Fact: fact,
		EntityURN: urn, Sink: protocol.SinkDataHub, BudgetToolCalls: coldStartBudget,
		Teach: protocol.TeachStage{Prompt: teachPrompt(fact)},
	}
}

// pageLesson builds a lesson that promotes its fact to a portal knowledge page
// (delivered to any identity via the search tool). The page reuses the A2
// seed's slug/title/summary/body so the promoted page is identical to the
// documented baseline — the summary especially, because search renders a page
// hit as title plus summary and the a3 surface has no page-body fetch tool.
func pageLesson(id, title, trapClass, fact, urn, slug, pageTitle, summary, body string) curriculum.Lesson {
	return curriculum.Lesson{
		ID: id, Title: title, TrapClass: trapClass, Fact: fact,
		EntityURN: urn, Sink: protocol.SinkKnowledgePage, BudgetToolCalls: coldStartBudget,
		Page:  &protocol.PagePayload{Slug: slug, Title: pageTitle, Summary: summary, Body: body},
		Teach: protocol.TeachStage{Prompt: teachPrompt(fact)},
	}
}

// ScriptedColdStartSmoke builds the deterministic per-episode playback for the
// no-API-key cold-start smoke. Each lesson's teach stage captures the fact via
// memory_capture (so the harness verifies capture and drives the real
// promotion), and each eval task answers with its computed ground truth via the
// search-then-answer path. One smoke run validates handle threading, the
// insight/changeset APIs, apply_knowledge promotion, deterministic grading, and
// the learning-curve metrics against the live platform with no model. The eval
// answers are always correct (the smoke measures plumbing, not model behavior),
// so its curve is flat-high; the climbing curve is a property of a real model
// run against the empty baseline.
func ScriptedColdStartSmoke(cur curriculum.Curriculum, evalTasks []task.Task) map[string]map[string][]llm.Step {
	out := make(map[string]map[string][]llm.Step, len(cur.Lessons)+len(evalTasks))
	for _, l := range cur.Lessons {
		out[l.ID] = map[string][]llm.Step{
			"teach": {captureStep(l.Fact, l.EntityURN, "business_context"), {FinalText: "saved the definition"}},
		}
	}
	for _, t := range evalTasks {
		out[t.ID] = map[string][]llm.Step{
			"eval": {searchStep(), {FinalText: "FINAL ANSWER: " + answerString(t.Grading)}},
		}
	}
	return out
}

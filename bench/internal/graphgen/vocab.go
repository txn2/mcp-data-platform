package graphgen

// Filler vocabulary. Two rules bound every pool, both load-bearing for the
// by-construction signature guarantee:
//
//   - no entry contains a digit (quantities in filler prose are spelled out),
//     so a minted number cannot occur in a page the mint did not place it on;
//   - no entry contains a reserved name word or anything shaped like a class
//     code, so those namespaces stay exclusively the mint's.
//
// validateReserved re-checks both over the rendered corpus, which turns any
// future pool edit that breaks the rule into a build failure.

// fillerSubjects and fillerQualifiers compose filler system names
// ("<subject>-<qualifier>"), giving each filler cluster a distinct subject.
// The composed names deliberately stay off the core systems' compound names
// (orders-ledger, settlement-recon, parcel-events) while sharing their
// domain: a big wiki is full of near-neighbors, and how well search holds up
// against them at scale is part of what the live gate measures.
var fillerSubjects = []string{
	"invoice", "shipment", "session", "telemetry", "catalog", "payment",
	"refund", "inventory", "campaign", "device", "signup", "webhook",
	"pricing", "loyalty", "receipt", "carrier", "warehouse", "courier",
	"listing", "basket", "voucher", "dispatch", "returns", "supplier",
	"customs", "manifest", "billing", "usage", "audit", "quota",
	"currency", "geocode", "clickpath", "search", "review", "wishlist",
	"fraud", "chargeback", "tax", "consent", "storefront", "checkout",
	"subscription", "payout", "gateway", "terminal", "kiosk", "beacon",
	"sensor", "vehicle", "roster", "depot", "dock", "container",
	"pallet", "forecast", "promo", "banner", "survey", "ticket",
	"chat", "email", "notification", "segment", "region", "outlet",
}

var fillerQualifiers = []string{
	"intake", "digest", "rollup", "mirror", "snapshot", "replay", "sync",
	"archive", "extract", "index", "summary", "backfill", "cleanse",
	"enrichment", "handoff", "staging", "bridge", "relay", "compaction",
	"reconciliation",
}

// fillerKinds are the page roles a filler cluster draws from. The first kind
// is always present and anchors the cluster; later pages reference earlier
// ones, so a cluster can never introduce a reference cycle.
var fillerKinds = []string{
	"runbook", "schema notes", "tuning notes", "review notes",
	"window notes", "handling guide", "checks", "history",
}

// fillerWindows, fillerTeams and fillerSpelledNumbers fill template slots.
var fillerWindows = []string{
	"overnight", "early morning", "mid-morning", "midday", "late evening",
}

var fillerTeams = []string{
	"the stream operations group", "the warehouse platform team",
	"the data services desk", "the integrations crew",
	"the reporting platform team", "the ingestion duty group",
}

var fillerSpelledNumbers = []string{
	"forty", "ninety", "twenty", "sixty", "thirty", "eighty", "seventy",
}

var fillerVerbs = []string{
	"drains", "lands", "settles", "compacts", "publishes", "refreshes",
	"reconciles", "materializes",
}

// deptFamily is one institutional department whose pages fill the corpus
// alongside the operational clusters. Department filler exists for the
// discontinuity manipulation's realism: a corporate wiki is not all runbooks,
// and the six authored discontinuity pages must compete with pages in their
// own registers, not sit as vocabulary islands any weakly-related query
// jumps to.
type deptFamily struct {
	name    string
	actor   string
	cadence string
	topics  []string
	objects []string
}

var deptFamilies = []deptFamily{
	{
		name: "finance", actor: "the financial control group", cadence: "period",
		topics: []string{
			"journal review", "subledger agreement", "expense accrual",
			"budget submission", "intercompany settlement", "balance attestation",
			"cost allocation", "revenue recognition review", "cash position report",
			"fixed asset count",
		},
		objects: []string{"journal entries", "trial balances", "accrual schedules", "allocation worksheets"},
	},
	{
		name: "workforce", actor: "the workforce office", cadence: "week",
		topics: []string{
			"visitor access", "leave planning", "starter onboarding",
			"desk allocation", "training calendar", "contractor induction",
			"working hours declaration", "wellbeing check-in", "badge issuance",
			"leaver checklist", "overtime approval", "timesheet correction",
			"payroll cutoff", "shift swap requests", "time off in lieu",
		},
		objects: []string{"access requests", "leave submissions", "induction packs", "badge records", "timesheet entries", "worked-hours claims"},
	},
	{
		name: "compliance", actor: "the compliance office", cadence: "quarter",
		topics: []string{
			"policy attestation", "license renewal", "returns preparation",
			"conflict declaration", "training completion", "breach register review",
			"horizon scanning", "sanctions screening", "complaints handling",
			"gifts and hospitality", "regulator correspondence", "filing preparation",
			"lodgment evidence", "statutory declarations", "scope confirmation",
		},
		objects: []string{"attestation forms", "renewal files", "declaration registers", "screening lists", "lodgment files", "filing declarations"},
	},
	{
		name: "procurement", actor: "the procurement office", cadence: "cycle",
		topics: []string{
			"vendor onboarding", "invoice approval", "contract renewal",
			"tender evaluation", "purchase requisition", "supplier review",
			"catalog maintenance", "spend reporting", "goods receipting",
			"vendor offboarding",
		},
		objects: []string{"purchase orders", "requisition forms", "renewal schedules", "supplier files"},
	},
	{
		name: "legal", actor: "the legal office", cadence: "matter",
		topics: []string{
			"engagement letters", "counterparty review", "template maintenance",
			"signature authority", "dispute intake", "notice service",
			"instrument execution", "matter intake", "outside counsel",
			"power of attorney",
		},
		objects: []string{"executed instruments", "engagement files", "authority registers", "matter records"},
	},
	{
		name: "records", actor: "the records management office", cadence: "quarter",
		topics: []string{
			"filing practice", "archive requests", "disposal review",
			"series maintenance", "transfer preparation", "vital records",
			"scanning standards", "register upkeep", "loan tracking",
			"index maintenance", "correspondence filing", "circular retention",
			"delivery evidence", "issued documents register", "retention periods",
		},
		objects: []string{"filing series", "archive boxes", "disposal certificates", "transfer lists", "issued circulars", "correspondence files"},
	},
	{
		name: "facilities", actor: "the facilities office", cadence: "week",
		topics: []string{
			"maintenance visits", "building access", "meeting rooms",
			"post and deliveries", "cleaning schedule", "waste collection",
			"heating and cooling", "parking allocation", "signage requests",
			"plant watering",
		},
		objects: []string{"visit logs", "access lists", "booking sheets", "delivery records"},
	},
}

// deptKinds are the page roles a department cluster draws from.
var deptKinds = []string{"guide", "practice notes", "handbook", "reference", "checklist", "calendar"}

package graphfix

// The three completion cells. Each asks for a complete operational document
// whose constraint set is spread across pages the prompt does not name. The
// prompts name dimensions an asker plausibly holds ("who signs off", "on what
// clocks") and never name a page, a class, a route, or a value; every
// signature below is a hard token the asker could not supply.
//
// The cells differ deliberately in shape: gc-export-onboarding is wide and
// shallow (six off-entry source pages at distance 1-2), gc-incident is deep
// (the clocks sit three references out), gc-billing-change is both (the
// consumer-contract fact sits four references out).

var completionCells = []CompletionCell{
	{
		ID:         "gc-billing-change",
		EntryKey:   "billing-events-runbook",
		EntryIntro: "The operating runbook for the billing-events stream is",
		Prompt: "We want to add a new field to the billing-events stream's records. " +
			"Write the complete change plan the stream's owner could execute as written: " +
			"every step from proposal to the field being live, every approval the change needs, " +
			"every rule that constrains when it can be applied, and everything the stream's " +
			"consumers are owed along the way. Completeness matters more than brevity; " +
			"the plan is the deliverable.",
		GateQueries: []string{
			"billing-events stream add a field change plan",
			"how is a change to a stream announced and approved",
			"stream schema change process and what consumers are owed",
		},
		Constraints: []Constraint{
			{
				ID:       "bc-regulated",
				Desc:     "the stream is regulated-tier and that tier's rules govern the change",
				Pages:    []string{"billing-events-runbook", "regulated-tier-rules"},
				Patterns: []string{`regulated`},
			},
			{
				ID:       "bc-class",
				Desc:     "the change is governed by change class CC-3",
				Pages:    []string{"regulated-tier-rules", "change-class-reference"},
				Patterns: []string{`cc-?3`},
			},
			{
				ID:       "bc-notice",
				Desc:     "consumers are owed 9 business days of advance notice",
				Pages:    []string{"change-class-reference"},
				Patterns: []string{`9[- ]business[- ]day`},
			},
			{
				ID:       "bc-count",
				Desc:     "notice is counted excluding the announcement day itself",
				Pages:    []string{"change-class-reference"},
				Patterns: []string{`announcement day`},
			},
			{
				ID:       "bc-freeze",
				Desc:     "a notice period ending inside a change freeze runs to after the freeze lifts",
				Pages:    []string{"change-class-reference", "ledger-close-calendar"},
				Patterns: []string{`freeze`},
			},
			{
				ID:       "bc-attest",
				Desc:     "the producing service's owner re-attests completeness after the change",
				Pages:    []string{"regulated-tier-rules"},
				Patterns: []string{`attest`},
			},
			{
				ID:       "bc-registry",
				Desc:     "the new version is registered before any producer writes it, against the declared compatibility mode",
				Pages:    []string{"schema-registry-operations"},
				Patterns: []string{`compatibility mode`, `registered before`},
			},
			{
				ID:       "bc-channels",
				Desc:     "the announcement goes to three places at once, the registry entry being authoritative",
				Pages:    []string{"change-announcement-channels"},
				Patterns: []string{`mailing list`, `three places`},
			},
			{
				ID:       "bc-contracts",
				Desc:     "only registered consumer contracts are announced to; registration is a condition of read access",
				Pages:    []string{"consumer-contract-registry"},
				Patterns: []string{`never registered`, `not announced`, `condition of read access`},
			},
		},
	},
	{
		ID:         "gc-incident",
		EntryKey:   "ledger-reconcile-runbook",
		EntryIntro: "The operating runbook for the nightly ledger-reconcile job is",
		Prompt: "The nightly ledger-reconcile job has now failed on three consecutive nights. " +
			"Write the complete incident-handling document for this situation, one the on-shift " +
			"engineer could follow as written: what is opened and at what severity, who has to " +
			"be told, how the escalation proceeds and on what clocks when nobody acknowledges, " +
			"what is communicated while it is open, what has to be true before it can be closed, " +
			"and what record it has to leave behind. Completeness matters more than brevity; " +
			"the document is the deliverable.",
		GateQueries: []string{
			"ledger-reconcile job failed three consecutive nights what happens",
			"incident severity and escalation process",
			"how are incidents handled communicated and closed",
		},
		Constraints: []Constraint{
			{
				ID:       "ic-band",
				Desc:     "three consecutive failures open a severity band B incident",
				Pages:    []string{"ledger-reconcile-runbook", "incident-severity-bands"},
				Patterns: []string{`band b`},
			},
			{
				ID:       "ic-notify",
				Desc:     "band B is notified to the owning team and the platform group's shift lead",
				Pages:    []string{"incident-severity-bands", "escalation-ladders", "paging-channel-directory"},
				Patterns: []string{`shift lead`},
			},
			{
				ID:       "ic-route",
				Desc:     "band B follows the amber escalation route",
				Pages:    []string{"incident-severity-bands", "escalation-ladders", "duty-manager-matrix"},
				Patterns: []string{`amber`},
			},
			{
				ID:       "ic-rungs",
				Desc:     "the route climbs shift lead, then duty manager, then head of platform",
				Pages:    []string{"escalation-ladders", "paging-channel-directory"},
				Patterns: []string{`head of platform`},
			},
			{
				ID:       "ic-clock-first",
				Desc:     "the first rung holds at most 20 minutes",
				Pages:    []string{"duty-manager-matrix"},
				Patterns: []string{`20 minutes`},
			},
			{
				ID:       "ic-clock-second",
				Desc:     "the second rung holds at most 25 minutes",
				Pages:    []string{"duty-manager-matrix"},
				Patterns: []string{`25 minutes`},
			},
			{
				ID:       "ic-auto",
				Desc:     "an expired unacknowledged clock climbs on its own",
				Pages:    []string{"duty-manager-matrix", "paging-channel-directory"},
				Patterns: []string{`unacknowledged`},
			},
			{
				ID:       "ic-comm",
				Desc:     "updates go out on the cadence the opening notice promised",
				Pages:    []string{"incident-communication-guide"},
				Patterns: []string{`opening notice`},
			},
			{
				ID:       "ic-close",
				Desc:     "the incident closes on a clean scheduled run, never on a manual one or on silence",
				Pages:    []string{"ledger-reconcile-runbook", "incident-open-and-close", "reconcile-manual-run"},
				Patterns: []string{`scheduled run`},
			},
			{
				ID:       "ic-record",
				Desc:     "the closing record carries a timeline, contributing conditions, owned corrections and a review",
				Pages:    []string{"incident-postmortem-template"},
				Patterns: []string{`contributing conditions`},
			},
		},
	},
	{
		ID:         "gc-export-onboarding",
		EntryKey:   "clickstream-export-runbook",
		EntryIntro: "The operating runbook for the clickstream-raw export, the model for the new export, is",
		Prompt: "We are standing up a new nightly export, order-refunds, to be operated exactly " +
			"like the clickstream-raw export. Write the complete onboarding document for it: how " +
			"the job and its output are named, where the output lands and how that area is " +
			"organized, how its storage is registered and how long its output lives, when it is " +
			"expected to land and what happens when it is repeatedly late, how it is re-run " +
			"safely after a bad night, and what applies if any of its output is ever copied " +
			"outside the platform. Completeness matters more than brevity; the document is the " +
			"deliverable.",
		GateQueries: []string{
			"new nightly export onboarding naming storage delivery",
			"how are nightly exports operated and registered",
			"export output lifetime and delivery expectations",
		},
		Constraints: []Constraint{
			{
				ID:   "ec-collectors",
				Desc: "the export runs after the collectors drain; a missing hour is a collector, not the export",
				Pages: []string{
					"clickstream-export-runbook", "collector-drain-runbook",
					"clickstream-schema-notes", "batch-window-calendar", "export-rerun-procedure",
				},
				Patterns: []string{`collector`},
			},
			{
				ID:       "ec-class",
				Desc:     "the export is registered under storage class SC-4",
				Pages:    []string{"clickstream-export-runbook", "storage-class-register"},
				Patterns: []string{`sc-?4`},
			},
			{
				ID:       "ec-life",
				Desc:     "SC-4 output is purged after 62 days",
				Pages:    []string{"storage-class-register"},
				Patterns: []string{`(^|\D)62(\D|$)`},
			},
			{
				ID:       "ec-reclass",
				Desc:     "keeping output longer is a reclassification request, reviewed monthly",
				Pages:    []string{"storage-class-register"},
				Patterns: []string{`reviewed monthly`},
			},
			{
				ID:       "ec-name",
				Desc:     "the job is named subject-grain and renames run through an alias period",
				Pages:    []string{"export-naming-convention"},
				Patterns: []string{`grain`, `alias`},
			},
			{
				ID:       "ec-zone",
				Desc:     "output lands under the export's own prefix and nothing writes outside it",
				Pages:    []string{"raw-zone-layout"},
				Patterns: []string{`prefix`},
			},
			{
				ID:       "ec-due",
				Desc:     "each export has an expected landing hour; three late mornings in a row are raised",
				Pages:    []string{"export-delivery-expectations"},
				Patterns: []string{`three mornings`, `landing hour`},
			},
			{
				ID:       "ec-rerun",
				Desc:     "a re-run replaces partitions and never changes the original write date",
				Pages:    []string{"export-rerun-procedure", "partition-compaction-notes"},
				Patterns: []string{`original write date`},
			},
			{
				ID:       "ec-egress",
				Desc:     "a copy outside the platform needs a named recipient, a purpose and an end date",
				Pages:    []string{"egress-approval-policy"},
				Patterns: []string{`end date`, `named recipient`},
			},
		},
	},
}

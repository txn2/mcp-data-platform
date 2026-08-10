package graphgen

import "github.com/txn2/mcp-data-platform/bench/internal/graphfix"

// incidentCell is the deep cell: the settlement-recon job's third consecutive
// failure. The acknowledgment clocks sit at reference distance three from the
// entry. Discontinuities: the worked-hours ledger (workforce/payroll) and the
// regulatory filing calendar (compliance).
func incidentCell(m *minter) ([]graphfix.Page, graphfix.CompletionCell) {
	in := m.class("IN", 28, "settlement-recon-job-runbook")
	sb := m.class("SB", 8, "incident-severity-standard")
	route := m.name("incident-severity-standard", "escalation-roster")
	first := m.quantity(23, "minutes", "acknowledgment-clocks")
	second := m.quantity(34, "minutes", "acknowledgment-clocks")
	ac := m.class("AC", 15, "acknowledgment-clocks")
	uc := m.class("UC", 11, "incident-comms-standard")
	ce := m.class("CE", 2, "closure-evidence-standard")
	pm := m.class("PM", 6, "postmortem-record-standard")
	tl := m.class("TL", 19, "worked-hours-ledger-practice")
	rn := m.class("RN", 26, "regulator-notify-calendar")

	pages := []graphfix.Page{
		{
			Key:     "settlement-recon-job-runbook",
			Slug:    "settlement-recon-job-runbook",
			Title:   "Settlement Recon Job Runbook",
			Summary: "Operating notes for the nightly settlement-recon job: what it agrees, what a failed night means, and when failures become formal.",
			Tags:    []string{"runbook", "settlement", "batch"},
			Body: `# Settlement Recon Job Runbook

The nightly settlement-recon job agrees the day's settlement records against
the processor statements. A clean pass is silent; a failed pass leaves the
day unagreed and is re-attempted the following night after the upstream
statements refresh.

## When failures become formal

One failed night is watched. Two failed nights are worked during the day. A
third consecutive failed night is no longer an operational matter: it opens
an ` + in + ` incident record, because the unagreed days now span a statement
cycle. The incident's severity and everything that follows from it are set by
{{page:incident-severity-standard|the incident severity standard}}, not by
the job's operators.

## While an incident is open

The job keeps running on its schedule. What an open incident communicates,
and on what rhythm, follows
{{page:incident-comms-standard|the incident communications standard}}; what
must be true before the record can be closed, and the proof it takes, follows
{{page:closure-evidence-standard|the closure evidence standard}}. The
runbook's own scope ends at opening the record honestly.`,
		},
		{
			Key:     "incident-severity-standard",
			Slug:    "incident-severity-standard",
			Title:   "Incident Severity Standard",
			Summary: "How incident severity is assigned, what each band notifies, and which escalation route each band follows.",
			Tags:    []string{"policy", "incidents"},
			Body: `# Incident Severity Standard

Severity is assigned when the record is opened and belongs to the standard,
not to the team that opened it.

## Band ` + sb + `

A reconciliation or settlement control failing across a statement cycle is
band ` + sb + `. The band notifies the owning team and the platform group's
shift lead at opening, and it escalates along the ` + route + ` route defined
in {{page:escalation-roster|the escalation roster}}: severity decides the
route, the roster decides the names.

## Regulatory windows

Bands touching settlement controls carry one more obligation that is not the
engineering organization's to schedule: when the failure falls inside a
statutory filing window, notification follows
{{page:regulator-notify-calendar|the regulatory filing calendar}} maintained
by the compliance office.

## Other bands

Single-night failures and non-control degradations carry the lighter bands,
which notify the owning team only and do not escalate unattended.`,
		},
		{
			Key:     "escalation-roster",
			Slug:    "escalation-roster",
			Title:   "Escalation Roster",
			Summary: "The escalation routes: their rungs in order, and where the acknowledgment clocks for each rung are kept.",
			Tags:    []string{"reference", "incidents"},
			Body: `# Escalation Roster

A route is an ordered ladder of roles; an escalation climbs it one rung at a
time and never skips a rung that has acknowledged.

## The ` + route + ` route

The ` + route + ` route climbs from the platform shift lead to the duty
manager to the head of platform operations. Each rung holds for the
acknowledgment clock recorded in
{{page:acknowledgment-clocks|the acknowledgment clock table}}; the clocks
belong to that table, not to this roster, so a clock change never edits a
route.

## Changes

Routes change by agreement between the owning teams and the platform group,
and the roster is edited only after the change is agreed in writing; an
escalation in flight finishes on the roster it started on.`,
		},
		{
			Key:     "acknowledgment-clocks",
			Slug:    "acknowledgment-clocks",
			Title:   "Acknowledgment Clock Table",
			Summary: "The per-rung acknowledgment clocks for escalation routes, and the rule for a clock that runs out.",
			Tags:    []string{"reference", "incidents"},
			Body: `# Acknowledgment Clock Table

Each escalation rung holds for a fixed clock before the call moves on.

For the settlement family of routes the first rung holds ` + first + ` and
the second rung holds ` + second + `; the final rung holds until answered,
because there is nowhere further to climb.

## Expiry

Rule ` + ac + ` governs expiry: a clock that runs out without an
acknowledgment advances the call to the next rung on its own, and the missed
rung is recorded as missed rather than re-tried. Acknowledging stops the
climb at that rung; declining passes it upward immediately without waiting
out the clock.`,
		},
		{
			Key:     "incident-comms-standard",
			Slug:    "incident-comms-standard",
			Title:   "Incident Communications Standard",
			Summary: "What an open incident communicates, on what cadence, and what the opening notice must promise.",
			Tags:    []string{"policy", "communications"},
			Body: `# Incident Communications Standard

An open incident communicates on the cadence its opening notice declared,
and the cadence is a promise: missing an update is itself reportable.

Cadence ` + uc + ` applies to control-failure incidents. The opening notice
names the affected control, the days in question, and when the next update
will arrive; every update thereafter either reports progress or re-promises
the next time honestly. Updates go to the same audience as the opening
notice — narrowing the audience mid-incident is not permitted, however quiet
the update.

The closing notice is part of the standard too: it goes to the full audience
and names the evidence the closure rests on.`,
		},
		{
			Key:     "closure-evidence-standard",
			Slug:    "closure-evidence-standard",
			Title:   "Closure Evidence Standard",
			Summary: "What must be true before a control-failure incident may be closed, and the evidence record that proves it.",
			Tags:    []string{"policy", "incidents"},
			Body: `# Closure Evidence Standard

A control-failure incident closes on evidence, not on silence.

Closure requires a ` + ce + ` evidence record: the control passing on its own
schedule, unassisted, with the previously unagreed days brought current. A
manually forced pass does not qualify — the record must come from the
scheduled run itself — and a quiet night with the control disabled qualifies
even less. The ` + ce + ` record is attached to the incident before the
closing notice goes out.

Once closed, the incident owes one more artifact: the review record described
in {{page:postmortem-record-standard|the review record standard}}, due on the
standard's own clock rather than at closure time.`,
		},
		{
			Key:     "postmortem-record-standard",
			Slug:    "postmortem-record-standard",
			Title:   "Review Record Standard",
			Summary: "The record a closed incident leaves behind: what it contains and who owns each part.",
			Tags:    []string{"policy", "incidents"},
			Body: `# Review Record Standard

Every closed control-failure incident files a ` + pm + ` review record.

The record carries a timeline of the nights in question, the originating
conditions as they are actually understood, the corrections that will be
made with a named owner for each, and the review meeting's outcome. Owned
corrections are tracked to completion; a record whose corrections have no
owner is returned to the incident's owner rather than filed.

The record is filed against the incident and referenced from the control's
own operating notes, so the next operator to meet the same failure finds
what the last one learned.

The record also closes the people side: hours spent on the nights in
question are entered under
{{page:worked-hours-ledger-practice|the worked-hours ledger practice}} before
the record is filed, because the workforce office's ledger closes on its
own rhythm whether or not the review is done.`,
		},
		{
			Key:     "worked-hours-ledger-practice",
			Slug:    "worked-hours-ledger-practice",
			Title:   "Worked Hours Ledger Practice",
			Summary: "The workforce office's worked-hours ledger: how hours are entered and agreed, and when a period closes for payroll.",
			Tags:    []string{"workforce", "payroll"},
			Body: `# Worked Hours Ledger Practice

The workforce office keeps the worked-hours ledger: the record of hours
actually worked, against which payroll and leave balances are agreed.

## Entering hours

Ordinary working days flow into the ledger from the badge records without
anyone's attention. Hours worked outside the working day are different: they
are entered by hand under code ` + tl + `, with the date and a one-line
purpose, before the payroll cutoff of the following week. An unentered line
lapses — the ledger closes with payroll, and a late claim reopens nothing.

## Balances

Entered ` + tl + ` hours accrue toward time off in lieu at the rate the
employment terms set, and the balance is visible beside leave in the
workforce portal.

## Cutoff and corrections

The ledger closes with each payroll run. A correction to a closed period is
a payroll correction: the payroll administrators apply it on the following
run and it adjusts pay, never the closed ledger itself. Questions about a
closed period go to the workforce office in writing, and are answered
against the ledger as it closed rather than as anyone remembers the week.`,
		},
		{
			Key:     "regulator-notify-calendar",
			Slug:    "regulatory-filing-calendar",
			Title:   "Regulatory Filing Calendar",
			Summary: "The compliance office's filing calendar: statutory windows, filing forms, and what a disruption during a window obliges.",
			Tags:    []string{"compliance", "calendar"},
			Body: `# Regulatory Filing Calendar

The compliance office maintains the calendar of statutory filing windows:
the periods in which the firm's returns and statements are prepared and
lodged with its regulators.

## Windows

Windows are published a year ahead. Each window names its filing, the
lodgment date, and the list of in-scope arrangements the filing draws on;
the office confirms scope questions in writing within a working day.
Preparation begins when the window opens, and the office's attention during
a window belongs to the filing, not to new requests.

## Form ` + rn + `

Each filing is lodged with its supporting declarations. Where anything on
the in-scope list was interrupted while the filing was being prepared, form
` + rn + ` is lodged with it before the window closes, stating what was
interrupted, the days affected, and the basis on which the filed figures
stand. The office can only lodge what it has been told while the window is
open; word that arrives after lodgment becomes a correction, which the
regulators treat differently.`,
		},
	}

	cell := graphfix.CompletionCell{
		ID:         "gs-incident",
		EntryKey:   "settlement-recon-job-runbook",
		EntryIntro: "The operating runbook for the nightly settlement-recon job is",
		Prompt: "The nightly settlement-recon job has now failed on three consecutive nights. " +
			"Write the complete incident-handling document for this situation, one the on-shift " +
			"engineer could follow as written: what is opened and at what severity, who has to " +
			"be told, how the escalation proceeds and on what clocks when nobody responds, " +
			"what is communicated while it is open, what has to be true before it can be closed, " +
			"and what record it has to leave behind. Completeness matters more than brevity; " +
			"the document is the deliverable.",
		GateQueries: []string{
			"settlement-recon job failed several nights running what happens",
			"incident severity and escalation when nobody responds",
			"how are control failure incidents communicated and closed",
		},
		Constraints: []graphfix.Constraint{
			{
				ID:       "ic-open",
				Desc:     "a third consecutive failed night opens an " + in + " incident record",
				Pages:    []string{"settlement-recon-job-runbook"},
				Patterns: []string{patternFor(m, in)},
			},
			{
				ID:       "ic-band",
				Desc:     "a control failing across a statement cycle is severity band " + sb,
				Pages:    []string{"incident-severity-standard"},
				Patterns: []string{patternFor(m, sb)},
			},
			{
				ID:       "ic-route",
				Desc:     "band " + sb + " escalates along the " + route + " route",
				Pages:    []string{"incident-severity-standard", "escalation-roster"},
				Patterns: []string{patternFor(m, route)},
			},
			{
				ID:       "ic-clock-first",
				Desc:     "the first rung holds " + first,
				Pages:    []string{"acknowledgment-clocks"},
				Patterns: []string{patternFor(m, first)},
			},
			{
				ID:       "ic-clock-second",
				Desc:     "the second rung holds " + second,
				Pages:    []string{"acknowledgment-clocks"},
				Patterns: []string{patternFor(m, second)},
			},
			{
				ID:       "ic-auto",
				Desc:     "rule " + ac + ": an expired clock advances the call on its own",
				Pages:    []string{"acknowledgment-clocks"},
				Patterns: []string{patternFor(m, ac)},
			},
			{
				ID:       "ic-cadence",
				Desc:     "updates follow cadence " + uc + " as promised in the opening notice",
				Pages:    []string{"incident-comms-standard"},
				Patterns: []string{patternFor(m, uc)},
			},
			{
				ID:       "ic-evidence",
				Desc:     "closure requires a " + ce + " evidence record from a clean scheduled pass",
				Pages:    []string{"closure-evidence-standard"},
				Patterns: []string{patternFor(m, ce)},
			},
			{
				ID:       "ic-record",
				Desc:     "the closed incident files a " + pm + " review record",
				Pages:    []string{"postmortem-record-standard"},
				Patterns: []string{patternFor(m, pm)},
			},
			{
				ID:            "ic-hours",
				Desc:          "overnight hours on the affected nights are entered in the worked-hours ledger under code " + tl + " before the following payroll cutoff",
				Pages:         []string{"worked-hours-ledger-practice"},
				Patterns:      []string{patternFor(m, tl)},
				Discontinuity: true,
			},
			{
				ID:            "ic-filing",
				Desc:          "a disruption inside a statutory filing window is notified on form " + rn,
				Pages:         []string{"regulator-notify-calendar"},
				Patterns:      []string{patternFor(m, rn)},
				Discontinuity: true,
			},
		},
	}
	return pages, cell
}

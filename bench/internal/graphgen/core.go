package graphgen

import "github.com/txn2/mcp-data-platform/bench/internal/graphfix"

// The core: three hand-authored completion cells whose pages are identical at
// every scale. Scale varies only the corpus around them, so a coverage
// difference between scales can never be attributed to the task changing.
//
// Each cell carries two discontinuity constraints: facts whose source pages
// are connected to the task institutionally (a finance close calendar
// governing when a change ships) rather than topically, written entirely in
// their own department's vocabulary. Those pages are reachable in one
// authored edge and — by certified construction, not assumption — in no
// task-derived search. Everything else about the cells keeps the probe's
// shape: an entry-control constraint, a topical spread over the closure, and
// hard-token signatures minted so they exist nowhere else.

// core assembles the study's fixed pages, cells and mint registry.
func core() ([]graphfix.Page, []graphfix.CompletionCell, []Mint) {
	m := newMinter()
	var pages []graphfix.Page
	cells := make([]graphfix.CompletionCell, 0, 3)
	for _, build := range []func(*minter) ([]graphfix.Page, graphfix.CompletionCell){
		changePlanCell, incidentCell, feedOnboardingCell,
	} {
		p, c := build(m)
		pages = append(pages, p...)
		cells = append(cells, c)
	}
	return pages, cells, m.mints
}

// changePlanCell is the wide-and-deep cell: plan an incompatible change to a
// governed stream. Discontinuities: the company close calendar (finance) and
// the corporate records schedule (records management).
func changePlanCell(m *minter) ([]graphfix.Page, graphfix.CompletionCell) {
	rb := m.class("RB", 7, "governed-tier-obligations", "change-class-directory")
	notice := m.quantity(17, "business days", "change-class-directory")
	cm := m.class("CM", 3, "schema-version-register")
	at := m.class("AT", 12, "attestation-procedure")
	an := m.class("AN", 5, "change-announcement-directory")
	ca := m.class("CA", 9, "consumer-agreement-ledger")
	qw := m.class("QW", 4, "corp-close-calendar")
	rr := m.class("RR", 21, "records-retention-directory")

	pages := []graphfix.Page{
		{
			Key:     "orders-ledger-stream-runbook",
			Slug:    "orders-ledger-stream-runbook",
			Title:   "Orders Ledger Stream Runbook",
			Summary: "Operating notes for the orders-ledger stream: what it carries, its governed-tier standing, and where its change and announcement rules are kept.",
			Tags:    []string{"runbook", "stream", "orders"},
			Body: `# Orders Ledger Stream Runbook

The orders-ledger stream carries the order lifecycle records every revenue
report is built from. It is a governed-tier stream: its records feed the
statutory accounts, and everything about how it changes is bound by
{{page:governed-tier-obligations|the tier obligations register}} rather than by
the producing team's own judgment.

## Changing what the stream carries

Any change to the record shape — a new field added to the records, a field
renamed, retyped or removed — starts with a proposal against the stream's
registered schema; versions and their compatibility standing live in
{{page:schema-version-register|the schema version register}}. Nothing is
announced by the producing team directly: consumer-facing notices go out
through {{page:change-announcement-directory|the change announcement directory}},
which also says who must hear first.

## Day to day

The stream settles during the overnight window and its lag is reviewed each
morning. Operational questions land with the stream's owning team; the
governed-tier obligations above decide what the owner may approve alone.`,
		},
		{
			Key:     "governed-tier-obligations",
			Slug:    "governed-tier-obligations",
			Title:   "Governed Tier Obligations",
			Summary: "What owning a governed-tier stream obliges: change classification, owner attestation, and the calendars change timing observes.",
			Tags:    []string{"policy", "governance"},
			Body: `# Governed Tier Obligations

A governed-tier stream feeds the statutory accounts, so its obligations are
stricter than ordinary operational care.

## Changes

Every change to what a governed stream carries is classified before any work
begins. An incompatible change to the record shape is class ` + rb + ` under
{{page:change-class-directory|the change class directory}}, which also sets
what each class owes its consumers. The owner cannot waive a classification.

## Attestation

After any change lands, the producing service's owner re-attests the stream's
completeness under {{page:attestation-procedure|the attestation procedure}}.
An unattested change is treated as unfinished however long it has been live.

## Timing

Change timing does not belong to the producing team alone: work on systems
that feed the statutory accounts also observes
{{page:corp-close-calendar|the company close calendar}}, whose windows are set
by the finance organization, not by engineering.`,
		},
		{
			Key:     "change-class-directory",
			Slug:    "change-class-directory",
			Title:   "Change Class Directory",
			Summary: "The change classes for governed streams and what each class owes consumers before work may begin.",
			Tags:    []string{"policy", "change"},
			Body: `# Change Class Directory

Every governed-stream change carries exactly one class, and the class decides
the notice its consumers are owed.

## Class ` + rb + `

Class ` + rb + ` covers incompatible changes to a stream's record shape: a
field added, renamed, retyped or removed. Consumers of the stream are owed
` + notice + ` of advance notice before the change is applied, counted in the
consuming teams' working calendar. The notice runs from the day the
announcement is actually delivered, and the class does not shorten for
changes the producer considers small: compatibility is judged from the
consumer's side.

## Other classes

Compatible additions and operational-only changes carry the lighter classes,
which owe an announcement but no waiting period. When in doubt between two
classes, the heavier one applies until the owner of the consuming side agrees
otherwise in writing.`,
		},
		{
			Key:     "schema-version-register",
			Slug:    "schema-version-register",
			Title:   "Schema Version Register",
			Summary: "How stream schema versions are registered, and the compatibility standing every new version is checked against.",
			Tags:    []string{"reference", "schema"},
			Body: `# Schema Version Register

The register is the single record of what shape each governed stream's
records have ever had.

## Registering a version

A new version is registered before any producer writes it — a record shape
that reaches the stream unregistered is an incident, not a shortcut. Each
stream declares a compatibility mode in the register, and every proposed
version is checked against that mode at registration time. The orders and
settlement streams sit in mode ` + cm + `, the strictest: a new version must
be readable by every consumer of the previous one without a code change.

## Reading the register

Register entries are the authoritative answer to "what changed and when": the
entry carries the version, the classification of the change that produced it,
and who approved it.`,
		},
		{
			Key:     "attestation-procedure",
			Slug:    "attestation-procedure",
			Title:   "Attestation Procedure",
			Summary: "The owner attestation that closes any governed-stream change: who signs, what they sign, and when.",
			Tags:    []string{"procedure", "governance"},
			Body: `# Attestation Procedure

Attestation is how a governed stream's owner puts their name to the stream's
completeness after a change.

The producing service's owner files form ` + at + ` once the change is live
and the first full settlement has passed. The form states that the records
now flowing match the registered version, that no consumer-visible field was
dropped along the way, and that the announced scope is the delivered scope.
The signature is personal to the owner: it cannot be delegated below the
service's named owner, and a vacancy in that role suspends changes rather
than the attestation.

An unattested change stays open in the tier's books and is raised at every
review until the form is filed.`,
		},
		{
			Key:     "change-announcement-directory",
			Slug:    "change-announcement-directory",
			Title:   "Change Announcement Directory",
			Summary: "How governed-stream changes are announced: the bulletin, who receives it, and which record of it is authoritative.",
			Tags:    []string{"procedure", "communications"},
			Body: `# Change Announcement Directory

Consumer-facing change notices are issued centrally so no consumer's notice
depends on which team is making the change.

## The bulletin

A governed-stream change is announced as an ` + an + ` bulletin. The bulletin
is delivered to every consumer the stream's agreement ledger lists — see
{{page:consumer-agreement-ledger|the consumer agreement ledger}} — and posted
against the stream's register entry, which is the authoritative copy when a
delivered copy and the register disagree.

## Retention

Bulletin issues are corporate correspondence, and their retention is not the
announcing team's decision: issued bulletins are filed under
{{page:records-retention-directory|the corporate records schedule}} like any
other controlled record.`,
		},
		{
			Key:     "consumer-agreement-ledger",
			Slug:    "consumer-agreement-ledger",
			Title:   "Consumer Agreement Ledger",
			Summary: "The ledger of registered stream consumers: what registration records, and what standing it grants.",
			Tags:    []string{"reference", "consumers"},
			Body: `# Consumer Agreement Ledger

The ledger records who consumes each governed stream and under what
agreement.

Each consuming team registers a ` + ca + ` agreement per stream it reads. The
agreement names the consuming service, its owner, and the fields it depends
on. Registration is a condition of read access, and it is also the notice
boundary: announcements are delivered to registered consumers, and a team
that never registered is owed nothing, however long it has been reading.

Agreements are reviewed when a stream's shape changes: a consumer whose
declared fields are untouched by a change may be notified without being
waited for, at the announcing owner's discretion.`,
		},
		{
			Key:     "corp-close-calendar",
			Slug:    "company-close-calendar",
			Title:   "Company Close Calendar",
			Summary: "The finance close calendar: period-end cutoffs, the quiet window, and when the books are open or closed.",
			Tags:    []string{"finance", "calendar"},
			Body: `# Company Close Calendar

The close calendar is maintained by the financial control group and governs
the accounting period end.

## The close

Each period closes on the working-day cadence the controller publishes:
journal cutoff first, then subledger agreement, then consolidation, then
sign-off of the statements. Between journal cutoff and sign-off the books are
closing and the control group's attention is on agreement of balances, not on
new activity.

## The quiet window

The stretch from journal cutoff to statement sign-off is the ` + qw + ` quiet
window. During ` + qw + `, systems whose figures flow into the statements are
left as they are: production alterations to those systems wait until the
books are signed, because an alteration mid-close turns every reconciliation
difference into a question about the alteration. The controller's office
grants exceptions in writing and treats them as findings in the close report.

Period dates, cutoff times and the current window's standing are published on
the finance group's internal page each period.`,
		},
		{
			Key:     "records-retention-directory",
			Slug:    "corporate-records-retention-directory",
			Title:   "Corporate Records Retention Directory",
			Summary: "The records management directory: filing series, retention standing, and disposal review for controlled corporate records.",
			Tags:    []string{"records", "policy"},
			Body: `# Corporate Records Retention Directory

The records management office maintains the filing series every controlled
corporate record belongs to. A record's series decides how long it is kept,
where it is filed, and who may dispose of it.

## Series ` + rr + `

Series ` + rr + ` covers issued circulars and bulletins: formal
correspondence the firm sends out in its own name and must be able to
produce later. Items in the series are filed with their delivery evidence at
the time of issue — filing is part of issuing, not an afterthought — and are
kept for the period the directory lists for the series, with disposal
reviewed by the records office rather than by the issuing team.

## Everything else

Minutes, agreements and working papers carry their own series. A record that
fits no listed series is filed under the general series and flagged for the
next directory review.`,
		},
	}

	cell := graphfix.CompletionCell{
		ID:         "gs-change-plan",
		EntryKey:   "orders-ledger-stream-runbook",
		EntryIntro: "The operating runbook for the orders-ledger stream is",
		Prompt: "We want to add a new field to the records on the orders-ledger stream. " +
			"Write the complete change plan the stream's owner could execute as written: " +
			"every step from proposal to the field being live, every approval the change needs, " +
			"every rule that constrains when it can be applied, and everything the stream's " +
			"consumers are owed along the way. Completeness matters more than brevity; " +
			"the plan is the deliverable.",
		GateQueries: []string{
			"orders-ledger stream add a field change plan",
			"how is a change to a governed stream approved and announced",
			"stream schema change consumer notice requirements",
		},
		Constraints: []graphfix.Constraint{
			{
				ID:       "cp-tier",
				Desc:     "the stream is governed-tier and the tier's obligations bind the change",
				Pages:    []string{"orders-ledger-stream-runbook", "governed-tier-obligations"},
				Patterns: []string{`governed[- ]tier`},
			},
			{
				ID:       "cp-class",
				Desc:     "an incompatible record-shape change is class " + rb,
				Pages:    []string{"governed-tier-obligations", "change-class-directory"},
				Patterns: []string{patternFor(m, rb)},
			},
			{
				ID:       "cp-notice",
				Desc:     "consumers are owed " + notice + " of advance notice",
				Pages:    []string{"change-class-directory"},
				Patterns: []string{patternFor(m, notice)},
			},
			{
				ID:       "cp-register",
				Desc:     "the new version is registered before any producer writes it, against compatibility mode " + cm,
				Pages:    []string{"schema-version-register"},
				Patterns: []string{patternFor(m, cm)},
			},
			{
				ID:       "cp-attest",
				Desc:     "the owner re-attests completeness on form " + at + " after the change",
				Pages:    []string{"attestation-procedure"},
				Patterns: []string{patternFor(m, at)},
			},
			{
				ID:       "cp-bulletin",
				Desc:     "the change is announced as an " + an + " bulletin, the register copy authoritative",
				Pages:    []string{"change-announcement-directory"},
				Patterns: []string{patternFor(m, an)},
			},
			{
				ID:       "cp-agreements",
				Desc:     "only consumers registered in the " + ca + " agreement ledger are announced to",
				Pages:    []string{"consumer-agreement-ledger"},
				Patterns: []string{patternFor(m, ca)},
			},
			{
				ID:            "cp-quiet",
				Desc:          "the change may not ship inside the " + qw + " quiet window of the close calendar",
				Pages:         []string{"corp-close-calendar"},
				Patterns:      []string{patternFor(m, qw)},
				Discontinuity: true,
			},
			{
				ID:            "cp-filing",
				Desc:          "the issued bulletin is filed under records series " + rr,
				Pages:         []string{"records-retention-directory"},
				Patterns:      []string{patternFor(m, rr)},
				Discontinuity: true,
			},
		},
	}
	return pages, cell
}

// patternFor returns the minted grading pattern for a token. Constraints and
// the mint registry stay consistent because both read the same record.
func patternFor(m *minter, token string) string {
	for _, mint := range m.mints {
		if mint.Token == token {
			return mint.Pattern
		}
	}
	panic("graphgen: no mint recorded for token " + token)
}

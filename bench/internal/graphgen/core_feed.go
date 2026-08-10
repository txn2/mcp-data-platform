package graphgen

import "github.com/txn2/mcp-data-platform/bench/internal/graphfix"

// feedOnboardingCell is the wide-and-shallow cell: stand up a new nightly
// feed operated like the parcel-events feed. Discontinuities: the procurement
// commitment ledger (procurement) and the data sharing agreement register
// (legal).
func feedOnboardingCell(m *minter) ([]graphfix.Page, graphfix.CompletionCell) {
	fn := m.class("FN", 13, "feed-naming-standard")
	lz := m.class("LZ", 14, "landing-zone-layout")
	st := m.class("ST", 16, "storage-tier-register")
	life := m.quantity(47, "days", "storage-tier-register")
	lh := m.class("LH", 18, "delivery-expectation-standard")
	rp := m.class("RP", 22, "feed-rerun-procedure")
	eg := m.class("EG", 24, "offplatform-copy-policy")
	pv := m.class("PV", 25, "procurement-commitment-ledger")
	sa := m.class("SA", 27, "sharing-agreement-register")

	pages := []graphfix.Page{
		{
			Key:     "parcel-events-feed-runbook",
			Slug:    "parcel-events-feed-runbook",
			Title:   "Parcel Events Feed Runbook",
			Summary: "Operating notes for the nightly parcel-events feed: what it writes, when it runs, and where its operating standards are kept.",
			Tags:    []string{"runbook", "feed", "parcel"},
			Body: `# Parcel Events Feed Runbook

The parcel-events feed writes the day's parcel scan records nightly. It runs
only after the parcel collectors drain: a missing hour in the output is a
collector matter, not a feed matter, and re-running the feed cannot supply
records a collector never delivered.

## How it is operated

The feed is the model other nightly feeds are stood up from, and everything
that makes it operable lives in the standards it follows rather than in this
page: its name follows {{page:feed-naming-standard|the feed naming standard}},
its output lands as {{page:landing-zone-layout|the landing zone layout}}
prescribes, its storage is registered in
{{page:storage-tier-register|the storage tier register}}, its landing time is
declared in {{page:delivery-expectation-standard|the delivery expectation standard}},
and a bad night is repaired under
{{page:feed-rerun-procedure|the re-run procedure}}.

## Leaving the platform

Feed output copied anywhere outside the platform is not the operator's call:
every such copy runs through {{page:offplatform-copy-policy|the off-platform copy policy}}.`,
		},
		{
			Key:     "feed-naming-standard",
			Slug:    "feed-naming-standard",
			Title:   "Feed Naming Standard",
			Summary: "How nightly feeds and their outputs are named, and how renames are carried out without stranding consumers.",
			Tags:    []string{"standard", "feeds"},
			Body: `# Feed Naming Standard

A feed's name is its contract with everyone who schedules against it, so
names follow pattern ` + fn + `: the subject first, then the grain of the
records, joined with a dash — the parcel-events feed itself is the model.
The output carries the feed's name; a consumer should never need a lookup
table to connect the two.

## Renames

A rename runs through an alias period: the old name keeps working while
registered consumers move over, and the alias is retired only when the
delivery records show no reads against it. Renaming without the alias period
is treated as decommissioning one feed and launching another, with
everything that obliges.`,
		},
		{
			Key:     "landing-zone-layout",
			Slug:    "landing-zone-layout",
			Title:   "Landing Zone Layout",
			Summary: "How the raw landing area is organized: per-feed prefixes, partition layout, and what may write where.",
			Tags:    []string{"standard", "storage"},
			Body: `# Landing Zone Layout

The landing area is organized by layout ` + lz + `: every feed owns exactly
one prefix, named for the feed, and everything the feed writes lands under
that prefix partitioned by write date. Nothing writes outside its own
prefix — a feed that needs a second area is two feeds — and nothing but the
feed writes inside it, so ownership of any object in the zone is readable
from its path alone.

Consumers read from the prefix; they do not write markers, checkpoints or
scratch output into it. Consumer state lives with the consumer.`,
		},
		{
			Key:     "storage-tier-register",
			Slug:    "storage-tier-register",
			Title:   "Storage Tier Register",
			Summary: "How feed storage is registered: tiers, output lifetime, and where the capacity a new registration draws on comes from.",
			Tags:    []string{"reference", "storage"},
			Body: `# Storage Tier Register

Every feed registers its output in exactly one storage tier, and the tier
decides the output's lifetime.

## Tier ` + st + `

Nightly operational feeds register in tier ` + st + `. Output in the tier is
purged ` + life + ` after its write date; keeping anything longer is a
re-registration into a retention tier, reviewed by the register's owners
rather than granted by the feed team. The parcel-events feed sits in this
tier, and a feed operated like it registers here too.

## Capacity

Registration is also where capacity is accounted for. The platform's storage
is bought ahead under a standing vendor commitment, so a new registration
draws against {{page:procurement-commitment-ledger|the procurement commitment ledger}}
maintained by the procurement office — registering the feed is what makes
the draw visible to them.`,
		},
		{
			Key:     "delivery-expectation-standard",
			Slug:    "delivery-expectation-standard",
			Title:   "Delivery Expectation Standard",
			Summary: "How each feed's expected landing time is declared, and when repeated lateness is raised rather than watched.",
			Tags:    []string{"standard", "feeds"},
			Body: `# Delivery Expectation Standard

Every nightly feed declares its expected landing hour in table ` + lh + `,
and the declaration is what consumers schedule against — an undeclared feed
has no lateness, only unpredictability.

A feed landing after its declared hour is late, and lateness is tracked per
morning against the declaration. One late morning is noted. Repeated late
mornings in the same week are raised with the owning team as a delivery
matter: either the window moves and the declaration is corrected, or the
cause is fixed. Quietly re-declaring a later hour to absorb lateness is a
change consumers must be told about like any other.`,
		},
		{
			Key:     "feed-rerun-procedure",
			Slug:    "feed-rerun-procedure",
			Title:   "Feed Re-run Procedure",
			Summary: "How a nightly feed's bad night is repaired: replacement by partition, and what a re-run must never change.",
			Tags:    []string{"procedure", "feeds"},
			Body: `# Feed Re-run Procedure

A re-run repairs a bad night by replacing it, never by appending to it.

Procedure ` + rp + ` governs the repair: the affected write-date partitions
are replaced whole, under the feed's own prefix, and the original write date
stays the partition's identity — a re-run performed days later still lands
under the night it repairs, so consumers re-reading the partition see the
corrected records where the bad ones were. A re-run that would change a
partition's write date is not a re-run; it is a new feed writing into an old
feed's zone, and the layout forbids it.

Re-runs happen after the upstream collectors have drained for the affected
window; re-running ahead of the upstream repair reproduces the bad night.`,
		},
		{
			Key:     "offplatform-copy-policy",
			Slug:    "offplatform-copy-policy",
			Title:   "Off-platform Copy Policy",
			Summary: "What any copy of feed output outside the platform requires: the approval, its contents, and the covering instrument.",
			Tags:    []string{"policy", "egress"},
			Body: `# Off-platform Copy Policy

No feed output leaves the platform on an operator's judgment alone.

Every copy outside the platform carries an ` + eg + ` approval naming the
receiving party, the purpose, and the date the copy will be deleted — an
approval without all three is returned unactioned. The approval is filed
before the copy is made, and re-copies under an existing approval are
re-copies only while the purpose and recipient are unchanged.

## The covering instrument

An approval also cites the agreement that makes the transfer lawful: the
covering instrument is looked up in
{{page:sharing-agreement-register|the data sharing agreement register}}
maintained by the legal office, and a transfer with no instrument to cite
does not proceed, whatever its purpose.`,
		},
		{
			Key:     "procurement-commitment-ledger",
			Slug:    "procurement-commitment-ledger",
			Title:   "Procurement Commitment Ledger",
			Summary: "The procurement office's ledger of standing vendor commitments: lines, draw-downs, and renewal.",
			Tags:    []string{"procurement", "ledger"},
			Body: `# Procurement Commitment Ledger

The procurement office buys recurring capacity ahead under standing vendor
commitments, and the ledger records each commitment line, what has been
drawn against it, and when it renews.

## Line ` + pv + `

Line ` + pv + ` is the standing commitment for bulk storage. Draw-downs
against the line are recorded as internal registrations are made, and the
office sizes the renewal from the recorded draws — an unrecorded draw is
capacity the renewal will not cover. New draws are confirmed with the
vendor manager before they begin, and a draw that would take the line past
its committed volume waits for the renewal rather than assuming it.

Renewals run on the office's own negotiation calendar; teams planning a
large draw talk to the office a cycle ahead.`,
		},
		{
			Key:     "sharing-agreement-register",
			Slug:    "data-sharing-agreement-register",
			Title:   "Data Sharing Agreement Register",
			Summary: "The legal office's register of data sharing instruments: what each instrument covers and how one is cited.",
			Tags:    []string{"legal", "register"},
			Body: `# Data Sharing Agreement Register

The legal office keeps the register of instruments under which the firm
shares records with outside parties.

## Instruments

Each instrument is registered as an ` + sa + ` entry: the counterparty, the
categories of records covered, the permitted purposes, and the instrument's
expiry. A transfer to an outside party cites the ` + sa + ` entry that
covers it, and the citation is checked against the entry's categories and
purposes, not against the transferring team's intent. An expired or absent
instrument means the transfer waits for the legal office, however routine
the request.

The register is the only authoritative list; copies of instruments held by
teams are working copies, and the register's expiry dates govern.`,
		},
	}

	cell := graphfix.CompletionCell{
		ID:         "gs-feed-onboarding",
		EntryKey:   "parcel-events-feed-runbook",
		EntryIntro: "The operating runbook for the parcel-events feed, the model for the new feed, is",
		Prompt: "We are standing up a new nightly feed, invoice-adjustments, to be operated exactly " +
			"like the parcel-events feed. Write the complete onboarding document for it: how " +
			"the feed and its output are named, where the output lands and how that area is " +
			"organized, how its storage is registered and how long its output lives, when it is " +
			"expected to land and what happens when it is repeatedly late, how it is re-run " +
			"safely after a bad night, and what applies if any of its output is ever copied " +
			"outside the platform. Completeness matters more than brevity; the document is the " +
			"deliverable.",
		GateQueries: []string{
			"new nightly feed onboarding naming storage delivery",
			"how are nightly feeds operated and registered",
			"feed output lifetime and delivery expectations",
		},
		Constraints: []graphfix.Constraint{
			{
				ID:       "fc-collectors",
				Desc:     "the feed runs after the collectors drain; a missing hour is a collector matter",
				Pages:    []string{"parcel-events-feed-runbook"},
				Patterns: []string{`collectors drain`},
			},
			{
				ID:       "fc-naming",
				Desc:     "the feed is named under pattern " + fn + " with renames through an alias period",
				Pages:    []string{"feed-naming-standard"},
				Patterns: []string{patternFor(m, fn)},
			},
			{
				ID:       "fc-layout",
				Desc:     "output lands under the feed's own prefix per layout " + lz,
				Pages:    []string{"landing-zone-layout"},
				Patterns: []string{patternFor(m, lz)},
			},
			{
				ID:       "fc-tier",
				Desc:     "the feed registers in storage tier " + st,
				Pages:    []string{"storage-tier-register"},
				Patterns: []string{patternFor(m, st)},
			},
			{
				ID:       "fc-lifetime",
				Desc:     "tier output is purged " + life + " after its write date",
				Pages:    []string{"storage-tier-register"},
				Patterns: []string{patternFor(m, life)},
			},
			{
				ID:       "fc-landing",
				Desc:     "the expected landing hour is declared in table " + lh,
				Pages:    []string{"delivery-expectation-standard"},
				Patterns: []string{patternFor(m, lh)},
			},
			{
				ID:       "fc-rerun",
				Desc:     "a re-run replaces partitions under procedure " + rp + " and never changes the write date",
				Pages:    []string{"feed-rerun-procedure"},
				Patterns: []string{patternFor(m, rp)},
			},
			{
				ID:       "fc-egress",
				Desc:     "an outside copy carries an " + eg + " approval naming recipient, purpose and deletion date",
				Pages:    []string{"offplatform-copy-policy"},
				Patterns: []string{patternFor(m, eg)},
			},
			{
				ID:            "fc-commitment",
				Desc:          "storage registration draws against procurement commitment line " + pv,
				Pages:         []string{"procurement-commitment-ledger"},
				Patterns:      []string{patternFor(m, pv)},
				Discontinuity: true,
			},
			{
				ID:            "fc-instrument",
				Desc:          "an outside transfer cites a covering " + sa + " instrument from the sharing register",
				Pages:         []string{"sharing-agreement-register"},
				Patterns:      []string{patternFor(m, sa)},
				Discontinuity: true,
			},
		},
	}
	return pages, cell
}

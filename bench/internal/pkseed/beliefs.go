package pkseed

// The frozen beliefs, curated from the capture corpus (stage 1). The
// corpus archives are the provenance; each fragment's source scenario is
// named in its comment.
//
// Provenance is not uniform across fragments, and the difference matters
// enough to state plainly rather than average away:
//
//   - The claims, evidence, corroboration, consequences, and the
//     suppression fragment are curated from captured prose. Curation is
//     editorial (choose, trim, neutralize the run-specific detail), not
//     authorial.
//   - The affordance fragment is NOT captured prose. What capture actually
//     wrote in its place was an imperative ("should first re-check
//     list_monitors", "check monitors again first"), and an imperative to
//     perform the measured action is exactly what the anti-tautology
//     invariant forbids in a treatment string. The fragment here is a
//     deliberately weakened, estimator-form rendering of that attested
//     behavior: it states that re-observation exists and what it costs,
//     and stops. The study therefore tests a WEAKER affordance than the
//     one capture produces, which biases against H2b rather than for it.
//
// Curation rules applied to every fragment:
//   - No wall-clock or run-specific values (capture wrote "as of
//     2026-07-24"; the dated form uses a fixed capture date instead, so
//     seeds do not drift with the day they were generated).
//   - No operation ids the fixture does not have, and no claim the fixture
//     does not make true in the captured world.
//   - Nothing that tells the reader to perform the measured action.

// captureDate is the fixed date the dated form reports. Point-in-time
// framing needs a date; a wall-clock one would make the seed set
// irreproducible, so the study pins it.
const captureDate = "2026-06-30"

// Beliefs returns the frozen belief set.
func Beliefs() []Belief {
	return []Belief{perishableAbsent(), perishablePresent(), durableGranularity(), eternalUniqueReach()}
}

// perishableAbsent is the study's primary belief and the direct analog of
// the motivating case: nothing is provisioned, so the downstream question
// has no valid call. It is the belief the RQ2 factorial runs over, because
// it is the one whose staleness makes a trusting agent refuse a question
// that has become answerable.
//
// Curated from the perishable-absent and perishable-forbidden capture
// episodes: the claim, the 200-versus-403 evidence, the sibling
// corroboration, and the consequence are the absent episode's own; the
// suppression fragment is the forbidden episode's own guidance, which is
// where this platform's capture actually produced self-sealing prose.
func perishableAbsent() Belief {
	return Belief{
		ID:            "perishable-absent",
		Class:         ClassPerishable,
		Asserts:       "the account has zero listening monitors provisioned, so no volume or sentiment trend can be reported",
		CapturedWorld: "monitors-0",
		Factorial:     true,
		Standing:      "The ACME account has zero listening monitors provisioned.",
		DatedForm:     "As of " + captureDate + ", the ACME account had zero listening monitors provisioned.",
		Body: "GET /insights/monitors (list_monitors) returns HTTP 200 with an empty items array, both unscoped and scoped to each of the account's workspaces, and not a 403, so this is an absence of monitors rather than an entitlement gap. " +
			"GET /insights/workspaces and GET /insights/profiles answer normally on the same credential. " +
			"Because GET /insights/monitors/{id}/trend (list_monitor_trend) takes a monitor id as a path parameter, there is no valid call to make for a volume or sentiment trend over any reporting window. " +
			"Owned-profile metrics are a different measurement and carry no sentiment dimension.",
		Suppression: "Do not re-attempt the listening endpoints for this account; report the gap rather than retrying variations.",
		Affordance:  "The monitor listing is a single GET, so this state is re-observable at the cost of one call.",
	}
}

// perishablePresent is the opposite direction: a belief that monitors
// exist, which goes stale when the account empties. Trusting it produces a
// fabricated value or a call against an id that is gone, where trusting the
// absent belief produces a wrong refusal. It carries the neutral phrasing:
// the factorial is run over one belief so the phrasing effect is estimated
// on one factual core rather than confounded with which belief it sits on.
//
// Curated from the perishable-present capture episode.
func perishablePresent() Belief {
	return Belief{
		ID:            "perishable-present",
		Class:         ClassPerishable,
		Asserts:       "the account has three listening monitors provisioned, two of which track ACME's own brand",
		CapturedWorld: "monitors-3",
		Standing:      "The ACME account has three listening monitors provisioned, across two workspaces.",
		DatedForm:     "As of " + captureDate + ", the ACME account had three listening monitors provisioned, across two workspaces.",
		Body: "Monitor 501 \"Brand mentions\" and monitor 503 \"Product launch\" track ACME's own brand; monitor 502 \"Competitor watch\" tracks rival brands and is not ACME's own coverage. " +
			"GET /insights/monitors/{id}/trend (list_monitor_trend) returns daily {date, volume, sentiment_score} for a monitor over a requested window, where sentiment_score runs 0 (negative) to 100 (positive).",
	}
}

// durableGranularity is the durable-contract control: a fact about API
// behavior that only a vendor release invalidates. A treatment that raises
// verification on the perishable belief should not raise it here, which is
// the discriminant half of H3.
//
// Curated from the durable-granularity capture episode.
func durableGranularity() Belief {
	return Belief{
		ID:            "durable-granularity",
		Class:         ClassDurable,
		Asserts:       "the granularity parameter is accepted and silently ignored on profile metrics",
		CapturedWorld: "monitors-0",
		Standing:      "GET /insights/profiles/{id}/metrics (list_profile_metrics) ignores the granularity query parameter on this connection.",
		DatedForm:     "As of " + captureDate + ", GET /insights/profiles/{id}/metrics (list_profile_metrics) ignored the granularity query parameter on this connection.",
		Body: "Passing granularity=week and omitting it both return daily rows, one per calendar day, despite the schema documenting \"day (default) or week\". " +
			"Nothing in the response reports which bucketing was applied, so the only signal is the dates on the rows. " +
			"Weekly figures have to be built from the full daily series, paginated to the end, and summed into calendar weeks.",
	}
}

// eternalUniqueReach is the eternal-invariant control: an identity over the
// units themselves, which nothing in the world can invalidate. Verification
// here is never rational, so a treatment that raises it is adding noise
// rather than calibrating.
//
// Curated from the eternal-unique-reach capture episode.
func eternalUniqueReach() Belief {
	return Belief{
		ID:            "eternal-unique-reach",
		Class:         ClassEternal,
		Asserts:       "daily unique counts must not be summed to a period unique; the aggregate reports the deduplicated figure",
		CapturedWorld: "monitors-0",
		Standing:      "unique_reach is a distinct-accounts count, deduplicated across whatever window is requested, so daily values must not be summed to a period figure.",
		DatedForm:     "As of " + captureDate + ", unique_reach was a distinct-accounts count, deduplicated across whatever window was requested, so daily values were not to be summed to a period figure.",
		Body: "Summing the daily unique_reach values double-counts every account that appears on more than one day, and overstates the period figure substantially. " +
			"GET /insights/profiles/{id}/metrics:aggregate (aggregate_profile_metrics) reports the deduplicated figure for the requested window directly.",
	}
}

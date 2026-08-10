package graphgen

import (
	"fmt"
	// nosemgrep: go.lang.security.audit.crypto.math_random.math-random-used -- deterministic corpus generation for the graph-completion study; not security-sensitive, and a seedable PRNG is required so the same Spec regenerates the same corpus.
	"math/rand/v2"
	"strings"

	"github.com/txn2/mcp-data-platform/bench/internal/graphfix"
)

// filler generation. The corpus around the study's core is an operations
// wiki at scale: clusters of pages about one system each (a runbook and its
// satellite notes), cross-linked within the cluster and occasionally to an
// earlier cluster. Filler is the haystack — semantically the same genre as
// the core so search has real competition, but structurally inert: filler
// never references a core page's closure into existence and never carries a
// minted token.

// fillerCluster is one system's page group under construction.
type fillerCluster struct {
	system string
	pages  []graphfix.Page
}

// generateFiller produces n filler pages as clusters, deterministically from
// rng. Earlier clusters are referenced by later ones, never the reverse, so
// the whole filler graph stays acyclic by construction. Roughly a quarter of
// clusters are department clusters (finance, workforce, compliance, and so
// on) while (family, topic) pairs last, so the corpus reads as a company's
// wiki rather than a wall of runbooks and the discontinuity pages have
// in-register competition.
func generateFiller(rng *rand.Rand, n, meanOutDegree int) ([]graphfix.Page, error) {
	names, err := fillerSystemNames(rng, (n+3)/4+1)
	if err != nil {
		return nil, err
	}
	dept := newDeptTopics(rng)
	var out []graphfix.Page
	var anchors []string // earlier clusters' anchor keys, for cross links
	for i := 0; len(out) < n; i++ {
		size := 4 + rng.IntN(5) // 4-8 pages
		if remaining := n - len(out); size > remaining {
			size = remaining
		}
		var cluster fillerCluster
		if i%3 == 2 && dept.remaining() > 0 {
			cluster = dept.buildCluster(rng, min(size, len(deptKinds)), anchors)
		} else {
			cluster = buildCluster(rng, names[0], size, meanOutDegree, anchors)
			names = names[1:]
		}
		out = append(out, cluster.pages...)
		anchors = append(anchors, cluster.pages[0].Key)
	}
	return out, nil
}

// fillerSystemNames draws distinct system names.
func fillerSystemNames(rng *rand.Rand, n int) ([]string, error) {
	if limit := len(fillerSubjects) * len(fillerQualifiers); n > limit {
		return nil, fmt.Errorf("graphgen: %d filler clusters exceed the %d composable system names", n, limit)
	}
	seen := map[string]bool{}
	out := make([]string, 0, n)
	for len(out) < n {
		name := fillerSubjects[rng.IntN(len(fillerSubjects))] + "-" + fillerQualifiers[rng.IntN(len(fillerQualifiers))]
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out, nil
}

// buildCluster writes one cluster: an anchor runbook first, then satellite
// pages that reference the anchor or an earlier sibling. crossAnchors are
// earlier clusters' runbooks; at most one page links out to one of them.
func buildCluster(rng *rand.Rand, system string, size, meanOutDegree int, crossAnchors []string) fillerCluster {
	c := fillerCluster{system: system}
	kinds := clusterKinds(rng, size)
	crossAt := -1
	if len(crossAnchors) > 0 {
		crossAt = rng.IntN(size)
	}
	for i, kind := range kinds {
		var cross string
		if i == crossAt {
			cross = crossAnchors[rng.IntN(len(crossAnchors))]
		}
		c.pages = append(c.pages, fillerPage(rng, system, kind, c.pages, meanOutDegree, cross))
	}
	return c
}

// clusterKinds picks the page roles for one cluster: the anchor runbook plus
// distinct satellite kinds.
func clusterKinds(rng *rand.Rand, size int) []string {
	kinds := []string{fillerKinds[0]}
	perm := rng.Perm(len(fillerKinds) - 1)
	for i := 0; len(kinds) < size; i++ {
		kinds = append(kinds, fillerKinds[1+perm[i%(len(fillerKinds)-1)]])
	}
	return kinds[:size]
}

// fillerPage renders one filler page. Earlier sibling pages are the reference
// candidates; cross, when set, adds one link to an earlier cluster's anchor.
func fillerPage(rng *rand.Rand, system, kind string, siblings []graphfix.Page, meanOutDegree int, cross string) graphfix.Page {
	key := system + "-" + strings.ReplaceAll(kind, " ", "-")
	title := titleCase(strings.ReplaceAll(system, "-", " ") + " " + kind)
	refs := siblingRefs(rng, siblings, meanOutDegree)
	if cross != "" {
		refs = append(refs, refPhrase(cross, "the neighboring runbook"))
	}
	return graphfix.Page{
		Key:     key,
		Slug:    key,
		Title:   title,
		Summary: fillerSummary(rng, system, kind),
		Body:    fillerBody(rng, system, kind, refs),
		Tags:    []string{"operations", strings.Split(key, "-")[0]},
	}
}

// refPhrase renders one {{page:key|fallback}} placeholder.
func refPhrase(key, fallback string) string {
	return fmt.Sprintf("{{page:%s|%s}}", key, fallback)
}

// siblingRefs picks reference placeholders to earlier sibling pages. The
// draw count centers on meanOutDegree, clamped by what exists.
func siblingRefs(rng *rand.Rand, siblings []graphfix.Page, meanOutDegree int) []string {
	if len(siblings) == 0 {
		return nil
	}
	n := min(1+rng.IntN(2*meanOutDegree-1), len(siblings)) // 1 .. 2*mean-1, mean on average
	perm := rng.Perm(len(siblings))
	out := make([]string, 0, n)
	for _, idx := range perm[:n] {
		p := siblings[idx]
		out = append(out, refPhrase(p.Key, "the "+strings.ToLower(p.Title)))
	}
	return out
}

// fillerSummary writes the page summary.
func fillerSummary(rng *rand.Rand, system, kind string) string {
	return fmt.Sprintf("Operating %s for the %s pipeline: what it covers, when it runs and who looks after it (%s).",
		kind, system, fillerTeams[rng.IntN(len(fillerTeams))])
}

// fillerBody writes the page body: an opening, two operational sections
// drawn from a rotating pool, and a notes section carrying the page's
// references. The section pool covers what real operating pages discuss —
// lateness, failures, changes, standing new pipelines up — so at scale the
// haystack competes for the study's task queries with genuinely similar
// content, not just with the same nouns. All quantities are spelled out; the
// digit and reserved-word namespaces belong to the mint.
func fillerBody(rng *rand.Rand, system, kind string, refs []string) string {
	pick := func(pool []string) string { return pool[rng.IntN(len(pool))] }
	sys := strings.ReplaceAll(system, "-", " ")
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", titleCase(sys+" "+kind))
	fmt.Fprintf(&b, "The %s pipeline %s during the %s window and normally finishes in under %s minutes. ",
		sys, pick(fillerVerbs), pick(fillerWindows), pick(fillerSpelledNumbers))
	fmt.Fprintf(&b, "These %s belong to %s, who review them after every schedule change.\n\n", kind, pick(fillerTeams))
	sections := rng.Perm(len(fillerSections))
	for _, idx := range sections[:2] {
		b.WriteString(fillerSections[idx](rng, sys))
	}
	fmt.Fprintf(&b, "## Notes\n\n")
	if len(refs) == 0 {
		fmt.Fprintf(&b, "Questions about the %s land with %s.", sys, pick(fillerTeams))
	} else {
		fmt.Fprintf(&b, "Related material: %s.", strings.Join(refs, ", "))
	}
	return b.String()
}

// fillerSections are the operational section templates a filler page draws
// two of. Each stays inside the namespace rules and off the core's signature
// tokens, and each varies its heading and phrasing per page: a haystack of
// near-identical chunks would either drown every entry page under one
// verbatim swarm or rank as one block, and neither is what a real wiki does.
var fillerSections = []func(*rand.Rand, string) string{
	func(rng *rand.Rand, sys string) string {
		n := fillerSpelledNumbers[rng.IntN(len(fillerSpelledNumbers))]
		return pickVariant(rng,
			fmt.Sprintf("## What to watch\n\nA late run usually means the upstream had not drained when the window opened. "+
				"Give it %s minutes before raising anything; repeated late mornings mean the window itself needs moving.\n\n", n),
			fmt.Sprintf("## Lateness\n\nMost mornings the output is simply there. When it is not, wait %s minutes before asking questions: "+
				"the usual cause is a slow upstream, and it clears itself. A whole week of slow mornings is a schedule conversation.\n\n", n),
			fmt.Sprintf("## Daily review\n\nThe morning review glances at volume and timing. Swings beyond roughly %s percent against the "+
				"trailing week get a same-day look; anything smaller waits for the weekly pass.\n\n", n),
		)
	},
	func(rng *rand.Rand, sys string) string {
		team := fillerTeams[rng.IntN(len(fillerTeams))]
		return pickVariant(rng,
			fmt.Sprintf("## Bad nights\n\nA single miss is re-attempted after the upstream refresh and noted. When misses repeat, "+
				"%s takes over and the affected days are worked until the output is current again.\n\n", team),
			fmt.Sprintf("## When output is wrong\n\nWrong output is worse than missing output. Stop the consumers reading it, "+
				"say so where they will see it, and let %s coordinate the repair rather than patching quietly.\n\n", team),
			fmt.Sprintf("## Recovery\n\nRecovery is always upstream-first: confirm the source is healthy, then repair here. "+
				"%s keeps the running list of nights still owed a repair.\n\n", team),
			"## Raising\n\nA repeating failure is raised on the platform escalation path: the owning team first, the "+
				"shift lead when they do not answer, and onward one step at a time with a clock on each step. "+
				"Acknowledging a step stops the climb; silence moves it on.\n\n",
			fmt.Sprintf("## Keeping people told\n\nWhile something here is broken, %s says so on the channel the first notice "+
				"named and keeps to the promised rhythm until it is fixed; going quiet reads as fixed, and it never is.\n\n", team),
		)
	},
	func(rng *rand.Rand, sys string) string {
		return pickVariant(rng,
			fmt.Sprintf("## Evolving the output\n\nWidening the %s records or retiring an attribute starts with a written note to the "+
				"consuming teams, and nothing visible moves until they have had their say.\n\n", sys),
			fmt.Sprintf("## Shape changes\n\nThe %s output's shape is a promise. Reworking it runs through the owning team's proposal "+
				"process, and the readers hear about it before it happens, not after.\n\n", sys),
			"## Versions\n\nEach reworking of the output lands as a new version beside the old one, and readers move over on "+
				"their own schedule. Retiring the old version waits until the read counts say nobody is left on it.\n\n",
		)
	},
	func(rng *rand.Rand, sys string) string {
		return pickVariant(rng,
			"## Provenance\n\nEverything here descends from the platform's shared operating conventions: naming by subject, "+
				"storage by registered tier, delivery by declared time. Local exceptions are written down or they do not exist.\n\n",
			"## Ownership\n\nThe pipeline has exactly one owning team, and the owning team's name is on the registration. "+
				"Shared curiosity is welcome; shared ownership is how output rots.\n\n",
			"## History\n\nThe notes here outlive their authors. Date anything surprising, and when a rule stops being true, "+
				"delete it rather than stacking a correction on top.\n\n",
		)
	},
	func(rng *rand.Rand, sys string) string {
		n := fillerSpelledNumbers[rng.IntN(len(fillerSpelledNumbers))]
		return pickVariant(rng,
			fmt.Sprintf("## Repairs\n\nA repair replaces whole partitions and lands them where the originals were. Repairing ahead of the "+
				"upstream fix reproduces the problem, so give the upstream its %s minutes and confirm the refresh first.\n\n", n),
			"## Backfills\n\nA backfill is a repair wearing a bigger coat: same replacement rules, same partition boundaries, "+
				"just more nights of them. Announce the span before starting so readers can hold their queries.\n\n",
			"## Idempotence\n\nEvery write here is safe to repeat: partitions replace, they never append. If a run is interrupted, "+
				"run it again whole rather than resuming it.\n\n",
		)
	},
}

// pickVariant chooses one section variant.
func pickVariant(rng *rand.Rand, variants ...string) string {
	return variants[rng.IntN(len(variants))]
}

// titleCase upper-cases each word, ASCII-only, which is all the pools hold.
func titleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

package graphgen

import (
	"fmt"
	"math/rand/v2"
	"strings"

	"github.com/txn2/mcp-data-platform/bench/internal/graphfix"
)

// Department filler clusters. Same construction rules as the operational
// clusters — digit-free, reserved-free, acyclic, referencing earlier pages
// only — in each department's own register.

// deptTopics deals out (family, topic) pairs without replacement.
type deptTopics struct {
	pairs []deptPair
}

// deptPair is one undealt department topic.
type deptPair struct {
	family deptFamily
	topic  string
}

// newDeptTopics shuffles every (family, topic) pair deterministically.
func newDeptTopics(rng *rand.Rand) *deptTopics {
	var pairs []deptPair
	for _, family := range deptFamilies {
		for _, topic := range family.topics {
			pairs = append(pairs, deptPair{family: family, topic: topic})
		}
	}
	rng.Shuffle(len(pairs), func(i, j int) { pairs[i], pairs[j] = pairs[j], pairs[i] })
	return &deptTopics{pairs: pairs}
}

// remaining reports how many topics are still undealt.
func (d *deptTopics) remaining() int { return len(d.pairs) }

// buildCluster writes one department cluster of up to size pages around the
// next undealt topic.
func (d *deptTopics) buildCluster(rng *rand.Rand, size int, crossAnchors []string) fillerCluster {
	pair := d.pairs[0]
	d.pairs = d.pairs[1:]
	c := fillerCluster{system: pair.family.name + "-" + strings.ReplaceAll(pair.topic, " ", "-")}
	crossAt := -1
	if len(crossAnchors) > 0 {
		crossAt = rng.IntN(size)
	}
	for i := 0; i < size && i < len(deptKinds); i++ {
		var cross string
		if i == crossAt {
			cross = crossAnchors[rng.IntN(len(crossAnchors))]
		}
		c.pages = append(c.pages, deptPage(rng, pair, deptKinds[i], c.pages, cross))
	}
	return c
}

// deptPage renders one department page.
func deptPage(rng *rand.Rand, pair deptPair, kind string, siblings []graphfix.Page, cross string) graphfix.Page {
	key := pair.family.name + "-" + strings.ReplaceAll(pair.topic+" "+kind, " ", "-")
	refs := siblingRefs(rng, siblings, 1)
	if cross != "" {
		refs = append(refs, refPhrase(cross, "the neighboring pages"))
	}
	return graphfix.Page{
		Key:     key,
		Slug:    key,
		Title:   titleCase(pair.topic + " " + kind),
		Summary: fmt.Sprintf("How %s is handled by %s: the working sequence, who is involved, and where questions land.", pair.topic, pair.family.actor),
		Body:    deptBody(rng, pair, kind, refs),
		Tags:    []string{pair.family.name, strings.Split(pair.topic, " ")[0]},
	}
}

// deptBody writes one department page's body in its family's register.
func deptBody(rng *rand.Rand, pair deptPair, kind string, refs []string) string {
	f := pair.family
	object := f.objects[rng.IntN(len(f.objects))]
	second := f.objects[rng.IntN(len(f.objects))]
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", titleCase(pair.topic+" "+kind))
	fmt.Fprintf(&b, "%s maintains the %s practice for the firm. %s are prepared and agreed on the %s rhythm, and questions about them land with %s rather than with the teams the work touches.\n\n",
		sentenceCase(f.actor), pair.topic, sentenceCase(object), f.cadence, f.actor)
	fmt.Fprintf(&b, "## Practice\n\nWork follows the published sequence: %s are gathered first, then %s are agreed, then the %s is signed off. An item arriving after its slot is carried to the next %s unless %s agrees otherwise in writing.\n\n",
		object, second, f.cadence, f.cadence, f.actor)
	fmt.Fprintf(&b, "## Notes\n\n")
	if len(refs) == 0 {
		fmt.Fprintf(&b, "Standing questions are collected by %s and answered at the %s review.", f.actor, f.cadence)
	} else {
		fmt.Fprintf(&b, "Related material: %s.", strings.Join(refs, ", "))
	}
	return b.String()
}

// sentenceCase upper-cases the first letter only.
func sentenceCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// Package graphgen deterministically generates operations-wiki corpora for
// the graph-completion study (#1250), at controlled scale and edge density,
// under the graphfix validator battery plus the generator's own invariants.
//
// A corpus is a fixed, hand-authored core — three completion cells whose
// pages are byte-identical at every scale, each carrying two certified
// discontinuity constraints — surrounded by generated filler clusters that
// scale the haystack without touching the task. Same Spec, same corpus:
// generation is a pure function of (Scale, Seed, EdgeDensity), so a run
// archive records the Spec instead of thousands of pages and any reader can
// regenerate the exact corpus.
//
// Signature uniqueness is guaranteed by construction rather than by scan
// (see mint.go), and the scan the probe relied on is kept as a verification
// that must pass trivially at every scale.
package graphgen

import (
	"fmt"
	// nosemgrep: go.lang.security.audit.crypto.math_random.math-random-used -- deterministic corpus generation for the graph-completion study; not security-sensitive, and a seedable PRNG is required so the same Spec regenerates the same corpus.
	"math/rand/v2"

	"github.com/txn2/mcp-data-platform/bench/internal/graphfix"
)

// DefaultSeed is the study's frozen generation seed. The design doc fixes it
// so every stage of the study reads the same corpora; it is a Spec field
// only so tests can prove corpora differ across seeds.
const DefaultSeed = 1250

// DefaultEdgeDensity is the filler mean out-degree, matching the probe
// fixture's authored density of roughly three references per page.
const DefaultEdgeDensity = 3

// Scales are the study's corpus sizes: within the probe's measured
// enumeration ceiling, an order past it, and two orders past it.
var Scales = []int{50, 500, 5000}

// Spec parameterizes one corpus generation.
type Spec struct {
	// Scale is the total page count, core included.
	Scale int `json:"scale"`
	// Seed drives every random draw. Same Spec, same corpus, always.
	Seed uint64 `json:"seed"`
	// EdgeDensity is the filler clusters' mean out-degree. Zero means
	// DefaultEdgeDensity. Core edges are authored, not parameterized.
	EdgeDensity int `json:"edge_density,omitempty"`
}

// Result is one generated corpus with its provenance.
type Result struct {
	Spec   Spec            `json:"spec"`
	Corpus graphfix.Corpus `json:"corpus"`
	// Mints is the signature-token registry: every minted literal, its
	// grading pattern, and the pages allowed to carry it.
	Mints []Mint `json:"mints"`
	// CorePages counts the fixed study pages; Scale minus this is filler.
	CorePages int `json:"core_pages"`
}

// Generate builds the corpus for one Spec and validates it against the full
// graphfix battery and the generator's own invariants before returning it.
func Generate(spec Spec) (*Result, error) {
	corePages, cells, mints := core()
	if spec.EdgeDensity == 0 {
		spec.EdgeDensity = DefaultEdgeDensity
	}
	switch {
	case spec.Scale < len(corePages):
		return nil, fmt.Errorf("graphgen: scale %d is below the %d-page core", spec.Scale, len(corePages))
	case spec.EdgeDensity < 1:
		return nil, fmt.Errorf("graphgen: edge density must be positive, got %d", spec.EdgeDensity)
	}
	// The generator's whole point is a seeded, reproducible draw: same Spec,
	// same corpus. Nothing here is security-sensitive, and Scale is already
	// proven positive above.
	rng := rand.New(rand.NewPCG(spec.Seed, uint64(spec.Scale))) // #nosec G404 G115 -- deterministic corpus generation from a validated positive scale
	filler, err := generateFiller(rng, spec.Scale-len(corePages), spec.EdgeDensity)
	if err != nil {
		return nil, err
	}
	res := &Result{
		Spec:      spec,
		Corpus:    graphfix.Corpus{Pages: append(corePages, filler...), Cells: cells},
		Mints:     mints,
		CorePages: len(corePages),
	}
	if err := res.Corpus.Validate(); err != nil {
		return nil, err
	}
	if err := res.validate(coreKeySet(corePages)); err != nil {
		return nil, err
	}
	return res, nil
}

// coreKeySet indexes the core pages by key.
func coreKeySet(pages []graphfix.Page) map[string]bool {
	out := make(map[string]bool, len(pages))
	for _, p := range pages {
		out[p.Key] = true
	}
	return out
}

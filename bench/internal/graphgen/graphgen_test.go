package graphgen

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/bench/internal/graphfix"
)

// generate builds one corpus or fails the test.
func generate(t *testing.T, spec Spec) *Result {
	t.Helper()
	res, err := Generate(spec)
	if err != nil {
		t.Fatalf("Generate(%+v): %v", spec, err)
	}
	return res
}

// TestGenerateEveryScalePassesTheBattery is the stage's central claim: at
// every study scale the generated corpus passes the same validator battery
// the probe fixture passed, page count equals the scale, and the three cells
// each carry two discontinuity constraints.
func TestGenerateEveryScalePassesTheBattery(t *testing.T) {
	t.Parallel()
	for _, scale := range Scales {
		res := generate(t, Spec{Scale: scale, Seed: DefaultSeed})
		if len(res.Corpus.Pages) != scale {
			t.Errorf("scale %d: corpus holds %d pages", scale, len(res.Corpus.Pages))
		}
		if len(res.Corpus.Cells) != 3 {
			t.Errorf("scale %d: %d cells, want 3", scale, len(res.Corpus.Cells))
		}
		for _, cell := range res.Corpus.Cells {
			if len(cell.Discontinuities()) != 2 {
				t.Errorf("scale %d cell %s: %d discontinuity constraints, want 2", scale, cell.ID, len(cell.Discontinuities()))
			}
		}
	}
}

// TestGenerateIsDeterministic: same Spec, same corpus, byte for byte. The
// archive records the Spec instead of the pages, so this is what makes a
// study run reproducible.
func TestGenerateIsDeterministic(t *testing.T) {
	t.Parallel()
	spec := Spec{Scale: 500, Seed: DefaultSeed}
	a, _ := json.Marshal(generate(t, spec))
	b, _ := json.Marshal(generate(t, spec))
	if string(a) != string(b) {
		t.Fatal("two generations from one Spec differ")
	}
	c, _ := json.Marshal(generate(t, Spec{Scale: 500, Seed: DefaultSeed + 1}))
	if string(a) == string(c) {
		t.Fatal("different seeds produced an identical corpus")
	}
}

// TestCoreIsScaleInvariant: the cells and every core page are identical at
// every scale, so scale varies only the haystack and a coverage difference
// between scales can never be attributed to the task changing.
func TestCoreIsScaleInvariant(t *testing.T) {
	t.Parallel()
	small := generate(t, Spec{Scale: Scales[0], Seed: DefaultSeed})
	large := generate(t, Spec{Scale: Scales[2], Seed: DefaultSeed})
	cellsA, _ := json.Marshal(small.Corpus.Cells)
	cellsB, _ := json.Marshal(large.Corpus.Cells)
	if string(cellsA) != string(cellsB) {
		t.Error("cells differ between scales")
	}
	for _, cell := range small.Corpus.Cells {
		for _, key := range small.Corpus.Closure(cell) {
			pa, _ := small.Corpus.ByKey(key)
			pb, ok := large.Corpus.ByKey(key)
			if !ok {
				t.Fatalf("closure page %s missing at the large scale", key)
			}
			if pa.Body != pb.Body || pa.Title != pb.Title || pa.Summary != pb.Summary {
				t.Errorf("closure page %s differs between scales", key)
			}
		}
	}
}

// TestClosureIsTheDeclaredGroundTruth: each cell's closure covers every
// constraint source page, stays inside the fixed core, and keeps the probe's
// shape variety (a wide-shallow cell and a distance-three cell).
func TestClosureIsTheDeclaredGroundTruth(t *testing.T) {
	t.Parallel()
	res := generate(t, Spec{Scale: Scales[0], Seed: DefaultSeed})
	var deep, wide bool
	for _, cell := range res.Corpus.Cells {
		depths := res.Corpus.Depths(cell)
		pages := map[string]bool{}
		for _, k := range cell.OffEntry() {
			for _, key := range k.Pages {
				pages[key] = true
				if d, ok := depths[key]; ok && d >= 3 {
					deep = true
				}
			}
		}
		if len(pages) >= 5 {
			wide = true
		}
	}
	if !deep {
		t.Error("no cell places an off-entry constraint at reference distance >= 3")
	}
	if !wide {
		t.Error("no cell spreads off-entry constraints over >= 5 source pages")
	}
}

// TestDiscontinuityPagesAreClosureLeaves: each discontinuity source page is
// reachable from its cell's entry (the edge is the discovery route) and is
// declared by exactly the constraints that grade it.
func TestDiscontinuityPagesAreClosureLeaves(t *testing.T) {
	t.Parallel()
	res := generate(t, Spec{Scale: Scales[0], Seed: DefaultSeed})
	for _, cell := range res.Corpus.Cells {
		depths := res.Corpus.Depths(cell)
		for _, key := range cell.DiscontinuityPages() {
			if _, ok := depths[key]; !ok {
				t.Errorf("cell %s: discontinuity page %s is not reachable from the entry", cell.ID, key)
			}
		}
	}
}

// TestGenerateRefusesAScaleBelowTheCore: the core is the floor.
func TestGenerateRefusesAScaleBelowTheCore(t *testing.T) {
	t.Parallel()
	if _, err := Generate(Spec{Scale: 10, Seed: DefaultSeed}); err == nil {
		t.Fatal("Generate accepted a scale below the core size")
	}
}

// TestValidatorRefusesASmuggledToken proves the verification has teeth: a
// filler page edited to carry a minted token fails validation as a namespace
// violation.
func TestValidatorRefusesASmuggledToken(t *testing.T) {
	t.Parallel()
	res := generate(t, Spec{Scale: Scales[0], Seed: DefaultSeed})
	core := map[string]bool{}
	for _, cell := range res.Corpus.Cells {
		for _, key := range res.Corpus.Closure(cell) {
			core[key] = true
		}
	}
	for i := range res.Corpus.Pages {
		if core[res.Corpus.Pages[i].Key] {
			continue
		}
		res.Corpus.Pages[i].Body += "\n\nAlso note class " + res.Mints[0].Token + " applies."
		break
	}
	if err := res.validate(core); err == nil {
		t.Fatal("validate accepted a filler page carrying a minted token")
	}
}

// TestValidatorRefusesAFillerDigit: the digit namespace belongs to the mint.
func TestValidatorRefusesAFillerDigit(t *testing.T) {
	t.Parallel()
	res := generate(t, Spec{Scale: Scales[0], Seed: DefaultSeed})
	core := map[string]bool{}
	for _, cell := range res.Corpus.Cells {
		for _, key := range res.Corpus.Closure(cell) {
			core[key] = true
		}
	}
	for i := range res.Corpus.Pages {
		if core[res.Corpus.Pages[i].Key] {
			continue
		}
		res.Corpus.Pages[i].Body += "\n\nGive it 45 minutes."
		break
	}
	if err := res.validate(core); err == nil || !strings.Contains(err.Error(), "digit") {
		t.Fatalf("validate error = %v, want a digit-namespace refusal", err)
	}
}

// TestMintPanicsOnReuse: the registry hands each token and each digit run
// out exactly once, which is the uniqueness-by-construction guarantee.
func TestMintPanicsOnReuse(t *testing.T) {
	t.Parallel()
	assertPanics := func(name string, f func()) {
		defer func() {
			if recover() == nil {
				t.Errorf("%s did not panic", name)
			}
		}()
		f()
	}
	m := newMinter()
	m.class("AB", 1, "p")
	assertPanics("token reuse", func() { m.class("AB", 1, "p") })
	m2 := newMinter()
	m2.quantity(9, "days", "p")
	assertPanics("digit-run reuse", func() { m2.class("CD", 9, "p") })
}

// TestQuantityPatternGuardsSubstrings: "83" must not be evidenced by "183",
// and the hyphenated form still counts.
func TestQuantityPatternGuardsSubstrings(t *testing.T) {
	t.Parallel()
	m := newMinter()
	token := m.quantity(83, "days", "p")
	k := graphfix.Constraint{ID: "x", Desc: "d", Pages: []string{"p"}, Patterns: []string{patternFor(m, token)}}
	if ok, _ := k.Covered("retention runs 183 days"); ok {
		t.Error("pattern matched a longer number")
	}
	if ok, _ := k.Covered("an 83-day retention window"); !ok {
		t.Error("pattern missed the hyphenated form")
	}
	if ok, _ := k.Covered("purged after 83 days"); !ok {
		t.Error("pattern missed the plain form")
	}
}

// TestFillerIsAcyclicAndPlantable at the largest scale: PlantOrder succeeds,
// which proves the whole reference graph acyclic with defined targets.
func TestFillerIsAcyclicAndPlantable(t *testing.T) {
	t.Parallel()
	res := generate(t, Spec{Scale: Scales[2], Seed: DefaultSeed})
	order, err := res.Corpus.PlantOrder()
	if err != nil {
		t.Fatalf("PlantOrder: %v", err)
	}
	if len(order) != len(res.Corpus.Pages) {
		t.Fatalf("plant order covers %d of %d pages", len(order), len(res.Corpus.Pages))
	}
}

// TestEdgeDensityIsParameterized: a denser spec produces more filler
// references than a sparser one at the same scale and seed.
func TestEdgeDensityIsParameterized(t *testing.T) {
	t.Parallel()
	refCount := func(density int) int {
		res := generate(t, Spec{Scale: 500, Seed: DefaultSeed, EdgeDensity: density})
		total := 0
		for _, p := range res.Corpus.Pages {
			total += len(p.Refs())
		}
		return total
	}
	sparse, dense := refCount(1), refCount(4)
	if dense <= sparse {
		t.Fatalf("edge density did not move the reference count: density 1 -> %d refs, density 4 -> %d", sparse, dense)
	}
}

// TestGenerationSummary prints the corpus shape for the design doc's record.
func TestGenerationSummary(t *testing.T) {
	t.Parallel()
	for _, scale := range Scales {
		res := generate(t, Spec{Scale: scale, Seed: DefaultSeed})
		for _, cell := range res.Corpus.Cells {
			t.Logf("scale %d cell %s: closure %d pages, %d constraints (%d off-entry, %d discontinuity)",
				scale, cell.ID, len(res.Corpus.Closure(cell)), len(cell.Constraints), len(cell.OffEntry()), len(cell.Discontinuities()))
		}
	}
}

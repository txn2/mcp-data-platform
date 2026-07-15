package report

import (
	// nosemgrep: go.lang.security.audit.crypto.math_random.math-random-used -- only the *rand.Rand type is used here; the seeded, reproducible RNG is constructed in internal/stats, crypto/rand would break reproducibility
	"math/rand"
	"slices"
	"sort"

	"github.com/txn2/mcp-data-platform/bench/internal/stats"
)

// Cross-arm comparison (#943): the benchmark's headline is arm-vs-arm on a
// pinned model, so the report generator loads one Results per arm and renders
// arm-by-suite accuracy, pass^k, efficiency, and the S3 trap-class breakdown,
// each accuracy carrying a bootstrap confidence interval. The bootstrap
// primitives live in internal/stats (shared with the S5 lifecycle report) and
// use a FIXED seed so two runs with identical inputs produce identical intervals
// (the #930 reproducibility criterion).

// Cell is one arm's aggregate over a slice of attempts (a suite or trap class).
type Cell struct {
	Arm             string  `json:"arm"`
	Graded          int     `json:"graded"`
	Correct         int     `json:"correct"`
	Accuracy        float64 `json:"accuracy"`
	CILow           float64 `json:"ci_low"`
	CIHigh          float64 `json:"ci_high"`
	PassKRate       float64 `json:"pass_k_rate"`
	MedianToolCalls float64 `json:"median_tool_calls"`
	P90ToolCalls    float64 `json:"p90_tool_calls"`
	MedianWallMS    float64 `json:"median_wall_ms"`
	HarnessFailures int     `json:"harness_failures"`
}

// Delta is one arm's accuracy difference from the baseline over a suite, with a
// bootstrap CI on the difference.
type Delta struct {
	Arm    string  `json:"arm"`
	Suite  string  `json:"suite"`
	Points float64 `json:"points"` // (arm - baseline) accuracy, in [-1, 1]
	CILow  float64 `json:"ci_low"`
	CIHigh float64 `json:"ci_high"`
}

// Comparison is the cross-arm view.
type Comparison struct {
	Arms        []string            `json:"arms"`
	Baseline    string              `json:"baseline"`
	Manifests   map[string]Manifest `json:"manifests"`
	Suites      []string            `json:"suites"`
	SuiteCells  map[string][]Cell   `json:"suite_cells"` // suite -> cells aligned to Arms
	TrapClasses []string            `json:"trap_classes"`
	TrapCells   map[string][]Cell   `json:"trap_cells"` // trap class -> cells aligned to Arms
	Overall     []Cell              `json:"overall"`    // per-arm across all suites
	Deltas      []Delta             `json:"deltas"`     // non-baseline arm vs baseline, per suite
}

// NewComparison builds the cross-arm comparison from one Results per arm. Arms
// are ordered by name; the baseline is "a0" when present, else the first arm.
func NewComparison(all []*Results) *Comparison {
	byArm := indexByArm(all)
	arms := sortedArms(byArm)
	c := &Comparison{
		Arms:       arms,
		Baseline:   pickBaseline(arms),
		Manifests:  map[string]Manifest{},
		SuiteCells: map[string][]Cell{},
		TrapCells:  map[string][]Cell{},
	}
	for _, a := range arms {
		c.Manifests[a] = byArm[a].Manifest
	}
	rng := stats.NewRNG()
	c.buildSuites(byArm, rng)
	c.buildTraps(byArm, rng)
	c.buildOverall(byArm, rng)
	c.buildDeltas(byArm, rng)
	return c
}

// indexByArm keys results by their manifest arm (last wins on duplicates).
func indexByArm(all []*Results) map[string]*Results {
	byArm := map[string]*Results{}
	for _, r := range all {
		if r != nil {
			byArm[r.Manifest.Arm] = r
		}
	}
	return byArm
}

// sortedArms returns the arm names sorted.
func sortedArms(byArm map[string]*Results) []string {
	arms := make([]string, 0, len(byArm))
	for a := range byArm {
		arms = append(arms, a)
	}
	sort.Strings(arms)
	return arms
}

// pickBaseline returns "a0" when present, else the first arm.
func pickBaseline(arms []string) string {
	for _, a := range arms {
		if a == "a0" {
			return a
		}
	}
	if len(arms) > 0 {
		return arms[0]
	}
	return ""
}

// buildSuites fills SuiteCells for every suite present in any arm.
func (c *Comparison) buildSuites(byArm map[string]*Results, rng *rand.Rand) {
	suites := collectSuites(byArm, c.Arms)
	c.Suites = suites
	for _, s := range suites {
		cells := make([]Cell, 0, len(c.Arms))
		for _, arm := range c.Arms {
			r := byArm[arm]
			cells = append(cells, cellFromAttempts(arm, filterSuite(r.Attempts, s), r.Manifest.K, rng))
		}
		c.SuiteCells[s] = cells
	}
}

// buildTraps fills TrapCells for every trap class present.
func (c *Comparison) buildTraps(byArm map[string]*Results, rng *rand.Rand) {
	classes := collectTrapClasses(byArm, c.Arms)
	c.TrapClasses = classes
	for _, class := range classes {
		cells := make([]Cell, 0, len(c.Arms))
		for _, arm := range c.Arms {
			r := byArm[arm]
			cells = append(cells, cellFromAttempts(arm, filterTrap(r.Attempts, class), r.Manifest.K, rng))
		}
		c.TrapCells[class] = cells
	}
}

// buildOverall fills the per-arm across-all-suites cells.
func (c *Comparison) buildOverall(byArm map[string]*Results, rng *rand.Rand) {
	for _, arm := range c.Arms {
		r := byArm[arm]
		c.Overall = append(c.Overall, cellFromAttempts(arm, r.Attempts, r.Manifest.K, rng))
	}
}

// buildDeltas computes each non-baseline arm's per-suite accuracy delta vs the
// baseline, with a bootstrap CI on the difference.
func (c *Comparison) buildDeltas(byArm map[string]*Results, rng *rand.Rand) {
	base := byArm[c.Baseline]
	if base == nil {
		return
	}
	for _, s := range c.Suites {
		baseBools := gradedBools(filterSuite(base.Attempts, s))
		for _, arm := range c.Arms {
			if arm == c.Baseline {
				continue
			}
			armBools := gradedBools(filterSuite(byArm[arm].Attempts, s))
			pts, lo, hi := stats.BootstrapDeltaCI(armBools, baseBools, rng)
			c.Deltas = append(c.Deltas, Delta{Arm: arm, Suite: s, Points: pts, CILow: lo, CIHigh: hi})
		}
	}
}

// cellFromAttempts aggregates one arm's attempts into a Cell (graded attempts
// only; harness failures are counted separately).
func cellFromAttempts(arm string, attempts []Attempt, k int, rng *rand.Rand) Cell {
	cell := Cell{Arm: arm}
	var bools []bool
	var calls, wall []float64
	byTask := map[string][]Attempt{}
	for _, a := range attempts {
		byTask[a.TaskID] = append(byTask[a.TaskID], a)
		if a.Error != "" {
			cell.HarnessFailures++
			continue
		}
		cell.Graded++
		bools = append(bools, a.Correct)
		if a.Correct {
			cell.Correct++
		}
		calls = append(calls, float64(a.ToolCalls))
		wall = append(wall, float64(a.WallMS))
	}
	if cell.Graded > 0 {
		cell.Accuracy = float64(cell.Correct) / float64(cell.Graded)
	}
	cell.CILow, cell.CIHigh = stats.BootstrapMeanCI(bools, rng)
	cell.MedianToolCalls = percentile(calls, 0.5)
	cell.P90ToolCalls = percentile(calls, 0.9)
	cell.MedianWallMS = percentile(wall, 0.5)
	cell.PassKRate = passKRate(byTask, k)
	return cell
}

// passKRate is the fraction of tasks with all k attempts graded and correct.
func passKRate(byTask map[string][]Attempt, k int) float64 {
	if len(byTask) == 0 || k <= 0 {
		return 0
	}
	pass := 0
	for _, attempts := range byTask {
		graded, correct := 0, 0
		for _, a := range attempts {
			if a.Error != "" {
				continue
			}
			graded++
			if a.Correct {
				correct++
			}
		}
		if graded == k && correct == k {
			pass++
		}
	}
	return float64(pass) / float64(len(byTask))
}

// gradedBools returns the correct/incorrect outcomes of graded attempts.
func gradedBools(attempts []Attempt) []bool {
	var out []bool
	for _, a := range attempts {
		if a.Error == "" {
			out = append(out, a.Correct)
		}
	}
	return out
}

// filterSuite returns attempts in the given suite.
func filterSuite(attempts []Attempt, suite string) []Attempt {
	var out []Attempt
	for _, a := range attempts {
		if a.Suite == suite {
			out = append(out, a)
		}
	}
	return out
}

// filterTrap returns attempts tagged with the given trap class.
func filterTrap(attempts []Attempt, class string) []Attempt {
	var out []Attempt
	for _, a := range attempts {
		if slices.Contains(a.TrapClasses, class) {
			out = append(out, a)
		}
	}
	return out
}

// collectSuites returns the sorted union of suites across arms.
func collectSuites(byArm map[string]*Results, arms []string) []string {
	set := map[string]bool{}
	for _, arm := range arms {
		for _, a := range byArm[arm].Attempts {
			set[a.Suite] = true
		}
	}
	return sortedKeys(set)
}

// collectTrapClasses returns the sorted union of trap classes across arms.
func collectTrapClasses(byArm map[string]*Results, arms []string) []string {
	set := map[string]bool{}
	for _, arm := range arms {
		for _, a := range byArm[arm].Attempts {
			for _, c := range a.TrapClasses {
				set[c] = true
			}
		}
	}
	return sortedKeys(set)
}

// sortedKeys returns a set's keys sorted.
func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Package pkseed builds the perishable-knowledge study's frozen seed set
// (#1054, protocol section 6 stage 2): the stored beliefs every cell is
// delivered, and the RQ2 phrasing factorial over them.
//
// Seeds are composed, not written eight times. A belief has one factual
// core, and each phrasing factor contributes one fixed fragment or nothing,
// so the 2x2x2 cells are minimal pairs by construction rather than by
// careful editing. That is what makes a main effect attributable to the
// factor rather than to whatever else drifted between two hand-written
// paragraphs. The fragments are curated from the capture corpus (stage 1);
// beliefs.go records each one's provenance, including the one fragment that
// is a deliberately weakened rendering rather than captured prose.
package pkseed

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// Volatility classes, mirroring the fixture's.
const (
	ClassPerishable = "perishable"
	ClassDurable    = "durable"
	ClassEternal    = "eternal"
)

// Phrasing is one cell of the RQ2 factorial (protocol 4.2). Every factor
// is a property of the stored prose alone; none of them changes what the
// belief asserts.
type Phrasing struct {
	// Dated frames the belief as a point-in-time observation ("as of
	// DATE") rather than a standing present-tense truth.
	Dated bool `json:"dated"`
	// Suppressive adds guidance steering the reader away from re-attempting
	// the endpoints the belief is about. This is the self-sealing factor:
	// H2's primary contrast is its main effect.
	Suppressive bool `json:"suppressive"`
	// Affordance adds a statement that the state is re-observable and what
	// observing it costs. Constrained by the anti-tautology invariant
	// (protocol 10.1.3): it may state that re-observation exists and its
	// cost, never that the reader should perform it.
	Affordance bool `json:"affordance"`
}

// Label renders the cell as a stable, readable suffix.
func (p Phrasing) Label() string {
	parts := []string{"standing", "plain", "noaff"}
	if p.Dated {
		parts[0] = "dated"
	}
	if p.Suppressive {
		parts[1] = "suppressive"
	}
	if p.Affordance {
		parts[2] = "aff"
	}
	return strings.Join(parts, "-")
}

// Cells returns the eight phrasing cells in a fixed order.
func Cells() []Phrasing {
	out := make([]Phrasing, 0, 8)
	for _, dated := range []bool{false, true} {
		for _, suppressive := range []bool{false, true} {
			for _, affordance := range []bool{false, true} {
				out = append(out, Phrasing{Dated: dated, Suppressive: suppressive, Affordance: affordance})
			}
		}
	}
	return out
}

// Belief is one factual core plus the fragments each phrasing factor
// contributes. Splitting the opening clause from the body is what lets
// temporal framing move without touching a word of the evidence.
type Belief struct {
	// ID names the belief; a seed's id is this plus the phrasing label.
	ID string `json:"id"`
	// Class is the volatility class the belief belongs to.
	Class string `json:"class"`
	// Asserts is the proposition, in plain terms, for the archive and the
	// grader. It is never delivered to an agent.
	Asserts string `json:"asserts"`
	// CapturedWorld is the fixture world this belief is true in. A cell is
	// stale exactly when the query-time world is not this one.
	CapturedWorld string `json:"captured_world"`
	// Standing is the opening clause in present-tense standing form.
	Standing string `json:"standing"`
	// DatedForm is the opening clause as a point-in-time observation. It
	// must assert the same fact as Standing.
	DatedForm string `json:"dated_form"`
	// Body is the evidence, corroboration, and consequence. Identical in
	// every cell.
	Body string `json:"body"`
	// Suppression is the fragment the suppressive factor adds.
	Suppression string `json:"suppression"`
	// Affordance is the fragment the recheck-affordance factor adds.
	Affordance string `json:"affordance"`
	// Factorial marks the belief the RQ2 2x2x2 is run over. The others
	// carry a single neutral phrasing: they are controls for H3's
	// discriminant clause and for the stale-available direction, not
	// subjects of the phrasing manipulation.
	Factorial bool `json:"factorial"`
}

// Seed is one frozen, deliverable stored belief.
type Seed struct {
	ID       string   `json:"id"`
	BeliefID string   `json:"belief_id"`
	Class    string   `json:"class"`
	Asserts  string   `json:"asserts"`
	World    string   `json:"captured_world"`
	Phrasing Phrasing `json:"phrasing"`
	// Text is the composed prose delivered to the agent.
	Text string `json:"text"`
}

// Compose builds one seed's prose from a belief and a phrasing cell.
// Fragment order is fixed: claim, evidence and consequence, then any
// guidance, then any affordance. Order is held constant so that a cell
// differs from its pair only by the presence of a fragment.
func Compose(b Belief, p Phrasing) Seed {
	opening := b.Standing
	if p.Dated {
		opening = b.DatedForm
	}
	parts := make([]string, 0, 4)
	parts = append(parts, opening, b.Body)
	if p.Suppressive {
		parts = append(parts, b.Suppression)
	}
	if p.Affordance {
		parts = append(parts, b.Affordance)
	}
	return Seed{
		ID:       b.ID + "-" + p.Label(),
		BeliefID: b.ID,
		Class:    b.Class,
		Asserts:  b.Asserts,
		World:    b.CapturedWorld,
		Phrasing: p,
		Text:     strings.Join(parts, " "),
	}
}

// Seeds returns the frozen seed set: the factorial belief in all eight
// phrasings, and every other belief in the neutral cell (standing, no
// guidance, no affordance).
func Seeds() []Seed {
	var out []Seed
	for _, b := range Beliefs() {
		if !b.Factorial {
			out = append(out, Compose(b, Phrasing{}))
			continue
		}
		for _, p := range Cells() {
			out = append(out, Compose(b, p))
		}
	}
	return out
}

// Hash is the canonical SHA-256 of the frozen seed set, recorded in run
// manifests so a result names the exact beliefs that produced it.
func Hash() string {
	raw, err := json.Marshal(Seeds())
	if err != nil {
		// Plain data; marshal cannot fail.
		panic(fmt.Sprintf("pkseed: marshal seeds: %v", err))
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Validate checks the properties the RQ2 contrast rests on: the factorial
// belief produces all eight cells, cells differ only by their fragments,
// and every belief names a world the fixture has.
func Validate(worldKnown func(string) bool) error {
	seeds := Seeds()
	seen := map[string]bool{}
	factorial := map[string]int{}
	for _, s := range seeds {
		if seen[s.ID] {
			return fmt.Errorf("pkseed: duplicate seed id %s", s.ID)
		}
		seen[s.ID] = true
		if s.Text == "" {
			return fmt.Errorf("pkseed: seed %s has no text", s.ID)
		}
		if !worldKnown(s.World) {
			return fmt.Errorf("pkseed: seed %s names world %q, which is not in the fixture registry", s.ID, s.World)
		}
	}
	for _, b := range Beliefs() {
		if err := validateBelief(b); err != nil {
			return err
		}
		if b.Factorial {
			factorial[b.ID] = len(Cells())
		}
	}
	if len(factorial) != 1 {
		return fmt.Errorf("pkseed: %d factorial beliefs, want exactly 1", len(factorial))
	}
	return nil
}

// validateBelief checks one belief's fragments are present and that its two
// temporal forms are genuinely a pair rather than two different claims.
func validateBelief(b Belief) error {
	switch {
	case b.Standing == "" || b.Body == "":
		return fmt.Errorf("pkseed: belief %s is missing its core", b.ID)
	case b.Asserts == "":
		return fmt.Errorf("pkseed: belief %s does not record what it asserts", b.ID)
	case b.CapturedWorld == "":
		return fmt.Errorf("pkseed: belief %s names no captured world", b.ID)
	}
	if !b.Factorial {
		return nil
	}
	switch {
	case b.DatedForm == "":
		return fmt.Errorf("pkseed: factorial belief %s has no dated form", b.ID)
	case b.Suppression == "":
		return fmt.Errorf("pkseed: factorial belief %s has no suppression fragment", b.ID)
	case b.Affordance == "":
		return fmt.Errorf("pkseed: factorial belief %s has no affordance fragment", b.ID)
	}
	return nil
}

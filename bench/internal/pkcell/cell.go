// Package pkcell defines the perishable-knowledge study's experimental
// units and what counts as correct in each (#1054, protocol section 7).
//
// A cell's correct behavior is derived, never assigned. It falls out of
// two computed facts: whether the question is answerable in the world the
// agent is asked in, and whether the belief the agent was handed is true
// in that world. Deriving it is what makes the study's separation
// structural rather than aspirational: cells at different staleness have
// mechanically different correct behaviors because the derivation says so,
// and a cell whose behavior was mislabeled by hand cannot exist.
package pkcell

import (
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/bench/internal/apigen"
	"github.com/txn2/mcp-data-platform/bench/internal/pkseed"
)

// Behavior is the one correct thing to do in a cell.
type Behavior string

const (
	// BehaviorAnswer: the question is answerable and the belief is true.
	// Answering is correct; verification is permitted but not required.
	BehaviorAnswer Behavior = "answer"
	// BehaviorRefuse: the question is unanswerable and the belief says so.
	// Refusing is correct. This is the fresh control for the direction the
	// motivating case sits in, and the cell a trusting agent gets right.
	BehaviorRefuse Behavior = "refuse"
	// BehaviorVerifyAnswer: the belief is stale and the question has
	// become answerable. Only an agent that looks can answer; trusting
	// yields a wrong refusal.
	BehaviorVerifyAnswer Behavior = "verify_then_answer"
	// BehaviorVerifyRefuse: the belief is stale and the question has
	// become unanswerable. Trusting yields a fabricated value.
	BehaviorVerifyRefuse Behavior = "verify_then_refuse"
	// BehaviorProbeRefuse: no belief was delivered and the question is
	// unanswerable. The agent must find that out for itself.
	BehaviorProbeRefuse Behavior = "probe_then_refuse"
)

// RequiresVerification reports whether reaching the correct outcome in this
// behavior requires observing the world.
func (b Behavior) RequiresVerification() bool {
	switch b {
	case BehaviorVerifyAnswer, BehaviorVerifyRefuse, BehaviorProbeRefuse:
		return true
	default:
		return false
	}
}

// RequiresRefusal reports whether the correct outcome is a stated
// unavailability rather than a value.
func (b Behavior) RequiresRefusal() bool {
	switch b {
	case BehaviorRefuse, BehaviorVerifyRefuse, BehaviorProbeRefuse:
		return true
	default:
		return false
	}
}

// Cell is one experimental unit: a question, the belief the agent holds
// going in, the delivery arm, and the world it is asked in.
type Cell struct {
	// ID is unique across the matrix.
	ID string `json:"id"`
	// Question is what the agent is asked.
	Question Question `json:"question"`
	// Seed is the belief planted before the episode. The zero value is
	// the no-knowledge control.
	Seed *pkseed.Seed `json:"seed,omitempty"`
	// Metadata is the RQ3 delivery arm.
	Metadata pkseed.Metadata `json:"metadata"`
	// CaptureWorld is the world the belief describes.
	CaptureWorld string `json:"capture_world"`
	// QueryWorld is the world the question is asked in. A cell is stale
	// exactly when the belief is false here.
	QueryWorld string `json:"query_world"`
	// Behavior is the derived correct behavior.
	Behavior Behavior `json:"behavior"`
	// BeliefTrue records whether the delivered belief holds at query time,
	// so an archived cell carries its own staleness rather than requiring
	// the reader to recompute it.
	BeliefTrue bool `json:"belief_true"`
	// Answerable records whether the world admits an answer.
	Answerable bool `json:"answerable"`
}

// Stale reports whether the cell delivered a belief that is false at query
// time.
func (c Cell) Stale() bool { return c.Seed != nil && !c.BeliefTrue }

// truths maps a belief to what makes it true in a world. Every belief must
// have one; Validate fails otherwise, so a belief cannot be added to the
// study without saying what would falsify it.
//
// These are the study's staleness definitions, and they are deliberately
// mechanical: a belief is true or false by inspection of the world, never
// by judgment about the prose.
var truths = map[string]func(apigen.World) bool{
	// "zero listening monitors provisioned"
	"perishable-absent": func(w apigen.World) bool { return w.Monitors == 0 },
	// "three listening monitors provisioned"
	"perishable-present": func(w apigen.World) bool { return w.Monitors == 3 },
	// "the granularity parameter is accepted and silently ignored"
	"durable-granularity": func(w apigen.World) bool { return w.Contract == apigen.Contract20261 },
	// "daily unique counts must not be summed to a period unique" — an
	// identity over the units, true in every world by construction.
	"eternal-unique-reach": func(apigen.World) bool { return true },
	// The reporting convention holds in every world: no world change can
	// falsify a definition the world never states.
	"coverage-convention": func(apigen.World) bool { return true },
}

// Derive builds one cell and computes its correct behavior. seed may be nil
// for the no-knowledge control.
func Derive(q Question, seed *pkseed.Seed, meta pkseed.Metadata, queryWorld string) (Cell, error) {
	w, ok := apigen.WorldByName(queryWorld)
	if !ok {
		return Cell{}, fmt.Errorf("pkcell: query world %q is not in the fixture registry", queryWorld)
	}
	c := Cell{
		Question: q, Seed: seed, Metadata: meta,
		QueryWorld: queryWorld, Answerable: q.AnswerableIn(w),
	}
	// A convention-bound question is answerable only when the convention
	// was delivered: the world cannot supply the missing definition.
	if q.RequiresBelief && seed == nil {
		c.Answerable = false
	}
	if seed == nil {
		c.ID = q.ID + "/none/" + queryWorld
		c.Behavior = BehaviorAnswer
		if !c.Answerable {
			c.Behavior = BehaviorProbeRefuse
		}
		return c, nil
	}
	if seed.BeliefID != q.BeliefID {
		return Cell{}, fmt.Errorf("pkcell: question %s is about belief %s, seed %s is about %s",
			q.ID, q.BeliefID, seed.ID, seed.BeliefID)
	}
	truth, ok := truths[seed.BeliefID]
	if !ok {
		return Cell{}, fmt.Errorf("pkcell: belief %s has no truth condition", seed.BeliefID)
	}
	if seed.Phrasing.Affordance && w.RecheckCalls() != 1 {
		return Cell{}, fmt.Errorf("pkcell: seed %s states re-observation costs one call, but world %s makes it cost %d; "+
			"an affordance that misstates the cost is a false estimator, not a treatment",
			seed.ID, queryWorld, w.RecheckCalls())
	}
	c.CaptureWorld = seed.World
	c.BeliefTrue = truth(w)
	c.Behavior = behaviorFor(c.BeliefTrue, c.Answerable)
	c.ID = q.ID + "/" + seed.ID + "/" + armLabel(meta) + "/" + queryWorld
	return c, nil
}

// behaviorFor is the derivation itself: two booleans in, one correct
// behavior out.
func behaviorFor(beliefTrue, answerable bool) Behavior {
	switch {
	case beliefTrue && answerable:
		return BehaviorAnswer
	case beliefTrue && !answerable:
		return BehaviorRefuse
	case answerable:
		return BehaviorVerifyAnswer
	default:
		return BehaviorVerifyRefuse
	}
}

// armLabel names the delivery arm for a cell id.
func armLabel(m pkseed.Metadata) string {
	if m.Enriched {
		return "enriched"
	}
	return "bare"
}

// Validate checks the study's definitions are complete before a run: every
// belief has a truth condition, every question names a belief that exists,
// and the question set is not silently missing a class.
func Validate() error {
	beliefs, err := validateTruths()
	if err != nil {
		return err
	}
	return validateQuestions(beliefs)
}

// validateTruths checks beliefs and truth conditions correspond exactly, so
// no belief enters the study without saying what would falsify it and no
// truth condition survives the belief it was written for.
func validateTruths() (map[string]pkseed.Belief, error) {
	beliefs := map[string]pkseed.Belief{}
	for _, b := range pkseed.Beliefs() {
		beliefs[b.ID] = b
		if _, ok := truths[b.ID]; !ok {
			return nil, fmt.Errorf("pkcell: belief %s has no truth condition, so its staleness is undefined", b.ID)
		}
	}
	for id := range truths {
		if _, ok := beliefs[id]; !ok {
			return nil, fmt.Errorf("pkcell: truth condition %s names no belief", id)
		}
	}
	return beliefs, nil
}

// validateQuestions checks the question set is well formed and covers every
// volatility class, since the discriminant clause of H3 needs all three.
func validateQuestions(beliefs map[string]pkseed.Belief) error {
	seen := map[string]bool{}
	classes := map[string]int{}
	for _, q := range Questions() {
		b, ok := beliefs[q.BeliefID]
		switch {
		case seen[q.ID]:
			return fmt.Errorf("pkcell: duplicate question id %s", q.ID)
		case !ok:
			return fmt.Errorf("pkcell: question %s names belief %s, which does not exist", q.ID, q.BeliefID)
		}
		seen[q.ID] = true
		classes[b.Class]++
	}
	for _, class := range []string{pkseed.ClassPerishable, pkseed.ClassDurable, pkseed.ClassEternal} {
		if classes[class] == 0 {
			return fmt.Errorf("pkcell: no question exercises the %s class", class)
		}
	}
	return nil
}

// CostSweepCells vary the price of checking and nothing else.
//
// The pre-run found verification at ceiling, which the normative model
// explains rather than contradicts: with an unscoped account a recheck is
// one call, so c/L is near zero and verify-always is the rational policy.
// A study whose threshold sits at zero cannot observe a threshold. These
// cells move c from 1 to 11 while holding the belief, its phrasing, the
// delivery arm, the question, and the world's staleness fixed: every one
// of them is the same empty account with the same true belief, where
// refusing is correct and checking is optional.
//
// If verification falls as c rises, the study has the curve its primary
// dependent variable is defined over. If it does not, the finding is that
// these agents check whatever it costs.
func CostSweepCells() ([]Cell, error) {
	neutral, err := neutralSeed("perishable-absent")
	if err != nil {
		return nil, err
	}
	q, err := questionByID("trend-volume")
	if err != nil {
		return nil, err
	}
	worlds := []string{"monitors-0", "monitors-0-scoped", "monitors-0-scoped-5", "monitors-0-scoped-10"}
	cells := make([]Cell, 0, len(worlds))
	costs := map[int]bool{}
	for _, name := range worlds {
		c, err := Derive(q, neutral, pkseed.Metadata{}, name)
		if err != nil {
			return nil, err
		}
		w, _ := apigen.WorldByName(name)
		if costs[w.RecheckCalls()] {
			return nil, fmt.Errorf("pkcell: two sweep worlds cost %d calls; the sweep would not vary c", w.RecheckCalls())
		}
		costs[w.RecheckCalls()] = true
		if c.Behavior != BehaviorRefuse {
			return nil, fmt.Errorf("pkcell: sweep world %s requires %s, not %s; the sweep must hold everything but cost fixed",
				name, c.Behavior, BehaviorRefuse)
		}
		cells = append(cells, c)
	}
	return cells, nil
}

// AnswerSweepCells are the cost sweep's mirror: a belief that hands the
// agent the answer rather than telling it none exists.
//
// The first sweep only ever delivered "this cannot be answered", and in
// that direction checking is also the route to a defensible refusal, so a
// verifying agent might be reluctant to decline rather than distrustful of
// the note. Here the belief is true, the question is answerable, and
// trusting means reading the answer straight off the note and stating it.
// An agent that checks anyway is spending calls it does not need to reach
// an answer it already has.
//
// The set carries two cells with no belief at all, at the cheapest and
// dearest cost. Without them a uniform verification rate says nothing:
// these are the reference for what an agent does when it has to find the
// answer itself, and the comparison that shows whether delivered knowledge
// changes behavior at all.
func AnswerSweepCells() ([]Cell, error) {
	neutral, err := neutralSeed("perishable-present")
	if err != nil {
		return nil, err
	}
	q, err := questionByID("monitor-count")
	if err != nil {
		return nil, err
	}
	worlds := []string{"monitors-3", "monitors-3-scoped", "monitors-3-scoped-5", "monitors-3-scoped-10"}
	cells := make([]Cell, 0, len(worlds)+2)
	for _, name := range worlds {
		c, err := Derive(q, neutral, pkseed.Metadata{}, name)
		if err != nil {
			return nil, err
		}
		if c.Stale() || c.Behavior != BehaviorAnswer {
			return nil, fmt.Errorf("pkcell: sweep world %s gives %s on a stale=%v cell; the belief must be true and the question answerable",
				name, c.Behavior, c.Stale())
		}
		cells = append(cells, c)
	}
	for _, name := range []string{"monitors-3", "monitors-3-scoped-10"} {
		c, err := Derive(q, nil, pkseed.Metadata{}, name)
		if err != nil {
			return nil, err
		}
		cells = append(cells, c)
	}
	return cells, nil
}

// BridgeProbeCells are the derivability bridge (the two-regime probe):
// the same question, once with the convention delivered and once without,
// in the same world, bare arm.
//
// The convention cell's correct behavior is to answer, and the correct
// answer is reachable only by combining fetched trend data with the
// delivered threshold, so a correct answer IS reliance on the note. The
// control cell is the derivability check itself: with no note, the
// threshold is unknowable and the correct behavior is to establish that
// and decline. A control agent that produces the "correct" count without
// the note means the convention leaked or is guessable, and the probe is
// invalid rather than positive.
func BridgeProbeCells() ([]Cell, error) {
	return bridgeCellsFor("positive-coverage-days")
}

// BridgeDirectiveProbeCells are the bridge pair on the directive twin
// question (gate probe): identical convention dependence, but the prompt
// names the exact endpoint and parameters, so the task presents no visible
// reason to discover. This is the phrasing under which instruction-only
// steering was historically skipped; the cells exist to measure whether
// discovery still happens here without the gate.
func BridgeDirectiveProbeCells() ([]Cell, error) {
	return bridgeCellsFor("positive-coverage-days-directive")
}

// bridgeCellsFor derives a bridge pair (note delivered, no-note control)
// for one convention-bound question, enforcing the pair's defining
// property: answering is correct exactly when the note was delivered.
func bridgeCellsFor(questionID string) ([]Cell, error) {
	neutral, err := neutralSeed("coverage-convention")
	if err != nil {
		return nil, err
	}
	q, err := questionByID(questionID)
	if err != nil {
		return nil, err
	}
	withNote, err := Derive(q, neutral, pkseed.Metadata{}, "monitors-3")
	if err != nil {
		return nil, err
	}
	control, err := Derive(q, nil, pkseed.Metadata{}, "monitors-3")
	if err != nil {
		return nil, err
	}
	if withNote.Behavior != BehaviorAnswer || control.Behavior != BehaviorProbeRefuse {
		return nil, fmt.Errorf("pkcell: bridge cells derive %s and %s; want answer with the note and probe-then-refuse without it",
			withNote.Behavior, control.Behavior)
	}
	return []Cell{withNote, control}, nil
}

// StaleAnswerCells are the last cell of the capability-by-derivability
// matrix: a note that hands the agent an answer which the world has since
// falsified. The belief says three monitors; the account has been emptied;
// the truthful answer is zero. A trusting agent states three — a
// confidently wrong value, not a wrong refusal — which is the direction
// the tier that trusts answer-bearing notes is exposed in. The companion
// no-knowledge control at the same world separates "trusted the stale
// note" from "cannot count an empty account".
func StaleAnswerCells() ([]Cell, error) {
	seed, err := neutralSeed("perishable-present")
	if err != nil {
		return nil, err
	}
	q, err := questionByID("monitor-count")
	if err != nil {
		return nil, err
	}
	stale, err := Derive(q, seed, pkseed.Metadata{}, "monitors-0")
	if err != nil {
		return nil, err
	}
	if !stale.Stale() || stale.Behavior != BehaviorVerifyAnswer {
		return nil, fmt.Errorf("pkcell: the stale-answer cell derives stale=%v %s; want a stale cell requiring verify-then-answer",
			stale.Stale(), stale.Behavior)
	}
	control, err := Derive(q, nil, pkseed.Metadata{}, "monitors-0")
	if err != nil {
		return nil, err
	}
	return []Cell{stale, control}, nil
}

// PreRunCells are the two cells of the internal power pre-run (protocol
// section 9). They are the two ends of the staleness axis on the study's
// primary question, with everything else held at its plainest: the same
// belief in its neutral phrasing, delivered bare, asked once where it is
// true and once where the world has moved on.
//
// Two points are what the pre-run needs. The primary contrast is a
// verification rate as a function of staleness, so its variance is
// estimated from a cell where verification is required and one where it is
// not, and the observed rates set the k that a target minimum detectable
// effect needs.
//
// The pre-run is exploratory by construction and its attempts are excluded
// from any confirmatory analysis.
func PreRunCells() ([]Cell, error) {
	neutral, err := neutralSeed("perishable-absent")
	if err != nil {
		return nil, err
	}
	q, err := questionByID("trend-volume")
	if err != nil {
		return nil, err
	}
	fresh, err := Derive(q, neutral, pkseed.Metadata{}, "monitors-0")
	if err != nil {
		return nil, err
	}
	stale, err := Derive(q, neutral, pkseed.Metadata{}, "monitors-3")
	if err != nil {
		return nil, err
	}
	if fresh.Behavior == stale.Behavior {
		return nil, fmt.Errorf("pkcell: the pre-run cells both require %s and cannot estimate a contrast", fresh.Behavior)
	}
	return []Cell{fresh, stale}, nil
}

// neutralSeed returns a belief's plainest phrasing: standing, with no
// guidance and no affordance.
func neutralSeed(beliefID string) (*pkseed.Seed, error) {
	for _, s := range pkseed.Seeds() {
		if s.BeliefID != beliefID || s.Phrasing.Dated || s.Phrasing.Suppressive || s.Phrasing.Affordance {
			continue
		}
		seed := s
		return &seed, nil
	}
	return nil, errors.New("pkcell: no neutral seed for belief " + beliefID)
}

// questionByID resolves a question from the committed set.
func questionByID(id string) (Question, error) {
	for _, q := range Questions() {
		if q.ID == id {
			return q, nil
		}
	}
	return Question{}, errors.New("pkcell: no question " + id)
}

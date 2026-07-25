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
		case q.Budget <= 0:
			return fmt.Errorf("pkcell: question %s has no tool-call budget", q.ID)
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

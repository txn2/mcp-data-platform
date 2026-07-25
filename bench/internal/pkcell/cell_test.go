package pkcell

// The derivation is the study's separation guarantee expressed as code, so
// these tests are about that guarantee: cells at different staleness have
// mechanically different correct behaviors, a stale cell cannot be passed
// by trusting, and no belief can enter the study without saying what would
// falsify it.

import (
	"testing"

	"github.com/txn2/mcp-data-platform/bench/internal/apigen"
	"github.com/txn2/mcp-data-platform/bench/internal/pkseed"
)

// seedFor returns the neutral seed for a belief.
func seedFor(t *testing.T, beliefID string) *pkseed.Seed {
	t.Helper()
	for _, s := range pkseed.Seeds() {
		if s.BeliefID == beliefID && !s.Phrasing.Dated && !s.Phrasing.Suppressive && !s.Phrasing.Affordance {
			return &s
		}
	}
	t.Fatalf("no neutral seed for belief %s", beliefID)
	return nil
}

// questionFor returns a question by id.
func questionFor(t *testing.T, id string) Question {
	t.Helper()
	for _, q := range Questions() {
		if q.ID == id {
			return q
		}
	}
	t.Fatalf("no question %s", id)
	return Question{}
}

// TestStalenessChangesTheCorrectBehavior is the separation guarantee: the
// same question and the same belief, asked in two worlds, require
// different things. If this ever collapses, the study is measuring a
// foregone conclusion.
func TestStalenessChangesTheCorrectBehavior(t *testing.T) {
	q := questionFor(t, "trend-volume")
	seed := seedFor(t, "perishable-absent")

	fresh, err := Derive(q, seed, pkseed.Metadata{}, "monitors-0")
	if err != nil {
		t.Fatal(err)
	}
	stale, err := Derive(q, seed, pkseed.Metadata{}, "monitors-3")
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Behavior == stale.Behavior {
		t.Fatalf("both worlds require %s; the cells do not separate", fresh.Behavior)
	}
	if fresh.Behavior != BehaviorRefuse {
		t.Errorf("belief true and question unanswerable requires %s, got %s", BehaviorRefuse, fresh.Behavior)
	}
	if stale.Behavior != BehaviorVerifyAnswer {
		t.Errorf("belief stale and question answerable requires %s, got %s", BehaviorVerifyAnswer, stale.Behavior)
	}
	if fresh.Stale() || !stale.Stale() {
		t.Errorf("staleness computed wrong: fresh=%v stale=%v", fresh.Stale(), stale.Stale())
	}
	// The stale cell cannot be passed by trusting: reaching the answer
	// requires observing the world.
	if !stale.Behavior.RequiresVerification() {
		t.Error("the stale cell can be passed without looking at the world")
	}
}

// TestBothStalenessDirections covers the other direction: a belief that
// monitors exist, in a world that has emptied. Trusting there yields a
// value where none is available, not a refusal where an answer is.
func TestBothStalenessDirections(t *testing.T) {
	q := questionFor(t, "monitor-count")
	seed := seedFor(t, "perishable-present")
	stale, err := Derive(q, seed, pkseed.Metadata{}, "monitors-0")
	if err != nil {
		t.Fatal(err)
	}
	if !stale.Stale() {
		t.Fatal("a three-monitor belief in an empty world is not stale")
	}
	// The count question is answerable in every world (zero is an answer),
	// so the correct behavior is to look and then answer.
	if stale.Behavior != BehaviorVerifyAnswer {
		t.Errorf("behavior %s, want %s", stale.Behavior, BehaviorVerifyAnswer)
	}
	fresh, err := Derive(q, seed, pkseed.Metadata{}, "monitors-3")
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Stale() || fresh.Behavior != BehaviorAnswer {
		t.Errorf("fresh cell: stale=%v behavior=%s", fresh.Stale(), fresh.Behavior)
	}
}

// TestNoKnowledgeControl checks the control cell: with no belief, an
// unanswerable question has to be found unanswerable.
func TestNoKnowledgeControl(t *testing.T) {
	q := questionFor(t, "trend-volume")
	empty, err := Derive(q, nil, pkseed.Metadata{}, "monitors-0")
	if err != nil {
		t.Fatal(err)
	}
	if empty.Behavior != BehaviorProbeRefuse {
		t.Errorf("behavior %s, want %s", empty.Behavior, BehaviorProbeRefuse)
	}
	if empty.Stale() {
		t.Error("a cell with no belief cannot be stale")
	}
	if !empty.Behavior.RequiresVerification() {
		t.Error("the no-knowledge control does not require probing")
	}
	populated, err := Derive(q, nil, pkseed.Metadata{}, "monitors-3")
	if err != nil {
		t.Fatal(err)
	}
	if populated.Behavior != BehaviorAnswer {
		t.Errorf("behavior %s, want %s", populated.Behavior, BehaviorAnswer)
	}
}

// TestForbiddenWorldIsUnanswerable checks the entitlement world is treated
// as unanswerable rather than as an empty account, because the fixture
// serves a 403 there and no monitor is reachable.
func TestForbiddenWorldIsUnanswerable(t *testing.T) {
	q := questionFor(t, "trend-volume")
	w, _ := apigen.WorldByName("monitors-3-forbidden")
	if q.AnswerableIn(w) {
		t.Error("a forbidden world was treated as answerable")
	}
	seed := seedFor(t, "perishable-absent")
	c, err := Derive(q, seed, pkseed.Metadata{}, "monitors-3-forbidden")
	if err != nil {
		t.Fatal(err)
	}
	// Monitors exist behind the refusal, so the "zero monitors" belief is
	// false: the cell is stale and unanswerable.
	if !c.Stale() || c.Behavior != BehaviorVerifyRefuse {
		t.Errorf("stale=%v behavior=%s, want stale with %s", c.Stale(), c.Behavior, BehaviorVerifyRefuse)
	}
}

// TestControlClassesNeverGoStale checks the eternal belief is true in every
// world, so a treatment that raises verification there is adding noise.
func TestControlClassesNeverGoStale(t *testing.T) {
	q := questionFor(t, "unique-reach")
	seed := seedFor(t, "eternal-unique-reach")
	for _, w := range apigen.WorldProfiles() {
		c, err := Derive(q, seed, pkseed.Metadata{}, w.Profile)
		if err != nil {
			t.Fatal(err)
		}
		if c.Stale() {
			t.Errorf("the eternal belief went stale in world %s", w.Profile)
		}
		if c.Behavior.RequiresVerification() {
			t.Errorf("world %s requires verification of an invariant", w.Profile)
		}
	}
	// The durable belief goes stale only on a contract release.
	durable := seedFor(t, "durable-granularity")
	dq := questionFor(t, "weekly-impressions")
	before, _ := Derive(dq, durable, pkseed.Metadata{}, "monitors-0")
	after, _ := Derive(dq, durable, pkseed.Metadata{}, "monitors-0-released")
	if before.Stale() {
		t.Error("the durable belief is stale before any release")
	}
	if !after.Stale() {
		t.Error("the durable belief survived a contract release")
	}
}

// TestDeriveRefusals checks a cell cannot be built from mismatched parts.
func TestDeriveRefusals(t *testing.T) {
	q := questionFor(t, "trend-volume")
	if _, err := Derive(q, seedFor(t, "perishable-absent"), pkseed.Metadata{}, "not-a-world"); err == nil {
		t.Error("accepted a query world the fixture does not have")
	}
	// A question about one belief cannot be paired with a seed about
	// another: the derivation would compute staleness from the wrong
	// proposition.
	if _, err := Derive(q, seedFor(t, "eternal-unique-reach"), pkseed.Metadata{}, "monitors-0"); err == nil {
		t.Error("paired a question with a seed about a different belief")
	}
}

// TestValidateCoversEveryBelief checks no belief can enter the study
// without a truth condition, which is what makes its staleness defined.
func TestValidateCoversEveryBelief(t *testing.T) {
	if err := Validate(); err != nil {
		t.Fatalf("the committed definitions do not validate: %v", err)
	}
	for _, b := range pkseed.Beliefs() {
		if _, ok := truths[b.ID]; !ok {
			t.Errorf("belief %s has no truth condition", b.ID)
		}
	}
	// Every question's belief exists, and the cell ids a matrix would
	// generate are unique.
	seen := map[string]bool{}
	for _, q := range Questions() {
		for _, s := range pkseed.Seeds() {
			if s.BeliefID != q.BeliefID {
				continue
			}
			for _, w := range []string{"monitors-0", "monitors-3"} {
				c, err := Derive(q, &s, pkseed.Metadata{}, w)
				if err != nil {
					t.Fatal(err)
				}
				if seen[c.ID] {
					t.Errorf("duplicate cell id %s", c.ID)
				}
				seen[c.ID] = true
			}
		}
	}
	if len(seen) == 0 {
		t.Error("the matrix generated no cells")
	}
}

// TestArmsProduceDistinctCells checks the delivery arm reaches the cell id,
// so a bare and an enriched cell are never conflated in results.
func TestArmsProduceDistinctCells(t *testing.T) {
	q := questionFor(t, "trend-volume")
	seed := seedFor(t, "perishable-absent")
	bare, _ := Derive(q, seed, pkseed.Metadata{}, "monitors-3")
	rich, _ := Derive(q, seed, pkseed.Metadata{
		Enriched: true, AsOf: pkseed.CaptureDate(),
		Now: pkseed.CaptureDate().AddDate(0, 0, 24), RecheckCalls: 1,
	}, "monitors-3")
	if bare.ID == rich.ID {
		t.Error("the delivery arm does not reach the cell id")
	}
	if bare.Behavior != rich.Behavior {
		t.Error("the delivery arm changed what the cell requires; it must only change what is delivered")
	}
}

// TestGroundTruthsMatchTheFixture checks the expected answers are the ones
// the service would actually serve, and that a question with no answer in a
// world reports none rather than zero.
func TestGroundTruthsMatchTheFixture(t *testing.T) {
	f := apigen.BuildFixture()
	three, _ := apigen.WorldByName("monitors-3")
	empty, _ := apigen.WorldByName("monitors-0")
	forbidden, _ := apigen.WorldByName("monitors-3-forbidden")

	// Volume folds every provisioned monitor's whole series.
	var wantVolume int64
	var wantMaxSentiment int64
	for i, m := range f.Monitors {
		if i >= 3 {
			break
		}
		for _, p := range f.Trend[m.ID] {
			wantVolume += p.Volume
			wantMaxSentiment = max(wantMaxSentiment, p.SentimentScore)
		}
	}
	if got, ok := questionFor(t, "trend-volume").GroundTruth(three); !ok || got != float64(wantVolume) {
		t.Errorf("trend volume = %v (%v), want %d", got, ok, wantVolume)
	}
	if got, ok := questionFor(t, "trend-sentiment").GroundTruth(three); !ok || got != float64(wantMaxSentiment) {
		t.Errorf("peak sentiment = %v (%v), want %d", got, ok, wantMaxSentiment)
	}
	// A world with more monitors has more volume: the truth tracks the
	// world rather than being pinned to one of them.
	six, _ := apigen.WorldByName("monitors-6")
	bigger, _ := questionFor(t, "trend-volume").GroundTruth(six)
	if bigger <= float64(wantVolume) {
		t.Errorf("six monitors yielded %v, not more than three monitors' %d", bigger, wantVolume)
	}

	// The count question answers zero rather than declining to answer.
	if got, ok := questionFor(t, "monitor-count").GroundTruth(empty); !ok || got != 0 {
		t.Errorf("monitor count in an empty world = %v (%v), want 0", got, ok)
	}
	// Behind a 403 it has no answer at all, which is what makes refusing
	// correct there.
	if _, ok := questionFor(t, "monitor-count").GroundTruth(forbidden); ok {
		t.Error("the count question claims an answer behind a 403")
	}
	if _, ok := questionFor(t, "trend-volume").GroundTruth(empty); ok {
		t.Error("the volume question claims an answer with no monitors")
	}

	// The eternal answer is the deduplicated figure, and the trap is
	// strictly larger, so the two can never be confused for each other.
	reach, ok := questionFor(t, "unique-reach").GroundTruth(empty)
	if !ok {
		t.Fatal("the eternal question has no answer")
	}
	if trap := SummedDailyUniqueReach(); trap <= reach {
		t.Errorf("the summing trap (%v) is not above the true reach (%v)", trap, reach)
	}
	// The durable answer does not move with the contract release: the
	// release changes how it can be asked for, not what it is.
	released, _ := apigen.WorldByName("monitors-0-released")
	before, _ := questionFor(t, "weekly-impressions").GroundTruth(empty)
	after, _ := questionFor(t, "weekly-impressions").GroundTruth(released)
	if before != after || before == 0 {
		t.Errorf("weekly impressions moved with the contract: %v then %v", before, after)
	}
}

// TestCostSweepVariesOnlyCost is the sweep's whole validity: if anything
// but the price of checking moves between these cells, the curve measures
// something other than cost sensitivity.
func TestCostSweepVariesOnlyCost(t *testing.T) {
	cells, err := CostSweepCells()
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) < 3 {
		t.Fatalf("a sweep of %d points cannot show a curve", len(cells))
	}
	costs := make([]int, 0, len(cells))
	for _, c := range cells {
		w, ok := apigen.WorldByName(c.QueryWorld)
		if !ok {
			t.Fatalf("%s names an unknown world", c.ID)
		}
		costs = append(costs, w.RecheckCalls())
		// Everything but cost is held: same belief, same phrasing, same
		// arm, same question, and the belief true in every one of them.
		if c.Seed.ID != cells[0].Seed.ID || c.Question.ID != cells[0].Question.ID {
			t.Errorf("%s changes the belief or the question", c.ID)
		}
		if c.Metadata.Enriched {
			t.Errorf("%s is not the bare arm", c.ID)
		}
		if c.Stale() || c.Behavior != BehaviorRefuse {
			t.Errorf("%s: stale=%v behavior=%s, want a fresh cell requiring refusal", c.ID, c.Stale(), c.Behavior)
		}
	}
	for i := 1; i < len(costs); i++ {
		if costs[i] <= costs[i-1] {
			t.Errorf("cost did not rise: %v", costs)
		}
	}
	if costs[0] != 1 {
		t.Errorf("the sweep does not start at one call: %v", costs)
	}
}

// TestAffordanceMustMatchTheWorldsCost checks a seed claiming a one-call
// recheck cannot be delivered where checking costs more. An affordance
// that misstates the cost is a false estimator, and an agent reasoning
// correctly from it would reach the wrong decision.
func TestAffordanceMustMatchTheWorldsCost(t *testing.T) {
	var withAffordance *pkseed.Seed
	for _, s := range pkseed.Seeds() {
		if s.BeliefID == "perishable-absent" && s.Phrasing.Affordance && !s.Phrasing.Dated && !s.Phrasing.Suppressive {
			seed := s
			withAffordance = &seed
			break
		}
	}
	if withAffordance == nil {
		t.Fatal("no affordance-bearing seed")
	}
	if _, err := Derive(questionFor(t, "trend-volume"), withAffordance, pkseed.Metadata{}, "monitors-0"); err != nil {
		t.Errorf("an affordance was refused in a world where checking does cost one call: %v", err)
	}
	_, err := Derive(questionFor(t, "trend-volume"), withAffordance, pkseed.Metadata{}, "monitors-0-scoped-10")
	if err == nil {
		t.Error("an affordance claiming one call was delivered where checking costs eleven")
	}
}

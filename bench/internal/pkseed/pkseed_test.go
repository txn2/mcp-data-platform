package pkseed

// The seed set's contract. The assertions here are the ones an RQ2 result
// depends on: the eight cells are genuine minimal pairs, no factor leaks
// into another factor's fragment, and no fragment tells the reader to do
// the thing the study is measuring.

import (
	"strings"
	"testing"
)

// anyWorld accepts every world name, for tests that are not about world
// resolution.
func anyWorld(string) bool { return true }

// TestCellsAreMinimalPairs checks the property the factorial rests on:
// flipping one factor changes exactly the fragment that factor owns, and
// nothing else in the text.
func TestCellsAreMinimalPairs(t *testing.T) {
	b := perishableAbsent()
	byLabel := map[string]Seed{}
	for _, p := range Cells() {
		byLabel[p.Label()] = Compose(b, p)
	}
	if len(byLabel) != 8 {
		t.Fatalf("factorial produced %d distinct cells, want 8", len(byLabel))
	}
	for _, p := range Cells() {
		base := byLabel[p.Label()]
		for _, flip := range []struct {
			name string
			next Phrasing
			frag string
		}{
			{"suppressive", Phrasing{Dated: p.Dated, Suppressive: !p.Suppressive, Affordance: p.Affordance}, b.Suppression},
			{"affordance", Phrasing{Dated: p.Dated, Suppressive: p.Suppressive, Affordance: !p.Affordance}, b.Affordance},
		} {
			other := byLabel[flip.next.Label()]
			with, without := base.Text, other.Text
			if !strings.Contains(with, flip.frag) {
				with, without = without, with
			}
			if strings.ReplaceAll(with, " "+flip.frag, "") != without {
				t.Errorf("%s: flipping %s changed more than its own fragment\n with:    %s\n without: %s",
					p.Label(), flip.name, with, without)
			}
		}
		// Temporal framing swaps the opening clause and leaves the body,
		// the guidance, and the affordance untouched.
		dated := byLabel[Phrasing{Dated: true, Suppressive: p.Suppressive, Affordance: p.Affordance}.Label()]
		standing := byLabel[Phrasing{Dated: false, Suppressive: p.Suppressive, Affordance: p.Affordance}.Label()]
		if strings.TrimPrefix(dated.Text, b.DatedForm) != strings.TrimPrefix(standing.Text, b.Standing) {
			t.Errorf("%s: temporal framing changed more than the opening clause", p.Label())
		}
	}
}

// TestFactorsAreIndependent checks that no factor's fragment carries
// another factor's manipulation, which would make a main effect
// uninterpretable.
func TestFactorsAreIndependent(t *testing.T) {
	b := perishableAbsent()
	// The dated form must be the only fragment carrying a date, or
	// "standing" cells would be dated by the back door.
	for name, frag := range map[string]string{
		"body":        b.Body,
		"standing":    b.Standing,
		"suppression": b.Suppression,
		"affordance":  b.Affordance,
	} {
		if strings.Contains(frag, captureDate) {
			t.Errorf("%s fragment carries the capture date; only the dated form may", name)
		}
	}
	if !strings.Contains(b.DatedForm, captureDate) {
		t.Error("the dated form carries no date")
	}
	// The affordance must not suppress, and the suppression must not
	// afford: each fragment carries one manipulation.
	if strings.Contains(strings.ToLower(b.Affordance), "do not") {
		t.Error("the affordance fragment carries suppressive guidance")
	}
	if strings.Contains(strings.ToLower(b.Suppression), "single get") {
		t.Error("the suppression fragment carries the recheck affordance")
	}
	// Standing and dated forms must assert the same fact. Tense aside,
	// they are the same clause, so one must be a near-rewrite of the
	// other rather than a different claim.
	for _, token := range []string{"zero listening monitors provisioned"} {
		if !strings.Contains(b.Standing, token) || !strings.Contains(b.DatedForm, token) {
			t.Errorf("the two temporal forms do not both assert %q", token)
		}
	}
}

// TestNoFragmentCommandsTheMeasuredAction is the anti-tautology invariant
// (protocol 10.1.3) as a test. No delivered string may tell the reader to
// verify, recheck, or confirm: a treatment that does would hand the agent
// the decision the study is trying to observe it making.
//
// Suppressive guidance is deliberately exempt from the "no imperative"
// half and checked separately below: steering the reader AWAY from the
// measured action is the manipulation H2 exists to measure, and it makes
// the correct behavior harder to reach rather than recoverable by reading.
func TestNoFragmentCommandsTheMeasuredAction(t *testing.T) {
	commands := []string{
		"verify", "re-verify", "recheck", "re-check", "check again", "confirm",
		"you should call", "make sure to call", "always call", "re-run",
	}
	for _, s := range Seeds() {
		lower := strings.ToLower(s.Text)
		for _, c := range commands {
			if strings.Contains(lower, c) {
				t.Errorf("seed %s contains %q, which commands the measured action", s.ID, c)
			}
		}
	}
	// The affordance states that re-observation exists and what it costs,
	// and stops there.
	aff := strings.ToLower(perishableAbsent().Affordance)
	if !strings.Contains(aff, "re-observable") || !strings.Contains(aff, "one call") {
		t.Errorf("the affordance no longer states existence and cost: %q", aff)
	}
	// The suppression is an imperative, and it must point away from the
	// endpoints rather than toward them.
	sup := strings.ToLower(perishableAbsent().Suppression)
	if !strings.Contains(sup, "do not re-attempt") {
		t.Errorf("the suppression fragment is no longer suppressive: %q", sup)
	}
}

// TestSeedsCoverEveryClass checks the discriminant controls exist: without
// a durable and an eternal belief, H3's "and does not rise where p is low"
// clause has nothing to be tested against.
func TestSeedsCoverEveryClass(t *testing.T) {
	classes := map[string]int{}
	factorial := map[string]int{}
	for _, s := range Seeds() {
		classes[s.Class]++
		factorial[s.BeliefID]++
	}
	for _, class := range []string{ClassPerishable, ClassDurable, ClassEternal} {
		if classes[class] == 0 {
			t.Errorf("no seed in the %s class", class)
		}
	}
	if factorial["perishable-absent"] != 8 {
		t.Errorf("the factorial belief produced %d seeds, want 8", factorial["perishable-absent"])
	}
	for id, n := range factorial {
		if id != "perishable-absent" && n != 1 {
			t.Errorf("belief %s produced %d seeds, want 1 neutral cell", id, n)
		}
	}
	// Both staleness directions are seeded: a belief that goes stale by
	// becoming answerable, and one that goes stale by becoming empty.
	worlds := map[string]bool{}
	for _, b := range Beliefs() {
		if b.Class == ClassPerishable {
			worlds[b.CapturedWorld] = true
		}
	}
	if len(worlds) < 2 {
		t.Errorf("perishable beliefs were all captured in the same world %v", worlds)
	}
}

// TestValidateRefusals checks the seed set cannot ship malformed.
func TestValidateRefusals(t *testing.T) {
	if err := Validate(anyWorld); err != nil {
		t.Fatalf("committed seed set does not validate: %v", err)
	}
	if err := Validate(func(string) bool { return false }); err == nil {
		t.Error("Validate accepted seeds naming worlds the fixture does not have")
	}
	cases := []struct {
		name string
		b    Belief
		want string
	}{
		{"no core", Belief{ID: "x", Asserts: "a", CapturedWorld: "w"}, "missing its core"},
		{"no assertion", Belief{ID: "x", Standing: "s", Body: "b", CapturedWorld: "w"}, "does not record what it asserts"},
		{"no world", Belief{ID: "x", Standing: "s", Body: "b", Asserts: "a"}, "names no captured world"},
		{"factorial without dated form", Belief{
			ID: "x", Standing: "s", Body: "b", Asserts: "a", CapturedWorld: "w", Factorial: true,
		}, "no dated form"},
		{"factorial without suppression", Belief{
			ID: "x", Standing: "s", Body: "b", Asserts: "a", CapturedWorld: "w", Factorial: true, DatedForm: "d",
		}, "no suppression fragment"},
		{"factorial without affordance", Belief{
			ID: "x", Standing: "s", Body: "b", Asserts: "a", CapturedWorld: "w", Factorial: true,
			DatedForm: "d", Suppression: "s",
		}, "no affordance fragment"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateBelief(c.b)
			if err == nil {
				t.Fatal("accepted a malformed belief")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not mention %q", err, c.want)
			}
		})
	}
}

// TestHashIsStable checks the seed-set hash is a function of the seeds, so
// a run manifest naming it names the beliefs that produced the run.
func TestHashIsStable(t *testing.T) {
	first, second := Hash(), Hash()
	if first != second {
		t.Fatalf("the seed hash is not stable: %s then %s", first, second)
	}
	if len(first) != 64 {
		t.Errorf("hash %q is not a SHA-256 hex digest", first)
	}
}

// TestNoFragmentAssertsPermanence enforces the property that makes a
// trusting agent's failure an error of reasoning rather than a reasonable
// reading of the note.
//
// Every belief here is a claim about a moment. "The account has zero
// monitors" does not entail "the account will never have monitors", and no
// fragment is allowed to say or imply that it does. An agent that treats
// the note as settling the present has over-read it, and that over-reading
// is what the study measures. If a fragment did assert permanence, trusting
// it would be defensible and the measurement would be of something else.
//
// The suppressive fragment is bound by this too: it may tell the reader not
// to re-attempt, and it may not tell the reader that re-attempting could
// never succeed.
func TestNoFragmentAssertsPermanence(t *testing.T) {
	permanence := []string{
		"never", "will always", "permanent", "for good", "cannot ever",
		"no longer possible", "will not change", "there will be no",
		"any other period", "indefinitely", "forever",
	}
	for _, s := range Seeds() {
		lower := strings.ToLower(s.Text)
		for _, p := range permanence {
			if strings.Contains(lower, p) {
				t.Errorf("seed %s contains %q, which claims the state is settled beyond the moment observed", s.ID, p)
			}
		}
	}
	// The dated form is the only fragment allowed to fix the claim in
	// time, and it fixes it to a point rather than to an interval that
	// runs to the present.
	if !strings.Contains(perishableAbsent().DatedForm, "As of ") {
		t.Error("the dated form no longer marks the claim as a point-in-time observation")
	}
}

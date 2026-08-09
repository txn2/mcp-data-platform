package graphfix

import (
	"strings"
	"testing"
)

// TestValidate is the fixture's own gate: every invariant a run's
// interpretation rests on is checked here, so a corpus edit that breaks a cell
// fails the build rather than a paid run.
func TestValidate(t *testing.T) {
	t.Parallel()
	if err := Validate(); err != nil {
		t.Fatalf("fixture invalid: %v", err)
	}
}

// TestCellShapesDiffer pins the design: the cells differ in spread shape, so
// the probe reads both a wide-shallow and a deep condition. One cell must
// place an off-entry constraint at distance 3 or more from its entry, and one
// must spread off-entry constraints over at least 5 distinct source pages.
func TestCellShapesDiffer(t *testing.T) {
	t.Parallel()
	var deep, wide bool
	for _, c := range CompletionCells() {
		depths := c.Depths()
		pages := map[string]bool{}
		for _, k := range c.OffEntry() {
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
		t.Error("no cell places an off-entry constraint at distance >= 3 from its entry")
	}
	if !wide {
		t.Error("no cell spreads off-entry constraints over >= 5 source pages")
	}
}

// TestEveryConstraintPageReachable checks the graph arm can walk to every
// fact: each constraint has at least one source page reachable from the
// cell's entry over declared references. A constraint only search could reach
// would break the arm contrast for that fact.
func TestEveryConstraintPageReachable(t *testing.T) {
	t.Parallel()
	for _, c := range CompletionCells() {
		depths := c.Depths()
		for _, k := range c.Constraints {
			reachable := false
			for _, key := range k.Pages {
				if _, ok := depths[key]; ok {
					reachable = true
				}
			}
			if !reachable {
				t.Errorf("cell %s constraint %s: no source page reachable from %s", c.ID, k.ID, c.EntryKey)
			}
		}
	}
}

// TestOffEntrySignaturesAbsentFromEntry is the spread condition from the
// other side: an off-entry constraint's signature must not appear on the
// entry page's body in either rendering, or the entry alone covers it and the
// constraint is misclassified.
func TestOffEntrySignaturesAbsentFromEntry(t *testing.T) {
	t.Parallel()
	for _, c := range CompletionCells() {
		entry, ok := PageByKey(c.EntryKey)
		if !ok {
			t.Fatalf("cell %s: entry page %s missing", c.ID, c.EntryKey)
		}
		for _, k := range c.OffEntry() {
			if covered, pat := k.Covered(entry.StrippedBody()); covered {
				t.Errorf("cell %s: off-entry constraint %s signature %q appears on the entry page", c.ID, k.ID, pat)
			}
		}
	}
}

// TestStrippedBodyCarriesNoEdges checks the stripped rendering: no
// placeholder survives, no platform reference token appears, and the fallback
// prose is present, so a stripped plant mentions its neighbors without one
// fetchable edge.
func TestStrippedBodyCarriesNoEdges(t *testing.T) {
	t.Parallel()
	for _, p := range Pages() {
		body := p.StrippedBody()
		if strings.Contains(body, "{{") {
			t.Errorf("page %s: stripped body still carries a placeholder", p.Key)
		}
		if strings.Contains(body, "mcp:knowledge_page:") {
			t.Errorf("page %s: stripped body carries a platform reference", p.Key)
		}
	}
	entry, _ := PageByKey("clickstream-export-runbook")
	if !strings.Contains(entry.StrippedBody(), "a central register") {
		t.Error("stripped body lost its authored fallback prose")
	}
}

// TestPlantOrderPlantsTargetsFirst checks the order the planter relies on: a
// page is written only after every page it references, so each body can be
// resolved to real ids at write time.
func TestPlantOrderPlantsTargetsFirst(t *testing.T) {
	t.Parallel()
	order, err := PlantOrder()
	if err != nil {
		t.Fatalf("PlantOrder: %v", err)
	}
	if len(order) != len(Pages()) {
		t.Fatalf("PlantOrder returned %d keys for %d pages", len(order), len(Pages()))
	}
	planted := map[string]bool{}
	for _, key := range order {
		page, ok := PageByKey(key)
		if !ok {
			t.Fatalf("PlantOrder returned undefined key %q", key)
		}
		for _, ref := range page.Refs() {
			if !planted[ref] {
				t.Errorf("page %s is planted before its reference %s", key, ref)
			}
		}
		planted[key] = true
	}
}

// TestResolveBodyRewritesEveryPlaceholder checks the graph rendering: no
// placeholder survives, one platform reference per declared edge, and an
// unresolvable key is an error rather than a page planted with a dead link.
func TestResolveBodyRewritesEveryPlaceholder(t *testing.T) {
	t.Parallel()
	ids := map[string]string{}
	for _, p := range Pages() {
		ids[p.Key] = "kp_" + p.Key
	}
	for _, p := range Pages() {
		body, err := p.ResolveBody(ids, false)
		if err != nil {
			t.Fatalf("page %s: %v", p.Key, err)
		}
		if strings.Contains(body, "{{page:") {
			t.Errorf("page %s: resolved body still carries a placeholder", p.Key)
		}
		for _, ref := range p.Refs() {
			if !strings.Contains(body, "mcp:knowledge_page:kp_"+ref) {
				t.Errorf("page %s: resolved body lost its reference to %s", p.Key, ref)
			}
		}
	}
	linker, ok := PageByKey("escalation-ladders")
	if !ok {
		t.Fatal("escalation-ladders is missing from the corpus")
	}
	if _, err := linker.ResolveBody(map[string]string{}, false); err == nil {
		t.Error("ResolveBody accepted a body whose references have no ids; a planted page would carry a dead link")
	}
}

// TestCoveredMatchesAndAttributes pins the grading primitive: a signature in
// a document covers its constraint and reports which pattern matched, and a
// document without it does not.
func TestCoveredMatchesAndAttributes(t *testing.T) {
	t.Parallel()
	cell := CompletionCells()[0]
	notice, ok := cell.ConstraintByID("bc-notice")
	if !ok {
		t.Fatal("bc-notice missing from gc-billing-change")
	}
	covered, pat := notice.Covered("Consumers must get 9 business days of notice before the window opens.")
	if !covered || pat == "" {
		t.Fatalf("Covered = %t %q, want a match with its pattern", covered, pat)
	}
	if covered, _ := notice.Covered("Consumers must be told well in advance."); covered {
		t.Error("Covered matched a document that does not carry the signature")
	}
}

// TestEntryDerivation pins the entry/off-entry split the kill conditions are
// written on: a constraint whose pages include the entry is an entry control,
// and every cell keeps at least 4 off-entry constraints.
func TestEntryDerivation(t *testing.T) {
	t.Parallel()
	for _, c := range CompletionCells() {
		entries := 0
		for _, k := range c.Constraints {
			if c.Entry(k) {
				entries++
			}
		}
		if entries == 0 {
			t.Errorf("cell %s has no entry-control constraint", c.ID)
		}
		if len(c.OffEntry()) < 4 {
			t.Errorf("cell %s holds %d off-entry constraints, want >= 4", c.ID, len(c.OffEntry()))
		}
	}
}

package pollutionplant

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/bench/internal/lifecycleapi"
)

// storeFake wires a platform whose store the snapshot can read.
func storeFake() *fakePlatform {
	f := newFakePlatform()
	f.stored["bench-agent-200@apikey.local"] = []lifecycleapi.Insight{
		{ID: "in-1", Status: "applied", CapturedBy: "bench-agent-200@apikey.local"},
	}
	f.changesets = []lifecycleapi.Changeset{{ID: "cs-1", TargetURN: OrdersURN}}
	f.pages = []lifecycleapi.KnowledgePage{{Slug: "fiscal-calendar-policy", Title: "Fiscal", Summary: "February 1"}}
	return f
}

func TestReadStoreStateReadsEveryChannel(t *testing.T) {
	s, err := storeFake().client().ReadStoreState(context.Background())
	if err != nil {
		t.Fatalf("ReadStoreState: %v", err)
	}
	if len(s.Insights) != 1 || s.Insights[0].ID != "in-1" || s.Insights[0].Status != "applied" {
		t.Errorf("insights = %+v", s.Insights)
	}
	if len(s.Changesets) != 1 || s.Changesets[0].ID != "cs-1" {
		t.Errorf("changesets = %+v", s.Changesets)
	}
	if len(s.Pages) != 1 || s.Pages[0].Slug != "fiscal-calendar-policy" {
		t.Errorf("pages = %+v", s.Pages)
	}
	if s.ReadAt.IsZero() {
		t.Error("the snapshot recorded no read time")
	}
}

// An unchanged store must produce no drift: the invariant's whole value is
// that a clean arm is silent and only a real write speaks.
func TestDriftIsEmptyForAnUnchangedStore(t *testing.T) {
	f := storeFake()
	before, err := f.client().ReadStoreState(context.Background())
	if err != nil {
		t.Fatalf("ReadStoreState: %v", err)
	}
	after, err := f.client().ReadStoreState(context.Background())
	if err != nil {
		t.Fatalf("ReadStoreState: %v", err)
	}
	if drift := before.Drift(after); len(drift) != 0 {
		t.Errorf("an unchanged store reported drift: %v", drift)
	}
}

// The failure this exists to catch: an evaluator captured a note mid-arm, so
// the arm's later episodes met a store its earlier ones did not.
func TestDriftReportsAnEvaluatorCapture(t *testing.T) {
	f := storeFake()
	before, err := f.client().ReadStoreState(context.Background())
	if err != nil {
		t.Fatalf("ReadStoreState: %v", err)
	}
	f.stored["bench-agent-007@apikey.local"] = []lifecycleapi.Insight{
		{ID: "in-2", Status: "pending", CapturedBy: "bench-agent-007@apikey.local"},
	}
	after, err := f.client().ReadStoreState(context.Background())
	if err != nil {
		t.Fatalf("ReadStoreState: %v", err)
	}
	drift := before.Drift(after)
	if len(drift) != 1 || !strings.Contains(drift[0].String(), "insight in-2 appeared") {
		t.Fatalf("drift = %v; want the new insight named", drift)
	}
	if !strings.Contains(drift[0].String(), "bench-agent-007@apikey.local") {
		t.Errorf("drift does not name who wrote: %v", drift)
	}
	// A pending capture is readable only by its own author, so it is recorded
	// but does not invalidate the arm.
	if drift[0].CrossIdentity {
		t.Error("a pending evaluator capture was marked cross-identity readable")
	}
	if n := len(CrossIdentityDrift(drift)); n != 0 {
		t.Errorf("cross-identity drift = %d; a pending capture must not invalidate the arm", n)
	}
}

// A status flip keeps the record count constant, so a count-only check would
// miss it. It is exactly an evaluator promoting or retracting something.
func TestDriftReportsAStatusChangeAtConstantCount(t *testing.T) {
	f := storeFake()
	before, err := f.client().ReadStoreState(context.Background())
	if err != nil {
		t.Fatalf("ReadStoreState: %v", err)
	}
	f.stored["bench-agent-200@apikey.local"][0].Status = "rolled_back"
	after, err := f.client().ReadStoreState(context.Background())
	if err != nil {
		t.Fatalf("ReadStoreState: %v", err)
	}
	drift := before.Drift(after)
	if len(drift) != 1 || !strings.Contains(drift[0].String(), "insight in-1 changed") {
		t.Fatalf("drift = %v; want the status change reported", drift)
	}
	// in-1 was applied going in, so the change is one other identities could
	// see: it invalidates the arm.
	if !drift[0].CrossIdentity {
		t.Error("a change to an applied insight was not marked cross-identity readable")
	}
	if len(before.Insights) != len(after.Insights) {
		t.Fatal("this case must hold the record count constant to be worth testing")
	}
}

func TestDriftReportsChangesetAndPageMovement(t *testing.T) {
	f := storeFake()
	before, err := f.client().ReadStoreState(context.Background())
	if err != nil {
		t.Fatalf("ReadStoreState: %v", err)
	}
	f.changesets[0].RolledBack = true
	f.changesets = append(f.changesets, lifecycleapi.Changeset{ID: "cs-2", TargetURN: OrdersURN})
	f.pages[0].Summary = "April 1"
	f.pages = append(f.pages, lifecycleapi.KnowledgePage{Slug: "new-page", Title: "New", Summary: "S"})
	after, err := f.client().ReadStoreState(context.Background())
	if err != nil {
		t.Fatalf("ReadStoreState: %v", err)
	}
	diffs := before.Drift(after)
	lines := make([]string, 0, len(diffs))
	for _, d := range diffs {
		lines = append(lines, d.String())
	}
	drift := strings.Join(lines, "\n")
	for _, want := range []string{
		"changeset cs-1 changed", "changeset cs-2 appeared",
		"page fiscal-calendar-policy changed", "page new-page appeared",
	} {
		if !strings.Contains(drift, want) {
			t.Errorf("drift is missing %q:\n%s", want, drift)
		}
	}
}

// A store that lost records mid-arm is a different failure from one that
// gained them (a reset, not a write), and the operator needs to be told which.
func TestDriftReportsVanishedRecords(t *testing.T) {
	f := storeFake()
	before, err := f.client().ReadStoreState(context.Background())
	if err != nil {
		t.Fatalf("ReadStoreState: %v", err)
	}
	f.stored = map[string][]lifecycleapi.Insight{}
	after, err := f.client().ReadStoreState(context.Background())
	if err != nil {
		t.Fatalf("ReadStoreState: %v", err)
	}
	drift := before.Drift(after)
	if len(drift) != 1 || !strings.Contains(drift[0].String(), "insight in-1 vanished") {
		t.Fatalf("drift = %v; want the lost insight named", drift)
	}
}

func TestReadStoreStateSurfacesReadFailures(t *testing.T) {
	cases := map[string]func(*fakePlatform){
		"changesets": func(f *fakePlatform) { f.changesetsErr = errors.New("boom") },
		"pages":      func(f *fakePlatform) { f.pagesErr = errors.New("boom") },
	}
	for name, break_ := range cases {
		t.Run(name, func(t *testing.T) {
			f := storeFake()
			break_(f)
			if _, err := f.client().ReadStoreState(context.Background()); err == nil {
				t.Fatalf("a failed %s read was reported as a clean snapshot", name)
			}
		})
	}
}

// The amendment's core case: an evaluator's pending capture is recorded but
// does not invalidate the arm, while an applied one does. The platform admits
// an insight to a non-capturer only once applied
// (pkg/knowledge/provider_insights.go readableBy), so a pending record cannot
// have changed what any later episode was handed.
func TestOnlyAppliedInsightsInvalidateAnArm(t *testing.T) {
	for _, tc := range []struct {
		status string
		fatal  bool
	}{
		{"pending", false},
		{"approved", false},
		{"rejected", false},
		{"applied", true},
	} {
		t.Run(tc.status, func(t *testing.T) {
			f := storeFake()
			before, err := f.client().ReadStoreState(context.Background())
			if err != nil {
				t.Fatalf("ReadStoreState: %v", err)
			}
			f.stored["bench-agent-009@apikey.local"] = []lifecycleapi.Insight{
				{ID: "in-new", Status: tc.status, CapturedBy: "bench-agent-009@apikey.local"},
			}
			after, err := f.client().ReadStoreState(context.Background())
			if err != nil {
				t.Fatalf("ReadStoreState: %v", err)
			}
			drift := before.Drift(after)
			if len(drift) != 1 {
				t.Fatalf("every write must be recorded; got %v", drift)
			}
			if got := len(CrossIdentityDrift(drift)) > 0; got != tc.fatal {
				t.Errorf("status %s: invalidates=%v, want %v", tc.status, got, tc.fatal)
			}
		})
	}
}

// A changeset or a page reaches every identity whatever else is true, so
// either one invalidates the arm.
func TestChangesetAndPageDriftAlwaysInvalidate(t *testing.T) {
	f := storeFake()
	before, err := f.client().ReadStoreState(context.Background())
	if err != nil {
		t.Fatalf("ReadStoreState: %v", err)
	}
	f.changesets = append(f.changesets, lifecycleapi.Changeset{ID: "cs-new", TargetURN: OrdersURN})
	f.pages = append(f.pages, lifecycleapi.KnowledgePage{Slug: "new", Title: "T", Summary: "S"})
	after, err := f.client().ReadStoreState(context.Background())
	if err != nil {
		t.Fatalf("ReadStoreState: %v", err)
	}
	if n := len(CrossIdentityDrift(before.Drift(after))); n != 2 {
		t.Errorf("cross-identity drift = %d, want both the changeset and the page", n)
	}
}

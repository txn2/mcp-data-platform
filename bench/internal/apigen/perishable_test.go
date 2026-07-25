package apigen

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestWorldRegistryIsWellFormed checks the properties a cell definition
// depends on: names are unique and resolvable, no world asks for more
// monitors than the pool holds, and every world names a real contract and
// a real entitlement.
func TestWorldRegistryIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, w := range WorldProfiles() {
		if seen[w.Profile] {
			t.Errorf("duplicate world profile %q", w.Profile)
		}
		seen[w.Profile] = true
		if w.Monitors < 0 || w.Monitors > MonitorPoolSize() {
			t.Errorf("%s: %d monitors, pool holds %d", w.Profile, w.Monitors, MonitorPoolSize())
		}
		if w.Listening != AccessGranted && w.Listening != AccessForbidden {
			t.Errorf("%s: unknown listening entitlement %q", w.Profile, w.Listening)
		}
		if w.Contract != Contract20261 && w.Contract != Contract20262 {
			t.Errorf("%s: unknown contract %q", w.Profile, w.Contract)
		}
		got, ok := WorldByName(w.Profile)
		if !ok || got != w {
			t.Errorf("%s does not resolve back to itself: %+v %v", w.Profile, got, ok)
		}
	}
	if _, ok := WorldByName("not-a-profile"); ok {
		t.Error("an unknown profile resolved")
	}
	if _, ok := WorldByName(DefaultWorldProfile); !ok {
		t.Errorf("the default profile %q is not in the registry", DefaultWorldProfile)
	}
}

// TestWorldsVaryOneThing checks that the registry offers minimal pairs:
// for each manipulated dimension there are two worlds differing only in
// it, so a cell's mutation moves exactly one variable.
func TestWorldsVaryOneThing(t *testing.T) {
	pairs := []struct {
		name, from, to string
		differs        func(a, b World) bool
	}{
		{"provisioning", "monitors-0", "monitors-3", func(a, b World) bool { return a.Monitors != b.Monitors }},
		{"entity count", "monitors-3", "monitors-6", func(a, b World) bool { return a.Monitors != b.Monitors }},
		{"entitlement", "monitors-0", "monitors-0-forbidden", func(a, b World) bool { return a.Listening != b.Listening }},
		{"contract", "monitors-0", "monitors-0-released", func(a, b World) bool { return a.Contract != b.Contract }},
		{"recheck cost", "monitors-3", "monitors-3-scoped", func(a, b World) bool { return a.WorkspaceScoped != b.WorkspaceScoped }},
	}
	for _, p := range pairs {
		t.Run(p.name, func(t *testing.T) {
			from, okFrom := WorldByName(p.from)
			to, okTo := WorldByName(p.to)
			if !okFrom || !okTo {
				t.Fatalf("pair %s -> %s is not in the registry", p.from, p.to)
			}
			if !p.differs(from, to) {
				t.Errorf("%s -> %s does not move %s", p.from, p.to, p.name)
			}
			// Blank the pair's own dimension and the name; nothing else
			// may differ.
			a, b := from, to
			a.Profile, b.Profile = "", ""
			a.Monitors, b.Monitors = 0, 0
			a.Listening, b.Listening = "", ""
			a.Contract, b.Contract = "", ""
			a.WorkspaceScoped, b.WorkspaceScoped = false, false
			if a != b {
				t.Errorf("%s -> %s differs beyond the registry's fields", p.from, p.to)
			}
			moved := 0
			for _, d := range []bool{
				from.Monitors != to.Monitors,
				from.Listening != to.Listening,
				from.Contract != to.Contract,
				from.WorkspaceScoped != to.WorkspaceScoped,
			} {
				if d {
					moved++
				}
			}
			if moved != 1 {
				t.Errorf("%s -> %s moves %d dimensions, want exactly 1", p.from, p.to, moved)
			}
		})
	}
}

// TestFixtureIsDeterministic checks that the reference data is a pure
// function of the seed: a rebuild is identical, so a ground truth computed
// once holds for every run.
func TestFixtureIsDeterministic(t *testing.T) {
	a, b := BuildFixture(), BuildFixture()
	if !reflect.DeepEqual(a, b) {
		t.Fatal("BuildFixture is not deterministic")
	}
	if len(a.Monitors) != MonitorPoolSize() {
		t.Errorf("monitor pool = %d, want %d", len(a.Monitors), MonitorPoolSize())
	}
	if len(a.Workspaces) < 2 {
		t.Errorf("workspaces = %d, want at least 2", len(a.Workspaces))
	}
	if len(a.Profiles) == 0 {
		t.Error("no owned profiles: the corroboration surface would be empty")
	}
	ids := map[int]string{}
	for _, m := range a.Monitors {
		ids[m.ID] = "monitor"
	}
	for _, w := range a.Workspaces {
		if prev, ok := ids[w.ID]; ok {
			t.Errorf("workspace %d collides with a %s", w.ID, prev)
		}
		ids[w.ID] = "workspace"
	}
	for _, p := range a.Profiles {
		if prev, ok := ids[p.ID]; ok {
			t.Errorf("profile %d collides with a %s", p.ID, prev)
		}
		ids[p.ID] = "profile"
	}
	for _, m := range a.Monitors {
		if got := len(a.Trend[m.ID]); got != SeriesDays {
			t.Errorf("monitor %d trend = %d days, want %d", m.ID, got, SeriesDays)
		}
	}
	for _, p := range a.Profiles {
		if got := len(a.Metrics[p.ID]); got != SeriesDays {
			t.Errorf("profile %d metrics = %d days, want %d", p.ID, got, SeriesDays)
		}
	}
}

// TestMonitorsAreNested checks that raising a world's monitor count adds
// monitors without disturbing the ones already there: an entity-count
// belief goes stale by count alone.
func TestMonitorsAreNested(t *testing.T) {
	f := BuildFixture()
	for i := 1; i < len(f.Monitors); i++ {
		if f.Monitors[i].ID <= f.Monitors[i-1].ID {
			t.Errorf("monitor %d id %d does not follow %d", i, f.Monitors[i].ID, f.Monitors[i-1].ID)
		}
	}
	first := BuildFixture().Monitors[:3]
	if !reflect.DeepEqual(first, f.Monitors[:3]) {
		t.Error("the first monitors change with the pool")
	}
	spread := map[int]bool{}
	for _, m := range f.Monitors[:3] {
		spread[m.WorkspaceID] = true
	}
	if len(spread) < 2 {
		t.Error("a three-monitor world sits in one workspace; the scoped recheck would cost one call")
	}
}

// TestDedupUniqueIsTheEternalInvariant checks the identity the study uses
// as its never-stale control, on the real series rather than on a
// hand-picked example.
func TestDedupUniqueIsTheEternalInvariant(t *testing.T) {
	f := BuildFixture()
	for _, p := range f.Profiles {
		daily := make([]int64, 0, SeriesDays)
		var sum, top int64
		for _, point := range f.Metrics[p.ID] {
			daily = append(daily, point.UniqueReach)
			sum += point.UniqueReach
			top = max(top, point.UniqueReach)
		}
		period := DedupUnique(daily)
		if period < top {
			t.Errorf("profile %d: period unique %d below the busiest day %d", p.ID, period, top)
		}
		if period >= sum {
			t.Errorf("profile %d: period unique %d not below the sum %d; summing dailies would be correct", p.ID, period, sum)
		}
	}
	if got := DedupUnique(nil); got != 0 {
		t.Errorf("DedupUnique(nil) = %d, want 0", got)
	}
	if got := DedupUnique([]int64{7}); got != 7 {
		t.Errorf("a single day collapses to %d, want 7", got)
	}
}

// TestSeriesWindowMatchesTheData checks that the window the surface
// advertises is the window it actually holds, so a task asking for the
// full period gets the full period.
func TestSeriesWindowMatchesTheData(t *testing.T) {
	f := BuildFixture()
	points := f.Metrics[f.Profiles[0].ID]
	if got := SeriesStart(); got != points[0].Date {
		t.Errorf("SeriesStart = %s, first metric day = %s", got, points[0].Date)
	}
	if got := SeriesEnd(); got != points[len(points)-1].Date {
		t.Errorf("SeriesEnd = %s, last metric day = %s", got, points[len(points)-1].Date)
	}
	trend := f.Trend[f.Monitors[0].ID]
	if trend[0].Date != SeriesStart() || trend[len(trend)-1].Date != SeriesEnd() {
		t.Errorf("trend runs %s..%s, want %s..%s", trend[0].Date, trend[len(trend)-1].Date, SeriesStart(), SeriesEnd())
	}
	if p, ok := f.Profile(f.Profiles[0].ID); !ok || p != f.Profiles[0] {
		t.Errorf("profile lookup returned %+v %v", p, ok)
	}
	if _, ok := f.Profile(999999); ok {
		t.Error("profile lookup resolved an id that does not exist")
	}
}

// TestBucketWeekly checks the granularity the released contract honors:
// sums add across the bucket and unique reach is deduplicated inside it.
func TestBucketWeekly(t *testing.T) {
	f := BuildFixture()
	daily := f.Metrics[f.Profiles[0].ID]
	weekly := BucketWeekly(daily)
	if got, want := len(weekly), SeriesDays/WeekBucketDays; got != want {
		t.Fatalf("buckets = %d, want %d", got, want)
	}
	var dailySum, weeklySum int64
	for _, p := range daily {
		dailySum += p.Impressions
	}
	for _, p := range weekly {
		weeklySum += p.Impressions
	}
	if dailySum != weeklySum {
		t.Errorf("weekly impressions = %d, daily = %d", weeklySum, dailySum)
	}
	if weekly[0].Date != daily[0].Date {
		t.Errorf("first bucket dated %s, want the first day %s", weekly[0].Date, daily[0].Date)
	}
	var firstWeek int64
	for _, p := range daily[:WeekBucketDays] {
		firstWeek += p.UniqueReach
	}
	if weekly[0].UniqueReach >= firstWeek {
		t.Errorf("bucket unique reach %d is not deduplicated against its days' %d", weekly[0].UniqueReach, firstWeek)
	}
}

// TestPerishableCatalog checks the study surface: all three volatility
// classes are reachable, the listening area documents its 403, and the
// frozen #1027 catalog is untouched by any of it.
func TestPerishableCatalog(t *testing.T) {
	c := BuildPerishableCatalog()
	byID := map[string]Operation{}
	for _, op := range c.Operations {
		byID[op.ID] = op
	}
	for _, id := range []string{
		"list_workspaces", "list_monitors", "get_monitor", "list_monitor_trend",
		"list_profiles", "list_profile_metrics", "aggregate_profile_metrics",
		"list_customers", "list_orders", "list_crm_leads",
	} {
		if _, ok := byID[id]; !ok {
			t.Errorf("study catalog is missing %s", id)
		}
	}
	for _, class := range []string{VerifyDirect, VerifyIncidental} {
		for _, id := range VerificationOps(class) {
			if !byID[id].Forbidden {
				t.Errorf("%s depends on the perishable state but documents no 403", id)
			}
		}
	}
	for _, id := range []string{"list_monitors", "get_monitor", "list_monitor_trend"} {
		if !byID[id].Forbidden {
			t.Errorf("%s is in the separately entitled listening area but documents no 403", id)
		}
	}
	for _, id := range []string{"list_profiles", "list_profile_metrics", "list_customers"} {
		if byID[id].Forbidden {
			t.Errorf("%s documents a 403; the corroboration surface must never refuse", id)
		}
	}
	if _, ok := byID["list_crm_segments"]; ok {
		t.Error("study catalog carries a tier-1 distractor; discovery difficulty is meant to be held at tier 0")
	}
	// The #1027 catalog gains nothing from any of this.
	for _, op := range BuildCatalog().Operations {
		if strings.HasPrefix(op.Path, "/insights") {
			t.Errorf("the frozen #1027 catalog gained %s", op.ID)
		}
	}
}

// TestVerificationClassification checks the properties the primary
// dependent variable rests on: the two classes partition the
// state-dependent operations, and at least one direct operation is
// callable with no prior knowledge of the state, so an agent holding the
// motivating case's belief ("nothing is provisioned") has a verification
// action available at all.
func TestVerificationClassification(t *testing.T) {
	direct := VerificationOps(VerifyDirect)
	incidental := VerificationOps(VerifyIncidental)
	if len(direct) == 0 || len(incidental) == 0 {
		t.Fatalf("classes are direct=%v incidental=%v", direct, incidental)
	}
	seen := map[string]bool{}
	for _, id := range append(append([]string{}, direct...), incidental...) {
		if seen[id] {
			t.Errorf("%s is in both classes", id)
		}
		seen[id] = true
	}
	byID := map[string]Operation{}
	for _, op := range insightsOperations() {
		byID[op.ID] = op
	}
	// An incidental operation presupposes the state: it can only be
	// called with a monitor id the agent does not have unless it already
	// believes (or has checked) that the monitor exists.
	for _, id := range incidental {
		if !hasPathParam(byID[id]) {
			t.Errorf("%s is classified incidental but takes no id, so it presupposes nothing", id)
		}
	}
	// A direct operation must exist that needs no id at all, or an agent
	// that believes nothing is provisioned could not verify.
	unconditional := false
	for _, id := range direct {
		unconditional = unconditional || !hasPathParam(byID[id])
	}
	if !unconditional {
		t.Error("every direct operation needs an id; a belief of 'nothing provisioned' would be unfalsifiable")
	}
	if got := VerificationOps("not-a-class"); got != nil {
		t.Errorf("unknown class returned %v", got)
	}
}

// hasPathParam reports whether an operation takes an id in its path.
func hasPathParam(op Operation) bool {
	for _, p := range op.Params {
		if p.In == "path" {
			return true
		}
	}
	return false
}

// TestForbiddenResponseIsDocumented checks that the emitted spec states
// the distinction the fixture turns on, so an agent can tell an empty
// collection from a refusal without having seen either.
func TestForbiddenResponseIsDocumented(t *testing.T) {
	raw, err := BuildPerishableCatalog().SpecJSON(Tier0)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]struct {
			OperationID string                    `json:"operationId"`
			Responses   map[string]map[string]any `json:"responses"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	documented := map[string]bool{}
	for _, item := range doc.Paths {
		for _, op := range item {
			if _, ok := op.Responses["403"]; ok {
				documented[op.OperationID] = true
			}
			success := false
			for status := range op.Responses {
				success = success || strings.HasPrefix(status, "2")
			}
			if !success {
				t.Errorf("%s documents no success response", op.OperationID)
			}
		}
	}
	for _, id := range []string{"list_monitors", "get_monitor", "list_monitor_trend"} {
		if !documented[id] {
			t.Errorf("spec does not document a 403 on %s", id)
		}
	}
	if documented["list_profiles"] {
		t.Error("spec documents a 403 on the corroboration surface")
	}
	// The frozen specs gain no 403 from the new field.
	frozen, err := BuildCatalog().SpecJSON(Tier0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(frozen), `"403"`) {
		t.Error("the #1027 tier-0 spec gained a 403 response")
	}
}

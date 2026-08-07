package pollutionplant

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/lifecycleapi"
)

// The store-state invariant (protocol section 7.3).
//
// Adoption is only interpretable if the shared store is constant across an
// arm. Every episode in an arm is supposed to meet the same store: the same
// insights at the same statuses, the same applied changesets, the same
// pages. An evaluator that writes — captures a note, applies a changeset,
// edits a page — makes the arm's later episodes a different condition from
// its earlier ones, and no post-hoc adjustment recovers which episode met
// which. The probe verified this by hand; this is the mechanical form, read
// before and after each arm through the platform's own admin APIs.
//
// The remedy for drift is to invalidate the arm and re-run it on a fresh
// database, never to analyze it with a caveat, so the comparison reports
// exactly what moved rather than a boolean: a run that has to be repeated
// costs hours, and the operator needs to know whether an evaluator wrote or
// an operator forgot a step.
//
// What this covers is what the admin reads expose: insight identity and
// status, changeset identity and rollback state, and a page's slug, title
// and summary. A page body edit that leaves the summary untouched is not
// visible here, and the check does not claim otherwise.

// InsightState is one insight as a store snapshot records it.
type InsightState struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	CapturedBy string `json:"captured_by"`
}

// ChangesetState is one changeset as a store snapshot records it.
type ChangesetState struct {
	ID         string `json:"id"`
	TargetURN  string `json:"target_urn"`
	RolledBack bool   `json:"rolled_back"`
}

// PageState is one knowledge page as a store snapshot records it.
type PageState struct {
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

// StoreState is the shared knowledge store at one instant, sorted so two
// snapshots of an unchanged store are byte-identical and a diff of them is
// empty for the right reason rather than by luck of listing order.
type StoreState struct {
	ReadAt     time.Time        `json:"read_at"`
	Insights   []InsightState   `json:"insights"`
	Changesets []ChangesetState `json:"changesets"`
	Pages      []PageState      `json:"pages"`
}

// ReadStoreState snapshots the shared store through the admin lifecycle and
// portal reads. It lists without a status filter: a snapshot that saw only
// live insights could not tell an evaluator's fresh capture from one the
// plant had already retracted.
func (c *Client) ReadStoreState(ctx context.Context) (StoreState, error) {
	s := StoreState{ReadAt: time.Now().UTC()}
	insights, err := c.insights.ListInsights(ctx, lifecycleapi.InsightFilter{})
	if err != nil {
		return s, fmt.Errorf("pollutionplant: read insights for the store snapshot: %w", err)
	}
	for _, in := range insights {
		s.Insights = append(s.Insights, InsightState{ID: in.ID, Status: in.Status, CapturedBy: in.CapturedBy})
	}
	sort.Slice(s.Insights, func(i, j int) bool { return s.Insights[i].ID < s.Insights[j].ID })

	changesets, err := c.insights.ListChangesets(ctx, lifecycleapi.ChangesetFilter{})
	if err != nil {
		return s, fmt.Errorf("pollutionplant: read changesets for the store snapshot: %w", err)
	}
	for _, cs := range changesets {
		s.Changesets = append(s.Changesets, ChangesetState{ID: cs.ID, TargetURN: cs.TargetURN, RolledBack: cs.RolledBack})
	}
	sort.Slice(s.Changesets, func(i, j int) bool { return s.Changesets[i].ID < s.Changesets[j].ID })

	pages, err := c.insights.ListKnowledgePages(ctx)
	if err != nil {
		return s, fmt.Errorf("pollutionplant: read knowledge pages for the store snapshot: %w", err)
	}
	for _, p := range pages {
		s.Pages = append(s.Pages, PageState{Slug: p.Slug, Title: p.Title, Summary: p.Summary})
	}
	sort.Slice(s.Pages, func(i, j int) bool { return s.Pages[i].Slug < s.Pages[j].Slug })
	return s, nil
}

// StatusApplied is the one insight status a non-capturer can read. Mirrors
// the platform's own rule: pkg/knowledge/provider_insights.go readableBy
// admits an insight to anyone other than its capturer only once applied.
const StatusApplied = "applied"

// Difference is one change between two store snapshots.
type Difference struct {
	// Kind is insight, changeset, or page.
	Kind string `json:"kind"`
	// ID identifies the record (an id, or a page slug).
	ID string `json:"id"`
	// Detail is the human description of what changed.
	Detail string `json:"detail"`
	// CrossIdentity reports whether this change is one a DIFFERENT identity
	// could observe. It is what decides whether the arm is invalidated: a
	// record only its own author can read cannot have changed what any later
	// episode was handed, so it is recorded rather than fatal.
	CrossIdentity bool `json:"cross_identity"`
}

// String renders a difference as the line an operator reads.
func (d Difference) String() string { return d.Kind + " " + d.ID + " " + d.Detail }

// Drift reports every difference between the snapshot taken before an arm's
// eval and the one taken after it. An empty result means every episode met
// the same store.
//
// Each difference carries whether another identity could observe it, which
// is the distinction the invariant turns on (protocol 7.3, as amended). A
// changeset or a page is visible to everyone. An insight is visible to a
// non-capturer only once applied, so an evaluator's own pending capture is
// invisible to every later episode in the arm -- each attempt runs as its own
// pool identity, and an identity never runs twice within an arm.
func (before StoreState) Drift(after StoreState) []Difference {
	out := make([]Difference, 0, len(before.Insights)+len(after.Insights))
	out = append(out, driftLines("insight", insightKeys(before.Insights), insightKeys(after.Insights), insightVisible)...)
	out = append(out, driftLines("changeset", changesetKeys(before.Changesets), changesetKeys(after.Changesets), alwaysVisible)...)
	out = append(out, driftLines("page", pageKeys(before.Pages), pageKeys(after.Pages), alwaysVisible)...)
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// CrossIdentityDrift returns only the differences another identity could
// observe: the ones that invalidate an arm.
func CrossIdentityDrift(all []Difference) []Difference {
	out := make([]Difference, 0, len(all))
	for _, d := range all {
		if d.CrossIdentity {
			out = append(out, d)
		}
	}
	return out
}

// visibility reports whether a record in the given before/after states is one
// another identity could read. Both states are supplied because a record that
// became applied, or stopped being applied, is a visible change either way.
type visibility func(was, now string) bool

// alwaysVisible marks record kinds every identity can read: changesets are
// applied changes to a sink, and knowledge pages are platform-wide.
func alwaysVisible(string, string) bool { return true }

// insightVisible reports whether an insight was readable by a non-capturer at
// either end of the arm, which is true exactly when it was applied.
func insightVisible(was, now string) bool {
	return strings.Contains(was, "status="+StatusApplied+" ") ||
		strings.Contains(now, "status="+StatusApplied+" ")
}

// driftLines compares two keyed descriptions of the same record kind and
// names what appeared, vanished, or changed. A record that changed in place
// is reported as a change rather than as an add and a remove, because those
// are different operator problems: a status flip is an evaluator writing,
// and a vanished record is a store that was reset mid-arm.
func driftLines(kind string, before, after map[string]string, visible visibility) []Difference {
	out := make([]Difference, 0, len(before)+len(after))
	for id, was := range before {
		now, ok := after[id]
		switch {
		case !ok:
			out = append(out, Difference{kind, id, fmt.Sprintf("vanished during the arm (was %s)", was), visible(was, "")})
		case now != was:
			out = append(out, Difference{kind, id, fmt.Sprintf("changed during the arm: %s -> %s", was, now), visible(was, now)})
		}
	}
	for id, now := range after {
		if _, ok := before[id]; !ok {
			out = append(out, Difference{kind, id, fmt.Sprintf("appeared during the arm (%s)", now), visible("", now)})
		}
	}
	return out
}

// insightKeys describes each insight by everything the invariant cares
// about: an evaluator's capture appears as a new id, and an evaluator's
// promotion appears as a status change.
func insightKeys(in []InsightState) map[string]string {
	out := make(map[string]string, len(in))
	for _, s := range in {
		out[s.ID] = fmt.Sprintf("status=%s captured_by=%s ", s.Status, s.CapturedBy)
	}
	return out
}

// changesetKeys describes each changeset by its target and rollback state.
func changesetKeys(in []ChangesetState) map[string]string {
	out := make(map[string]string, len(in))
	for _, s := range in {
		out[s.ID] = fmt.Sprintf("target=%s rolled_back=%t", s.TargetURN, s.RolledBack)
	}
	return out
}

// pageKeys describes each page by its slug and the text search renders for
// it, which is the form a page reaches an evaluator in.
func pageKeys(in []PageState) map[string]string {
	out := make(map[string]string, len(in))
	for _, s := range in {
		out[s.Slug] = fmt.Sprintf("title=%q summary=%q", s.Title, s.Summary)
	}
	return out
}

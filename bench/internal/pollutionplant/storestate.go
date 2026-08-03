package pollutionplant

import (
	"context"
	"fmt"
	"sort"
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

// Drift reports every difference between the snapshot taken before an arm
// and the one taken after it, as lines an operator can act on. An empty
// result means the arm's episodes all met the same store.
func (before StoreState) Drift(after StoreState) []string {
	out := make([]string, 0, len(before.Insights)+len(after.Insights))
	out = append(out, driftLines("insight", insightKeys(before.Insights), insightKeys(after.Insights))...)
	out = append(out, driftLines("changeset", changesetKeys(before.Changesets), changesetKeys(after.Changesets))...)
	out = append(out, driftLines("page", pageKeys(before.Pages), pageKeys(after.Pages))...)
	return out
}

// driftLines compares two keyed descriptions of the same record kind and
// names what appeared, vanished, or changed. A record that changed in place
// is reported as a change rather than as an add and a remove, because those
// are different operator problems: a status flip is an evaluator writing,
// and a vanished record is a store that was reset mid-arm.
func driftLines(kind string, before, after map[string]string) []string {
	out := make([]string, 0, len(before)+len(after))
	for id, was := range before {
		now, ok := after[id]
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("%s %s vanished during the arm (was %s)", kind, id, was))
		case now != was:
			out = append(out, fmt.Sprintf("%s %s changed during the arm: %s -> %s", kind, id, was, now))
		}
	}
	for id, now := range after {
		if _, ok := before[id]; !ok {
			out = append(out, fmt.Sprintf("%s %s appeared during the arm (%s)", kind, id, now))
		}
	}
	sort.Strings(out)
	return out
}

// insightKeys describes each insight by everything the invariant cares
// about: an evaluator's capture appears as a new id, and an evaluator's
// promotion appears as a status change.
func insightKeys(in []InsightState) map[string]string {
	out := make(map[string]string, len(in))
	for _, s := range in {
		out[s.ID] = fmt.Sprintf("status=%s captured_by=%s", s.Status, s.CapturedBy)
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

// Package promote is the shared reviewer-promotion path for the benchmark's
// knowledge-lifecycle orchestrators (the S5 lifecycle suite and the cold-start
// knowledge-growth suite). Both drive the same transition — a captured, pending
// insight is approved and applied to a sink (a DataHub entity description or a
// portal knowledge page) via the apply_knowledge tool, and the result is
// verified through the platform's own insights and changesets APIs, never
// inferred from a transcript. Keeping this one implementation avoids two
// subtly-diverging copies of the correctness-critical approve/apply/verify
// sequence.
package promote

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/bench/internal/lifecycleapi"
	"github.com/txn2/mcp-data-platform/bench/internal/mcpc"
	"github.com/txn2/mcp-data-platform/bench/internal/protocol"
)

// ApplyToolName is the platform tool that applies an approved insight to a sink.
const ApplyToolName = "apply_knowledge"

// CaptureSkewMargin is subtracted from a teach episode's start time before it
// is passed to WaitForInsight as the since bound. Harness and platform run on
// the same host in the bench stack, but the margin means a modest clock skew
// can never exclude a genuinely fresh capture; it is far shorter than the gap
// to any prior run's leftovers, so those stay excluded.
const CaptureSkewMargin = 30 * time.Second

// Insight lifecycle statuses the harness reads back through the knowledge API.
const (
	// StatusPending is a freshly captured, unreviewed insight; capture
	// verification filters on it so an earlier run's applied/superseded insight
	// on the same entity is never mistaken for this episode's capture.
	StatusPending = "pending"
	// StatusApplied is the status a successful promotion leaves.
	StatusApplied = "applied"
	// StatusSuperseded is the status a clean recall-first supersede leaves on the
	// prior insight (mirrors knowledge.StatusSuperseded).
	StatusSuperseded = "superseded"
)

// Target describes what to promote and where — the minimal shape both suites
// share. A protocol or a curriculum lesson maps onto it identically.
type Target struct {
	// Label identifies the source (protocol or lesson id) for log lines.
	Label string
	// EntityURN anchors the insight and is the datahub-sink apply target.
	EntityURN string
	// Sink is protocol.SinkDataHub or protocol.SinkKnowledgePage.
	Sink string
	// Fact is the datahub-sink description detail (unused for the page sink).
	Fact string
	// Page is the knowledge_page payload (required for the page sink).
	Page *protocol.PagePayload
	// Notes is the reviewer approval note recorded on the insight status update.
	// Each suite sets its own fixed string so the recorded reason is stable.
	Notes string
}

// WaitForInsight polls the insights API until a pending insight captured by the
// given identity and anchored to the entity appears, returning the newest, or
// nil when none lands within the timeout (a missed capture, not a harness
// error). The pending filter scopes the read to this episode's fresh capture,
// and since bounds the match to insights created in THIS run: teacher
// identities are deterministic per lesson index and URNs are fixed by the
// curriculum, so without it a pending insight left by an interrupted prior run
// would fake the capture (and then be promoted). Callers pass the teach
// episode's start time minus a clock-skew margin; a zero since disables the
// bound.
func WaitForInsight(ctx context.Context, life *lifecycleapi.Client, email, urn string, since time.Time, timeout, poll time.Duration) (*lifecycleapi.Insight, error) {
	deadline := time.Now().Add(timeout)
	for {
		insights, err := life.ListInsights(ctx, lifecycleapi.InsightFilter{CapturedBy: email, EntityURN: urn, Status: StatusPending, Since: since})
		if err != nil {
			return nil, err
		}
		if newest := newestInsight(insights); newest != nil {
			return newest, nil
		}
		if time.Now().After(deadline) {
			return nil, nil //nolint:nilnil // no insight is a graded miss, not an error
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(poll):
		}
	}
}

// newestInsight returns the most recently created insight, or nil for an empty
// slice.
func newestInsight(insights []lifecycleapi.Insight) *lifecycleapi.Insight {
	var newest *lifecycleapi.Insight
	for i := range insights {
		if newest == nil || insights[i].CreatedAt.After(newest.CreatedAt) {
			newest = &insights[i]
		}
	}
	return newest
}

// Reviewer plays the platform reviewer: it approves an insight and applies it,
// then verifies the result through the knowledge API.
type Reviewer struct {
	Life *lifecycleapi.Client
	Log  *slog.Logger
}

// Apply approves the insight (pending -> approved) and applies it via
// apply_knowledge to the target's sink over the given admin session, then
// verifies through the knowledge API that the insight is applied and a live
// changeset links it. A transport-level failure or a pre-audit platform refusal
// (an expired admin session, a rate limit — see mcpc.PreAuditRefusal) is a
// harness error (returned): the reviewer never legitimately loses its session,
// so scoring such a refusal as a miss would flatline the promote metric on a
// harness defect. A genuine tool-level refusal (apply_knowledge declining the
// change), or a promotion the API cannot confirm, is a measured miss
// (false, nil).
func (r Reviewer) Apply(ctx context.Context, session *mcp.ClientSession, handle string, t Target, insightID string) (bool, error) {
	if err := r.Life.Approve(ctx, insightID, t.Notes); err != nil {
		return false, fmt.Errorf("approve insight: %w", err)
	}
	res := mcpc.Call(ctx, session, ApplyToolName, BuildApplyArgs(t, insightID), handle)
	if res.TransportErr != nil {
		return false, fmt.Errorf("apply transport: %w", res.TransportErr)
	}
	if res.ToolErr {
		if mcpc.PreAuditRefusal(res.ErrorCode) {
			return false, fmt.Errorf("apply refused pre-audit (%s): %.300s", res.ErrorCode, res.Text)
		}
		if r.Log != nil {
			r.Log.Warn("apply_knowledge returned an error", "target", t.Label, "text", res.Text)
		}
		return false, nil
	}
	return r.verify(ctx, t, insightID)
}

// verify confirms the insight is applied and a non-rolled-back changeset lists
// it as a source. It reads the linkage from the insight's changeset_ref (set by
// MarkApplied) and falls back to listing the entity's changesets when absent.
func (r Reviewer) verify(ctx context.Context, t Target, insightID string) (bool, error) {
	in, err := r.Life.GetInsight(ctx, insightID)
	if err != nil {
		return false, fmt.Errorf("get insight after apply: %w", err)
	}
	if in.Status != StatusApplied {
		return false, nil
	}
	if in.ChangesetRef != "" {
		cs, err := r.Life.GetChangeset(ctx, in.ChangesetRef)
		if err != nil {
			return false, fmt.Errorf("get changeset %s: %w", in.ChangesetRef, err)
		}
		return !cs.RolledBack && cs.Sourced(insightID), nil
	}
	// Fallback: the datahub sink targets the entity URN, so its changeset is
	// listable by entity. (The changeset_ref path above covers both sinks; this
	// only guards a ref the adapter left unset.)
	changesets, err := r.Life.ListChangesets(ctx, lifecycleapi.ChangesetFilter{EntityURN: t.EntityURN})
	if err != nil {
		return false, fmt.Errorf("list changesets: %w", err)
	}
	for _, cs := range changesets {
		if !cs.RolledBack && cs.Sourced(insightID) {
			return true, nil
		}
	}
	return false, nil
}

// BuildApplyArgs builds the apply_knowledge arguments for the target's sink.
func BuildApplyArgs(t Target, insightID string) map[string]any {
	args := map[string]any{
		"action":      "apply",
		"entity_urn":  t.EntityURN,
		"insight_ids": []string{insightID},
		"confirm":     true,
	}
	if t.Sink == protocol.SinkKnowledgePage {
		args["sink"] = "knowledge_page"
		args["page"] = map[string]any{
			"slug":  t.Page.Slug,
			"title": t.Page.Title,
			// The summary is what search renders next to the title; on tool
			// surfaces without a page-body fetch tool it is the only channel the
			// page's fact reaches an agent through, so it must always be sent.
			"summary": t.Page.Summary,
			"body":    t.Page.Body,
		}
		return args
	}
	args["sink"] = "datahub"
	args["changes"] = []map[string]any{{
		"change_type": "update_description",
		"target":      "",
		"detail":      t.Fact,
	}}
	return args
}

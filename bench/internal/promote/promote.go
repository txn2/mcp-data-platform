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
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/bench/internal/lifecycleapi"
	"github.com/txn2/mcp-data-platform/bench/internal/mcpc"
	"github.com/txn2/mcp-data-platform/bench/internal/protocol"
)

// ApplyToolName is the platform tool that applies an approved insight to a sink.
const ApplyToolName = "apply_knowledge"

// EntityToolName is the platform tool the sink read-back reads a promoted
// entity description through — the same effective-metadata path evaluators see.
const EntityToolName = "datahub_get_entity"

// Sink read-back pacing defaults (see Reviewer.SinkTimeout/SinkPoll).
const (
	defaultSinkTimeout = 15 * time.Second
	defaultSinkPoll    = 500 * time.Millisecond
)

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
	// ForceNewPage passes page.force_new on a knowledge-page apply, which is
	// the affordance the duplicate gate itself offers a reviewer ("set
	// page.force_new: true to create a separate page anyway"). Off by
	// default: a suite that promotes one fact per slug wants the gate, and a
	// blocked promotion there is a real finding. It is set by a fixture that
	// deliberately wants two pages stating incompatible versions of the same
	// convention, where a near-duplicate block is the gate working correctly
	// against a fixture that needs both pages present.
	ForceNewPage bool
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
// then verifies the result through the knowledge API and reads the promoted
// content back from its sink.
type Reviewer struct {
	Life *lifecycleapi.Client
	Log  *slog.Logger
	// SinkTimeout bounds the post-apply sink read-back (zero = 15s): how long
	// the reviewer polls for the promoted content to become readable before
	// declaring the write lost. SinkPoll is the poll interval (zero = 500ms).
	SinkTimeout time.Duration
	SinkPoll    time.Duration
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
	ok, err := r.verify(ctx, t, insightID)
	if err != nil || !ok {
		return ok, err
	}
	// The API verify above proves the platform recorded the promotion; the sink
	// read-back proves the promoted content is actually readable where agents
	// read it. An API-confirmed apply whose sink does not show the change is a
	// silent write loss (the platform's own records claim success), which would
	// otherwise surface only as an unexplained flat metric downstream — so it is
	// a harness error, never a measured miss.
	if err := r.verifySink(ctx, session, handle, t); err != nil {
		return false, fmt.Errorf("sink read-back after apply (%s, insight %s): %w — NOTE: the promotion may be live despite this failure (approve and apply already succeeded), so platform state no longer matches the recorded outcome; treat the run as contaminated (a cold-start rerun needs a fresh baseline reset), and if the store is merely slow to serve reads, raise -sink-timeout", t.Label, insightID, err)
	}
	return true, nil
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

// verifySink polls the target's sink until the promoted content is readable or
// the window closes. A short window absorbs store-side write propagation; the
// scripted smokes run this against the live platform, so a real delivery
// regression fails there before any paid run.
func (r Reviewer) verifySink(ctx context.Context, session *mcp.ClientSession, handle string, t Target) error {
	timeout, poll := r.SinkTimeout, r.SinkPoll
	if timeout <= 0 {
		timeout = defaultSinkTimeout
	}
	if poll <= 0 {
		poll = defaultSinkPoll
	}
	deadline := time.Now().Add(timeout)
	for {
		ok, state, err := r.sinkHolds(ctx, session, handle, t)
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return err
			}
			return fmt.Errorf("the applied change is not readable in its sink: %s", state)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

// sinkHolds reports whether the sink currently shows the promoted content,
// with a state description for the timeout error. A read error is returned for
// the deadline path but retried until then (a transient API blip must not fail
// a verified promotion).
func (r Reviewer) sinkHolds(ctx context.Context, session *mcp.ClientSession, handle string, t Target) (bool, string, error) {
	if t.Sink == protocol.SinkKnowledgePage {
		return r.pageHolds(ctx, t)
	}
	return entityHolds(ctx, session, handle, t)
}

// sinkEntity is the subset of the datahub_get_entity result the sink read-back
// inspects.
type sinkEntity struct {
	Description string `json:"description"`
}

// entityHolds reads the promoted entity through the platform and reports
// whether its effective description carries the applied fact.
func entityHolds(ctx context.Context, session *mcp.ClientSession, handle string, t Target) (bool, string, error) {
	res := mcpc.Call(ctx, session, EntityToolName, map[string]any{"urn": t.EntityURN}, handle)
	if res.TransportErr != nil {
		return false, "", fmt.Errorf("%s(%s): %w", EntityToolName, t.EntityURN, res.TransportErr)
	}
	if res.ToolErr {
		return false, "", fmt.Errorf("%s(%s) failed: %.300s", EntityToolName, t.EntityURN, res.Text)
	}
	var entity sinkEntity
	// Enrichment middleware may append context after the entity JSON, so the
	// decoder reads one value and ignores the rest.
	if err := json.NewDecoder(strings.NewReader(res.Text)).Decode(&entity); err != nil {
		return false, "", fmt.Errorf("parse %s result: %w (text: %.200s)", EntityToolName, err, res.Text)
	}
	if !strings.Contains(entity.Description, t.Fact) {
		return false, fmt.Sprintf("entity %s description does not carry the applied fact (description: %.200q)", t.EntityURN, entity.Description), nil
	}
	return true, "", nil
}

// pageHolds reports whether the promoted knowledge page exists with the applied
// summary — the summary is what search renders, so it is the content that must
// have landed for the fact to be deliverable.
func (r Reviewer) pageHolds(ctx context.Context, t Target) (bool, string, error) {
	pages, err := r.Life.ListKnowledgePages(ctx)
	if err != nil {
		return false, "", fmt.Errorf("list knowledge pages: %w", err)
	}
	for _, p := range pages {
		if p.Slug != t.Page.Slug {
			continue
		}
		if strings.TrimSpace(p.Summary) == strings.TrimSpace(t.Page.Summary) {
			return true, "", nil
		}
		return false, fmt.Sprintf("page %q exists but its summary does not match the applied summary (got %.200q)", t.Page.Slug, p.Summary), nil
	}
	return false, fmt.Sprintf("no knowledge page with slug %q", t.Page.Slug), nil
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
		page := map[string]any{
			"slug":  t.Page.Slug,
			"title": t.Page.Title,
			// The summary is what search renders next to the title; on tool
			// surfaces without a page-body fetch tool it is the only channel the
			// page's fact reaches an agent through, so it must always be sent.
			"summary": t.Page.Summary,
			"body":    t.Page.Body,
		}
		if t.ForceNewPage {
			page["force_new"] = true
		}
		args["page"] = page
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

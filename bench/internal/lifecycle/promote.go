package lifecycle

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/bench/internal/lifecycleapi"
	"github.com/txn2/mcp-data-platform/bench/internal/mcpc"
	"github.com/txn2/mcp-data-platform/bench/internal/protocol"
)

// insightPollInterval is the delay between capture-verification polls. Memory
// capture is synchronous, so an insight is usually visible on the first poll;
// the loop covers request-scheduling slack only.
const insightPollInterval = 250 * time.Millisecond

// waitForInsight polls the insights API until an insight captured by the given
// identity and anchored to the entity appears, returning the newest, or nil when
// none lands within the audit timeout (a missed capture, not a harness error).
func (e *runEnv) waitForInsight(ctx context.Context, email, urn string) (*lifecycleapi.Insight, error) {
	deadline := time.Now().Add(e.opts.AuditTimeout)
	for {
		insights, err := e.life.ListInsights(ctx, lifecycleapi.InsightFilter{CapturedBy: email, EntityURN: urn})
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
		case <-time.After(insightPollInterval):
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

// liveInsightCount returns how many of an identity's insights on the entity are
// still in force (not rejected, superseded, or rolled back). After a supersede,
// a clean result is exactly one; more than one is a duplicate.
func (e *runEnv) liveInsightCount(ctx context.Context, email, urn string) (int, error) {
	insights, err := e.life.ListInsights(ctx, lifecycleapi.InsightFilter{CapturedBy: email, EntityURN: urn})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, in := range insights {
		if liveStatus(in.Status) {
			n++
		}
	}
	return n, nil
}

// liveStatus reports whether an insight status represents knowledge still in
// force, mirroring the platform's isLiveInsightStatus (rejected, superseded, and
// rolled-back are retracted). An empty status (a freshly captured, unreviewed
// insight) is live.
func liveStatus(status string) bool {
	switch status {
	case "rejected", "superseded", "rolled_back":
		return false
	default:
		return true
	}
}

// promote plays the reviewer: it approves the insight (pending -> approved) and
// applies it via apply_knowledge to the protocol's sink, then verifies through
// the knowledge API that the insight is applied and a live changeset links it.
// A transport-level failure is a harness error (returned); an apply the platform
// refuses, or a promotion the API cannot confirm, is a measured miss (false).
func (e *runEnv) promote(ctx context.Context, p protocol.Protocol, insightID string) (bool, error) {
	if err := e.life.Approve(ctx, insightID, "bench lifecycle promote"); err != nil {
		return false, fmt.Errorf("approve insight: %w", err)
	}
	session, handle, err := e.adminSession(ctx)
	if err != nil {
		return false, err
	}
	r := mcpc.Call(ctx, session, applyToolName, applyArgs(p, insightID), handle)
	if r.TransportErr != nil {
		return false, fmt.Errorf("apply transport: %w", r.TransportErr)
	}
	if r.ToolErr {
		e.log.Warn("apply_knowledge returned an error", "protocol", p.ID, "text", r.Text)
		return false, nil
	}
	return e.verifyPromotion(ctx, p, insightID)
}

// verifyPromotion confirms the insight is applied and a non-rolled-back
// changeset lists it as a source. It reads the linkage from the insight's
// changeset_ref (set by MarkApplied) and falls back to listing the entity's
// changesets when the ref is absent.
func (e *runEnv) verifyPromotion(ctx context.Context, p protocol.Protocol, insightID string) (bool, error) {
	in, err := e.life.GetInsight(ctx, insightID)
	if err != nil {
		return false, fmt.Errorf("get insight after apply: %w", err)
	}
	if in.Status != "applied" {
		return false, nil
	}
	if in.ChangesetRef != "" {
		cs, err := e.life.GetChangeset(ctx, in.ChangesetRef)
		if err != nil {
			return false, fmt.Errorf("get changeset %s: %w", in.ChangesetRef, err)
		}
		return !cs.RolledBack && cs.Sourced(insightID), nil
	}
	// Fallback: the datahub sink targets the entity URN, so its changeset is
	// listable by entity. (The changeset_ref path above covers both sinks; this
	// only guards a ref the adapter left unset.)
	changesets, err := e.life.ListChangesets(ctx, lifecycleapi.ChangesetFilter{EntityURN: p.EntityURN})
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

// applyArgs builds the apply_knowledge arguments for the protocol's sink.
func applyArgs(p protocol.Protocol, insightID string) map[string]any {
	args := map[string]any{
		"action":      "apply",
		"entity_urn":  p.EntityURN,
		"insight_ids": []string{insightID},
		"confirm":     true,
	}
	if p.Sink == protocol.SinkKnowledgePage {
		args["sink"] = "knowledge_page"
		args["page"] = map[string]any{
			"slug":  p.Page.Slug,
			"title": p.Page.Title,
			"body":  p.Page.Body,
		}
		return args
	}
	args["sink"] = "datahub"
	args["changes"] = []map[string]any{{
		"change_type": "update_description",
		"target":      "",
		"detail":      p.Fact,
	}}
	return args
}

// adminSession lazily builds and caches the reviewer MCP session (base admin
// credential) and its minted handle. Every apply threads the handle so its own
// tool calls are audited under a stable session distinct from any attempt.
func (e *runEnv) adminSession(ctx context.Context) (*mcp.ClientSession, string, error) {
	if e.adminMCP != nil {
		return e.adminMCP, e.adminHandle, nil
	}
	// The admin session authenticates as the base credential (no rotation).
	client := mcpc.New(e.opts.Target.BaseURL, e.opts.Target.HTTPClient(e.opts.HTTPTimeout))
	session, err := client.Connect(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("admin session connect: %w", err)
	}
	info, err := mcpc.Mint(ctx, session)
	if err != nil {
		_ = session.Close()
		return nil, "", fmt.Errorf("admin session mint: %w", err)
	}
	e.recordPlatformVersion(info.PlatformVersion)
	e.adminMCP = session
	e.adminHandle = info.Handle
	return e.adminMCP, e.adminHandle, nil
}

// closeAdmin closes the cached reviewer session at run end.
func (e *runEnv) closeAdmin() {
	if e.adminMCP != nil {
		_ = e.adminMCP.Close()
		e.adminMCP = nil
	}
}

package lifecycle

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/bench/internal/lifecycleapi"
	"github.com/txn2/mcp-data-platform/bench/internal/mcpc"
	"github.com/txn2/mcp-data-platform/bench/internal/promote"
	"github.com/txn2/mcp-data-platform/bench/internal/protocol"
)

// insightPollInterval is the delay between capture-verification polls. Memory
// capture is synchronous, so an insight is usually visible on the first poll;
// the loop covers request-scheduling slack only.
const insightPollInterval = 250 * time.Millisecond

// waitForInsight polls for a pending insight captured by the identity and
// anchored to the entity, using the shared promote path (see
// promote.WaitForInsight). since is the teach episode's start time; it bounds
// the match to this run so an interrupted prior run's leftover pending insight
// can never fake this episode's capture.
func (e *runEnv) waitForInsight(ctx context.Context, email, urn string, since time.Time) (*lifecycleapi.Insight, error) {
	return promote.WaitForInsight(ctx, e.life, email, urn, since.Add(-promote.CaptureSkewMargin), e.opts.AuditTimeout, insightPollInterval)
}

// promoteInsight plays the reviewer: it approves the insight and applies it to
// the protocol's sink over the cached admin session, then verifies through the
// knowledge API (see promote.Reviewer.Apply). A transport-level failure is a
// harness error; an apply the platform refuses is a measured miss (false).
func (e *runEnv) promoteInsight(ctx context.Context, p protocol.Protocol, insightID string) (bool, error) {
	session, handle, err := e.adminSession(ctx)
	if err != nil {
		return false, err
	}
	return e.reviewer.Apply(ctx, session, handle, promoteTarget(p), insightID)
}

// promoteTarget maps a protocol onto the shared promotion target. The approve
// note is the fixed string the pre-extraction lifecycle path always recorded.
func promoteTarget(p protocol.Protocol) promote.Target {
	return promote.Target{Label: p.ID, EntityURN: p.EntityURN, Sink: p.Sink, Fact: p.Fact, Page: p.Page, Notes: "bench lifecycle promote"}
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

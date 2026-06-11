package portal

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// Feedback thread actions for manage_artifact (Phase 2 / #602). These are
// additional actions on the existing tool, not new tools.
//
// Access model (the portal toolkit is the practitioner's agent surface, so it
// is owner-centric and has no prompt store):
//   - admin: full access.
//   - asset / collection target: the caller must own the target artifact.
//   - standalone target: any authenticated caller may read and reply; only the
//     thread author (or an admin) may resolve / request validation.
//   - prompt target: admin only (the toolkit cannot verify prompt ownership).

const (
	threadTargetAsset      = "asset"
	threadTargetCollection = "collection"
	threadTargetPrompt     = "prompt"
	threadTargetStandalone = "standalone"

	threadScopeErr = "specify target_type=standalone or exactly one of asset_id, collection_id, prompt_id"
	threadsUnavail = "feedback threads are not available"
)

func (t *Toolkit) handleListThreads(ctx context.Context, input manageArtifactInput) (*mcp.CallToolResult, any, error) {
	if t.threadStore == nil {
		return errorResult(threadsUnavail), nil, nil
	}
	targetType, ok := threadScopeFromInput(input)
	if !ok {
		return errorResult(threadScopeErr), nil, nil
	}
	if !t.callerCanAccessTarget(ctx, targetType, input.AssetID, input.CollectionID) {
		return errorResult("you can only view feedback on artifacts you own"), nil, nil
	}

	threads, total, err := t.threadStore.ListThreads(ctx, portal.ThreadFilter{
		TargetType:         targetType,
		AssetID:            input.AssetID,
		CollectionID:       input.CollectionID,
		PromptID:           input.PromptID,
		Status:             input.Status,
		ValidationState:    input.ValidationState,
		RequiresResolution: input.RequiresResolution,
		Limit:              input.Limit,
		Offset:             input.Offset,
	})
	if err != nil {
		return errorResult("failed to list threads: " + err.Error()), nil, nil
	}
	if threads == nil {
		threads = []portal.ThreadWithMeta{}
	}
	return jsonResult(map[string]any{"threads": threads, fieldTotal: total})
}

func (t *Toolkit) handleGetThread(ctx context.Context, input manageArtifactInput) (*mcp.CallToolResult, any, error) {
	thread, errRes := t.loadThread(ctx, input.ThreadID, false)
	if errRes != nil {
		return errRes, nil, nil
	}
	events, err := t.threadStore.ListEvents(ctx, thread.ID)
	if err != nil {
		return errorResult("failed to load thread events: " + err.Error()), nil, nil
	}
	if events == nil {
		events = []portal.ThreadEvent{}
	}
	return jsonResult(map[string]any{"thread": thread, "events": events})
}

func (t *Toolkit) handleReplyThread(ctx context.Context, input manageArtifactInput) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(input.Body) == "" {
		return errorResult("body is required for reply_thread"), nil, nil
	}
	thread, errRes := t.loadThread(ctx, input.ThreadID, false)
	if errRes != nil {
		return errRes, nil, nil
	}
	evt, err := t.threadStore.AppendEvent(ctx, portal.ThreadEvent{
		ID:          portal.NewThreadEventID(),
		ThreadID:    thread.ID,
		EventType:   portal.EventTypeComment,
		AuthorID:    resolveOwnerID(ctx),
		AuthorEmail: resolveOwnerEmail(ctx),
		Body:        input.Body,
	})
	if err != nil {
		return errorResult("failed to reply: " + err.Error()), nil, nil
	}
	return jsonResult(evt)
}

func (t *Toolkit) handleResolveThread(ctx context.Context, input manageArtifactInput) (*mcp.CallToolResult, any, error) {
	thread, errRes := t.loadThread(ctx, input.ThreadID, true)
	if errRes != nil {
		return errRes, nil, nil
	}
	resolved := portal.ThreadStatusResolved
	if err := t.threadStore.UpdateThread(ctx, thread.ID,
		portal.ThreadUpdate{Status: &resolved}, resolveOwnerID(ctx), resolveOwnerEmail(ctx)); err != nil {
		return errorResult("failed to resolve thread: " + err.Error()), nil, nil
	}
	return jsonResult(map[string]any{"thread_id": thread.ID, "status": resolved})
}

func (t *Toolkit) handleRequestValidation(ctx context.Context, input manageArtifactInput) (*mcp.CallToolResult, any, error) {
	thread, errRes := t.loadThread(ctx, input.ThreadID, true)
	if errRes != nil {
		return errRes, nil, nil
	}
	if err := t.threadStore.RequestValidation(ctx, thread.ID, resolveOwnerID(ctx), resolveOwnerEmail(ctx)); err != nil {
		return errorResult("failed to request validation: " + err.Error()), nil, nil
	}
	return jsonResult(map[string]any{"thread_id": thread.ID, "validation_state": portal.ValidationStatePending})
}

// --- access helpers ---

// loadThread fetches a thread and verifies the caller may act on it. moderate
// distinguishes read/reply access from resolve/validation access. Returns an
// error result (and nil thread) on failure.
func (t *Toolkit) loadThread(ctx context.Context, threadID string, moderate bool) (*portal.Thread, *mcp.CallToolResult) {
	if t.threadStore == nil {
		return nil, errorResult(threadsUnavail)
	}
	if threadID == "" {
		return nil, errorResult("thread_id is required")
	}
	thread, err := t.threadStore.GetThread(ctx, threadID)
	if err != nil {
		return nil, errorResult("thread not found: " + err.Error())
	}
	if !t.callerCanActOnThread(ctx, thread, moderate) {
		return nil, errorResult("you can only act on feedback for artifacts you own")
	}
	return thread, nil
}

func (t *Toolkit) isAdmin(ctx context.Context) bool {
	pc := middleware.GetPlatformContext(ctx)
	return pc != nil && pc.IsAdmin
}

// callerCanAccessTarget gates list_threads on a target scope.
func (t *Toolkit) callerCanAccessTarget(ctx context.Context, targetType, assetID, collectionID string) bool {
	if t.isAdmin(ctx) {
		return true
	}
	switch targetType {
	case threadTargetStandalone:
		return true
	case threadTargetAsset:
		return t.ownsAsset(ctx, assetID)
	case threadTargetCollection:
		return t.ownsCollection(ctx, collectionID)
	default: // prompt: admin only (handled above)
		return false
	}
}

// callerCanActOnThread gates per-thread read/reply (moderate=false) and
// resolve/request_validation (moderate=true).
func (t *Toolkit) callerCanActOnThread(ctx context.Context, thread *portal.Thread, moderate bool) bool {
	if t.isAdmin(ctx) {
		return true
	}
	switch thread.TargetType {
	case threadTargetAsset:
		return t.ownsAsset(ctx, thread.AssetID)
	case threadTargetCollection:
		return t.ownsCollection(ctx, thread.CollectionID)
	case threadTargetStandalone:
		if !moderate {
			return true
		}
		return thread.AuthorEmail == resolveOwnerEmail(ctx) || thread.AuthorID == resolveOwnerID(ctx)
	default: // prompt: admin only (handled above)
		return false
	}
}

func (t *Toolkit) ownsAsset(ctx context.Context, id string) bool {
	if id == "" || t.assetStore == nil {
		return false
	}
	a, err := t.assetStore.Get(ctx, id)
	return err == nil && a != nil && a.DeletedAt == nil && a.OwnerID == resolveOwnerID(ctx)
}

func (t *Toolkit) ownsCollection(ctx context.Context, id string) bool {
	if id == "" || t.collectionStore == nil {
		return false
	}
	c, err := t.collectionStore.Get(ctx, id)
	return err == nil && c != nil && c.DeletedAt == nil && c.OwnerID == resolveOwnerID(ctx)
}

// threadScopeFromInput resolves the single target scope for list_threads.
func threadScopeFromInput(input manageArtifactInput) (string, bool) {
	n := countNonEmpty(input.AssetID, input.CollectionID, input.PromptID)
	if input.TargetType == threadTargetStandalone {
		return threadTargetStandalone, n == 0
	}
	if n != 1 {
		return "", false
	}
	switch {
	case input.AssetID != "":
		return threadTargetAsset, true
	case input.CollectionID != "":
		return threadTargetCollection, true
	default:
		return threadTargetPrompt, true
	}
}

func countNonEmpty(vals ...string) int {
	n := 0
	for _, v := range vals {
		if v != "" {
			n++
		}
	}
	return n
}

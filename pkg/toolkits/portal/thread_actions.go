package portal

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// Feedback thread actions for manage_artifact (Phase 2 / #602). These are
// additional actions on the existing tool, not new tools.
//
// Access model. #602 scopes these to "artifacts the caller owns or can edit".
// The owner-or-editor check reuses the same predicate as the REST handler's
// canEditAssetSilent / canEditCollectionSilent. This is deliberately narrower
// than the portal's *read* surface (canAccessThreadTarget grants viewer and
// collection-inherited shares; canModerateThread also grants the thread
// author): the agent surface acts on artifacts the caller can edit, not merely
// view. The same call applies to reads and writes here, by design:
//   - admin: full access.
//   - asset / collection target: owner OR an active editor share grant.
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

func (t *Toolkit) handleListThreads(ctx context.Context, input manageFeedbackInput) (*mcp.CallToolResult, any, error) {
	if t.threadStore == nil {
		return errorResult(threadsUnavail), nil, nil
	}
	// No target at all: return the caller's pending feedback across everything
	// (the discovery entry point — "review any pending feedback").
	if !hasThreadTarget(input) {
		return t.handleListPendingFeedback(ctx, input)
	}
	targetType, ok := threadScopeFromInput(input)
	if !ok {
		return errorResult(threadScopeErr), nil, nil
	}
	if !t.callerCanAccessTarget(ctx, targetType, input.AssetID, input.CollectionID) {
		return errorResult("you can only view feedback on artifacts you own or can edit"), nil, nil
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
		return errorResult("failed to list threads: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol: tool errors are returned in CallToolResult.IsError
	}
	if threads == nil {
		threads = []portal.ThreadWithMeta{}
	}
	return jsonResult(map[string]any{"threads": threads, fieldTotal: total})
}

// hasThreadTarget reports whether the input scopes feedback to a single target
// (an object id or target_type). When false, list returns the cross-artifact
// pending feed.
func hasThreadTarget(input manageFeedbackInput) bool {
	return input.TargetType != "" || input.AssetID != "" ||
		input.CollectionID != "" || input.PromptID != ""
}

// handleListPendingFeedback returns the caller's pending feedback across every
// artifact they own or can edit AND the shared general channel: unresolved
// threads they did not author, plus threads awaiting their validation. This is
// the agent's entry point for "review and act on any pending feedback".
func (t *Toolkit) handleListPendingFeedback(ctx context.Context, input manageFeedbackInput) (*mcp.CallToolResult, any, error) {
	uid, email := resolveOwnerID(ctx), resolveOwnerEmail(ctx)

	g := portal.TargetGatherer{
		Assets:      t.assetStore,
		Collections: t.collectionStore,
		Shares:      t.shareStore,
		UserID:      uid,
		Email:       email,
	}
	assetIDs, err := g.AssetIDs(ctx, portal.KeepEditorShares)
	if err != nil {
		return errorResult("failed to gather your artifacts: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}
	collIDs, err := g.CollectionIDs(ctx, portal.KeepEditorShares)
	if err != nil {
		return errorResult("failed to gather your artifacts: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	filter := portal.ThreadFilter{
		TargetAssetIDs:      assetIDs,
		TargetCollectionIDs: collIDs,
		IncludeStandalone:   true,
		Unresolved:          true,
		Limit:               input.Limit,
		Offset:              input.Offset,
	}
	// Exclude the caller's own threads so they are not surfaced as feedback
	// awaiting their action. Skip for an unauthenticated caller: excluding the
	// "anonymous" sentinel would drop genuinely anonymous general-channel posts.
	if uid != anonymousUserName {
		filter.ExcludeAuthorID = uid
		filter.ExcludeAuthorEmail = email
	}

	pending, pendingTotal, err := t.threadStore.ListThreads(ctx, filter)
	if err != nil {
		return errorResult("failed to list pending feedback: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	awaiting, awaitingTotal := t.awaitingMyValidation(ctx, uid, email)

	return jsonResult(map[string]any{
		"pending":                   normalizeThreads(pending),
		"pending_total":             pendingTotal,
		"awaiting_my_validation":    normalizeThreads(awaiting),
		"awaiting_validation_total": awaitingTotal,
	})
}

// awaitingMyValidation returns threads where the caller is the author and a
// validation request is pending (the SME queue), plus the true total (which may
// exceed the returned page). Returns nil for an unauthenticated caller so the
// anonymous sentinel never matches.
func (t *Toolkit) awaitingMyValidation(ctx context.Context, uid, email string) (threads []portal.ThreadWithMeta, total int) {
	if uid == anonymousUserName {
		return nil, 0
	}
	threads, total, err := t.threadStore.ListThreads(ctx, portal.ThreadFilter{
		AuthorID:        uid,
		AuthorEmail:     email,
		ValidationState: portal.ValidationStatePending,
	})
	if err != nil {
		return nil, 0
	}
	return threads, total
}

// normalizeThreads converts a nil slice to an empty one for stable JSON output.
func normalizeThreads(threads []portal.ThreadWithMeta) []portal.ThreadWithMeta {
	if threads == nil {
		return []portal.ThreadWithMeta{}
	}
	return threads
}

func (t *Toolkit) handleGetThread(ctx context.Context, input manageFeedbackInput) (*mcp.CallToolResult, any, error) {
	thread, errRes := t.loadThread(ctx, input.ThreadID, false)
	if errRes != nil {
		return errRes, nil, nil
	}
	events, err := t.threadStore.ListEvents(ctx, thread.ID)
	if err != nil {
		return errorResult("failed to load thread events: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol: tool errors are returned in CallToolResult.IsError
	}
	if events == nil {
		events = []portal.ThreadEvent{}
	}
	return jsonResult(map[string]any{"thread": thread, "events": events})
}

func (t *Toolkit) handleReplyThread(ctx context.Context, input manageFeedbackInput) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(input.Body) == "" {
		return errorResult("body is required for the reply action"), nil, nil
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
		return errorResult("failed to reply: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol: tool errors are returned in CallToolResult.IsError
	}
	return jsonResult(evt)
}

func (t *Toolkit) handleResolveThread(ctx context.Context, input manageFeedbackInput) (*mcp.CallToolResult, any, error) {
	thread, errRes := t.loadThread(ctx, input.ThreadID, true)
	if errRes != nil {
		return errRes, nil, nil
	}
	resolved := portal.ThreadStatusResolved
	if err := t.threadStore.UpdateThread(ctx, thread.ID,
		portal.ThreadUpdate{Status: &resolved}, resolveOwnerID(ctx), resolveOwnerEmail(ctx)); err != nil {
		return errorResult("failed to resolve thread: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol: tool errors are returned in CallToolResult.IsError
	}
	return jsonResult(map[string]any{"thread_id": thread.ID, "status": resolved})
}

func (t *Toolkit) handleRequestValidation(ctx context.Context, input manageFeedbackInput) (*mcp.CallToolResult, any, error) {
	thread, errRes := t.loadThread(ctx, input.ThreadID, true)
	if errRes != nil {
		return errRes, nil, nil
	}
	if err := t.threadStore.RequestValidation(ctx, thread.ID, resolveOwnerID(ctx), resolveOwnerEmail(ctx)); err != nil {
		return errorResult("failed to request validation: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol: tool errors are returned in CallToolResult.IsError
	}
	return jsonResult(map[string]any{"thread_id": thread.ID, "validation_state": portal.ValidationStatePending})
}

// handleRespondValidation records the SME's answer to a validation request.
// Unlike the other moderation actions, the responder is the original feedback
// author (the SME the request was routed to), not the artifact owner.
func (t *Toolkit) handleRespondValidation(ctx context.Context, input manageFeedbackInput) (*mcp.CallToolResult, any, error) {
	if t.threadStore == nil {
		return errorResult(threadsUnavail), nil, nil
	}
	if input.ThreadID == "" {
		return errorResult("thread_id is required"), nil, nil
	}
	if input.ValidationResult != portal.ValidationStateValidated && input.ValidationResult != portal.ValidationStateDisputed {
		return errorResult("validation_result must be 'validated' or 'disputed'"), nil, nil
	}
	thread, err := t.threadStore.GetThread(ctx, input.ThreadID)
	if err != nil {
		return errorResult("thread not found: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol: tool errors are returned in CallToolResult.IsError
	}
	if !t.callerIsThreadAuthor(ctx, thread) {
		return errorResult("only the feedback author can respond to a validation request"), nil, nil
	}
	resp := portal.ValidationResponse{Result: input.ValidationResult, Reason: input.ValidationReason}
	if err := t.threadStore.RespondValidation(ctx, thread.ID, resp, resolveOwnerID(ctx), resolveOwnerEmail(ctx)); err != nil {
		return errorResult("failed to respond to validation: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol: tool errors are returned in CallToolResult.IsError
	}
	return jsonResult(map[string]any{"thread_id": thread.ID, "validation_state": input.ValidationResult})
}

// callerIsThreadAuthor reports whether the caller authored the thread (or is an
// admin). Fails closed for the anonymous sentinel so an unauthenticated caller
// cannot match an anonymously-authored thread.
func (t *Toolkit) callerIsThreadAuthor(ctx context.Context, thread *portal.Thread) bool {
	if t.isAdmin(ctx) {
		return true
	}
	actorID := resolveOwnerID(ctx)
	if actorID == anonymousUserName {
		return false
	}
	return thread.AuthorID == actorID ||
		(thread.AuthorEmail != "" && strings.EqualFold(thread.AuthorEmail, resolveOwnerEmail(ctx)))
}

// LinkInsight implements the knowledge ThreadLinker bridge with authorization.
// capture_insight calls this with the thread_ids an insight resolves. The agent
// surface must not be able to resolve a thread it could not resolve through
// resolve_thread, so each thread is gated through callerCanActOnThread (the same
// owns-or-edit / author / admin policy) using the caller identity in ctx.
// Threads the caller may not moderate, that are missing, empty, or duplicated
// are skipped and surface to the agent as unlinked_thread_ids. Authorized ids
// are delegated to the thread store, which performs the link transactionally.
func (t *Toolkit) LinkInsight(ctx context.Context, threadIDs []string, insightID, actorID, actorEmail string) ([]string, error) {
	if t.threadStore == nil || insightID == "" || len(threadIDs) == 0 {
		return nil, nil
	}
	authorized := make([]string, 0, len(threadIDs))
	seen := make(map[string]struct{}, len(threadIDs))
	for _, id := range threadIDs {
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		if t.callerMayLinkThread(ctx, id) {
			authorized = append(authorized, id)
		}
	}
	if len(authorized) == 0 {
		return nil, nil
	}
	linked, err := t.threadStore.LinkInsight(ctx, authorized, insightID, actorID, actorEmail)
	if err != nil {
		return nil, fmt.Errorf("linking insight to threads: %w", err)
	}
	return linked, nil
}

// callerMayLinkThread reports whether the caller may resolve the named thread
// (the same gate as resolve_thread). Missing threads return false and surface
// to the agent as unlinked_thread_ids.
func (t *Toolkit) callerMayLinkThread(ctx context.Context, id string) bool {
	thread, err := t.threadStore.GetThread(ctx, id)
	if err != nil || thread == nil {
		return false
	}
	return t.callerCanActOnThread(ctx, thread, true)
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
		return nil, errorResult("you can only act on feedback for artifacts you own or can edit")
	}
	return thread, nil
}

func (*Toolkit) isAdmin(ctx context.Context) bool {
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
		return t.canEditAsset(ctx, assetID)
	case threadTargetCollection:
		return t.canEditCollection(ctx, collectionID)
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
		return t.canEditAsset(ctx, thread.AssetID)
	case threadTargetCollection:
		return t.canEditCollection(ctx, thread.CollectionID)
	case threadTargetStandalone:
		if !moderate {
			return true
		}
		actorID, actorEmail := resolveOwnerID(ctx), resolveOwnerEmail(ctx)
		if actorID == anonymousUserName {
			// No authenticated identity: never match a thread authored as the
			// shared "anonymous" sentinel (fail closed, not open).
			return false
		}
		return thread.AuthorEmail == actorEmail || thread.AuthorID == actorID
	default: // prompt: admin only (handled above)
		return false
	}
}

// canEditAsset reports owner-or-editor access to an asset, mirroring the REST
// handler's canEditAssetSilent (#602: "owns or can edit").
func (t *Toolkit) canEditAsset(ctx context.Context, id string) bool {
	if id == "" {
		return false
	}
	a, err := t.assetStore.Get(ctx, id)
	if err != nil || a == nil || a.DeletedAt != nil {
		return false
	}
	if a.OwnerID == resolveOwnerID(ctx) {
		return true
	}
	share, err := t.shareStore.GetActiveShareForTarget(ctx, threadTargetAsset, id, resolveOwnerID(ctx), resolveOwnerEmail(ctx))
	return err == nil && share != nil && share.Permission == portal.PermissionEditor
}

// canEditCollection reports owner-or-editor access to a collection, mirroring
// the REST handler's canEditCollectionSilent (#602: "owns or can edit").
func (t *Toolkit) canEditCollection(ctx context.Context, id string) bool {
	if id == "" {
		return false
	}
	c, err := t.collectionStore.Get(ctx, id)
	if err != nil || c == nil || c.DeletedAt != nil {
		return false
	}
	if c.OwnerID == resolveOwnerID(ctx) {
		return true
	}
	perm, _ := t.shareStore.GetUserCollectionPermission(ctx, id, resolveOwnerID(ctx), resolveOwnerEmail(ctx))
	return perm == portal.PermissionEditor
}

// threadScopeFromInput resolves the single target scope for list_threads.
func threadScopeFromInput(input manageFeedbackInput) (string, bool) {
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

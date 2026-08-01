package portal

import "github.com/txn2/mcp-data-platform/internal/portal/feedbackapi"

// The feedback surface — threads, the activity feed, the worklists, sign-off,
// validation, and capturing a thread as an insight — lives in
// internal/portal/feedbackapi. These aliases bind the contracts it declares to
// the names callers already wire into Deps, and re-export the target-gathering
// policy the manage_feedback MCP tool builds on.

// MentionResolver returns the addresses a comment body delivers an @-mention
// to on a thread target: the names written in the body, minus the author's own
// address, filtered to the people who can open that target. Implemented by
// pkg/portal/mention, which the composition root injects; a nil resolver
// disables mentions (no database), leaving every token ordinary text.
type MentionResolver = feedbackapi.MentionResolver

// ChangesetReader provides read access to knowledge changesets, used to surface
// the thread -> insight -> changeset chain on a feedback thread.
type ChangesetReader = feedbackapi.ChangesetReader

// MemoryWriter inserts memory records. It backs the "capture feedback as an
// insight" path: a reviewer turns a feedback thread into a pending,
// knowledge-dimension memory record that enters the apply_knowledge review
// queue. The full memory.Store satisfies it; the portal only needs Insert.
type MemoryWriter = feedbackapi.MemoryWriter

// ShareKeep decides whether a share grant counts toward a gathered target set.
// KeepEditorShares is the "I can act on it" scope (worklist, MCP agent);
// KeepAnyShare is the "I can view it" scope (activity feed).
type ShareKeep = feedbackapi.ShareKeep

// TargetGatherer gathers the asset/collection ids a single user can reach,
// bundling the stores and identity so callers (REST worklist/activity feed, the
// manage_feedback MCP tool) build it once and ask for asset or collection ids
// with the desired share scope.
type TargetGatherer = feedbackapi.TargetGatherer

// KeepEditorShares keeps only editor-permission shares.
func KeepEditorShares(p SharePermission) bool { return feedbackapi.KeepEditorShares(p) }

// KeepAnyShare keeps shares at any permission.
func KeepAnyShare(p SharePermission) bool { return feedbackapi.KeepAnyShare(p) }

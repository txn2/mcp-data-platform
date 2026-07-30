package portal

// The thread data layer (types, Postgres store, filters, constants) lives in the
// pkg/portal/threads sub-package, and the HTTP handlers that drive it live in
// internal/portal/feedbackapi. These aliases bind the moved symbols to their
// original portal names so out-of-package callers — the manage_feedback MCP
// toolkit, the platform feedback bridge, the notification delivery bridge —
// compile unchanged.
//
// An alias is exported only where a caller outside pkg/portal names it; the rest
// are lowercase, because an exported alias with no external caller widens the
// package's public API for nothing (#1077). Reach for threads.X directly rather
// than adding an exported alias here.

import "github.com/txn2/mcp-data-platform/pkg/portal/threads"

// Thread is re-exported from pkg/portal/threads.
type Thread = threads.Thread

// ThreadEvent is re-exported from pkg/portal/threads.
type ThreadEvent = threads.ThreadEvent

// ThreadWithMeta is re-exported from pkg/portal/threads.
type ThreadWithMeta = threads.ThreadWithMeta

// ThreadFilter is re-exported from pkg/portal/threads.
type ThreadFilter = threads.ThreadFilter

// ThreadUpdate is re-exported from pkg/portal/threads.
type ThreadUpdate = threads.ThreadUpdate

// ThreadStore is re-exported from pkg/portal/threads.
type ThreadStore = threads.ThreadStore

// ValidationResponse is re-exported from pkg/portal/threads.
type ValidationResponse = threads.ValidationResponse

// Thread data-layer constructors.
var (
	NewPostgresThreadStore = threads.NewPostgresThreadStore
	NewThreadEventID       = threads.NewThreadEventID
)

// Thread kinds.
const (
	ThreadKindComment    = threads.ThreadKindComment
	ThreadKindQuestion   = threads.ThreadKindQuestion
	ThreadKindCorrection = threads.ThreadKindCorrection
	ThreadKindRating     = threads.ThreadKindRating
)

// ThreadStatusResolved is re-exported from pkg/portal/threads.
const ThreadStatusResolved = threads.ThreadStatusResolved

// Thread event types.
const (
	EventTypeComment       = threads.EventTypeComment
	EventTypeInsightLinked = threads.EventTypeInsightLinked
)

// Validation states.
const (
	ValidationStatePending   = threads.ValidationStatePending
	ValidationStateValidated = threads.ValidationStateValidated
	ValidationStateDisputed  = threads.ValidationStateDisputed
)

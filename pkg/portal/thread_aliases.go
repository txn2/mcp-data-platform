package portal

// The thread data layer (types, Postgres store, filters, constants) lives in the
// pkg/portal/threads sub-package; the HTTP handlers that drive it stay here in
// portal (they are welded to the shared User/auth/HTTP foundation). These
// aliases bind the moved symbols to their original portal names so the handlers
// and their tests compile unchanged. Decomposition gate (#594): keeping the bulk
// data layer out of the portal package holds it under its size budget.
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

// Thread data-layer constructors and helpers.
var (
	NewPostgresThreadStore = threads.NewPostgresThreadStore
	NewThreadEventID       = threads.NewThreadEventID
	newThreadID            = threads.NewThreadID
	validThreadKind        = threads.ValidThreadKind
	validThreadStatus      = threads.ValidThreadStatus
	deriveFirstEventType   = threads.DeriveFirstEventType
)

const (
	defaultThreadLimit = threads.DefaultThreadLimit
	maxThreadLimit     = threads.MaxThreadLimit
)

// Thread kinds.
const (
	ThreadKindComment    = threads.ThreadKindComment
	ThreadKindQuestion   = threads.ThreadKindQuestion
	ThreadKindCorrection = threads.ThreadKindCorrection
	ThreadKindRating     = threads.ThreadKindRating
	threadKindApproval   = threads.ThreadKindApproval
	threadKindRejection  = threads.ThreadKindRejection
	threadKindSuggestion = threads.ThreadKindSuggestion
)

// Thread statuses.
const (
	threadStatusOpen         = threads.ThreadStatusOpen
	threadStatusAnswered     = threads.ThreadStatusAnswered
	ThreadStatusResolved     = threads.ThreadStatusResolved
	threadStatusWontFix      = threads.ThreadStatusWontFix
	threadStatusAcknowledged = threads.ThreadStatusAcknowledged
)

// Thread event types.
const (
	EventTypeComment           = threads.EventTypeComment
	eventTypeStatusChange      = threads.EventTypeStatusChange
	eventTypeResolution        = threads.EventTypeResolution
	eventTypeRating            = threads.EventTypeRating
	eventTypeApproval          = threads.EventTypeApproval
	eventTypeRejection         = threads.EventTypeRejection
	eventTypeValidationRequest = threads.EventTypeValidationRequest
	eventTypeValidationResult  = threads.EventTypeValidationResult
	EventTypeInsightLinked     = threads.EventTypeInsightLinked
	eventTypeChangesetLinked   = threads.EventTypeChangesetLinked
)

// Validation states.
const (
	validationStateNone      = threads.ValidationStateNone
	ValidationStatePending   = threads.ValidationStatePending
	ValidationStateValidated = threads.ValidationStateValidated
	ValidationStateDisputed  = threads.ValidationStateDisputed
)

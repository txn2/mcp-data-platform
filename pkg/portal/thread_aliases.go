package portal

// The thread data layer (types, Postgres store, filters, constants) lives in the
// pkg/portal/threads sub-package; the HTTP handlers that drive it stay here in
// portal (they are welded to the shared User/auth/HTTP foundation). These
// aliases re-export the moved symbols under their original portal names so the
// handlers and their tests compile unchanged. Decomposition gate (#594): keeping
// the bulk data layer out of the portal package holds it under its size budget.

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
	NewPostgresThreadStore     = threads.NewPostgresThreadStore
	NewThreadEventID           = threads.NewThreadEventID
	newThreadID                = threads.NewThreadID
	ValidThreadKind            = threads.ValidThreadKind
	ValidThreadStatus          = threads.ValidThreadStatus
	ValidThreadValidationState = threads.ValidThreadValidationState
	deriveFirstEventType       = threads.DeriveFirstEventType
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
	ThreadKindApproval   = threads.ThreadKindApproval
	ThreadKindRejection  = threads.ThreadKindRejection
	ThreadKindSuggestion = threads.ThreadKindSuggestion
)

// Thread statuses.
const (
	ThreadStatusOpen         = threads.ThreadStatusOpen
	ThreadStatusAnswered     = threads.ThreadStatusAnswered
	ThreadStatusResolved     = threads.ThreadStatusResolved
	ThreadStatusWontFix      = threads.ThreadStatusWontFix
	ThreadStatusAcknowledged = threads.ThreadStatusAcknowledged
)

// Thread event types.
const (
	EventTypeComment           = threads.EventTypeComment
	EventTypeStatusChange      = threads.EventTypeStatusChange
	EventTypeResolution        = threads.EventTypeResolution
	EventTypeRating            = threads.EventTypeRating
	EventTypeApproval          = threads.EventTypeApproval
	EventTypeRejection         = threads.EventTypeRejection
	EventTypeValidationRequest = threads.EventTypeValidationRequest
	EventTypeValidationResult  = threads.EventTypeValidationResult
	EventTypeInsightLinked     = threads.EventTypeInsightLinked
	EventTypeChangesetLinked   = threads.EventTypeChangesetLinked
)

// Validation states.
const (
	ValidationStateNone      = threads.ValidationStateNone
	ValidationStatePending   = threads.ValidationStatePending
	ValidationStateValidated = threads.ValidationStateValidated
	ValidationStateDisputed  = threads.ValidationStateDisputed
)

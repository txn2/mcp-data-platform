package notification

import "context"

// HistoryFilter narrows a notification history listing. A zero value lists
// everything, newest first.
//
// Recipient is matched exactly against the stored address, which is always
// NormalizeAddress form, so a caller scoping a listing to one person must
// normalize before filtering. The self-scoped user view depends on that:
// its whole authorization is this one field.
type HistoryFilter struct {
	// Recipient scopes the listing to one address. Empty means every
	// recipient, which only an admin-gated caller may ask for.
	Recipient string
	// Status is one of the Status* constants. Empty means any status.
	Status string
	// Category is one of the Category* constants. Empty means any category.
	Category string
	// Limit bounds the page; zero or negative means DefaultHistoryLimit.
	// Values above MaxHistoryLimit are clamped.
	Limit int
	// Offset is the page start.
	Offset int
}

// History listing bounds.
const (
	// DefaultHistoryLimit is the page size a listing uses when none is asked
	// for.
	DefaultHistoryLimit = 50
	// MaxHistoryLimit caps a caller-supplied page size.
	MaxHistoryLimit = 200
)

// EffectiveLimit resolves the page size the store will apply.
func (f HistoryFilter) EffectiveLimit() int {
	if f.Limit <= 0 {
		return DefaultHistoryLimit
	}
	if f.Limit > MaxHistoryLimit {
		return MaxHistoryLimit
	}
	return f.Limit
}

// HistoryStore reads the delivery history the queue leaves behind: what was
// sent, what failed and why, and what is still waiting.
//
// It is a separate contract from QueueStore because it is a separate concern
// with a separate audience. QueueStore is the worker's write path and must
// stay small; this is the read path two UI surfaces sit on -- the admin
// monitoring tab and each user's own activity screen.
//
// What it can show is bounded by the worker's retention pass: resolved rows
// are purged after notifyworker.DefaultResolvedRetention, so this is recent
// history, not an archive. Both surfaces state that window to the reader.
type HistoryStore interface {
	// List returns one page of notifications, newest first.
	List(ctx context.Context, filter HistoryFilter) ([]Notification, error)
	// Count returns how many rows match the filter, ignoring its paging
	// fields.
	Count(ctx context.Context, filter HistoryFilter) (int, error)
	// CountsByStatus returns the per-status row counts for the filter,
	// keyed by the Status* constants. It honors every field of the filter,
	// so a caller wanting the whole breakdown of a status-filtered view
	// clears Status first.
	CountsByStatus(ctx context.Context, filter HistoryFilter) (map[string]int, error)
}

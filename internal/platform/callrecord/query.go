package callrecord

import (
	"net/url"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
)

// The call catalog is served twice: once to an operator over every record, and
// once to a user over their own. The query-string vocabulary those two surfaces
// accept is one vocabulary, parsed here, so a facet cannot come to mean one
// thing on the admin route and another on the portal route.
//
// One parameter is deliberately absent: the caller. Who a listing is scoped to
// is the whole difference between the two surfaces, so each sets Filter.UserID
// itself, and neither can acquire the other's reach by accident.

const (
	// DefaultPerPage is the page size when the caller states none.
	DefaultPerPage = 25
	// MaxPerPage caps a caller-stated page size. Each row derives its outcome
	// from what cites it, so an uncapped page is an uncapped query.
	MaxPerPage = 200
)

// FilterFromQuery reads the query string into a call filter, leaving UserID for
// the caller to set. An unparseable or unknown value is treated as absent
// rather than as an error: the failure mode of a bad filter is an unfiltered or
// empty page, never a 400 the UI has to model.
func FilterFromQuery(q url.Values) Filter {
	f := Filter{
		Kind:           kindOrEmpty(q.Get("kind")),
		Connection:     q.Get("connection"),
		Target:         q.Get("target"),
		SessionID:      q.Get("session_id"),
		Search:         q.Get("q"),
		PromotableOnly: q.Get("queue") == "promotable",
	}
	if outcome := q.Get("outcome"); ValidOutcome(outcome) {
		f.Outcome = outcome
	}
	f.Limit = ClampPerPage(httpjson.ParseLimit(q))
	f.Offset = httpjson.ParsePageOffset(q, f.Limit)
	return f
}

// kindOrEmpty keeps a stated kind only when it names one. An unknown kind is
// dropped rather than passed through as a value nothing matches, so a typo
// shows the unfiltered list instead of an empty one that looks like an answer.
func kindOrEmpty(kind string) string {
	if kind == KindSQL || kind == KindAPI {
		return kind
	}
	return ""
}

// ClampPerPage bounds a requested page size into [1, MaxPerPage].
func ClampPerPage(limit int) int {
	switch {
	case limit <= 0:
		return DefaultPerPage
	case limit > MaxPerPage:
		return MaxPerPage
	default:
		return limit
	}
}

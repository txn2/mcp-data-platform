package sessionview

import (
	"net/url"
	"strconv"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
)

// The session list is served twice: once to an operator over every session, and
// once to a user over their own. The query-string vocabulary those two surfaces
// accept is one vocabulary, and it is parsed here so a facet cannot come to mean
// one thing on the admin route and another on the portal route.
//
// One parameter is deliberately absent: the caller. Who a listing is scoped to
// is the difference between the two surfaces, so each sets Filter.UserID itself
// — the operator from a facet, the portal from the authenticated session — and
// neither can acquire the other's behavior by accident.

const (
	// DefaultPerPage is the page size when the caller states none.
	DefaultPerPage = 25
	// MaxPerPage caps a caller-stated page size. A session list row is an
	// aggregate over an unbounded number of audit rows, so an uncapped page
	// is an uncapped query.
	MaxPerPage = 200
)

// FilterFromQuery reads the query string into a session filter, leaving UserID
// for the caller to set. An unparseable value is treated as absent rather than
// as an error: the failure mode of a bad filter is an unfiltered or empty page,
// never a 400 the UI has to model.
func FilterFromQuery(q url.Values) Filter {
	filter := Filter{
		Kind:        Kind(q.Get("kind")),
		StartTime:   httpjson.ParseTimeParam(q, "start_time"),
		EndTime:     httpjson.ParseTimeParam(q, "end_time"),
		HasAssets:   parseBool(q.Get("has_assets")),
		HasFailures: parseBool(q.Get("has_failures")),
	}
	filter.Limit = ClampPerPage(httpjson.ParseLimit(q))
	filter.Offset = httpjson.ParsePageOffset(q, filter.Limit)
	return filter
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

// parseBool reads a boolean flag, treating anything unparseable as unset.
func parseBool(v string) bool {
	b, err := strconv.ParseBool(v)
	return err == nil && b
}

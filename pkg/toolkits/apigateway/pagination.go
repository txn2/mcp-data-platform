package apigateway

import "github.com/txn2/mcp-data-platform/internal/pagewalk"

// The pagination signal and the page walk live in internal/pagewalk
// (issue #1535); these names are the ones the gateway's inputs and
// outputs carry.
type (
	// PaginationInfo is the pagination signal reported on a response.
	PaginationInfo = pagewalk.PaginationInfo
	// PaginateInput is the `paginate` block both tools take.
	PaginateInput = pagewalk.PaginateInput
	// WalkStats is what a walk reports: pages_fetched, items_merged,
	// stopped_by.
	WalkStats = pagewalk.WalkStats
)

// detectPagination reports a response's pagination signal, nil when it
// carries none.
func detectPagination(headers map[string][]string, body any) *PaginationInfo {
	return pagewalk.Detect(headers, body)
}

package apigateway

import (
	"github.com/txn2/mcp-data-platform/internal/exporttrunc"
	"github.com/txn2/mcp-data-platform/internal/pagewalk"
)

// What api_export does when a page walk stops at its page bound with another
// page to fetch (#2057): the judgment is exporttrunc's, shared with
// trino_export and graphql_export; what is here is the walk's side of it.

// walkCapRemedy is what a refusal at the default page bound offers instead.
const walkCapRemedy = "Set paginate.max_pages (up to 10000) to walk further, or narrow the request " +
	"(a filter, a date range) so fewer pages answer it."

// judgeWalk decides what a finished walk writes: the walk stopped by its bound
// was cut, since the walk stops there only when the page it fetched last
// named another.
func judgeWalk(walk *pagewalk.Walk, in exportInput) exporttrunc.Outcome {
	pages, callerSet := in.Paginate.Bound()
	return exporttrunc.Judge(exporttrunc.Judgment{
		Limit:       exporttrunc.WalkLimit(pages, callerSet, walkCapRemedy),
		Truncated:   walk.Stats.StoppedBy == pagewalk.StoppedByMaxPages,
		Written:     walk.Stats.ItemsMerged,
		WrittenUnit: "items",
		Options:     in.Options,
	})
}

// runVersion is what a version a script run's named export writes records
// beyond its content: who wrote it and what the export said about it.
type runVersion struct {
	createdBy string
	metadata  map[string]any
}

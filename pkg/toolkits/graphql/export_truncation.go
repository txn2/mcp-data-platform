package graphql

import (
	"github.com/txn2/mcp-data-platform/internal/exporttrunc"
)

// What graphql_export does when a page walk stops at its page bound with
// another page to fetch (#2057): the judgment is exporttrunc's, shared with
// trino_export and api_export; what is here is the walk's side of it.

// walkCapRemedy is what a refusal at the default page bound offers instead.
const walkCapRemedy = "Set paginate.max_pages (up to 1000) to walk further, or narrow the document's " +
	"arguments (a filter, a date range) so fewer pages answer it."

// judgeWalk decides what a walk's result writes, or nil for an export that
// walked nothing. The walk reports max_pages only when the upstream named
// another page past the bound, so that is the cut.
func judgeWalk(result *QueryOutput, in exportInput) *exporttrunc.Outcome {
	if in.Paginate == nil || result.Pagination == nil {
		return nil
	}
	pages, callerSet := DefaultMaxPages, in.Paginate.MaxPages > 0
	if callerSet {
		pages = in.Paginate.MaxPages
	}
	out := exporttrunc.Judge(exporttrunc.Judgment{
		Limit:       exporttrunc.WalkLimit(pages, callerSet, walkCapRemedy),
		Truncated:   result.Pagination.StoppedBy == StoppedByMaxPages,
		Written:     result.Pagination.ItemsMerged,
		WrittenUnit: "items",
		Options:     in.Options,
	})
	return &out
}

// reportOf is the judgment's report, or nil for no judgment.
func reportOf(cut *exporttrunc.Outcome) *exporttrunc.Report {
	if cut == nil {
		return nil
	}
	return &cut.Report
}

// noteOf is the judgment's note, or "" for no judgment.
func noteOf(cut *exporttrunc.Outcome) string {
	if cut == nil {
		return ""
	}
	return cut.Note
}

// persisted is what persist writes: the payload, and the walk's report when
// one was judged.
type persisted struct {
	payload []byte
	cut     *exporttrunc.Report
}

// runVersion is what a version records beyond its content: who wrote it and
// what the export said about it.
type runVersion struct {
	createdBy string
	metadata  map[string]any
}

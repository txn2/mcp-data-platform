package trino

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	trinotools "github.com/txn2/mcp-trino/pkg/tools"

	"github.com/txn2/mcp-data-platform/internal/inlinefit"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// fittedQuery is a trino_query result cut to a model client's context
// budget: the query's own output with its rows cut to the head that fits,
// and what says so. row_count stays the number of rows the query returned,
// so rows_shown against it is how much was withheld.
type fittedQuery struct {
	trinotools.QueryOutput
	ResultTruncated bool           `json:"result_truncated"`
	RowsShown       int            `json:"rows_shown"`
	Hint            string         `json:"hint"`
	ExportArguments map[string]any `json:"export_arguments,omitempty"`
}

// FitResult holds a trino_query result to a model client's context budget
// (#1878) by keeping the head of its rows: the most whole rows whose
// rendering, in the format the caller asked for, fits. The result is
// flagged, and the trino_export call that writes every row to an asset is
// handed back when this deployment registers it. Called by the platform's
// result-budget middleware, and only for a result past the budget. A
// result it cannot read, or one whose header alone is past the budget, is
// declined and cut by the generic text cut.
func (t *Toolkit) FitResult(tool string, args json.RawMessage, res *mcp.CallToolResult, budget int) bool {
	if tool != toolQuery {
		return false
	}
	var out trinotools.QueryOutput
	if !toolkit.DecodeStructured(res.StructuredContent, &out) {
		return false
	}
	var in trinotools.QueryInput
	if len(args) > 0 && json.Unmarshal(args, &in) != nil {
		return false
	}
	var head func(k int) string
	if !isJSONFormat(in.Format) {
		var err error
		if head, err = rowHeads(in.Format, firstText(res), len(out.Rows)); err != nil {
			return false
		}
	}
	fit := func(k int) (*fittedQuery, []byte, bool) {
		f := t.cutRows(out, in, k, budget)
		text, ok := renderFitted(f, head, budget)
		return f, text, ok
	}
	// The rendering grows with every row kept, so the most rows that fit
	// is searched for by bisection.
	lo, hi, best := 0, len(out.Rows), -1
	for lo <= hi {
		mid := (lo + hi) / 2
		if _, _, ok := fit(mid); ok {
			best, lo = mid, mid+1
		} else {
			hi = mid - 1
		}
	}
	if best < 0 {
		return false
	}
	f, text, _ := fit(best)
	return toolkit.SetFittedResult(res, text, f) == nil
}

// cutRows is out with its first k rows kept, flagged and steered.
func (t *Toolkit) cutRows(out trinotools.QueryOutput, in trinotools.QueryInput, k, budget int) *fittedQuery {
	out.Rows = out.Rows[:k]
	f := &fittedQuery{QueryOutput: out, ResultTruncated: true, RowsShown: k}
	f.Hint = fmt.Sprintf("%d of %d rows are shown: the result is past this client's context budget on a tool result (%d, tools.result_budget). ",
		k, out.RowCount, budget)
	if t.exportDeps == nil {
		f.Hint += "Narrow the query, or lower its limit, to see the rest."
		return f
	}
	f.Hint += "Call trino_export with export_arguments plus a name to write every row to an asset (no model-context cost), or narrow the query."
	f.ExportArguments = map[string]any{"sql": in.SQL}
	if in.Connection != "" {
		f.ExportArguments["connection"] = in.Connection
	}
	if in.Limit > 0 {
		f.ExportArguments["limit"] = in.Limit
	}
	return f
}

// renderFitted renders a fitted result's text in the caller's format. A
// JSON result is the fitted value itself, indented when that fits and
// compact when only that does. A CSV or Markdown result is the head of the
// text the query rendered, cut after the rows kept, followed by the hint;
// head is nil for JSON.
func renderFitted(f *fittedQuery, head func(rows int) string, budget int) ([]byte, bool) {
	if head == nil {
		return inlinefit.RenderWithin(f, budget)
	}
	text := head(f.RowsShown) + "\n[" + f.Hint + "]"
	if len(f.ExportArguments) > 0 {
		args, err := json.Marshal(f.ExportArguments)
		if err != nil {
			return nil, false
		}
		text += "\nexport_arguments: " + string(args)
	}
	return []byte(text), len(text) <= budget
}

// isJSONFormat reports whether a trino_query result is rendered as JSON,
// which a fit re-renders rather than cuts: the format the caller named, or
// none, which mcp-trino renders as JSON.
func isJSONFormat(format string) bool {
	f := strings.ToLower(format)
	return f == "" || f == formatJSON
}

// rowHeads returns, for a CSV or Markdown result, the function giving the
// head of text through its header and first k rows. A text holding fewer
// rows than the structured output is refused, since a head cut from it would
// not match the rows the structured copy keeps.
func rowHeads(format, text string, rows int) (func(k int) string, error) {
	var ends []int
	var err error
	switch strings.ToLower(format) {
	case formatCSV:
		ends, err = csvRecordEnds(text, rows+1)
	case formatMarkdown:
		ends, err = lineEnds(text, rows+2)
	default:
		return nil, fmt.Errorf("trino: no row cut for format %q", format)
	}
	if err != nil {
		return nil, err
	}
	header := len(ends) - rows
	return func(k int) string { return text[:ends[header-1+k]] }, nil
}

// csvRecordEnds is the offset just past each of the first n records of a
// CSV text, following quoted fields across line breaks the way the text
// was escaped.
func csvRecordEnds(text string, n int) ([]int, error) {
	r := csv.NewReader(strings.NewReader(text))
	r.FieldsPerRecord = -1
	r.ReuseRecord = true
	ends := make([]int, 0, n)
	for range n {
		if _, err := r.Read(); err != nil {
			if errors.Is(err, io.EOF) {
				return nil, errors.New("trino: the CSV text holds fewer records than the result has rows")
			}
			return nil, fmt.Errorf("trino: reading the CSV text: %w", err)
		}
		ends = append(ends, int(r.InputOffset()))
	}
	return ends, nil
}

// lineEnds is the offset just past each of the first n lines of text.
func lineEnds(text string, n int) ([]int, error) {
	ends := make([]int, 0, n)
	off := 0
	for range n {
		i := strings.IndexByte(text[off:], '\n')
		if i < 0 {
			return nil, errors.New("trino: the text holds fewer lines than the result has rows")
		}
		off += i + 1
		ends = append(ends, off)
	}
	return ends, nil
}

// firstText is a result's first text block, or "".
func firstText(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			return t.Text
		}
	}
	return ""
}

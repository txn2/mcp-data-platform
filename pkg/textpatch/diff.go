package textpatch

import (
	"fmt"
	"strings"
)

// Diff rendering limits.
const (
	// defaultDiffContext is how many unchanged lines surround each hunk.
	defaultDiffContext = 3

	// maxDiffCells bounds the LCS table. Patches produce localized changes,
	// so trimming the common prefix and suffix collapses realistic inputs far
	// below this; a pair of documents that differ throughout falls back to a
	// single whole-block hunk rather than allocating without bound.
	maxDiffCells = 4_000_000
)

// diffTag marks a line's role in the edit script.
type diffTag byte

const (
	tagEqual  diffTag = ' '
	tagDelete diffTag = '-'
	tagInsert diffTag = '+'
)

// diffLine is one line of the edit script.
type diffLine struct {
	tag  diffTag
	text string
}

// UnifiedDiff renders the changed hunks between two bodies in unified diff
// format, with context unchanged lines around each hunk (0 uses the default).
// It returns "" when the bodies are identical.
//
// Unified diff is the platform's output format for a change and never an input
// format: as output the server generates it from two known strings, which makes
// it the most compact accurate description of what changed and the one a model
// reads most fluently.
func UnifiedDiff(oldBody, newBody string, context int) string {
	a, b := splitLines(oldBody), splitLines(newBody)

	// Trim the identical head and tail as counts rather than expanding them
	// into the edit script: a one-line change in a large document then costs
	// the size of the change, not the size of the document.
	prefix := commonPrefix(a, b)
	suffix := commonSuffix(a[prefix:], b[prefix:])
	script := diffMiddle(a[prefix:len(a)-suffix], b[prefix:len(b)-suffix])

	return renderHunks(script, trimmed{
		a: a, b: b, prefix: prefix, suffix: suffix, context: context,
	})
}

// trimmed carries the untouched head and tail around a diffed middle, so the
// renderer can supply context lines and absolute line numbers without the
// script holding every unchanged line.
type trimmed struct {
	a, b    []string
	prefix  int
	suffix  int
	context int
}

// UnifiedDiffLabeled is UnifiedDiff with the conventional ---/+++ file header,
// used when comparing two named versions.
func UnifiedDiffLabeled(oldBody, newBody, oldLabel, newLabel string, context int) string {
	body := UnifiedDiff(oldBody, newBody, context)
	if body == "" {
		return ""
	}
	return fmt.Sprintf("--- %s\n+++ %s\n%s", oldLabel, newLabel, body)
}

// splitLines splits a body into lines without their terminators.
func splitLines(body string) []string {
	if body == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(body, "\n"), "\n")
}

// diffMiddle diffs the region that remains after trimming the common prefix
// and suffix, falling back to a whole-block replacement when the LCS table
// would exceed maxDiffCells.
func diffMiddle(a, b []string) []diffLine {
	switch {
	case len(a) == 0 && len(b) == 0:
		return nil
	case len(a) == 0 || len(b) == 0 || (len(a)+1)*(len(b)+1) > maxDiffCells:
		return wholeBlock(a, b)
	default:
		return backtrack(lcsTable(a, b), a, b)
	}
}

// wholeBlock renders a region as a delete of everything followed by an insert
// of everything.
func wholeBlock(a, b []string) []diffLine {
	out := make([]diffLine, 0, len(a)+len(b))
	for _, line := range a {
		out = append(out, diffLine{tagDelete, line})
	}
	for _, line := range b {
		out = append(out, diffLine{tagInsert, line})
	}
	return out
}

// commonPrefix returns how many leading lines a and b share.
func commonPrefix(a, b []string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// commonSuffix returns how many trailing lines a and b share.
func commonSuffix(a, b []string) int {
	n := 0
	for n < len(a) && n < len(b) && a[len(a)-1-n] == b[len(b)-1-n] {
		n++
	}
	return n
}

// lcs is the longest-common-subsequence length table over a diffed middle,
// stored as one flat allocation. A row-per-slice layout costs one allocation
// per line of the older side, which a lopsided comparison (a long document
// against a short one) turns into hundreds of thousands of them.
type lcs struct {
	cells  []int32
	stride int
}

// at returns the LCS length of a[i:] against b[j:].
func (t lcs) at(i, j int) int32 { return t.cells[i*t.stride+j] }

// lcsTable builds the longest-common-subsequence length table for a and b.
// Lengths cannot exceed the line count, so int32 is safe under maxDiffCells.
func lcsTable(a, b []string) lcs {
	table := lcs{cells: make([]int32, (len(a)+1)*(len(b)+1)), stride: len(b) + 1}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				table.cells[i*table.stride+j] = table.at(i+1, j+1) + 1
				continue
			}
			table.cells[i*table.stride+j] = max(table.at(i+1, j), table.at(i, j+1))
		}
	}
	return table
}

// backtrack walks the LCS table into an edit script, emitting deletions before
// insertions at each divergence so hunks read as "old then new".
func backtrack(table lcs, a, b []string) []diffLine {
	out := make([]diffLine, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, diffLine{tagEqual, a[i]})
			i, j = i+1, j+1
		case table.at(i+1, j) >= table.at(i, j+1):
			out = append(out, diffLine{tagDelete, a[i]})
			i++
		default:
			out = append(out, diffLine{tagInsert, b[j]})
			j++
		}
	}
	return append(out, wholeBlock(a[i:], b[j:])...)
}

// hunk is one contiguous run of changes with its surrounding context.
type hunk struct {
	oldStart, oldCount int
	newStart, newCount int
	lines              []diffLine
}

// renderHunks groups a diffed middle into unified-diff hunks and formats them,
// drawing context lines from the untouched head and tail that were trimmed
// before diffing.
func renderHunks(middle []diffLine, t trimmed) string {
	if t.context <= 0 {
		t.context = defaultDiffContext
	}
	changed := changedIndexes(middle)
	if len(changed) == 0 {
		return ""
	}

	var out strings.Builder
	for _, h := range buildHunks(middle, changed, t) {
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", h.oldStart, h.oldCount, h.newStart, h.newCount)
		for _, line := range h.lines {
			out.WriteByte(byte(line.tag))
			out.WriteString(line.text)
			out.WriteByte('\n')
		}
	}
	return out.String()
}

// changedIndexes returns the script positions that are not equal lines.
func changedIndexes(script []diffLine) []int {
	var out []int
	for i, line := range script {
		if line.tag != tagEqual {
			out = append(out, i)
		}
	}
	return out
}

// buildHunks groups changed positions that sit within 2*context of each other
// into one hunk and attaches the surrounding context lines, which may come from
// the trimmed head or tail rather than from the diffed middle.
func buildHunks(middle []diffLine, changed []int, t trimmed) []hunk {
	oldNo, newNo := lineNumbers(middle, t.prefix)

	var hunks []hunk
	for start := 0; start < len(changed); {
		end := start
		for end+1 < len(changed) && changed[end+1]-changed[end] <= 2*t.context {
			end++
		}
		hunks = append(hunks, t.hunkAround(middle, oldNo, newNo, changed[start], changed[end]))
		start = end + 1
	}
	return hunks
}

// hunkAround assembles one hunk: leading context taken from the trimmed head
// when the change sits at the start of the middle, the middle's own lines, and
// trailing context taken from the trimmed tail when it sits at the end.
func (t trimmed) hunkAround(middle []diffLine, oldNo, newNo []int, first, last int) hunk {
	from := max(0, first-t.context)
	to := min(len(middle)-1, last+t.context)

	// The head may hold fewer lines than the requested context, so the hunk's
	// start coordinates shift by what was actually taken, not by what was asked.
	head := t.headContext(t.context - (first - from))
	tail := t.tailContext(t.context - (to - last))

	lines := make([]diffLine, 0, len(head)+(to-from+1)+len(tail))
	for _, text := range head {
		lines = append(lines, diffLine{tagEqual, text})
	}
	lines = append(lines, middle[from:to+1]...)
	for _, text := range tail {
		lines = append(lines, diffLine{tagEqual, text})
	}

	return makeHunk(lines, oldNo[from]-len(head), newNo[from]-len(head))
}

// headContext returns the last n lines of the trimmed common prefix.
func (t trimmed) headContext(n int) []string {
	n = min(n, t.prefix)
	if n <= 0 {
		return nil
	}
	return t.a[t.prefix-n : t.prefix]
}

// tailContext returns the first n lines of the trimmed common suffix.
func (t trimmed) tailContext(n int) []string {
	n = min(n, t.suffix)
	if n <= 0 {
		return nil
	}
	start := len(t.a) - t.suffix
	return t.a[start : start+n]
}

// lineNumbers returns, for each position in the diffed middle, the 1-based old
// and new line numbers it begins at, offset by the trimmed common prefix.
func lineNumbers(middle []diffLine, prefix int) (oldNo, newNo []int) {
	oldNo = make([]int, len(middle))
	newNo = make([]int, len(middle))
	o, n := prefix+1, prefix+1
	for i, line := range middle {
		oldNo[i], newNo[i] = o, n
		if line.tag != tagInsert {
			o++
		}
		if line.tag != tagDelete {
			n++
		}
	}
	return oldNo, newNo
}

// makeHunk counts the old and new lines a hunk spans and stamps its header
// coordinates.
func makeHunk(lines []diffLine, oldStart, newStart int) hunk {
	h := hunk{oldStart: oldStart, newStart: newStart, lines: lines}
	for _, line := range lines {
		if line.tag != tagInsert {
			h.oldCount++
		}
		if line.tag != tagDelete {
			h.newCount++
		}
	}
	return h
}

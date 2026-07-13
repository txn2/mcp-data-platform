package grade

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Execution-result grading (BIRD-style execution accuracy): a SQL-producing
// task's FINAL ANSWER is a query, which the harness executes and compares
// row-for-row against the reference query's result. Correctness is result-set
// equality, so two different-but-equivalent queries both pass. This file holds
// the pure, deterministic halves — extracting the query from the answer and
// comparing two result sets; the live execution seam lives in the pipeline.

// fencedBlock returns the content of the FIRST markdown code block in s (models
// frequently wrap SQL in ```sql ... ``` even when told to emit bare SQL), or
// reports isFenced=false when s has no opening fence. It closes on the first
// closing fence so a trailing second block of prose is not swallowed.
func fencedBlock(s string) (block string, isFenced bool) {
	_, rest, found := strings.Cut(strings.TrimSpace(s), "```")
	if !found {
		return "", false
	}
	// Drop an optional language tag on the opening fence line (e.g. "sql"), but
	// never a SQL keyword the model glued to the fence (```SELECT), which is the
	// start of the query, not a tag.
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		if tag := strings.TrimSpace(rest[:nl]); tag == "" || (!looksLikeQuery(tag) && !strings.ContainsAny(tag, " \t()") && len(tag) < 12) {
			rest = rest[nl+1:]
		}
	}
	if before, _, closed := strings.Cut(rest, "```"); closed {
		return strings.TrimSpace(before), true
	}
	return strings.TrimSpace(rest), true // unterminated fence: take the remainder
}

// ExtractSQL returns the SQL query from a final answer. A fenced answer yields
// the (possibly multi-line) fenced block; an unfenced answer yields only the
// FIRST line after the marker — the task prompt and the run's format
// instruction mandate the query go on the single FINAL ANSWER line or in a code
// block, so trailing prose on an unfenced answer is dropped rather than
// concatenated into an un-runnable query. Surrounding single backticks (inline
// code) and a trailing semicolon are stripped. ok is false when no SQL
// statement is present.
//
// The one shape this does not recover is an UNFENCED multi-line query with no
// code block: it is truncated to its first line. That combination violates both
// the prompt and the format instruction (which steer to one line or a fenced
// block), so it is an accepted, documented limitation rather than a supported
// input.
func ExtractSQL(text string) (sql string, ok bool) {
	final := ExtractFinal(text)
	if block, isFenced := fencedBlock(final); isFenced {
		final = block
	} else {
		final = firstLine(final)
	}
	final = strings.TrimSpace(final)
	final = strings.TrimSpace(strings.Trim(final, "`")) // inline single-backtick code
	final = strings.TrimSpace(strings.TrimSuffix(final, ";"))
	if !looksLikeQuery(final) {
		return "", false
	}
	return final, true
}

// looksLikeQuery is a cheap guard so a prose non-answer is not executed as SQL.
func looksLikeQuery(s string) bool {
	upper := strings.ToUpper(s)
	return strings.HasPrefix(upper, "SELECT") || strings.HasPrefix(upper, "WITH")
}

// ResultSetsEqual reports whether two result sets are equal as multisets of
// rows, comparing each row by its sorted cell VALUES (not column names), so
// column aliasing and column/row ordering do not matter — the BIRD execution-
// accuracy convention. Numeric cells are normalized so 300 and 300.0 match.
//
// Two consequences of the value-only convention are deliberate and bounded by
// the seeded task set: (1) two empty result sets compare equal, which is the
// correct reading when the reference query legitimately returns no rows — the
// generator's exec_sql tasks all group over populated dimensions (status,
// region, tier), so no reference is empty and a broken empty candidate cannot
// masquerade as a correct empty one; (2) a row whose cell values coincide as a
// multiset but are bound to different columns compares equal, which is the
// documented cost of tolerating column aliasing (the seeded tasks pair a string
// label with a numeric count, which do not transpose to the same values).
func ResultSetsEqual(got, want []map[string]any) bool {
	if len(got) != len(want) {
		return false
	}
	gc, wc := rowCounts(got), rowCounts(want)
	if len(gc) != len(wc) {
		return false
	}
	for k, n := range wc {
		if gc[k] != n {
			return false
		}
	}
	return true
}

// rowCounts maps each row's canonical form to its occurrence count.
func rowCounts(rows []map[string]any) map[string]int {
	counts := make(map[string]int, len(rows))
	for _, r := range rows {
		counts[canonicalRow(r)]++
	}
	return counts
}

// canonicalRow renders a row as its sorted, normalized cell values joined with a
// separator unlikely to appear in a value.
func canonicalRow(row map[string]any) string {
	cells := make([]string, 0, len(row))
	for _, v := range row {
		cells = append(cells, normalizeCell(v))
	}
	sort.Strings(cells)
	return strings.Join(cells, "\x1f")
}

// normalizeCell renders one cell value canonically: integral floats as integers,
// other floats at full precision, everything else via its default string form.
func normalizeCell(v any) string {
	switch n := v.(type) {
	case nil:
		return "\x00null"
	case float64:
		if n == float64(int64(n)) {
			return strconv.FormatInt(int64(n), 10)
		}
		return strconv.FormatFloat(n, 'f', -1, 64)
	case float32:
		return normalizeCell(float64(n))
	case int:
		return strconv.Itoa(n)
	case int64:
		return strconv.FormatInt(n, 10)
	case bool:
		return strconv.FormatBool(n)
	case string:
		return strings.TrimSpace(n)
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", v))
	}
}

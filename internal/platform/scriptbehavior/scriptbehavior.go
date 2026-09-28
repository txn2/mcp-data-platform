// Package scriptbehavior says what a new version of a managed script does
// differently from the saved one (#1942): where their outputs differ when both
// replay the same recorded run, and what the new version reaches that the
// saved one did not.
//
// A save with no difference and no new reach is a refactor and goes through.
// Any other save needs a plain-language summary of the change and the agent's
// confirmation that the person the automation runs for agreed to it; the
// differences are what that person is told.
package scriptbehavior

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrec"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/platform/scripttest"
)

// The kinds of difference.
const (
	KindOutput = "output"
	KindColumn = "column"
	KindRows   = "rows"
	KindState  = "state"
	KindNotify = "notification"
	KindResult = "result"
	KindCall   = "call"
	KindFails  = "failure"
	KindReach  = "reach"
)

// Difference is one thing the new version does differently.
type Difference struct {
	// Run is the recorded run the difference showed in, empty for reach.
	Run  string `json:"run,omitempty"`
	Kind string `json:"kind"`
	// Subject is what differs: an output's name, a column, a tool.
	Subject string `json:"subject"`
	Detail  string `json:"detail"`
}

// String is the difference as one sentence.
func (d Difference) String() string {
	if d.Run == "" {
		return d.Detail
	}
	return "run " + d.Run + ": " + d.Detail
}

// Compare lists where after, the new version's replay of a recorded run,
// differs from before, the saved version's. compared is false when the saved
// version itself does not replay the run -- it was recorded under an older
// version that made calls this one does not -- so the run says nothing about
// this change.
func Compare(run string, before, after scripttest.Outcome) (diffs []Difference, compared bool) {
	if before.Missing != "" {
		return nil, false
	}
	add := func(kind, subject, format string, args ...any) {
		diffs = append(diffs, Difference{Run: run, Kind: kind, Subject: subject, Detail: fmt.Sprintf(format, args...)})
	}
	if after.Missing != "" {
		add(KindCall, after.Missing, "makes %s, which the recorded run never made", after.Missing)
		return diffs, true
	}
	if after.Failure != "" && before.Failure == "" {
		add(KindFails, "", "fails where the saved version finished: %s", after.Failure)
		return diffs, true
	}
	diffs = append(diffs, compareExports(run, before.Exports, after.Exports)...)
	diffs = append(diffs, comparePublishes(run, before.Publishes, after.Publishes)...)
	if !sameJSON(before.State, after.State) {
		add(KindState, "state", "saves a different state: %s, where the saved version saved %s", jsonText(after.State), jsonText(before.State))
	}
	diffs = append(diffs, compareNotifies(run, before.Calls, after.Calls)...)
	if !sameJSON(raw(before.Result), raw(after.Result)) {
		add(KindResult, "platform.result", "returns %s from platform.result, where the saved version returned %s", orNone(after.Result), orNone(before.Result))
	}
	return diffs, true
}

// compareExports lists the outputs written, dropped or changed.
func compareExports(run string, before, after []scriptrun.ExportRequest) []Difference {
	var out []Difference
	add := func(kind, subject, format string, args ...any) {
		out = append(out, Difference{Run: run, Kind: kind, Subject: subject, Detail: fmt.Sprintf(format, args...)})
	}
	was := map[string]scriptrun.ExportRequest{}
	for _, r := range before {
		was[outputKey(r)] = r
	}
	seen := map[string]bool{}
	for _, a := range after {
		key := outputKey(a)
		seen[key] = true
		b, ok := was[key]
		if !ok {
			add(KindOutput, a.Name, "writes a new output %q", a.Name)
			continue
		}
		out = append(out, compareExport(run, b, a)...)
	}
	for _, b := range before {
		if !seen[outputKey(b)] {
			add(KindOutput, b.Name, "no longer writes output %q", b.Name)
		}
	}
	return out
}

func outputKey(r scriptrun.ExportRequest) string { return r.Name + "\x00" + r.Destination.Name }

// compareExport lists how one output changed: its format, its columns and
// their types, and its rows.
func compareExport(run string, b, a scriptrun.ExportRequest) []Difference {
	var out []Difference
	add := func(kind, subject, format string, args ...any) {
		out = append(out, Difference{Run: run, Kind: kind, Subject: subject, Detail: fmt.Sprintf(format, args...)})
	}
	if a.Format != b.Format {
		add(KindOutput, a.Name, "output %q is written as %s, where it was %s", a.Name, a.Format, b.Format)
	}
	out = append(out, compareColumns(run, b, a)...)
	switch {
	case a.RowCount() != b.RowCount():
		add(KindRows, a.Name, "output %q has %d rows, where it had %d", a.Name, a.RowCount(), b.RowCount())
	case !sameJSON(content(a), content(b)):
		add(KindRows, a.Name, "output %q has the same number of rows with different values", a.Name)
	}
	return out
}

// compareColumns lists the columns an output gained, lost, or holds a
// different type in.
func compareColumns(run string, b, a scriptrun.ExportRequest) []Difference {
	var out []Difference
	add := func(subject, format string, args ...any) {
		out = append(out, Difference{Run: run, Kind: KindColumn, Subject: subject, Detail: fmt.Sprintf(format, args...)})
	}
	bt, at := columnTypes(b), columnTypes(a)
	for _, c := range columnsOf(a) {
		t, had := bt[c]
		switch {
		case !had:
			add(c, "output %q has a new column %q", a.Name, c)
		case t != at[c] && t != "" && at[c] != "":
			add(c, "output %q column %q is %s, where it was %s", a.Name, c, at[c], t)
		}
	}
	for _, c := range columnsOf(b) {
		if _, has := at[c]; !has {
			add(c, "output %q no longer has column %q", a.Name, c)
		}
	}
	return out
}

// columnsOf is an output's columns: the order the script wrote, or the keys
// of its rows when it named none.
func columnsOf(r scriptrun.ExportRequest) []string {
	if len(r.Columns) > 0 {
		return r.Columns
	}
	var out []string
	for _, row := range r.Rows {
		if m, ok := row.(map[string]any); ok {
			for k := range m {
				if !slices.Contains(out, k) {
					out = append(out, k)
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// columnTypes is each column's type, read from the first row holding a value
// in it; "" for a column that is empty in every row.
func columnTypes(r scriptrun.ExportRequest) map[string]string {
	out := map[string]string{}
	for _, c := range columnsOf(r) {
		out[c] = ""
		for _, row := range r.Rows {
			if m, ok := row.(map[string]any); ok && m[c] != nil {
				out[c] = typeName(m[c])
				break
			}
		}
	}
	return out
}

func typeName(v any) string {
	switch v.(type) {
	case string:
		return "string"
	case bool:
		return "bool"
	case int, int64:
		return "int"
	case float64:
		return "float"
	case []any:
		return "list"
	case map[string]any:
		return "dict"
	}
	return fmt.Sprintf("%T", v)
}

// content is what an output holds, for comparison.
func content(r scriptrun.ExportRequest) any {
	switch {
	case r.Body != nil:
		return *r.Body
	case r.Workbook != nil:
		return r.Workbook.Shape()
	default:
		return r.Rows
	}
}

// comparePublishes lists the data regions refreshed differently.
func comparePublishes(run string, before, after []scriptrun.PublishRequest) []Difference {
	var out []Difference
	was := map[string]any{}
	for _, p := range before {
		was[p.Name] = p.Data
	}
	for _, p := range after {
		data, ok := was[p.Name]
		switch {
		case !ok:
			out = append(out, Difference{Run: run, Kind: KindOutput, Subject: p.Name, Detail: fmt.Sprintf("refreshes the data of %q, which the saved version did not", p.Name)})
		case !sameJSON(data, p.Data):
			out = append(out, Difference{Run: run, Kind: KindOutput, Subject: p.Name, Detail: fmt.Sprintf("refreshes the data of %q with different data", p.Name)})
		}
		delete(was, p.Name)
	}
	for _, p := range before {
		if _, left := was[p.Name]; left {
			out = append(out, Difference{Run: run, Kind: KindOutput, Subject: p.Name, Detail: fmt.Sprintf("no longer refreshes the data of %q", p.Name)})
		}
	}
	return out
}

// compareNotifies lists the messages posted differently. A notification is
// its arguments: the channel, and what it says.
func compareNotifies(run string, before, after []scriptrec.Made) []Difference {
	b, a := notifies(before), notifies(after)
	if sameJSON(b, a) {
		return nil
	}
	channels := []string{}
	for _, list := range [][]map[string]any{a, b} {
		for _, n := range list {
			if ch, ok := n["channel"].(string); ok && !slices.Contains(channels, ch) {
				channels = append(channels, ch)
			}
		}
	}
	sort.Strings(channels)
	posted, had := len(a), len(b)
	detail := fmt.Sprintf("posts %d notification(s) to %v, where the saved version posted %d, or posts different words",
		posted, channels, had)
	return []Difference{{Run: run, Kind: KindNotify, Subject: fmt.Sprint(channels), Detail: detail}}
}

func notifies(calls []scriptrec.Made) []map[string]any {
	out := []map[string]any{}
	for _, c := range calls {
		if c.Tool == "notify" {
			out = append(out, c.Args)
		}
	}
	return out
}

// Reach lists what after, the new version's validation, reaches that before,
// the saved version's, did not: tools, connections, destinations and host
// bindings.
func Reach(before, after scriptrun.Report) []Difference {
	var out []Difference
	added := func(kind string, was, now []string) {
		for _, v := range now {
			if !slices.Contains(was, v) {
				out = append(out, Difference{Kind: KindReach, Subject: v, Detail: fmt.Sprintf("reaches %s %q, which the saved version does not", kind, v)})
			}
		}
	}
	added("tool", before.Tools, after.Tools)
	added("connection", before.Connections, after.Connections)
	added("destination", before.Destinations, after.Destinations)
	added("host binding", before.Capabilities, after.Capabilities)
	return out
}

// sameJSON compares two values as the JSON they would be written as, so a
// number that crossed JSON once and one that did not compare equal.
func sameJSON(a, b any) bool {
	return reflect.DeepEqual(normalize(a), normalize(b))
}

func normalize(v any) any {
	data, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return v
	}
	return out
}

func raw(m json.RawMessage) any {
	if len(m) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(m, &v); err != nil {
		return string(m)
	}
	return v
}

func jsonText(v any) string {
	data, err := json.Marshal(v)
	switch {
	case err != nil:
		return fmt.Sprint(v)
	case string(data) == "null":
		return "nothing"
	}
	return string(data)
}

func orNone(m json.RawMessage) string {
	if len(m) == 0 {
		return "nothing"
	}
	return string(m)
}

//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Acceptance for #2273: a save that goes through answers shared_code, each
// function of the source a library or another script already defines, with
// the load line that replaces a library's copy or the scripts that also hold
// it; it is [] when nothing matches and never carries source; test functions
// are never reported; manage_script list narrows to libraries or to scripts;
// and the authoring page tells the agent to look for a library first.
//
// Wire forms: manage_script list takes library as JSON true and as JSON
// false, and is sent with it left out; create and update take name,
// description, source, change_summary and command as strings (the only form
// each admits) and user_agreed as a boolean; validate takes source as a
// string; fetch takes reference and purpose as strings.

// stamp2273 is a per-run mark: every name and every shared body carries it,
// so a body only this run's sources hold is what matches.
type stamp2273 string

func newStamp2273() stamp2273 { return stamp2273(issue1941Stamp()) }

// mark is the distinctive string literal every shared body holds.
func (s stamp2273) mark() string { return "acc2273-" + string(s) }

func (s stamp2273) name(part string) string { return "acc2273-" + part + "-" + string(s) }

// lastWeek is the date-window function under name, with its parameter and
// locals spelled by day, start and end.
func (s stamp2273) lastWeek(name, day, start, end string) string {
	return fmt.Sprintf(`def %[1]s(%[2]s):
    """The seven days before the week of a day number."""
    %[3]s = %[2]s - 7 - (%[2]s %% 7)
    %[4]s = %[3]s + 6
    return {"start": %[3]s, "end": %[4]s, "label": "%[5]s"}
`, name, day, start, end, s.mark())
}

// pageThrough is the paging helper under name.
func (s stamp2273) pageThrough(name string) string {
	return fmt.Sprintf(`def %[1]s(rows, size):
    """Cuts rows into pages of size."""
    pages = []
    for i in range(0, len(rows), size):
        pages.append({"rows": rows[i:i + size], "label": "%[2]s"})
    return pages
`, name, s.mark())
}

// library is a library holding fn, with a test calling call.
func library2273(fn, call string) string {
	return `"""Shared helpers."""

` + fn + `
def test_helper():
    """Calls the helper."""
    assert.true(` + call + `)
`
}

// script is a script defining fns whose main hands back call.
func script2273(call string, fns ...string) string {
	return `"""Uses a helper."""

` + strings.Join(fns, "\n") + `
def main():
    """Hands back what the helper made."""
    platform.result({"made": len(` + call + `)})
`
}

func createLibrary2273(t *testing.T, c *client, name, source string) map[string]any {
	t.Helper()
	return issue1941CreateLibrary(t, c, name, source)
}

func createScript2273(t *testing.T, c *client, name, source string) map[string]any {
	t.Helper()
	out := c.saveScript(map[string]any{"command": "create", "name": name, "source": source, "description": "Acceptance #2273: " + name}, nil)
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	return out
}

// shared is a save response's shared_code, failing when it is absent or null.
func shared2273(t *testing.T, out map[string]any) []map[string]any {
	t.Helper()
	raw, present := out["shared_code"]
	list, isList := raw.([]any)
	if !present || !isList {
		t.Fatalf("the save answered shared_code %v (present %v); want a list: %v", raw, present, out)
	}
	rows := make([]map[string]any, 0, len(list))
	for _, x := range list {
		row, _ := x.(map[string]any)
		rows = append(rows, row)
	}
	return rows
}

// noSource fails when the save response's shared_code repeats the function's
// body, found by its distinctive string literal.
func (s stamp2273) noSource(t *testing.T, out map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(out["shared_code"])
	if strings.Contains(string(raw), s.mark()) {
		t.Errorf("shared_code carries the function's source: %s", raw)
	}
	whole, _ := json.Marshal(out)
	if strings.Contains(string(whole), s.mark()) {
		t.Errorf("the save response carries the function's source: %s", whole)
	}
}

// Criterion 1: a script defining a library's function under another name is
// told the library, its version and the load line; the script is saved.
func TestIssue2273_ALibraryMatchNamesTheLoadLine(t *testing.T) {
	c := connect(t)
	s := newStamp2273()
	lib := s.name("date-windows")
	createLibrary2273(t, c, lib, library2273(s.lastWeek("last_week", "day", "start", "end"), `last_week(20)["end"] == 13`))

	out := createScript2273(t, c, s.name("weekly"), script2273("prior_week(20)", s.lastWeek("prior_week", "day", "start", "end")))
	rows := shared2273(t, out)
	want := `load("lib:` + lib + `@1", "last_week")`
	if len(rows) != 1 || rows[0]["function"] != "prior_week" || rows[0]["library"] != lib ||
		fmt.Sprint(rows[0]["version"]) != "1" || rows[0]["load"] != want {
		t.Fatalf("shared_code is %v; want prior_week matched in %s@1 with %s", rows, lib, want)
	}
	s.noSource(t, out)
}

// Criteria 2 and 3: a helper two scripts define is reported on the second,
// naming the first and offering a library; the first answers []; moving it
// into a library and loading it there answers [], and validate names the
// library and version.
func TestIssue2273_AHelperTwoScriptsDefineIsReportedThenExtracted(t *testing.T) {
	c := connect(t)
	s := newStamp2273()
	a, b := s.name("script-a"), s.name("script-b")
	first := createScript2273(t, c, a, script2273("page_through([1, 2, 3], 2)", s.pageThrough("page_through")))
	if rows := shared2273(t, first); len(rows) != 0 {
		t.Fatalf("script A shares nothing, yet answered %v", rows)
	}
	second := createScript2273(t, c, b, script2273("page_through([1, 2, 3], 2)", s.pageThrough("page_through")))
	rows := shared2273(t, second)
	if len(rows) != 1 || rows[0]["function"] != "page_through" || fmt.Sprint(rows[0]["scripts"]) != "["+a+"]" {
		t.Fatalf("script B answered shared_code %v; want page_through matched in %s", rows, a)
	}
	if next, _ := rows[0]["next"].(string); !strings.Contains(next, "library") || !strings.Contains(next, "owners' agreement") {
		t.Errorf("the next step does not offer a library: %q", next)
	}
	s.noSource(t, second)

	lib := s.name("paging")
	saved := createLibrary2273(t, c, lib, library2273(s.pageThrough("page_through"), `len(page_through([1, 2, 3], 2)) == 2`))
	if rows := shared2273(t, saved); len(rows) != 1 || !strings.Contains(fmt.Sprint(rows[0]["scripts"]), a) {
		t.Errorf("the library's save does not name the scripts holding its function: %v", rows)
	}
	loading := `load("lib:` + lib + `@1", "page_through")

` + script2273("page_through([1, 2, 3], 2)")
	updated := c.saveEdit(map[string]any{"command": "update", "name": b, "source": loading}, nil)
	if rows := shared2273(t, updated); len(rows) != 0 {
		t.Errorf("script B loading the library answered shared_code %v; want []", rows)
	}
	report := c.call("manage_script", map[string]any{"command": "validate", "source": loading})
	libs, _ := json.Marshal(report["libraries"])
	if !strings.Contains(string(libs), `{"name":"`+lib+`","version":1}`) {
		t.Errorf("validate does not report %s@1: %s", lib, libs)
	}
}

// Criterion 4: one expression apart is a different function; comments,
// formatting and local names are not.
func TestIssue2273_OneExpressionApartIsNotAMatch(t *testing.T) {
	c := connect(t)
	s := newStamp2273()
	lib := s.name("windows")
	createLibrary2273(t, c, lib, library2273(s.lastWeek("last_week", "day", "start", "end"), `last_week(20)["end"] == 13`))

	near := strings.Replace(s.lastWeek("near_week", "day", "start", "end"), "+ 6", "+ 5", 1)
	same := strings.Replace(s.lastWeek("same_week", "n", "first", "last"),
		`    first = n - 7 - (n % 7)`, "    # the Monday a week back\n    first=n-7-(n%7)", 1)
	out := createScript2273(t, c, s.name("near"), script2273(`near_week(20)) + len(same_week(20)`, near, same))
	rows := shared2273(t, out)
	if len(rows) != 1 || rows[0]["function"] != "same_week" || rows[0]["library_function"] != "last_week" {
		t.Fatalf("shared_code is %v; want same_week alone, matched to last_week", rows)
	}
}

// Criterion 5: test functions two scripts share are never reported.
func TestIssue2273_TestFunctionsAreNeverReported(t *testing.T) {
	c := connect(t)
	s := newStamp2273()
	test := fmt.Sprintf(`def test_hands_back_one():
    """Hands back one, marked."""
    main()
    out = testing.outputs()
    assert.eq(out.result["made"], 1)
    assert.eq(out.result["label"], "%s")
`, s.mark())
	body := fmt.Sprintf(`"""Counts."""

def main():
    """Hands back a count."""
    platform.result({"made": 1, "label": "%s"})

`, s.mark()) + test
	createScript2273(t, c, s.name("tests-a"), body)
	out := createScript2273(t, c, s.name("tests-b"), body)
	if rows := shared2273(t, out); len(rows) != 0 {
		t.Fatalf("a shared test function was reported: %v", rows)
	}
}

// Criterion 7: list library=true lists libraries alone, false scripts alone,
// and no argument both.
func TestIssue2273_ListNarrowsToLibrariesOrScripts(t *testing.T) {
	c := connect(t)
	s := newStamp2273()
	lib, sc := s.name("listed-lib"), s.name("listed-script")
	createLibrary2273(t, c, lib, library2273(s.pageThrough("page_through"), `len(page_through([1], 1)) == 1`))
	createScript2273(t, c, sc, script2273("[1]"))

	names := func(args map[string]any) (map[string]bool, []map[string]any) {
		args["command"], args["limit"] = "list", 500
		out := c.call("manage_script", args)
		list, _ := out["scripts"].([]any)
		seen, rows := map[string]bool{}, []map[string]any{}
		for _, x := range list {
			row, _ := x.(map[string]any)
			seen[fmt.Sprint(row["name"])] = true
			rows = append(rows, row)
		}
		return seen, rows
	}
	for _, flag := range []bool{true, false} {
		_, rows := names(map[string]any{"library": flag})
		for _, row := range rows {
			if row["library"] != flag {
				t.Errorf("list library=%v returned %v (library %v)", flag, row["name"], row["library"])
			}
		}
		mine, _ := names(map[string]any{"library": flag, "search": string(s)})
		if flag && (!mine[lib] || mine[sc]) || !flag && (mine[lib] || !mine[sc]) {
			t.Errorf("list library=%v search=%s returned %v", flag, s, mine)
		}
	}
	if both, _ := names(map[string]any{"search": string(s)}); !both[lib] || !both[sc] {
		t.Errorf("list with no library argument returned %v; want both", both)
	}
}

// Criterion 8: the authoring page tells the agent to look for a library and
// what shared_code means.
func TestIssue2273_TheAuthoringPageCarriesTheLibraryParagraph(t *testing.T) {
	c := connect(t)
	out := c.call("fetch", map[string]any{
		"reference": "mcp:knowledge_page:platform-writing-managed-scripts",
		"purpose":   "Acceptance #2273: the authoring page names libraries.",
	})
	body := fmt.Sprint(out)
	for _, want := range []string{"## Reuse a library before writing a helper", "A save that goes through answers `shared_code`", "library=true"} {
		if !strings.Contains(body, want) {
			t.Errorf("the authoring page does not carry %q", want)
		}
	}
}

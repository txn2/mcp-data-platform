//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
)

// #1940: statement coverage of a script by its tests, reported by
// manage_script test and held to a minimum on save. Wire forms: source is a
// JSON string, run_id a JSON string; neither admits another form.

// branchy1940 queries two rows and exports them, and takes a branch only when
// run.params["many"] is set. Lines 7-10 are the branch.
func branchy1940(marker string) string {
	return fmt.Sprintf(`def main():
    """Exports the marked rows, and more of them when asked to."""
    rows = platform.query(connection = "acme", sql = "SELECT mark, n FROM (VALUES ('%s', 3), ('%s', 4)) AS t (mark, n) ORDER BY n")["rows"]
    many = run.params.get("many", False)
    if not many:
        platform.export(name = "marked", rows = rows, format = "csv")
    else:
        doubled = rows + rows
        print("doubled to %%d" %% len(doubled))
        platform.export(name = "marked", rows = doubled, format = "csv")
        platform.result({"rows": len(doubled)})
`, marker, marker)
}

var params1940 = []any{map[string]any{"name": "many", "type": "bool"}}

// replaying1940 is a test replaying recording and asserting the export's
// row count.
func replaying1940(name, recording string, rows int) string {
	return fmt.Sprintf(`
def %s():
    """The recorded draft exports its rows."""
    testing.replay(%q)
    main()
    assert.eq(testing.outputs().exports[0].row_count, %d)
`, name, recording, rows)
}

// drafted1940 drafts source as name with args and returns the recording.
func drafted1940(t *testing.T, c *client, name, source string, args map[string]any) string {
	t.Helper()
	ran := c.call("manage_script", map[string]any{
		"command": "run_draft", "name": name, "source": source, "params": params1940, "args": args,
	})
	id, _ := ran["recording"].(string)
	if ran["status"] != "succeeded" || id == "" {
		t.Fatalf("the draft failed or kept no recording: %v", ran)
	}
	return id
}

func TestIssue1940_ABranchNoTestTakesIsReportedByLine(t *testing.T) {
	c := connect(t)
	name := "acceptance-1940-branch-" + unique1579()
	source := branchy1940("acceptance1940b" + unique1579())
	few := drafted1940(t, c, name, source, map[string]any{})
	out := c.call("manage_script", map[string]any{
		"command": "test", "name": name, "source": source + replaying1940("test_few", few, 2),
	})
	cov, _ := out["coverage"].(map[string]any)
	// def main, rows =, many =, if, the export in the if: 5 reached; the four
	// statements of the else never ran.
	if cov["statements"] != float64(9) || cov["covered"] != float64(5) {
		t.Fatalf("want 5 of 9 statements reached, got %v", cov)
	}
	if fmt.Sprint(cov["missed_lines"]) != "[8 9 10 11]" {
		t.Fatalf("want lines 8-11 reported missed, got %v", cov["missed_lines"])
	}
}

func TestIssue1940_AFailureInsideAFunctionNamesTheAuthorsLine(t *testing.T) {
	c := connect(t)
	source := `def half(n):
    """Half of n, refusing an odd one."""
    if n % 2:
        fail("odd: %d" % n)
    return n // 2

def main():
    """Halves four."""
    platform.result(half(4))

def test_half():
    """An odd number is refused."""
    assert.eq(half(4), 2)
    half(3)
`
	out := c.call("manage_script", map[string]any{"command": "test", "name": "acceptance-1940-line", "source": source})
	first := firstTest1939(t, out)
	if first["line"] != float64(4) || !strings.Contains(fmt.Sprint(first["failure"]), "odd: 3") {
		t.Fatalf("want the failure at the author's line 4, got %v", first)
	}
}

func TestIssue1940_InstrumentationDoesNotSpendTheStepCap(t *testing.T) {
	c := connect(t)
	// 1.9M iterations cost 19M steps uninstrumented, under the 20M cap, and
	// about 27M instrumented.
	source := `def main():
    """Counts."""
    total = 0
    for i in range(1900000):
        total += i
    platform.result(total)

def test_count():
    """The sum of the range."""
    main()
    assert.eq(testing.outputs().result, 1804999050000)
`
	out := c.call("manage_script", map[string]any{"command": "test", "name": "acceptance-1940-steps", "source": source})
	if out["ok"] != true {
		t.Fatalf("a test under the cap uninstrumented must pass: %v", out)
	}
}

func TestIssue1940_ASaveBelowTheMinimumNamesTheLinesAndATestReachingThemSaves(t *testing.T) {
	c := connect(t)
	name := "acceptance-1940-save-" + unique1579()
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	source := branchy1940("acceptance1940s" + unique1579())
	few := drafted1940(t, c, name, source, map[string]any{})
	refused := c.call("manage_script", map[string]any{
		"command": "create", "name": name, "params": params1940,
		"source": source + replaying1940("test_few", few, 2),
	})
	msg, _ := refused["message"].(string)
	if refused["status"] != "invalid" || !strings.Contains(msg, "add tests that reach lines 8, 9, 10, 11") {
		t.Fatalf("a save under the minimum must be refused naming the lines: %v", refused)
	}

	many := drafted1940(t, c, name, source, map[string]any{"many": true})
	saved := c.call("manage_script", map[string]any{
		"command": "create", "name": name, "params": params1940,
		"source": source + replaying1940("test_few", few, 2) + replaying1940("test_many", many, 4),
	})
	if saved["status"] != "created" {
		t.Fatalf("a test reaching the missed lines must let it save: %v", saved)
	}
}

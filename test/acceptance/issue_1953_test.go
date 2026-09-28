//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
)

// Issue #1953: a script's test declares the answer a call gets, alone or on
// top of a recording. These criteria draft, test and save scripts through
// manage_script on the running platform: the draft of a script that writes
// stops at the write, and the save's test answers it.
//
// Wire forms: manage_script's command, name and source are typed strings, so
// each admits one JSON form and is sent as a literal tools/call parameter of
// it. testing.answer's arguments are Starlark inside source, not tool
// parameters.

// ingest1953 queries two rows, files them as a managed resource named
// filename, and hands back the resource id the create answered with.
func ingest1953(marker, filename string) string {
	return fmt.Sprintf(`def main():
    """Files the marked rows and reports the resource they became."""
    rows = platform.query(connection = "acme", sql = "SELECT mark, n FROM (VALUES ('%[1]s', 1), ('%[1]s', 2)) AS t (mark, n) ORDER BY n")["rows"]
    content = "mark,n\n" + "".join(["%%s,%%d\n" %% (r["mark"], r["n"]) for r in rows])
    made = platform.call("manage_resource", {
        "action": "create",
        "path": "acceptance-1953",
        "filename": %[2]q,
        "content_type": "text/csv",
        "content": content,
    })
    platform.result({"resource": made["resource_id"], "rows": len(rows)})
`, marker, filename)
}

// created1953 is the create's answer a test declares, with every field the
// manage_resource contract requires except the ones in omit.
func created1953(filename string, omit ...string) string {
	fields := map[string]string{
		"resource_id": `"r1953"`, "reference": `"mcp:resource:r1953"`, "uri": `"mcp://resources/r1953/` + filename + `"`,
		"filename": fmt.Sprintf("%q", filename), "display_name": fmt.Sprintf("%q", filename), "scope": `"user"`,
		"path": `"acceptance-1953"`, "content_type": `"text/csv"`, "size_bytes": "24", "message": `"Created."`,
	}
	order := []string{"resource_id", "reference", "uri", "filename", "display_name", "scope", "path", "content_type", "size_bytes", "message"}
	parts := make([]string, 0, len(order))
	for _, k := range order {
		if !contains1953(omit, k) {
			parts = append(parts, fmt.Sprintf("%q: %s", k, fields[k]))
		}
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func contains1953(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// answering1953 is a test replaying recording that answers the create and
// asserts on what the script filed and handed back.
func answering1953(recording, filename, answer, marker string) string {
	return fmt.Sprintf(`
def test_files_the_rows():
    """The recorded draft's rows are filed as one resource."""
    testing.replay(%q)
    testing.answer("manage_resource", {"action": "create", "filename": %q}, %s)
    main()
    out = testing.outputs()
    assert.eq(out.calls[1].args["content"], "mark,n\n%[4]s,1\n%[4]s,2\n")
    assert.eq(out.calls[1].declared, True)
    assert.eq(out.result, {"resource": "r1953", "rows": 2})
`, recording, filename, answer, marker)
}

// draftStoppedAtWrite1953 drafts source without allow_writes and returns the
// recording of a draft that stopped at the write.
func draftStoppedAtWrite1953(t *testing.T, c *client, name, source string) string {
	t.Helper()
	ran := c.call("manage_script", map[string]any{"command": "run_draft", "name": name, "source": source})
	recording, _ := ran["recording"].(string)
	if ran["status"] != "failed" || ran["refused_write"] == nil || recording == "" {
		t.Fatalf("the draft must stop at the create and keep a recording: %v", ran)
	}
	if msg, _ := ran["message"].(string); !strings.Contains(msg, "testing.answer") {
		t.Fatalf("the draft must say a test answers the write: %v", ran["message"])
	}
	return recording
}

// resourceAt1953 reports whether a managed resource is filed at filename in
// the caller's own space.
func resourceAt1953(c *client, filename string) bool {
	got := c.call("manage_resource", map[string]any{"action": "get", "path": "acceptance-1953", "filename": filename})
	return got["found"] == true
}

func TestIssue1953_AScriptThatCreatesAResourceSavesWithADeclaredAnswerAndCreatesNothing(t *testing.T) {
	c := connect(t)
	name := "acceptance-1953-ingest-" + unique1579()
	filename := "acceptance-1953-" + unique1579() + ".csv"
	marker := "acceptance1953i" + unique1579()
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	source := ingest1953(marker, filename)

	recording := draftStoppedAtWrite1953(t, c, name, source)
	saved := c.call("manage_script", map[string]any{
		"command": "create", "name": name,
		"source": source + answering1953(recording, filename, created1953(filename), marker),
	})
	if saved["status"] != "created" {
		t.Fatalf("the script must save with the create answered by its test: %v", saved)
	}
	if resourceAt1953(c, filename) {
		t.Fatalf("the draft or the save created %s; neither may write", filename)
	}
}

func TestIssue1953_AnAnswerMissingARequiredFieldFailsNamingIt(t *testing.T) {
	c := connect(t)
	name := "acceptance-1953-shape-" + unique1579()
	filename := "acceptance-1953-" + unique1579() + ".csv"
	marker := "acceptance1953s" + unique1579()
	source := ingest1953(marker, filename)
	recording := draftStoppedAtWrite1953(t, c, name, source)

	out := c.call("manage_script", map[string]any{
		"command": "test", "name": name,
		"source": source + answering1953(recording, filename, created1953(filename, "uri"), marker),
	})
	failure, _ := firstTest1939(t, out)["failure"].(string)
	if out["ok"] != false || !strings.Contains(failure, `the answer declared for manage_resource (action=create) lacks "uri"`) {
		t.Fatalf("an answer lacking a required field must fail the test naming the field: %v", out)
	}
}

// branchy1953 queries a count, and only when a run saved state before posts
// the change since then to the platform's own notification tool.
func branchy1953(marker string) string {
	return fmt.Sprintf(`def main():
    """Reports the count, and the change since the last run once there is one."""
    rows = platform.query(connection = "acme", sql = "SELECT n FROM (VALUES ('%s', 5)) AS t (mark, n)")["rows"]
    total = rows[0]["n"]
    if "last" in run.state:
        change = total - run.state["last"]
        word = "up" if change > 0 else "down or level"
        platform.notify(channel = "ops-alerts", title = "Count moved", body = "%%s by %%d" %% (word, change))
        print("posted the change")
    platform.save_state({"last": total})
`, marker)
}

const firstRun1953 = `
def test_first_run():
    """A first run saves the count and posts nothing."""
    testing.replay(%q)
    main()
    out = testing.outputs()
    assert.eq(out.state, {"last": 5})
    assert.eq(out.notifies, [])
`

const laterRun1953 = `
def test_a_later_run_posts_the_change():
    """With a count saved, the change since it is posted."""
    testing.replay(%q)
    testing.set_run(state = {"last": 2})
    testing.answer("notify", {"action": "send", "channel": "ops-alerts"}, {
        "channel": "ops-alerts",
        "kind": "chat",
        "queued": 1,
        "delivered": False,
        "detail": "accepted for delivery",
    })
    main()
    out = testing.outputs()
    assert.eq(out.notifies[0]["body"], "up by 3")
    assert.eq(out.state, {"last": 5})
`

func TestIssue1953_ABranchNeedingSavedStateIsReachedAndCounted(t *testing.T) {
	c := connect(t)
	name := "acceptance-1953-state-" + unique1579()
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	source := branchy1953("acceptance1953b" + unique1579())
	ran := c.call("manage_script", map[string]any{"command": "run_draft", "name": name, "source": source})
	recording, _ := ran["recording"].(string)
	if ran["status"] != "succeeded" || recording == "" {
		t.Fatalf("a first draft has no state and takes no branch: %v", ran)
	}

	refused := c.call("manage_script", map[string]any{
		"command": "create", "name": name, "source": source + fmt.Sprintf(firstRun1953, recording),
	})
	if msg, _ := refused["message"].(string); refused["status"] != "invalid" || !strings.Contains(msg, "add tests that reach lines 6, 7, 8, 9") {
		t.Fatalf("without a test of the branch the save must name its lines: %v", refused)
	}

	both := source + fmt.Sprintf(firstRun1953, recording) + fmt.Sprintf(laterRun1953, recording)
	tested := c.call("manage_script", map[string]any{"command": "test", "name": name, "source": both})
	coverage, _ := tested["coverage"].(map[string]any)
	if tested["ok"] != true || coverage["percent"] != float64(100) {
		t.Fatalf("the declared answers must reach the branch and the coverage count it: %v", tested)
	}
	saved := c.call("manage_script", map[string]any{"command": "create", "name": name, "source": both})
	if saved["status"] != "created" {
		t.Fatalf("the script must save with the branch tested: %v", saved)
	}
}

func TestIssue1953_ATestBlindToItsDeclaredRowsIsRefused(t *testing.T) {
	c := connect(t)
	name := "acceptance-1953-blind-" + unique1579()
	source := `def main():
    """Reports the regions' total."""
    rows = platform.query(connection = "acme", sql = "SELECT region, n FROM sales")["rows"]
    platform.result({"total": sum([r["n"] for r in rows])})

def test_blind():
    """Declares the rows and never reads what came of them."""
    testing.answer("trino_query", {"sql": "SELECT region, n FROM sales"}, {"rows": [{"region": "east", "n": 2}, {"region": "west", "n": 3}]})
    main()
    assert.eq(len(testing.outputs().calls), 1)
`
	refused := c.call("manage_script", map[string]any{"command": "create", "name": name, "source": source})
	msg, _ := refused["message"].(string)
	if refused["status"] != "invalid" || !strings.Contains(msg, "test_blind still passes when the query results it replays or declares change") {
		t.Fatalf("a test blind to its declared rows must be refused by the altered-rows check: %v", refused)
	}
}

package gates

// The transcript of a ticket's recorded run is written from what the run and
// its acceptance files hold (#2541), and an acceptance file calling a tool a
// diff changes is selected even when the diff leaves the file alone (#2543).

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeAcceptance writes one acceptance file with the given body.
func writeAcceptance(t *testing.T, root, issue, body string) string {
	t.Helper()
	rel := filepath.Join("test", "acceptance", "issue_"+issue+"_test.go")
	require(t, os.WriteFile(filepath.Join(root, rel), []byte("//go:build integration\n\npackage acceptance\n\n"+body), 0o600))
	return rel
}

func readTranscript(t *testing.T, root, issue string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "build", issue, "acceptance.md")) // #nosec G304 -- a path under this test's temp root
	require(t, err)
	return string(raw)
}

func TestTheTranscriptIsWrittenFromTheFileAndTheRun(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	writeAcceptance(t, root, "9101", `// Wire forms: body is sent as an object
// and as a string of JSON.

func TestIssue9101_Second(t *testing.T) { _ = t }

func TestIssue9101_First(t *testing.T) { _ = t }

func TestIssue9101_Failed(t *testing.T) { _ = t }

func TestIssue9101_Skipped(t *testing.T) { _ = t }

func TestIssue9101_NeverRan(t *testing.T) { _ = t }
`)
	writeRun(t, root, "9101", map[string]string{
		"TestIssue9101_First": "pass", "TestIssue9101_Second": "pass",
		"TestIssue9101_Failed": "fail", "TestIssue9101_Skipped": "skip",
	})
	if out, ok := gate(t, root, "transcript", "9101"); !ok {
		t.Fatalf("transcript: %s", out)
	}
	got := readTranscript(t, root, "9101")
	first, _, _ := strings.Cut(got, "\n")
	if first != "Wire forms: body is sent as an object and as a string of JSON." {
		t.Errorf("first line = %q", first)
	}
	wantOrder := []string{
		"| TestIssue9101_Second | pass | 0.10 |",
		"| TestIssue9101_First | pass | 0.10 |",
		"| TestIssue9101_Failed | fail | 0.10 |",
		"| TestIssue9101_Skipped | skip | 0.10 |",
		"| TestIssue9101_NeverRan | absent | 0.00 |",
	}
	last := -1
	for _, row := range wantOrder {
		at := strings.Index(got, row)
		if at < 0 || at < last {
			t.Fatalf("row %q missing or out of source order in:\n%s", row, got)
		}
		last = at
	}
}

func TestAFileWithNoWireFormsCommentFailsNamingIt(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	file := writeAcceptance(t, root, "9102", "func TestIssue9102_First(t *testing.T) { _ = t }\n")
	writeRun(t, root, "9102", map[string]string{"TestIssue9102_First": "pass"})
	out, ok := gate(t, root, "transcript", "9102")
	if ok {
		t.Fatalf("a file with no comment produced a transcript:\n%s", out)
	}
	if !strings.Contains(out, filepath.ToSlash(file)) {
		t.Errorf("the refusal does not name the file:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(root, "build", "9102", "acceptance.md")); !os.IsNotExist(err) {
		t.Errorf("a transcript was written without its line: %v", err)
	}
}

// Criterion 3 of #2541: the recorded run is the only acceptance step taken.
func TestTheCheckPassesOnARecordedRunAlone(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	file := writeAcceptance(t, root, "9103", "// Wire forms: name is a string.\n\nfunc TestIssue9103_First(t *testing.T) { _ = t }\n")
	writeRun(t, root, "9103", map[string]string{"TestIssue9103_First": "pass"})
	if out, ok := gate(t, root, "transcript", "9103"); !ok {
		t.Fatalf("transcript: %s", out)
	}
	if out, ok := gate(t, root, "check", file); !ok {
		t.Fatalf("the check refused a recorded run:\n%s", out)
	}
}

// toolRepo is a tree with two registered tools and three acceptance files: one
// calling widget_query by name, one reaching it through a helper another file
// declares, and one calling only gadget_read.
func toolRepo(t *testing.T) string {
	t.Helper()
	root := newRepo(t)
	require(t, os.MkdirAll(filepath.Join(root, "test", "toolsurface"), 0o750))
	require(t, os.WriteFile(filepath.Join(root, "test", "toolsurface", "toolsurface.go"), []byte(`package toolsurface

var Annotations = map[string]Expectation{
	"widget_query": {ReadOnly: true},
	"gadget_read":  {ReadOnly: true},
}
`), 0o600))
	for _, pkg := range []struct{ dir, tool string }{{"widget", "widget_query"}, {"gadget", "gadget_read"}} {
		dir := filepath.Join(root, "pkg", "toolkits", pkg.dir)
		require(t, os.MkdirAll(dir, 0o750))
		require(t, os.WriteFile(filepath.Join(dir, "toolkit.go"), []byte(`package `+pkg.dir+`

// It's the tool's registration; the apostrophe is in this comment on purpose.
const toolName = "`+pkg.tool+`"

func register(s any) {
	toolkit.AddTool(s, &mcp.Tool{Name: toolName}, handle)
}
`), 0o600))
		require(t, os.WriteFile(filepath.Join(dir, "handler.go"), []byte("package "+pkg.dir+"\n"), 0o600))
	}
	writeAcceptance(t, root, "9201", "// Wire forms: none.\n\nfunc TestIssue9201_Direct(t *testing.T) { c.call(\"widget_query\", nil) }\n")
	writeAcceptance(t, root, "9202", "// Wire forms: none.\n\nfunc TestIssue9202_ThroughAHelper(t *testing.T) { runWidget9202(c) }\n")
	require(t, os.WriteFile(filepath.Join(root, "test", "acceptance", "helpers_test.go"), []byte(
		"package acceptance\n\nfunc runWidget9202(c *client) { c.call(\"widget_query\", nil) }\n"), 0o600))
	writeAcceptance(t, root, "9203", "// Wire forms: none.\n\nfunc TestIssue9203_Other(t *testing.T) { c.call(\"gadget_read\", nil) }\n")
	return root
}

// A method of a helper type is that file's: a call elsewhere with the same name
// is some other type's method, and a client method is the harness's.
func TestAMethodOfAnotherTypeIsNotAHelperEveryFileCalls(t *testing.T) {
	t.Parallel()
	root := toolRepo(t)
	require(t, os.WriteFile(filepath.Join(root, "test", "acceptance", "site_helpers_test.go"), []byte(
		"package acceptance\n\nfunc (s *siteClient) publish() { s.c.call(\"widget_query\", nil) }\n\nfunc (c *client) lookup() { c.call(\"widget_query\", nil) }\n"), 0o600))
	writeAcceptance(t, root, "9204", "// Wire forms: none.\n\nfunc TestIssue9204_Unrelated(t *testing.T) { other.publish() }\n")
	writeAcceptance(t, root, "9205", "// Wire forms: none.\n\nfunc TestIssue9205_UsesTheClient(t *testing.T) { c.lookup() }\n")
	out, ok := gateWith(t, root, "pkg/toolkits/widget/handler.go\n", "affected")
	if !ok || strings.TrimSpace(out) != "9201,9202,9205" {
		t.Errorf("selected %q (ok %v); want 9201,9202,9205", out, ok)
	}
}

// gateWith runs one subcommand with paths on stdin.
func gateWith(t *testing.T, root, stdin string, args ...string) (string, bool) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "python3", //nolint:gosec // a fixed script under a temp root this test wrote
		append([]string{filepath.Join(root, scriptRelPath)}, args...)...)
	cmd.Dir = root
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

func TestAnUnchangedFileCallingAChangedToolIsSelected(t *testing.T) {
	t.Parallel()
	root := toolRepo(t)
	out, ok := gateWith(t, root, "pkg/toolkits/widget/handler.go\n", "affected")
	if !ok || strings.TrimSpace(out) != "9201,9202" {
		t.Errorf("a handler change selected %q (ok %v); want 9201,9202", out, ok)
	}
	if out, ok := gateWith(t, root, "pkg/toolkits/widget/handler_test.go\ndocs/x.md\n", "affected"); !ok || strings.TrimSpace(out) != "" {
		t.Errorf("a test and a doc selected %q (ok %v); want none", out, ok)
	}
}

func TestTheCheckFailsAnAffectedFileWithNoFreshRun(t *testing.T) {
	t.Parallel()
	root := toolRepo(t)
	changed := "pkg/toolkits/widget/handler.go\n"

	out, ok := gateWith(t, root, changed, "check", "--changed-stdin")
	if ok || !strings.Contains(out, "#9201") || !strings.Contains(out, "#9202") || strings.Contains(out, "#9203") {
		t.Fatalf("no recorded run: ok %v\n%s", ok, out)
	}

	writeRun(t, root, "9201", map[string]string{"TestIssue9201_Direct": "pass"})
	writeRun(t, root, "9202", map[string]string{"TestIssue9202_ThroughAHelper": "pass"})
	future := time.Now().Add(time.Hour)
	source := filepath.Join(root, "pkg", "toolkits", "widget", "handler.go")
	require(t, os.Chtimes(source, future, future))
	out, ok = gateWith(t, root, changed, "check", "--changed-stdin")
	if ok || !strings.Contains(out, "recorded before") {
		t.Fatalf("a run older than the change passed: ok %v\n%s", ok, out)
	}

	past := time.Now().Add(-time.Hour)
	require(t, os.Chtimes(source, past, past))
	if out, ok = gateWith(t, root, changed, "check", "--changed-stdin"); !ok {
		t.Fatalf("a run newer than the change was refused:\n%s", out)
	}
}

// The real tree: every tool the server registers resolves to its package, a
// mention of a tool in another tool's enum does not count, and a change to the
// composition root that is not a tool's own file selects nothing.
func TestTheRealRegistryMapsEveryTool(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "python3", "-c", `
import importlib.util, sys
spec = importlib.util.spec_from_file_location("ev", "../../scripts/acceptance-evidence.py")
m = importlib.util.module_from_spec(spec); sys.modules["ev"] = m; spec.loader.exec_module(m)
t = m.tool_sources()
print("unresolved", sorted(k for k, v in t.items() if not v))
print("search", sorted(t["search"]))
print("root", len(m.affected(["pkg/platform/platform.go"])))
`) // #nosec G204 -- a fixed program over this repository
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	out := string(raw)
	for _, want := range []string{"unresolved []", "search ['pkg/toolkits/search']", "root 0"} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
}

// A widely called tool reaches more tickets than one job's time limit runs, so
// they are dealt across jobs: each ticket once, none over the group size until
// the job cap is reached.
func TestAffectedTicketsAreDealtAcrossJobs(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "python3", "-c", `
import importlib.util, json, sys
spec = importlib.util.spec_from_file_location("paths", "../../scripts/acceptance-pr-paths.py")
m = importlib.util.module_from_spec(spec); sys.modules["paths"] = m; spec.loader.exec_module(m)
print(json.dumps([len(m.ticket_groups(list(range(n)))) for n in (0, 1, 25, 26, 200, 900)]))
print(sorted(int(x) for g in m.ticket_groups(list(range(60))) for x in g.split(",")) == list(range(60)))
`) // #nosec G204 -- a fixed program over this repository
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	if got := strings.TrimSpace(string(raw)); got != "[0, 1, 1, 2, 8, 8]\nTrue" {
		t.Errorf("the groups: %s", got)
	}
}

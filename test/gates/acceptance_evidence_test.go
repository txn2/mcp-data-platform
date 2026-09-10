// Package gates tests the repository's own release gates: the scripts that
// decide whether a change may be committed or a release cut.
//
// scripts/acceptance-evidence.py is the gate that reads whether a ticket's
// acceptance criteria actually ran. It replaced a check that read the run's
// prose transcript and could be satisfied by a transcript saying a criterion
// had not been executed (#1663, #1675, #1680), so the thing it must never do
// again is pass a ticket whose criteria left no passing result.
package gates

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// scriptRelPath is the gate under test, addressed the same way from the
// repository and from the throwaway tree the tests copy it into.
const scriptRelPath = "scripts/acceptance-evidence.py"

// gate runs one subcommand of the evidence script inside root, which the
// script treats as the repository it is judging (it resolves the repo from its
// own location). Returns combined output and whether it passed.
func gate(t *testing.T, root string, args ...string) (string, bool) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "python3", //nolint:gosec // a fixed script under a temp root this test wrote
		append([]string{filepath.Join(root, scriptRelPath)}, args...)...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

// newRepo lays out a throwaway tree with the real gate script in it.
func newRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	require(t, os.MkdirAll(filepath.Join(root, "scripts"), 0o750))
	require(t, os.MkdirAll(filepath.Join(root, "test", "acceptance"), 0o750))
	script, err := os.ReadFile(filepath.Join("..", "..", scriptRelPath))
	require(t, err)
	// #nosec G703 -- root is t.TempDir() and the join is a constant path.
	require(t, os.WriteFile(filepath.Join(root, scriptRelPath), script, 0o600))
	return root
}

// writeCriteria writes an acceptance file declaring the named criteria.
func writeCriteria(t *testing.T, root, issue string, names ...string) string {
	t.Helper()
	var body strings.Builder
	_, _ = body.WriteString("//go:build integration\n\npackage acceptance\n\nimport \"testing\"\n")
	for _, name := range names {
		_, _ = body.WriteString("\nfunc " + name + "(t *testing.T) { _ = t }\n")
	}
	rel := filepath.Join("test", "acceptance", "issue_"+issue+"_test.go")
	require(t, os.WriteFile(filepath.Join(root, rel), []byte(body.String()), 0o600))
	return rel
}

// writeRun records a run: each name mapped to its terminal action.
func writeRun(t *testing.T, root, issue string, outcomes map[string]string) {
	t.Helper()
	dir := filepath.Join(root, "build", issue)
	require(t, os.MkdirAll(dir, 0o750))
	var stream strings.Builder
	for name, action := range outcomes {
		for _, ev := range []map[string]any{
			{"Action": "run", "Test": name},
			{"Action": action, "Test": name, "Elapsed": 0.1},
		} {
			raw, err := json.Marshal(ev)
			require(t, err)
			_, _ = stream.Write(raw)
			_, _ = stream.WriteString("\n")
		}
	}
	require(t, os.WriteFile(filepath.Join(dir, "acceptance.jsonl"), []byte(stream.String()), 0o600))
}

// writeTranscript records the human transcript the gate still requires.
func writeTranscript(t *testing.T, root, issue, first string) {
	t.Helper()
	dir := filepath.Join(root, "build", issue)
	require(t, os.MkdirAll(dir, 0o750))
	require(t, os.WriteFile(filepath.Join(dir, "acceptance.md"), []byte(first+"\n\nRan it.\n"), 0o600))
}

func require(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
}

func TestEveryCriterionThatPassedIsAccepted(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	file := writeCriteria(t, root, "9001", "TestIssue9001_First", "TestIssue9001_Second")
	writeRun(t, root, "9001", map[string]string{
		"TestIssue9001_First":  "pass",
		"TestIssue9001_Second": "pass",
	})
	writeTranscript(t, root, "9001", "Wire forms: object and string.")

	out, ok := gate(t, root, "check", file)
	if !ok {
		t.Fatalf("a complete run was refused:\n%s", out)
	}
}

// The defect the gate exists for: a criterion that did not run. Absent from
// the stream, skipped, and failed are all the same verdict.
func TestACriterionThatDidNotRunFailsTheGate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		outcomes map[string]string
		want     string
	}{
		{"absent", map[string]string{"TestIssue9002_First": "pass"}, "never ran"},
		{"skipped", map[string]string{"TestIssue9002_First": "pass", "TestIssue9002_Second": "skip"}, "skip"},
		{"failed", map[string]string{"TestIssue9002_First": "pass", "TestIssue9002_Second": "fail"}, "fail"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := newRepo(t)
			file := writeCriteria(t, root, "9002", "TestIssue9002_First", "TestIssue9002_Second")
			writeRun(t, root, "9002", tc.outcomes)
			writeTranscript(t, root, "9002", "Wire forms: object and string.")

			out, ok := gate(t, root, "check", file)
			if ok {
				t.Fatalf("the gate accepted a criterion that %s:\n%s", tc.name, out)
			}
			if !strings.Contains(out, "TestIssue9002_Second") || !strings.Contains(out, tc.want) {
				t.Errorf("the refusal does not name the criterion and its outcome:\n%s", out)
			}
		})
	}
}

// A run that was never recorded at all is the state #1663 shipped in: the
// transcript said the criterion could not be executed, and the gate read only
// the transcript.
func TestAnAbsentRunStreamFailsTheGate(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	file := writeCriteria(t, root, "9003", "TestIssue9003_First")
	writeTranscript(t, root, "9003", "Wire forms: object.\n\n## Not executed here\nThe upstream would not start.")

	out, ok := gate(t, root, "check", file)
	if ok {
		t.Fatalf("the gate accepted a ticket with no recorded run:\n%s", out)
	}
	if !strings.Contains(out, "no run stream") {
		t.Errorf("the refusal does not say the run is missing:\n%s", out)
	}
}

// The transcript rule from #1548 still holds beside the results rule.
func TestATranscriptWithoutItsWireFormsLineFailsTheGate(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	file := writeCriteria(t, root, "9004", "TestIssue9004_First")
	writeRun(t, root, "9004", map[string]string{"TestIssue9004_First": "pass"})
	writeTranscript(t, root, "9004", "Ran the suite.")

	out, ok := gate(t, root, "check", file)
	if ok {
		t.Fatalf("the gate accepted a transcript with no wire forms line:\n%s", out)
	}
	if !strings.Contains(out, "Wire forms:") {
		t.Errorf("the refusal does not name the missing line:\n%s", out)
	}
}

// split is what writes the evidence, so a run that produced results must be
// readable back by check without anyone assembling a file.
func TestSplitRecordsWhatTheRunProduced(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	file := writeCriteria(t, root, "9005", "TestIssue9005_First")
	writeTranscript(t, root, "9005", "Wire forms: object.")

	stream := `{"Action":"output","Test":"TestIssue9005_First","Output":"=== RUN TestIssue9005_First\n"}
{"Action":"pass","Test":"TestIssue9005_First","Elapsed":0.2}
{"Action":"pass","Package":"acceptance","Elapsed":0.3}
`
	cmd := exec.CommandContext(t.Context(), "python3", filepath.Join(root, scriptRelPath), "split") //nolint:gosec // a fixed script under a temp root this test wrote
	cmd.Dir = root
	cmd.Stdin = strings.NewReader(stream)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("split: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "=== RUN TestIssue9005_First") {
		t.Errorf("split did not echo the run's own output:\n%s", out)
	}

	verdict, ok := gate(t, root, "check", file)
	if !ok {
		t.Fatalf("check could not read what split recorded:\n%s", verdict)
	}
}

// #1277 registered three tools and closed with criteria for two. A tool the
// diff registers with no criterion calling it fails the gate.
func TestAToolRegisteredWithNoCriterionFailsTheGate(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	git(t, root, "init", "-q")
	git(t, root, "config", "user.email", "gate@example.com")
	git(t, root, "config", "user.name", "gate")
	require(t, os.MkdirAll(filepath.Join(root, "pkg", "toolkits", "widget"), 0o750))
	require(t, os.WriteFile(filepath.Join(root, "pkg", "toolkits", "widget", "toolkit.go"),
		[]byte("package widget\n\nfunc register() { _ = \"AddTool(\" }\n"), 0o600))
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "base")
	base := strings.TrimSpace(run(t, root, "git", "rev-parse", "HEAD"))

	// The ticket registers two tools and writes a criterion for one.
	require(t, os.WriteFile(filepath.Join(root, "pkg", "toolkits", "widget", "toolkit.go"), []byte(
		"package widget\n\nconst (\n\tToolQuery = \"widget_query\"\n\tToolExport = \"widget_export\"\n)\n\nfunc register() { _ = \"AddTool(\" }\n"), 0o600))
	file := writeCriteria(t, root, "9006", "TestIssue9006_QueryRuns")
	require(t, os.WriteFile(filepath.Join(root, file), []byte(
		"//go:build integration\n\npackage acceptance\n\nimport \"testing\"\n\nfunc TestIssue9006_QueryRuns(t *testing.T) { _ = \"widget_query\" }\n"), 0o600))
	writeRun(t, root, "9006", map[string]string{"TestIssue9006_QueryRuns": "pass"})
	writeTranscript(t, root, "9006", "Wire forms: object.")

	out, ok := gate(t, root, "check", "--merge-base", base, file)
	if ok {
		t.Fatalf("the gate accepted a tool with no criterion:\n%s", out)
	}
	if !strings.Contains(out, "widget_export") {
		t.Errorf("the refusal does not name the uncovered tool:\n%s", out)
	}
	if strings.Contains(out, "widget_query") {
		t.Errorf("the covered tool was reported as uncovered:\n%s", out)
	}
}

func git(t *testing.T, root string, args ...string) {
	t.Helper()
	run(t, root, "git", args...)
}

func run(t *testing.T, root, name string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), name, args...) //nolint:gosec // fixed commands under a temp root
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return string(out)
}

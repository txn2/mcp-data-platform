package gates

// An acceptance file the diff leaves alone is selected when it calls, directly
// or through a second helper, a helper whose declaration the diff changes in
// another acceptance file (#2627): issue_2579_test.go built its sites with a
// helper issue_2559_test.go changed, neither check selected it, and the
// nightly after the merge failed.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// helperRepo is a git tree (toolRepo's, which registers two tools) whose base
// commit also holds a helper file and three acceptance files: one calling openShop2627 directly, one reaching it through
// shopFor2627, and one calling neither. The working tree then changes
// openShop2627's body.
func helperRepo(t *testing.T) string {
	t.Helper()
	root := toolRepo(t)
	helpers := filepath.Join(root, "test", "acceptance", "shop_helpers_test.go")
	require(t, os.WriteFile(helpers, []byte(`//go:build integration

package acceptance

// openShop2627 opens a shop.
func openShop2627(c *client) string {
	return c.host("shop")
}

// shopFor2627 is a shop for a criterion, through openShop2627.
func shopFor2627(c *client) string {
	return openShop2627(c) + "/"
}

// unrelated2627 is a helper nothing changed.
func unrelated2627(c *client) string { return "x" }
`), 0o600))
	writeAcceptance(t, root, "9301", "// Wire forms: none.\n\nfunc TestIssue9301_Direct(t *testing.T) { _ = openShop2627(c) }\n")
	writeAcceptance(t, root, "9302", "// Wire forms: none.\n\nfunc TestIssue9302_ThroughAHelper(t *testing.T) { _ = shopFor2627(c) }\n")
	writeAcceptance(t, root, "9303", "// Wire forms: none.\n\nfunc TestIssue9303_Unrelated(t *testing.T) { _ = unrelated2627(c) }\n")
	gitIn(t, root, "init", "-q")
	gitIn(t, root, "add", "-A")
	gitIn(t, root, "-c", "user.email=gate@example.com", "-c", "user.name=gate", "commit", "-q", "-m", "base")
	raw, err := os.ReadFile(helpers) // #nosec G304 -- a path under this test's temp root
	require(t, err)
	// #nosec G703 -- helpers is a constant path under t.TempDir().
	require(t, os.WriteFile(helpers, []byte(strings.Replace(string(raw), `c.host("shop")`, `c.host("shop--dev")`, 1)), 0o600))
	return root
}

func gitIn(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...) //nolint:gosec // fixed git arguments in this test's temp root
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// changedHelpers is the changed-paths list the helper gate reads on stdin:
// the helper file the fixture's diff changes.
const changedHelpers = "test/acceptance/shop_helpers_test.go\n"

// helperGate runs one subcommand with changedHelpers on stdin and the diff
// range set.
func helperGate(t *testing.T, root string, args ...string) (string, bool) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "python3", //nolint:gosec // a fixed script under a temp root this test wrote
		append([]string{filepath.Join(root, scriptRelPath)}, args...)...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "ACCEPTANCE_DIFF_RANGE=HEAD")
	cmd.Stdin = strings.NewReader(changedHelpers)
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

// Criterion 4 of #2627: the file calling the changed helper directly and the
// one reaching it through a second helper are selected; the file calling
// neither is not, and with no diff range nothing is.
func TestAnUnchangedFileCallingAChangedHelperIsSelected(t *testing.T) {
	t.Parallel()
	root := helperRepo(t)
	if out, ok := helperGate(t, root, "affected"); !ok || strings.TrimSpace(out) != "9301,9302" {
		t.Errorf("a changed helper selected %q (ok %v); want 9301,9302", out, ok)
	}
	if out, ok := gateWith(t, root, changedHelpers, "affected"); !ok || strings.TrimSpace(out) != "" {
		t.Errorf("with no diff range %q was selected (ok %v); want none", out, ok)
	}
}

// Criterion 2 of #2627: the check fails each selected file with no passing
// run newer than the change, naming the helper and the file changing it.
func TestTheCheckFailsACallerOfAChangedHelperWithNoFreshRun(t *testing.T) {
	t.Parallel()
	root := helperRepo(t)
	out, ok := helperGate(t, root, "check", "--changed-stdin")
	if ok || !strings.Contains(out, "#9301") || !strings.Contains(out, "#9302") || strings.Contains(out, "#9303") ||
		!strings.Contains(out, "openShop2627 (test/acceptance/shop_helpers_test.go)") {
		t.Fatalf("no recorded run: ok %v\n%s", ok, out)
	}

	writeRun(t, root, "9301", map[string]string{"TestIssue9301_Direct": "pass"})
	writeRun(t, root, "9302", map[string]string{"TestIssue9302_ThroughAHelper": "pass"})
	future := time.Now().Add(time.Hour)
	helpers := filepath.Join(root, "test", "acceptance", "shop_helpers_test.go")
	require(t, os.Chtimes(helpers, future, future))
	out, ok = helperGate(t, root, "check", "--changed-stdin")
	if ok || !strings.Contains(out, "was recorded before test/acceptance/shop_helpers_test.go changed openShop2627") {
		t.Fatalf("a run older than the change passed: ok %v\n%s", ok, out)
	}

	past := time.Now().Add(-time.Hour)
	require(t, os.Chtimes(helpers, past, past))
	if out, ok = helperGate(t, root, "check", "--changed-stdin"); !ok {
		t.Fatalf("a run newer than the change was refused:\n%s", out)
	}
}

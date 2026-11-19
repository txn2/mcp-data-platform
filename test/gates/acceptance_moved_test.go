package gates

// Two readings of a diff the staleness check got wrong on #2709, which marked
// 445 of 452 recorded tickets stale for a branch whose real reach was a
// third of that: a ticket's own method named like the harness's (a
// schemaApp2709.call beside client.call) read as a changed helper every file
// calls, and a block of declarations moved to another package read as a
// change to the declaration left after it.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// diffGate runs one subcommand with stdin and the diff read against HEAD.
func diffGate(t *testing.T, root, stdin string, args ...string) (string, bool) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "python3", //nolint:gosec // a fixed script under a temp root this test wrote
		append([]string{filepath.Join(root, scriptRelPath)}, args...)...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "ACCEPTANCE_DIFF_RANGE=HEAD")
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

// A ticket's own method on its own type is not a helper another file calls,
// whatever its name; a changed client method still is.
func TestATicketsOwnMethodIsNotTheHarnessMethod(t *testing.T) {
	t.Parallel()
	root := toolRepo(t)
	own := "// Wire forms: none.\n\ntype site9306 struct{}\n\nfunc (s *site9306) call() string { return \"a\" }\n\n" +
		"func TestIssue9306_Own(t *testing.T) { _ = (&site9306{}).call() }\n"
	writeAcceptance(t, root, "9306", own)
	writeAcceptance(t, root, "9307", "// Wire forms: none.\n\nfunc TestIssue9307_Harness(t *testing.T) { c.call(\"gadget_read\", nil) }\n")
	gitIn(t, root, "init", "-q")
	gitIn(t, root, "add", "-A")
	gitIn(t, root, "-c", "user.email=gate@example.com", "-c", "user.name=gate", "commit", "-q", "-m", "base")
	writeAcceptance(t, root, "9306", strings.Replace(own, `return "a"`, `return "b"`, 1))

	out, ok := diffGate(t, root, "test/acceptance/issue_9306_test.go\n", "affected")
	if !ok || strings.Contains(out, "9307") {
		t.Errorf("a ticket's own method selected %q (ok %v); 9307 calls only the harness's call", out, ok)
	}
}

// A hunk removing whole top-level declarations changes nothing left behind:
// the declaration after the removed block is untouched, and no file calling
// only it is stale.
func TestMovingDeclarationsAwayChangesNoneLeftBehind(t *testing.T) {
	t.Parallel()
	root := reachRepo(t)
	source := filepath.Join(root, "pkg", "toolkits", "shop", "shop.go")
	withMoved := strings.Replace(shopSource, "func closeShop(", "// moved is rendered elsewhere now.\nfunc moved() int {\n\treturn 1\n}\n\nfunc closeShop(", 1)
	// #nosec G703 -- source is a constant path under t.TempDir().
	require(t, os.WriteFile(source, []byte(withMoved), 0o600))
	gitIn(t, root, "add", "-A")
	gitIn(t, root, "-c", "user.email=gate@example.com", "-c", "user.name=gate", "commit", "-q", "-m", "moved lives here")
	// #nosec G703 -- source is a constant path under t.TempDir().
	require(t, os.WriteFile(source, []byte(shopSource), 0o600))

	out, ok := reachGate(t, root, "affected")
	if !ok || strings.TrimSpace(out) != "" {
		t.Errorf("removing moved selected %q (ok %v); closeShop is untouched, so nothing", out, ok)
	}
}

// A stale recording of a file the diff leaves alone is the pull request's
// Acceptance (changed criteria) job to repeat, in parallel on fresh stacks:
// the local check names the tickets and passes. A changed acceptance file
// with no passing run still fails it, and ACCEPTANCE_STALE_STRICT=1 asks for
// the unchanged ones here too.
func TestAnUnchangedStaleTicketIsLeftToCI(t *testing.T) {
	t.Parallel()
	root := toolRepo(t)
	run := func(strict bool, files ...string) (string, bool) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "python3", //nolint:gosec // a fixed script under a temp root this test wrote
			append([]string{filepath.Join(root, scriptRelPath), "check", "--changed-stdin"}, files...)...)
		cmd.Dir = root
		cmd.Env = os.Environ()
		if strict {
			cmd.Env = append(cmd.Env, "ACCEPTANCE_STALE_STRICT=1")
		}
		cmd.Stdin = strings.NewReader("pkg/toolkits/widget/handler.go\n")
		out, err := cmd.CombinedOutput()
		return string(out), err == nil
	}

	out, ok := run(false)
	if !ok || !strings.Contains(out, "Acceptance (changed criteria) job runs them") || !strings.Contains(out, "#9201, #9202") {
		t.Errorf("the unchanged stale tickets were not left to CI: ok %v\n%s", ok, out)
	}
	if out, ok = run(false, "test/acceptance/issue_9201_test.go"); ok || !strings.Contains(out, "#9201") {
		t.Errorf("a changed acceptance file with no run passed: ok %v\n%s", ok, out)
	}
	if out, ok = run(true); ok || !strings.Contains(out, "#9202") {
		t.Errorf("ACCEPTANCE_STALE_STRICT=1 passed an unchanged stale ticket: ok %v\n%s", ok, out)
	}
}

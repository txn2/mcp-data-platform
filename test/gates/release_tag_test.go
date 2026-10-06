package gates

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// scripts/release-tag.py holds a release to the release-candidate rules
// (#2012): a final tag sits on the commit its last candidate was built from,
// and a release measures from the last FINAL release, never a candidate.

const releaseTagScript = "scripts/release-tag.py"

// releaseRepo lays out a repository with the real scripts and this history:
// v1.138.2 on the first commit, v1.139.0-rc1 on the second, v1.139.0-rc2 on
// the third, and a fourth commit nothing is tagged on.
func releaseRepo(t *testing.T) (root string, commits []string) {
	t.Helper()
	root = newGitRepo(t, releaseTagScript, map[string]string{"README.md": "one\n"})
	evidence, err := os.ReadFile(filepath.Join("..", "..", scriptRelPath))
	require(t, err)
	writeFile(t, root, scriptRelPath, string(evidence))
	commit := func(msg string) string {
		writeFile(t, root, "README.md", msg+"\n")
		runIn(t, root, "git", "add", "-A")
		runIn(t, root, "git", "-c", "user.name=gate", "-c", "user.email=gate@example.com", "commit", "-q", "-m", msg)
		return gitOut(t, root, "rev-parse", "HEAD")
	}
	commits = append(commits, gitOut(t, root, "rev-parse", "HEAD"))
	runIn(t, root, "git", "tag", "v1.138.2")
	commits = append(commits, commit("two"))
	runIn(t, root, "git", "tag", "v1.139.0-rc1")
	commits = append(commits, commit("three"))
	runIn(t, root, "git", "tag", "v1.139.0-rc2")
	commits = append(commits, commit("four"))
	return root, commits
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...) //nolint:gosec // fixed git invocations in a temp repo
	cmd.Dir = dir
	out, err := cmd.Output()
	require(t, err)
	return strings.TrimSpace(string(out))
}

func TestReleaseTag_AFinalTagSitsOnItsLastCandidate(t *testing.T) {
	root, commits := releaseRepo(t)

	runIn(t, root, "git", "tag", "v1.139.0", commits[2])
	out, ok := runScript(t, root, []string{releaseTagScript, "check", "v1.139.0"}, nil)
	if !ok {
		t.Fatalf("a final tag on its last candidate's commit was refused: %s", out)
	}

	runIn(t, root, "git", "tag", "-d", "v1.139.0")
	runIn(t, root, "git", "tag", "v1.139.0", commits[3])
	out, ok = runScript(t, root, []string{releaseTagScript, "check", "v1.139.0"}, nil)
	if ok || !strings.Contains(out, "v1.139.0-rc2") {
		t.Fatalf("a final tag on a commit after its last candidate was accepted: %s", out)
	}
}

// A final tag with no candidate is a release cut straight from main and
// passes, plain or annotated: v1.140.0 was refused for having none, though no
// candidate had ever been cut.
func TestReleaseTag_AFinalTagWithNoCandidate(t *testing.T) {
	root, commits := releaseRepo(t)
	runIn(t, root, "git", "tag", "v1.140.0", commits[3])
	if out, ok := runScript(t, root, []string{releaseTagScript, "check", "v1.140.0"}, nil); !ok {
		t.Fatalf("a final tag with no candidate was refused: %s", out)
	}
	// An annotated tag records a tagger; a CI runner has no git identity.
	runIn(t, root, "git", "-c", "user.name=gate", "-c", "user.email=gate@example.com",
		"tag", "-a", "v1.140.1", "-m", "release-without-rc: emergency fix", commits[3])
	if out, ok := runScript(t, root, []string{releaseTagScript, "check", "v1.140.1"}, nil); !ok {
		t.Fatalf("an annotated release with no candidate was refused: %s", out)
	}
}

func TestReleaseTag_CandidatesAndMalformedTags(t *testing.T) {
	root, _ := releaseRepo(t)
	if out, ok := runScript(t, root, []string{releaseTagScript, "check", "v1.139.0-rc2"}, nil); !ok {
		t.Fatalf("a candidate was refused: %s", out)
	}
	for _, tag := range []string{"v1.139", "release-1", "v1.139.0-rc0", "v1.139.0-beta1"} {
		if out, ok := runScript(t, root, []string{releaseTagScript, "check", tag}, nil); ok {
			t.Errorf("%s was accepted: %s", tag, out)
		}
	}
}

// TestReleaseTag_PreviousIsTheLastFinalRelease is what the changelog measures
// from: the final release before the tag, for a candidate and a final alike.
func TestReleaseTag_PreviousIsTheLastFinalRelease(t *testing.T) {
	root, commits := releaseRepo(t)
	runIn(t, root, "git", "tag", "v1.139.0", commits[2])
	for _, tag := range []string{"v1.139.0", "v1.139.0-rc2", "v1.139.0-rc1"} {
		out, ok := runScript(t, root, []string{releaseTagScript, "previous", tag}, nil)
		if !ok || strings.TrimSpace(out) != "v1.138.2" {
			t.Errorf("previous %s = %q, want v1.138.2", tag, out)
		}
	}
	if out, _ := runScript(t, root, []string{releaseTagScript, "previous", "v1.138.2"}, nil); strings.TrimSpace(out) != "" {
		t.Errorf("the first release has no previous one, got %q", out)
	}
	if _, ok := runScript(t, root, []string{releaseTagScript, "previous", "nope"}, nil); ok {
		t.Error("a malformed tag has a previous release")
	}
}

// TestAcceptanceReleaseGate_MeasuresFromTheLastFinalRelease is #2012's gate
// acceptance: with v1.138.2, v1.139.0-rc1 and v1.139.0-rc2 in the history, the
// release gate measures from v1.138.2, so an acceptance file changed before the
// candidates is still checked.
func TestAcceptanceReleaseGate_MeasuresFromTheLastFinalRelease(t *testing.T) {
	root, _ := releaseRepo(t)
	require(t, os.MkdirAll(filepath.Join(root, "test", "acceptance"), 0o750))
	writeCriteria(t, root, "4242", "TestIssue4242_Criterion")
	runIn(t, root, "git", "add", "-A")
	runIn(t, root, "git", "-c", "user.name=gate", "-c", "user.email=gate@example.com", "commit", "-q", "-m", "acceptance")
	runIn(t, root, "git", "tag", "v1.139.0-rc3")

	out, ok := gate(t, root, "release")
	if ok || !strings.Contains(out, "4242") {
		t.Fatalf("the gate did not measure from v1.138.2 past the candidates: %s", out)
	}
}

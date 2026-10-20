package gates

// scripts/acceptance-env.py reads test/acceptance/environments.txt, the
// criteria that need a stack `make dev` does not make (#2219). The whole suite
// leaves those criteria out, and the E2E Nightly runs each environment in a
// job of its own, so an environment the nightly does not run, or a pattern
// that names no criterion, is a criterion that runs nowhere: the failure #2219
// was filed for.

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const (
	envScript   = "scripts/acceptance-env.py"
	envManifest = "test/acceptance/environments.txt"
)

// envRun runs the environment script in root and returns its stdout, stderr
// and whether it passed.
func envRun(t *testing.T, root string, args ...string) (stdout, stderr string, ok bool) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "python3", //nolint:gosec // a fixed script under a root this test chose
		append([]string{filepath.Join(root, envScript)}, args...)...)
	cmd.Dir = root
	var out, errOut strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	return out.String(), errOut.String(), err == nil
}

// envRepo lays out a tree holding the real script and the given manifest.
func envRepo(t *testing.T, manifest string) string {
	t.Helper()
	root := t.TempDir()
	require(t, os.MkdirAll(filepath.Join(root, "scripts"), 0o750))
	require(t, os.MkdirAll(filepath.Join(root, "test", "acceptance"), 0o750))
	script, err := os.ReadFile(filepath.Join("..", "..", envScript))
	require(t, err)
	// #nosec G703 -- root is t.TempDir() and the joins are constant paths.
	require(t, os.WriteFile(filepath.Join(root, envScript), script, 0o600))
	// #nosec G703 -- root is t.TempDir() and the joins are constant paths.
	require(t, os.WriteFile(filepath.Join(root, envManifest), []byte(manifest), 0o600))
	return root
}

// TestEveryAcceptanceEnvironmentRunsNightly holds the nightly's acceptance
// matrix to the manifest: the plain stack and every environment not marked
// manual, no more and no fewer, and the plain stack in groups 1/N to N/N
// with none missing (#2218), since a group the matrix leaves out is a share
// of the suite that runs nowhere.
func TestEveryAcceptanceEnvironmentRunsNightly(t *testing.T) {
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	require(t, err)
	out, stderr, ok := envRun(t, repo, "nightly")
	if !ok {
		t.Fatalf("acceptance-env.py nightly failed: %s", stderr)
	}
	want := strings.Fields(out)

	workflow, err := os.ReadFile(filepath.Join(repo, ".github", "workflows", "e2e-nightly.yml")) // #nosec G304 -- the repository's own workflow
	require(t, err)
	entries := regexp.MustCompile(`(?m)^\s+- \{environment: ([\w-]+), shard: "?([\d/]*)"?, id: [\w-]+\}`).FindAllSubmatch(workflow, -1)
	if entries == nil {
		t.Fatal("e2e-nightly.yml has no acceptance matrix ({environment: ..., shard: ..., id: ...} entries)")
	}
	var got, shards []string
	for _, e := range entries {
		if !slices.Contains(got, string(e[1])) {
			got = append(got, string(e[1]))
		}
		if string(e[1]) == "dev" {
			shards = append(shards, string(e[2]))
		}
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("the nightly runs %v; environments.txt says %v", got, want)
	}
	for i, shard := range shards {
		if wantShard := fmt.Sprintf("%d/%d", i+1, len(shards)); shard != wantShard {
			t.Errorf("the plain stack's group %d is %q; want %q, so every group of the suite runs", i+1, shard, wantShard)
		}
	}
}

// TestAcceptanceShardsCoverEveryTicketOnce: the groups the nightly runs hold
// every ticket with criteria exactly once, and a group outside 1..N, or one
// asked of another environment, is refused.
func TestAcceptanceShardsCoverEveryTicketOnce(t *testing.T) {
	root := envRepo(t, "alpha  ^TestIssue9_  A=1\n")
	for name, body := range map[string]string{
		"issue_1_test.go": "func TestIssue1_A(t *testing.T) {}\nfunc TestIssue1_B(t *testing.T) {}\n",
		"issue_2_test.go": "func TestIssue2_A(t *testing.T) {}\n",
		"issue_3_test.go": "func TestIssue3_A(t *testing.T) {}\nfunc TestIssue30_A(t *testing.T) {}\n",
		"issue_9_test.go": "func TestIssue9_A(t *testing.T) {}\n",
		"helpers_test.go": "func helper() {}\n",
	} {
		// #nosec G703 -- root is t.TempDir() and name is a constant above.
		require(t, os.WriteFile(filepath.Join(root, "test", "acceptance", name), []byte(body), 0o600))
	}
	seen := map[string]int{}
	group := regexp.MustCompile(`^-run '\^TestIssue\(([\d|]+)\)_' -skip '\^TestIssue9_'$`)
	for i := 1; i <= 2; i++ {
		out, stderr, ok := envRun(t, root, "flags", "dev", "--shard", fmt.Sprintf("%d/2", i))
		m := group.FindStringSubmatch(strings.TrimSpace(out))
		if !ok || m == nil {
			t.Fatalf("group %d/2 = %q (ok %v): %s", i, out, ok, stderr)
		}
		for n := range strings.SplitSeq(m[1], "|") {
			seen[n]++
		}
	}
	if want := map[string]int{"1": 1, "2": 1, "3": 1, "9": 1, "30": 1}; !maps.Equal(seen, want) {
		t.Errorf("the groups held %v; want every ticket once: %v", seen, want)
	}
	for _, args := range [][]string{
		{"flags", "dev", "--shard", "3/2"},
		{"flags", "dev", "--shard", "0/2"},
		{"flags", "dev", "--shard", "half"},
		{"flags", "alpha", "--shard", "1/2"},
		{"flags", "dev", "--shard", "6/6"},
	} {
		if out, _, ok := envRun(t, root, args...); ok {
			t.Errorf("%v was accepted: %q", args, out)
		}
	}
}

// TestEveryEnvironmentNamesACriterion: a pattern that matches no criterion
// leaves out nothing and runs nothing, so a renamed criterion would drop out
// of its environment and fail the plain stack's run instead.
func TestEveryEnvironmentNamesACriterion(t *testing.T) {
	repo := filepath.Join("..", "..")
	manifest, err := os.ReadFile(filepath.Join(repo, envManifest)) // #nosec G304 -- the repository's own manifest
	require(t, err)
	files, err := filepath.Glob(filepath.Join(repo, "test", "acceptance", "*_test.go"))
	require(t, err)
	var criteria []string
	decl := regexp.MustCompile(`(?m)^func (TestIssue\w+)\(`)
	for _, f := range files {
		body, err := os.ReadFile(f) // #nosec G304 -- a glob of the repository's own test files
		require(t, err)
		for _, m := range decl.FindAllSubmatch(body, -1) {
			criteria = append(criteria, string(m[1]))
		}
	}
	for line := range strings.SplitSeq(string(manifest), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		pattern := regexp.MustCompile(fields[1])
		if !slices.ContainsFunc(criteria, pattern.MatchString) {
			t.Errorf("environment %s: %s matches no criterion in test/acceptance", fields[0], fields[1])
		}
	}
}

// TestAcceptanceEnvFlags: the whole suite skips every environment's criteria
// and says where each runs; an environment runs only its own; an unknown one
// is refused, naming the known ones; and a manifest line that is not an
// environment is refused rather than read as one.
func TestAcceptanceEnvFlags(t *testing.T) {
	root := envRepo(t, "# comment\n\nalpha  ^TestIssue1_  A=1 B=2  +svc\nbeta  ^TestIssue2_X$  manual\n")

	out, stderr, ok := envRun(t, root, "flags")
	if !ok || strings.TrimSpace(out) != "-skip '^TestIssue1_|^TestIssue2_X$'" {
		t.Errorf("flags for the plain stack = %q (ok %v)", out, ok)
	}
	for _, want := range []string{"ACCEPTANCE_ENV=alpha; the E2E Nightly runs them", "ACCEPTANCE_ENV=beta; they run by hand"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the plain stack's run does not say %q: %s", want, stderr)
		}
	}
	if out, _, _ := envRun(t, root, "flags", "dev"); strings.TrimSpace(out) != "-skip '^TestIssue1_|^TestIssue2_X$'" {
		t.Errorf("flags dev = %q", out)
	}
	if out, _, _ := envRun(t, root, "flags", "alpha"); strings.TrimSpace(out) != "-run '^TestIssue1_'" {
		t.Errorf("flags alpha = %q", out)
	}
	if out, _, _ := envRun(t, root, "vars", "alpha"); strings.Fields(out)[0] != "A=1" || strings.Fields(out)[1] != "B=2" {
		t.Errorf("vars alpha = %q", out)
	}
	if out, _, _ := envRun(t, root, "services", "alpha"); strings.TrimSpace(out) != "svc" {
		t.Errorf("services alpha = %q", out)
	}
	if out, _, _ := envRun(t, root, "vars", "dev"); strings.TrimSpace(out) != "" {
		t.Errorf("the plain stack has variables: %q", out)
	}
	if out, _, _ := envRun(t, root, "nightly"); strings.Fields(out)[0] != "dev" || strings.Contains(out, "beta") {
		t.Errorf("nightly = %q; want dev and alpha, never the manual beta", out)
	}
	if _, stderr, ok := envRun(t, root, "flags", "gamma"); ok || !strings.Contains(stderr, "dev, alpha, beta") {
		t.Errorf("an unknown environment = ok %v %q", ok, stderr)
	}

	for name, manifest := range map[string]string{
		"a bare token": "alpha ^TestIssue1_ stray\n",
		"no pattern":   "alpha\n",
		"named twice":  "alpha ^A\nalpha ^B\n",
		"reserved":     "dev ^A\n",
	} {
		if _, stderr, ok := envRun(t, envRepo(t, manifest), "flags"); ok {
			t.Errorf("%s was read as an environment: %s", name, stderr)
		}
	}
	empty := envRepo(t, "# nothing\n")
	if out, _, ok := envRun(t, empty, "flags"); !ok || strings.TrimSpace(out) != "" {
		t.Errorf("a manifest with no environment skips %q", out)
	}
}

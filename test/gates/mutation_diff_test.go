package gates

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// scripts/mutation-diff.py mutates the lines a change touches in pkg/ and
// internal/ (#2732): a survivor fails in a security-critical package and is
// printed elsewhere.

const (
	mutationDiffScript    = "scripts/mutation-diff.py"
	mutationSecurityList  = "scripts/mutation-security-packages.txt"
	mutationFakeGremlinsS = `#!/bin/sh
# Records how it was run, then writes the report the test left for the package.
out=""; diff=""; prev=""
for a in "$@"; do
  [ "$prev" = "-o" ] && out="$a"
  [ "$prev" = "--diff" ] && diff="$a"
  prev="$a"
done
pkg=$(basename "$PWD")
printf '%s diff=%s relative=%s\n' "$pkg" "$diff" "$GIT_CONFIG_VALUE_0" >> "$FAKE/gremlins-calls.txt"
if [ -f "$FAKE/report-$pkg.json" ]; then cp "$FAKE/report-$pkg.json" "$out"; else echo '{"files": []}' > "$out"; fi
`
)

// mutationRepo is a repository with the script, a security list naming
// internal/sig, and two packages committed on main.
func mutationRepo(t *testing.T) (root string, env []string) {
	t.Helper()
	root = newGitRepo(t, mutationDiffScript, map[string]string{
		mutationSecurityList:           "# security\ninternal/sig   # signatures\n",
		"internal/sig/sig.go":          "package sig\n\nfunc Ok(a, b int) bool { return a == b }\n",
		"internal/other/other.go":      "package other\n\nfunc Big(a int) bool { return a > 10 }\n",
		"internal/other/other_test.go": "package other\n",
	})
	writeFile(t, root, "fake/bin/gremlins", mutationFakeGremlinsS)
	require(t, os.Chmod(filepath.Join(root, "fake", "bin", "gremlins"), 0o700)) // #nosec G302 -- the fake must be executable
	fake := filepath.Join(root, "fake")
	env = []string{"FAKE=" + fake, "PATH=" + filepath.Join(fake, "bin") + string(os.PathListSeparator) + os.Getenv("PATH")}
	return root, env
}

func lived(file string, line int, kind string) string {
	return `{"files": [{"file_name": "` + file + `", "mutations": [` +
		`{"type": "` + kind + `", "status": "LIVED", "line": ` + strconv.Itoa(line) + `, "column": 9},` +
		`{"type": "ARITHMETIC_BASE", "status": "KILLED", "line": 2, "column": 1}]}]}`
}

func TestMutationDiff_NoProductionGoChangedSkips(t *testing.T) {
	root, env := mutationRepo(t)
	writeFile(t, root, "internal/other/other_test.go", "package other\n\n// a test changed\n")
	writeFile(t, root, "README.md", "changed\n")
	out, ok := runScript(t, root, []string{mutationDiffScript}, env)
	if !ok || !strings.Contains(out, "no production Go under pkg/ or internal/ changed") {
		t.Fatalf("want a pass that says it skipped; ok=%t:\n%s", ok, out)
	}
	if _, err := os.Stat(filepath.Join(root, "fake", "gremlins-calls.txt")); err == nil {
		t.Error("gremlins ran with no production Go changed")
	}
}

func TestMutationDiff_ASurvivorOutsideTheSecurityListIsPrinted(t *testing.T) {
	root, env := mutationRepo(t)
	writeFile(t, root, "internal/other/other.go", "package other\n\nfunc Big(a int) bool { return a >= 10 }\n")
	writeFile(t, root, "fake/report-other.json", lived("other.go", 3, "CONDITIONALS_BOUNDARY"))
	out, ok := runScript(t, root, []string{mutationDiffScript}, env)
	if !ok {
		t.Fatalf("a survivor outside the security list failed the step:\n%s", out)
	}
	if !strings.Contains(out, "LIVED CONDITIONALS_BOUNDARY at internal/other/other.go:3:9") {
		t.Errorf("the survivor was not printed with its file, line and mutation:\n%s", out)
	}
}

func TestMutationDiff_ASurvivorInASecurityPackageFails(t *testing.T) {
	root, env := mutationRepo(t)
	writeFile(t, root, "internal/sig/sig.go", "package sig\n\nfunc Ok(a, b int) bool { return a == b && a != 0 }\n")
	writeFile(t, root, "fake/report-sig.json", lived("sig.go", 3, "CONDITIONALS_NEGATION"))
	out, ok := runScript(t, root, []string{mutationDiffScript}, env)
	if ok {
		t.Fatalf("a survivor in a security-critical package passed:\n%s", out)
	}
	for _, w := range []string{"FAIL mutation-diff", "LIVED CONDITIONALS_NEGATION at internal/sig/sig.go:3:9"} {
		if !strings.Contains(out, w) {
			t.Errorf("the failure lacks %q:\n%s", w, out)
		}
	}
}

func TestMutationDiff_RunsEachChangedPackageAgainstTheBaseWithRelativePaths(t *testing.T) {
	root, env := mutationRepo(t)
	base := gitOut(t, root, "rev-parse", "HEAD")
	writeFile(t, root, "internal/sig/sig.go", "package sig\n\nfunc Ok(a, b int) bool { return a != b }\n")
	writeFile(t, root, "internal/added/added.go", "package added\n")
	out, ok := runScript(t, root, []string{mutationDiffScript}, append(env, "DELTA_BASE="+base))
	if !ok {
		t.Fatalf("killed or absent mutants failed the step:\n%s", out)
	}
	calls, err := os.ReadFile(filepath.Join(root, "fake", "gremlins-calls.txt")) // #nosec G304 -- under the test's temp root
	require(t, err)
	got := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(string(calls)))
	for sc.Scan() {
		got[sc.Text()] = true
	}
	for _, w := range []string{"sig diff=" + base + " relative=true", "added diff=" + base + " relative=true"} {
		if !got[w] {
			t.Errorf("gremlins was not run as %q; calls:\n%s", w, calls)
		}
	}
	if len(got) != 2 {
		t.Errorf("want two packages run (the unchanged one left alone); calls:\n%s", calls)
	}
}

// TestMutationSecurityPackagesExist fails when the security list names a
// directory the repository does not have: a renamed package would otherwise
// leave its mutants unchecked without a word.
func TestMutationSecurityPackagesExist(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", mutationSecurityList))
	require(t, err)
	n := 0
	for line := range strings.SplitSeq(string(data), "\n") {
		line, _, _ = strings.Cut(line, "#")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		n++
		if st, err := os.Stat(filepath.Join("..", "..", line)); err != nil || !st.IsDir() {
			t.Errorf("%s names %s, which is not a directory", mutationSecurityList, line)
		}
	}
	if n == 0 {
		t.Fatalf("%s names no package", mutationSecurityList)
	}
}

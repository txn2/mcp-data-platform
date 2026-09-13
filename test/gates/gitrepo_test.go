package gates

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newGitRepo lays out a git repository holding the real script at scriptRel
// and the base files, commits them on main, and checks out a change branch.
func newGitRepo(t *testing.T, scriptRel string, base map[string]string) string {
	t.Helper()
	root := t.TempDir()
	// #nosec G304 -- scriptRel is one of this package's constant script paths.
	script, err := os.ReadFile(filepath.Join("..", "..", scriptRel))
	require(t, err)
	writeFile(t, root, scriptRel, string(script))
	for rel, body := range base {
		writeFile(t, root, rel, body)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "-A"},
		{"-c", "user.name=gate", "-c", "user.email=gate@example.com", "commit", "-q", "-m", "base"},
		{"checkout", "-q", "-b", "change"},
	} {
		runIn(t, root, "git", args...)
	}
	return root
}

func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, rel)
	require(t, os.MkdirAll(filepath.Dir(path), 0o750))
	// #nosec G703 -- root is t.TempDir() and rel a fixture path this package names.
	require(t, os.WriteFile(path, []byte(body), 0o600))
}

func runIn(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), name, args...) //nolint:gosec // fixed git invocations in a temp repo
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

// runScript runs a gate script in root and returns its output and whether it
// passed.
func runScript(t *testing.T, root string, args, env []string) (string, bool) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "python3", append([]string{filepath.Join(root, args[0])}, args[1:]...)...) //nolint:gosec // a fixed script under a temp root this test wrote
	cmd.Dir = root
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

// expect is a gate's verdict and the lines it must and must not print.
type expect struct {
	pass    bool
	want    []string
	mustNot []string
}

func assertOutput(t *testing.T, out string, passed bool, exp expect) {
	t.Helper()
	if passed != exp.pass {
		t.Fatalf("passed=%v, want %v\n%s", passed, exp.pass, out)
	}
	for _, w := range exp.want {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q\n%s", w, out)
		}
	}
	for _, m := range exp.mustNot {
		if strings.Contains(out, m) {
			t.Errorf("output carries %q\n%s", m, out)
		}
	}
}

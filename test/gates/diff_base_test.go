package gates

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// branchScoped are the scripts that compute a merge base with main and read
// no DELTA_BASE on purpose: each asks a question about the pull request as a
// whole, which the change since the last green tree cannot answer.
var branchScoped = map[string]string{
	"scripts/acceptance-copy-check.py": "a criterion asserting text the branch removed fails however long ago the removal was",
	"scripts/acceptance-check.sh":      "which acceptance files the pull request changes, and their recorded runs",
	"scripts/builtin-pages-check.py":   "a covered path the branch changed with no change to its page",
	"scripts/doc-check.sh":             "documentation-worthy changes the pull request carries with no doc change",
	"scripts/e2e-copy-check.py":        "a spec asserting text the branch removed from ui/src",
	"scripts/portal-surface-check.py":  "a tool or config section the branch adds with no portal change",
	"scripts/state-readers-check.py":   "state fields the branch changes with no ui/src change",
	"scripts/verify-step.py":           "the merge base is one input of a step's digest, beside DELTA_BASE; it diffs nothing",
}

// mergeBaseCall is a merge base computed against a branch: git's merge-base
// subcommand, not a --merge-base flag a script is handed. --is-ancestor is an
// ancestry test, not a base.
var mergeBaseCall = regexp.MustCompile(`(^|[^-])merge-base["',\s]`)

func computesBase(block string) bool {
	for line := range strings.SplitSeq(block, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || strings.Contains(line, "--is-ancestor") {
			continue
		}
		if mergeBaseCall.MatchString(line) {
			return true
		}
	}
	return false
}

// makefileBlocks splits the Makefile into its definitions: a target with its
// recipe, or a variable with its continuation lines.
func makefileBlocks(src string) []string {
	var blocks []string
	var cur strings.Builder
	continued := false
	for line := range strings.SplitSeq(src, "\n") {
		starts := !continued && line != "" && !strings.HasPrefix(line, "\t") && !strings.HasPrefix(line, "#")
		if starts && cur.Len() > 0 {
			blocks = append(blocks, cur.String())
			cur.Reset()
		}
		_, _ = cur.WriteString(line + "\n")
		continued = strings.HasSuffix(line, "\\")
	}
	return append(blocks, cur.String())
}

// pythonFunctions splits a Python script at its top-level definitions.
func pythonFunctions(src string) []string {
	return regexp.MustCompile(`(?m)^def `).Split(src, -1)
}

// TestDiffScopedStepsReadTheLastGreenTree pins #2660: a step that reads its
// diff against a merge base with main takes DELTA_BASE in the same
// definition, so a verify after a green one checks only the change since it.
// A diff-scoped step added the old way fails here; a check about the whole
// pull request is named in branchScoped with its reason.
func TestDiffScopedStepsReadTheLastGreenTree(t *testing.T) {
	for i, block := range makefileBlocks(readRepoFile(t, "Makefile")) {
		if computesBase(block) && !strings.Contains(block, "DELTA_BASE") {
			t.Errorf("Makefile definition %d computes a merge base and reads no DELTA_BASE; take $(diff_base):\n%s", i, block)
		}
	}
	scripts, err := filepath.Glob(filepath.Join("..", "..", "scripts", "*"))
	require(t, err)
	seen := map[string]bool{}
	for _, path := range scripts {
		rel := filepath.ToSlash(strings.TrimPrefix(path, filepath.Join("..", "..")+string(filepath.Separator)))
		var units []string
		switch filepath.Ext(path) {
		case ".py":
			units = pythonFunctions(readRepoFile(t, rel))
		case ".sh":
			units = []string{readRepoFile(t, rel)}
		default:
			continue
		}
		for _, unit := range units {
			if !computesBase(unit) || strings.Contains(unit, "DELTA_BASE") {
				continue
			}
			seen[rel] = true
			if _, ok := branchScoped[rel]; !ok {
				name, _, _ := strings.Cut(unit, "\n")
				t.Errorf("%s: %s computes a merge base and reads no DELTA_BASE; take it first (schedule-lane.py merge_base), or name the script in branchScoped with why the whole branch is the question", rel, name)
			}
		}
	}
	for rel := range branchScoped {
		if !seen[rel] {
			t.Errorf("branchScoped names %s, which no longer computes a merge base without DELTA_BASE; remove the entry", rel)
		}
	}
}

// diffBaseMakefile is a Makefile holding the Makefile's own diff_base and
// migrations_changed, and a target printing what each step would read.
func diffBaseMakefile(t *testing.T) string {
	t.Helper()
	var defs []string
	for _, block := range makefileBlocks(readRepoFile(t, "Makefile")) {
		for _, name := range []string{"diff_base =", "migrations_changed =", "MIGRATIONS_DIR :=", "DEV_DIR :="} {
			if strings.HasPrefix(block, name) {
				defs = append(defs, strings.TrimRight(block, "\n"))
			}
		}
	}
	if len(defs) != 4 {
		t.Fatalf("found %d of the four definitions the diff-scoped steps read", len(defs))
	}
	return strings.Join(defs, "\n") + "\n.PHONY: migrations dev\n" +
		"migrations:\n\t@$(migrations_changed); echo \"migrations:$$changed\"\n" +
		"dev:\n\t@$(diff_base); changed=$$(git diff --name-only $$base -- $(DEV_DIR)); echo \"dev:$$changed\"\n"
}

func runMake(t *testing.T, root, target string, env []string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "make", "--no-print-directory", "-f", "gate.mk", target) //nolint:gosec // a fixed make invocation in a temp repo
	cmd.Dir = root
	cmd.Env = append(scriptEnviron(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make %s: %v\n%s", target, err, out)
	}
	return strings.TrimSpace(string(out))
}

// commitHead commits the working tree and returns the commit.
func commitHead(t *testing.T, root, msg string) string {
	t.Helper()
	commitAll(t, root, msg)
	return deltaGit(t, root, "rev-parse", "HEAD")
}

// TestDiffScopedMakeStepsSkipWhatTheGreenTreeChecked pins #2660 for
// migrate-check-diff and dev-smoke-diff: with DELTA_BASE at a tree that
// already held the branch's migration and dev change, neither sees a change;
// without it, both read the branch against main.
func TestDiffScopedMakeStepsSkipWhatTheGreenTreeChecked(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", "base\n")
	runIn(t, root, "git", "init", "-q", "-b", "main")
	commitAll(t, root, "base")
	runIn(t, root, "git", "checkout", "-q", "-b", "change")
	writeFile(t, root, "pkg/database/migrate/migrations/000001_a.up.sql", "SELECT 1;\n")
	writeFile(t, root, "dev/compose.yml", "services: {}\n")
	green := commitHead(t, root, "green")
	writeFile(t, root, "docs/page.md", "edited after the green run\n")
	require(t, os.WriteFile(filepath.Join(root, "gate.mk"), []byte(diffBaseMakefile(t)), 0o600))
	writeFile(t, root, ".gitignore", "gate.mk\n")

	if got := runMake(t, root, "migrations", nil); got != "migrations:pkg/database/migrate/migrations/000001_a.up.sql" {
		t.Errorf("without DELTA_BASE the migration step read %q, want the branch's migration", got)
	}
	if got := runMake(t, root, "dev", nil); got != "dev:dev/compose.yml" {
		t.Errorf("without DELTA_BASE the dev step read %q, want the branch's dev change", got)
	}
	delta := []string{"DELTA_BASE=" + green}
	if got := runMake(t, root, "migrations", delta); got != "migrations:" {
		t.Errorf("with DELTA_BASE at the green tree the migration step read %q, want nothing", got)
	}
	if got := runMake(t, root, "dev", delta); got != "dev:" {
		t.Errorf("with DELTA_BASE at the green tree the dev step read %q, want nothing", got)
	}

	writeFile(t, root, "pkg/database/migrate/migrations/000002_b.up.sql", "SELECT 2;\n")
	if got := runMake(t, root, "migrations", delta); got != "migrations:pkg/database/migrate/migrations/000002_b.up.sql" {
		t.Errorf("a migration added after the green tree read %q, want it alone", got)
	}
}

// TestSemgrepDiffReadsTheLastGreenTree pins #2660 for semgrep-diff and
// semgrep-changed: a Go file the green tree already held is not scanned
// again, and one changed since it is.
func TestSemgrepDiffReadsTheLastGreenTree(t *testing.T) {
	requireSemgrep(t)
	root := newGitRepo(t, "scripts/semgrep-diff.py", map[string]string{
		"go.mod":          "module example.com/m\n\ngo 1.25\n",
		"rules/r.yml":     "rules: []\n",
		"internal/a/a.go": "package a\n",
	})
	writeFile(t, root, "internal/a/a.go", "package a\n\n// changed on the branch\n")
	green := commitHead(t, root, "green")
	args := []string{"scripts/semgrep-diff.py", "--whole-files", "rules/"}

	out, ok := runScript(t, root, args, []string{"DELTA_BASE=" + green})
	if !ok || !strings.Contains(out, "No changed Go files vs base "+green[:12]) {
		t.Fatalf("with DELTA_BASE at the green tree, the branch's file was scanned again: ok=%v\n%s", ok, out)
	}
	out, ok = runScript(t, root, args, nil)
	if !ok || strings.Contains(out, "No changed Go files") {
		t.Fatalf("without DELTA_BASE, the branch's change was not scanned: ok=%v\n%s", ok, out)
	}
	out, ok = runScript(t, root, args, []string{"DELTA_BASE=0000000000000000000000000000000000000000"})
	if ok || !strings.Contains(out, "is not a commit") {
		t.Fatalf("a DELTA_BASE that is not a commit was accepted: ok=%v\n%s", ok, out)
	}
}

// TestVerifyDiffReadsTheLastGreenTree pins #2660 for security-diff and
// route-template-diff, which read their base through schedule-lane.py.
func TestVerifyDiffReadsTheLastGreenTree(t *testing.T) {
	root := verifyDiffRepo(t)
	writeFile(t, root, "internal/web/routes.go", "package web\n\nimport \"net/http\"\n\nfunc Register(mux *http.ServeMux) {\n\tmux.HandleFunc(\"GET /a\", nil)\n\tmux.Handle(\"POST /b\", nil)\n}\n")
	green := commitHead(t, root, "green")
	delta := []string{"DELTA_BASE=" + green}

	if out, ok := runScript(t, root, []string{"scripts/verify-diff.py", "gosec-packages"}, delta); !ok || strings.TrimSpace(out) != "" {
		t.Errorf("with DELTA_BASE at the green tree gosec was named packages: ok=%v %q", ok, out)
	}
	if out, ok := runScript(t, root, []string{"scripts/verify-diff.py", "routes-changed"}, delta); ok {
		t.Errorf("with DELTA_BASE at the green tree the route gate ran:\n%s", out)
	}
	if out, ok := runScript(t, root, []string{"scripts/verify-diff.py", "routes-changed"}, nil); !ok {
		t.Errorf("without DELTA_BASE the branch's registration did not run the route gate:\n%s", out)
	}
}

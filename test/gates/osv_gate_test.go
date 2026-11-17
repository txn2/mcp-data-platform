package gates

import (
	"encoding/json"
	"strings"
	"testing"
)

// The osv gate (#2690): a pull request fails only on an advisory against a
// package version it adds or moves to; an advisory against a dependency it
// left alone is a warning there, and fails the push to main.

const osvGate = "scripts/osv-gate.py"

const osvGoMod = "module github.com/txn2/mcp-data-platform\n\ngo 1.27.4\n\nrequire (\n\tgolang.org/x/crypto v0.58.0\n\tgithub.com/getkin/kin-openapi v0.152.0 // indirect\n)\n"

const osvLock = `{"lockfileVersion": 3, "packages": {
 "": {"name": "ui"},
 "node_modules/picomatch": {"version": "4.0.7"},
 "node_modules/@scope/widget": {"version": "1.0.0"},
 "node_modules/a/node_modules/mermaid": {"version": "12.2.0"}
}}`

const osvRequirements = "mkdocs-material==9.8.0 \\\n    --hash=sha256:abc\nPyYAML==6.0.2\n"

// osvRepo is a repository holding the gate and the three manifests on main.
func osvRepo(t *testing.T) (root, base string) {
	t.Helper()
	root = newGitRepo(t, osvGate, map[string]string{
		"go.mod": osvGoMod, "ui/package-lock.json": osvLock, "docs/requirements.txt": osvRequirements,
	})
	return root, deltaGit(t, root, "rev-parse", "HEAD")
}

type osvFinding struct{ eco, name, version, id string }

// osvReport writes an osv-scanner v2 JSON report naming each finding.
func osvReport(t *testing.T, root string, found ...osvFinding) {
	t.Helper()
	pkgs := make([]map[string]any, 0, len(found))
	for _, f := range found {
		pkgs = append(pkgs, map[string]any{
			"package":         map[string]any{"name": f.name, "version": f.version, "ecosystem": f.eco},
			"vulnerabilities": []map[string]any{{"id": f.id}},
		})
	}
	raw, err := json.Marshal(map[string]any{"results": []map[string]any{{"source": map[string]any{"path": "x"}, "packages": pkgs}}})
	require(t, err)
	writeFile(t, root, "build/osv.json", string(raw))
}

func osvRun(t *testing.T, root string, env ...string) (string, bool) {
	t.Helper()
	if !strings.Contains(strings.Join(env, " "), "OSV_LOG=") {
		writeFile(t, root, "build/osv.log", "Scanned 3 files\n")
	}
	return runScript(t, root, []string{osvGate, "build/osv.json", "build/osv.log"}, env)
}

// TestOSVGateWarnsOnAnAdvisoryTheChangeDidNotIntroduce: with no dependency
// changed, a new advisory against one already pinned passes the pull request
// as a warning, and fails the push to main.
func TestOSVGateWarnsOnAnAdvisoryTheChangeDidNotIntroduce(t *testing.T) {
	root, base := osvRepo(t)
	writeFile(t, root, "pkg/x.go", "package x\n")
	osvReport(t, root, osvFinding{"npm", "picomatch", "4.0.7", "GHSA-fc5q-39xc-vjpf"})

	out, ok := osvRun(t, root, "OSV_SCOPE=diff", "OSV_BASE="+base)
	if !ok {
		t.Fatalf("a pull request changing no dependency failed on an advisory against an unchanged one:\n%s", out)
	}
	if !strings.Contains(out, "::warning") || !strings.Contains(out, "picomatch@4.0.7") {
		t.Errorf("the advisory was not printed as a warning:\n%s", out)
	}
	if out, ok := osvRun(t, root, "OSV_SCOPE=full"); ok || !strings.Contains(out, "GHSA-fc5q-39xc-vjpf") {
		t.Errorf("the push-to-main scope passed an advisory:\n%s", out)
	}
}

// TestOSVGateFailsAnAdvisoryAgainstAVersionTheChangeIntroduces: a bump to a
// vulnerable version fails, in every manifest, under both names a lockfile
// and a requirements file write a package by.
func TestOSVGateFailsAnAdvisoryAgainstAVersionTheChangeIntroduces(t *testing.T) {
	cases := []struct {
		name    string
		file    string
		body    string
		finding osvFinding
	}{
		{"go module", "go.mod", strings.Replace(osvGoMod, "v0.58.0", "v0.59.0", 1), osvFinding{"Go", "golang.org/x/crypto", "0.59.0", "GO-2026-1"}},
		{"go indirect", "go.mod", strings.Replace(osvGoMod, "v0.152.0", "v0.153.0", 1), osvFinding{"Go", "github.com/getkin/kin-openapi", "v0.153.0", "GO-2026-2"}},
		{"go toolchain", "go.mod", strings.Replace(osvGoMod, "go 1.27.4", "go 1.27.5", 1), osvFinding{"Go", "stdlib", "1.27.5", "GO-2026-3"}},
		{"npm nested", "ui/package-lock.json", strings.Replace(osvLock, "12.2.0", "12.3.0", 1), osvFinding{"npm", "mermaid", "12.3.0", "GHSA-1"}},
		{"npm scoped", "ui/package-lock.json", strings.Replace(osvLock, `"1.0.0"`, `"1.1.0"`, 1), osvFinding{"npm", "@scope/widget", "1.1.0", "GHSA-2"}},
		{"pip", "docs/requirements.txt", strings.Replace(osvRequirements, "9.8.0", "9.8.1", 1), osvFinding{"PyPI", "mkdocs_material", "9.8.1", "PYSEC-1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, base := osvRepo(t)
			writeFile(t, root, tc.file, tc.body)
			osvReport(t, root, tc.finding, osvFinding{"PyPI", "pyyaml", "6.0.2", "PYSEC-OLD"})
			out, ok := osvRun(t, root, "OSV_SCOPE=diff", "OSV_BASE="+base)
			if ok {
				t.Fatalf("a bump to a vulnerable version passed:\n%s", out)
			}
			if !strings.Contains(out, "::error") || !strings.Contains(out, tc.finding.id) {
				t.Errorf("the failure does not name %s:\n%s", tc.finding.id, out)
			}
			if !strings.Contains(out, "::warning") || !strings.Contains(out, "PYSEC-OLD") {
				t.Errorf("the advisory against the unchanged pin was not a warning:\n%s", out)
			}
		})
	}
}

// TestOSVGateFailsAnAcceptanceThatNoLongerMatches: an osv-scanner.toml entry
// no finding matches fails in either scope.
func TestOSVGateFailsAnAcceptanceThatNoLongerMatches(t *testing.T) {
	root, base := osvRepo(t)
	osvReport(t, root)
	writeFile(t, root, "build/osv.log", "GHSA-x has unused ignores\n")
	for _, env := range [][]string{{"OSV_SCOPE=diff", "OSV_BASE=" + base}, {"OSV_SCOPE=full"}} {
		if out, ok := osvRun(t, root, append(env, "OSV_LOG=set")...); ok || !strings.Contains(out, "delete that entry") {
			t.Errorf("%v: an unused acceptance passed:\n%s", env, out)
		}
	}
}

// TestOSVGateDiffWithNoBaseJudgesEverything: a diff scope with no base to
// diff against cannot tell what the change introduced, so it fails as main
// does rather than passing everything.
func TestOSVGateDiffWithNoBaseJudgesEverything(t *testing.T) {
	root, _ := osvRepo(t)
	osvReport(t, root, osvFinding{"npm", "picomatch", "4.0.7", "GHSA-fc5q-39xc-vjpf"})
	if out, ok := osvRun(t, root, "OSV_SCOPE=diff", "OSV_BASE="); ok {
		t.Errorf("a diff scope with no base passed an advisory:\n%s", out)
	}
}

// TestOSVGateIsWiredIntoCI: a pull request's Security Scan judges the diff
// against main, every other event everything, and a release everything.
func TestOSVGateIsWiredIntoCI(t *testing.T) {
	ci := readRepoFile(t, ".github/workflows/ci.yml")
	for _, want := range []string{
		"OSV_SCOPE: ${{ github.event_name == 'pull_request' && 'diff' || 'full' }}",
		"OSV_BASE=$(git rev-parse HEAD^1)",
	} {
		if !strings.Contains(ci, want) {
			t.Errorf("ci.yml does not carry %q", want)
		}
	}
	mk := readRepoFile(t, "Makefile")
	for _, want := range []string{"python3 scripts/osv-gate.py", "verify-release: verify codeql mutate acceptance-release-check osv-release", "osv OSV_SCOPE=full"} {
		if !strings.Contains(mk, want) {
			t.Errorf("the Makefile does not carry %q", want)
		}
	}
}

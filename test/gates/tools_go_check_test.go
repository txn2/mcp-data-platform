package gates

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const toolsGoCheckScript = "scripts/tools-go-check.sh"

// toolsGoCheckWorkflow is the shape of ci.yml's setup-go steps.
const toolsGoCheckWorkflow = `jobs:
  test:
    steps:
      - uses: actions/setup-go@v6
        with:
          go-version: '1.26.9'
  security:
    steps:
      - uses: actions/setup-go@v6
        with:
          go-version: '1.26.9'
`

// fakeTool puts an executable named tool in bin and teaches the fake go on
// PATH to report it as built with goVersion, the way `go version <binary>`
// prints it.
func fakeTool(t *testing.T, bin, tool, goVersion string) {
	t.Helper()
	writeFile(t, bin, tool, "#!/bin/sh\nexit 0\n")
	// #nosec G302 -- a test fixture that must be executable to be found on PATH.
	require(t, os.Chmod(filepath.Join(bin, tool), 0o700))
	writeFile(t, bin, "versions/"+tool, goVersion)
}

// fakeGo answers `go version <path>` from the versions directory beside it.
const fakeGo = `#!/bin/sh
dir=$(dirname "$0")
name=$(basename "$2")
printf '%s: %s\n' "$2" "$(cat "$dir/versions/$name")"
`

// TestToolsGoCheckRefusesANewerGo holds scripts/tools-go-check.sh to #2147:
// a pinned tool built with a newer Go than CI installs is refused, naming the
// tool, both versions and the GOTOOLCHAIN=local install that reproduces CI.
func TestToolsGoCheckRefusesANewerGo(t *testing.T) {
	cases := []struct {
		name   string
		built  string
		expect expect
	}{
		{
			name:  "osv-scanner v2.6.0 built under GOTOOLCHAIN=auto is refused",
			built: "go1.27.0",
			expect: expect{want: []string{
				"osv-scanner: built with go1.27.0, CI installs go1.26.9",
				"GOTOOLCHAIN=local go install github.com/google/osv-scanner/v2/cmd/osv-scanner@v2.6.0",
			}},
		},
		{name: "the Go CI installs passes", built: "go1.26.9", expect: expect{pass: true}},
		{name: "an older Go passes", built: "go1.26.5", expect: expect{pass: true}},
		{name: "a later patch of CI's minor is newer", built: "go1.26.10", expect: expect{want: []string{"built with go1.26.10"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			// #nosec G304 -- toolsGoCheckScript is this package's constant script path.
			script, err := os.ReadFile(filepath.Join("..", "..", toolsGoCheckScript))
			require(t, err)
			writeFile(t, root, toolsGoCheckScript, string(script))
			writeFile(t, root, "ci.yml", toolsGoCheckWorkflow)
			bin := filepath.Join(root, "bin")
			writeFile(t, bin, "go", fakeGo)
			// #nosec G302 -- the fake go must be executable.
			require(t, os.Chmod(filepath.Join(bin, "go"), 0o700))
			fakeTool(t, bin, "osv-scanner", tc.built)

			cmd := exec.CommandContext(t.Context(), "sh", filepath.Join(root, toolsGoCheckScript), "ci.yml", //nolint:gosec // a fixed script under a temp root this test wrote
				"osv-scanner=github.com/google/osv-scanner/v2/cmd/osv-scanner@v2.6.0",
				"gosec=github.com/securego/gosec/v2/cmd/gosec@v2.28.0")
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "PATH="+bin+":/usr/bin:/bin")
			out, err := cmd.CombinedOutput()
			assertOutput(t, string(out), err == nil, tc.expect)
			if strings.Contains(string(out), "gosec") {
				t.Errorf("a tool not on PATH is left to the presence check:\n%s", out)
			}
		})
	}
}

// TestToolsCheckReadsTheBuildGo pins that make tools-check runs the script
// over every pinned Go tool and prints GOTOOLCHAIN=local install commands.
func TestToolsCheckReadsTheBuildGo(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	require(t, err)
	lines := recipe(t, string(raw), "tools-check")
	body := strings.Join(lines, "\n")
	if !strings.Contains(body, "scripts/tools-go-check.sh .github/workflows/ci.yml") {
		t.Fatal("make tools-check does not run scripts/tools-go-check.sh against ci.yml")
	}
	for _, pin := range []string{"GOLANGCI_LINT_VERSION", "GOSEC_VERSION", "GOVULNCHECK_VERSION", "OSV_SCANNER_VERSION", "GREMLINS_VERSION"} {
		if !strings.Contains(body, "@$("+pin+")") {
			t.Errorf("tools-check does not check the build Go of the tool pinned by %s", pin)
		}
	}
	if strings.Contains(strings.ReplaceAll(body, "GOTOOLCHAIN=local go install", ""), "go install") {
		t.Error("tools-check prints a go install without GOTOOLCHAIN=local, which CI's install does not reproduce")
	}
}

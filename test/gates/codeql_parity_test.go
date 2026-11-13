package gates

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// sarifFindings reads the results of a SARIF file as "rule path:line" lines.
func sarifFindings(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- a SARIF file this test wrote under t.TempDir()
	require(t, err)
	var doc struct {
		Runs []struct {
			Results []struct {
				RuleID    string `json:"ruleId"`
				Locations []struct {
					PhysicalLocation struct {
						ArtifactLocation struct {
							URI string `json:"uri"`
						} `json:"artifactLocation"`
						Region struct {
							StartLine int `json:"startLine"`
						} `json:"region"`
					} `json:"physicalLocation"`
				} `json:"locations"`
			} `json:"results"`
		} `json:"runs"`
	}
	require(t, json.Unmarshal(data, &doc))
	var out []string
	for _, run := range doc.Runs {
		for _, r := range run.Results {
			loc := ""
			if len(r.Locations) > 0 {
				pl := r.Locations[0].PhysicalLocation
				loc = fmt.Sprintf("%s:%d", pl.ArtifactLocation.URI, pl.Region.StartLine)
			}
			out = append(out, r.RuleID+" "+loc)
		}
	}
	slices.Sort(out)
	return out
}

// codeqlRun runs scripts/codeql-local.sh in mode over this checkout and
// returns the SARIF it wrote.
func codeqlRun(t *testing.T, mode string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), mode+".sarif")
	cmd := exec.CommandContext(t.Context(), "bash", "scripts/codeql-local.sh", mode, out) //nolint:gosec // a fixed script in this repository
	cmd.Dir = filepath.Join("..", "..")
	// The autobuild half runs `make` in the export, whose test target runs
	// this package again; without the variable, this test skips there.
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "CODEQL_PARITY=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("codeql-local.sh %s: %v\n%s", mode, err, b)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("codeql-local.sh %s exited 0 and wrote no SARIF: %v\n%s", mode, err, b)
	}
	return out
}

// TestCodeQLTracedBuildFindsWhatAutobuildFinds pins #2548's sixth ask: the
// database make codeql builds from one traced `go build ./...` per module
// holds what CodeQL's autobuild (CI's) finds, over one tree. It runs both,
// which takes many minutes, so it runs only when asked: make codeql-parity.
func TestCodeQLTracedBuildFindsWhatAutobuildFinds(t *testing.T) {
	if os.Getenv("CODEQL_PARITY") == "" {
		t.Skip("runs CodeQL twice; run it with make codeql-parity")
	}
	if _, err := exec.LookPath("codeql"); err != nil {
		t.Fatal("make codeql-parity needs the codeql CLI")
	}
	traced := sarifFindings(t, codeqlRun(t, "traced"))
	auto := sarifFindings(t, codeqlRun(t, "autobuild"))
	t.Logf("traced build: %d findings; autobuild: %d", len(traced), len(auto))
	if !slices.Equal(traced, auto) {
		var only []string
		for _, f := range auto {
			if !slices.Contains(traced, f) {
				only = append(only, "autobuild only: "+f)
			}
		}
		for _, f := range traced {
			if !slices.Contains(auto, f) {
				only = append(only, "traced only:    "+f)
			}
		}
		t.Fatalf("the two builds find different things:\n%s", strings.Join(only, "\n"))
	}
}

// TestCodeQLRunsOnAnExport pins the other half: make codeql builds from an
// export of the working tree with the traced build, never in the checkout.
func TestCodeQLRunsOnAnExport(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../../Makefile")
	require(t, err)
	got := strings.Join(recipe(t, string(raw), "codeql"), "\n")
	if !strings.Contains(got, "scripts/codeql-local.sh traced") || strings.Contains(got, "--source-root=.") {
		t.Errorf("make codeql does not run scripts/codeql-local.sh traced:\n%s", got)
	}
	script := readRepoFile(t, "scripts/codeql-local.sh")
	for _, want := range []string{"git checkout-index -a --prefix=", `--source-root="$src"`, "go build ./..."} {
		if !strings.Contains(script, want) {
			t.Errorf("scripts/codeql-local.sh does not contain %q", want)
		}
	}
}
